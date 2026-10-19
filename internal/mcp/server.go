// The MCP server: JSON-RPC 2.0 over stdio, the transport every MCP client
// speaks. stdout carries protocol frames and nothing else; every log line goes
// to stderr, because one stray byte on stdout breaks the client's message
// stream. Requests are handled concurrently so a slow tool does not stall the
// read loop, and responses are written in request order so pipelined calls stay
// answerable frame by frame.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// The protocol version this server implements. The six tools have not changed
// across the 2024-11 and 2025-03 revisions, so a client asking for a newer
// revision still gets a usable tool list.
const ProtocolVersion = "2024-11-05"

// ServerName is the serverInfo.name every initialize answer carries.
const ServerName = "errkb"

// MaxFrameBytes caps one inbound frame; a real MCP request is far smaller.
const MaxFrameBytes = 4 << 20

// MaxConcurrentTools bounds how many tool calls run at once. The read loop
// keeps reading while they run, so a slow lookup never blocks ping.
const MaxConcurrentTools = 8

// JSON-RPC error codes, from the specification.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Server is one MCP stdio session.
type Server struct {
	Version string
	Tools   []Tool
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
}

// NewServer builds a server over one backend. version is stamped into
// serverInfo, never printed to stdout.
func NewServer(in io.Reader, out, errOut io.Writer, backend Backend, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{
		Version: version,
		Tools:   NewTools(backend),
		In:      in,
		Out:     out,
		Err:     errOut,
	}
}

// rpcRequest is one JSON-RPC frame. The id stays raw so a client that numbers
// requests with an integer gets an integer back, not a string or a float.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (r rpcRequest) isNotification() bool { return len(bytes.TrimSpace(r.ID)) == 0 }

// Serve runs the session until the input stream ends. A protocol error is
// answered with a JSON-RPC error frame; it never returns from Serve, so the
// host keeps its tool set.
func (s *Server) Serve(ctx context.Context) error {
	if s.In == nil || s.Out == nil {
		return errors.New("mcp: the server needs a reader and a writer")
	}
	if s.Err == nil {
		s.Err = io.Discard
	}
	s.logf("serving MCP over stdio (protocol %s, %d tools)", ProtocolVersion, len(s.Tools))

	writer := newFrameWriter(s.Out)
	scanner := bufio.NewScanner(s.In)
	scanner.Buffer(make([]byte, 64*1024), MaxFrameBytes)

	limit := make(chan struct{}, MaxConcurrentTools)
	var wg sync.WaitGroup
	var sequence uint64

	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		request, err := decodeRPCRequest(line)
		if err != nil {
			s.logf("discarding a frame that is not JSON-RPC: %v", err)
			writer.enqueue(sequence, errorFrame(nil, codeParseError, err.Error()))
			sequence++
			continue
		}
		if request.isNotification() {
			s.handleNotification(request)
			continue
		}
		position := sequence
		sequence++
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			writer.enqueue(position, s.respond(ctx, request))
		}()
	}
	scanErr := scanner.Err()
	wg.Wait()
	if err := writer.err(); err != nil {
		return fmt.Errorf("mcp: writing a protocol frame: %w", err)
	}
	return scanErr
}

// respond turns one request into one response frame.
func (s *Server) respond(ctx context.Context, request rpcRequest) []byte {
	switch request.Method {
	case "initialize":
		return s.initialize(request)
	case "ping":
		return resultFrame(request.ID, map[string]any{})
	case "tools/list":
		return resultFrame(request.ID, map[string]any{"tools": s.toolDescriptors()})
	case "tools/call":
		return s.callTool(ctx, request)
	default:
		return errorFrame(request.ID, codeMethodNotFound, fmt.Sprintf("method %q not found", request.Method))
	}
}

// initialize answers the handshake and publishes the correction notice that
// says MCP is not passive capture.
func (s *Server) initialize(request rpcRequest) []byte {
	if len(bytes.TrimSpace(request.Params)) > 0 {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return errorFrame(request.ID, codeInvalidParams, "initialize params: "+err.Error())
		}
	}
	return resultFrame(request.ID, map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo":   map[string]any{"name": ServerName, "version": s.Version},
		"instructions": CorrectionNotice,
	})
}

// callTool finds the tool, validates its arguments and serialises the contract
// response as MCP text content.
func (s *Server) callTool(ctx context.Context, request rpcRequest) []byte {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(bytes.TrimSpace(request.Params)) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return errorFrame(request.ID, codeInvalidParams, "tools/call params: "+err.Error())
		}
	}
	if params.Name == "" {
		return errorFrame(request.ID, codeInvalidParams, "tools/call: name is required")
	}
	tool, ok := s.tool(params.Name)
	if !ok {
		return errorFrame(request.ID, codeInvalidParams, fmt.Sprintf("tools/call: unknown tool %q", params.Name))
	}
	result, err := tool.Call(ctx, params.Arguments)
	if err != nil {
		if errors.Is(err, ErrInvalidArguments) {
			s.logf("tools/call %s rejected: %v", params.Name, err)
			return errorFrame(request.ID, codeInvalidParams, err.Error())
		}
		s.logf("tools/call %s failed: %v", params.Name, err)
		return errorFrame(request.ID, codeInternalError, err.Error())
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return errorFrame(request.ID, codeInternalError, "tools/call: encode the result: "+err.Error())
	}
	return resultFrame(request.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(encoded)}},
	})
}

func (s *Server) tool(name string) (Tool, bool) {
	for _, tool := range s.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func (s *Server) toolDescriptors() []map[string]any {
	descriptors := make([]map[string]any, 0, len(s.Tools))
	for _, tool := range s.Tools {
		descriptors = append(descriptors, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		})
	}
	return descriptors
}

// handleNotification ignores the notifications the protocol defines. A client
// that sends "initialized" or "cancelled" gets no frame back, as the
// specification requires.
func (s *Server) handleNotification(request rpcRequest) {
	switch request.Method {
	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		return
	default:
		s.logf("ignoring notification %q", request.Method)
	}
}

// decodeRPCRequest parses one frame and insists on the parts of JSON-RPC the
// server relies on.
func decodeRPCRequest(line []byte) (rpcRequest, error) {
	var request rpcRequest
	decoder := json.NewDecoder(bytes.NewReader(line))
	if err := decoder.Decode(&request); err != nil {
		return rpcRequest{}, fmt.Errorf("parse error: %w", err)
	}
	if request.JSONRPC != "2.0" {
		return rpcRequest{}, fmt.Errorf("unsupported jsonrpc version %q", request.JSONRPC)
	}
	if request.Method == "" {
		return rpcRequest{}, errors.New("a frame without a method is not a request")
	}
	return request, nil
}

// resultFrame encodes a JSON-RPC result.
func resultFrame(id json.RawMessage, result any) []byte {
	frame := map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return errorFrame(id, codeInternalError, "encode the response: "+err.Error())
	}
	return encoded
}

// errorFrame encodes a JSON-RPC error. The id is echoed when known and null on
// a parse error, as the specification says.
func errorFrame(id json.RawMessage, code int, message string) []byte {
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"error":   map[string]any{"code": code, "message": message},
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"encode the error frame"}}`)
	}
	return encoded
}

func rawID(id json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(trimmed)
}

// logf writes one diagnostic line to stderr. Nothing here may touch stdout.
func (s *Server) logf(format string, args ...any) {
	if s.Err == nil {
		return
	}
	fmt.Fprintf(s.Err, "errkb mcp: "+format+"\n", args...)
}

// frameWriter writes whole frames in request order even though requests are
// handled concurrently: a frame is queued under its sequence number and
// flushed only when every earlier one is already on the wire, so bytes from
// two responses never interleave.
type frameWriter struct {
	mu       sync.Mutex
	out      io.Writer
	next     uint64
	pending  map[uint64][]byte
	writeErr error
}

func newFrameWriter(out io.Writer) *frameWriter {
	return &frameWriter{out: out, pending: make(map[uint64][]byte)}
}

func (w *frameWriter) enqueue(sequence uint64, payload []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return
	}
	w.pending[sequence] = payload
	for {
		frame, ok := w.pending[w.next]
		if !ok {
			return
		}
		delete(w.pending, w.next)
		w.next++
		if _, err := w.out.Write(append(frame, '\n')); err != nil {
			w.writeErr = err
			return
		}
	}
}

func (w *frameWriter) err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeErr
}
