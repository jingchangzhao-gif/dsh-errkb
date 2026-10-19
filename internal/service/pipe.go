//go:build windows

// The Windows Named Pipe transport for the local API (EKB-029).
//
// The daemon talks to connectors, CLIs and the DSH plugin only through this
// pipe. Three properties are load-bearing:
//
//   - There is no TCP listener anywhere in this package (the structural test in
//     pipe_test.go pins that). The plan turns loopback HTTP off by default; v1
//     does not implement it at all, so it cannot be on by accident.
//   - The pipe ACL grants only the current Windows user (and SYSTEM). It is
//     built from the current process token, never from Everyone or Users.
//   - Framing is one JSON envelope per line. A connection serializes its own
//     responses, so concurrent connections cannot interleave frames.
package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	// DefaultPipeName is the local pipe the daemon listens on.
	DefaultPipeName = `\\.\pipe\errkb`
	// DefaultPipeFrameBytes caps one request frame. It keeps a broken or
	// malicious client from growing the server buffer without limit.
	DefaultPipeFrameBytes = 1 << 20
	// pipeReadBuffer is the read buffer size; frames may span several reads.
	pipeReadBuffer = 64 << 10
)

// errPipeFrameTooLarge aborts one connection: the remainder of the oversized
// frame cannot be framed anymore.
var errPipeFrameTooLarge = errors.New("pipe frame exceeds the configured maximum")

// PipeOptions configures one listener.
type PipeOptions struct {
	// Name is a full local pipe path. Empty means DefaultPipeName.
	Name string
	// API answers the decoded frames. Required.
	API *API
	// BeginRequest, when set, registers one in-flight request with the daemon
	// so Shutdown can drain it. A non-nil error rejects the request with
	// CodeUnavailable and closes the connection (the daemon is stopping).
	BeginRequest func() (release func(), err error)
	// MaxFrameBytes caps one request frame. Zero means DefaultPipeFrameBytes.
	MaxFrameBytes int
}

// PipeServer is one running listener.
type PipeServer struct {
	name       string
	listener   net.Listener
	api        *API
	beginReq   func() (func(), error)
	maxFrame   int
	descriptor string

	closeOnce sync.Once
	closed    chan struct{}

	connsMu sync.Mutex
	conns   map[net.Conn]struct{}
}

// ListenPipe creates the named pipe listener with an ACL for the current user.
// The returned server has not accepted anything yet; call Serve.
func ListenPipe(options PipeOptions) (*PipeServer, error) {
	if options.API == nil {
		return nil, errors.New("service: a pipe listener needs an API")
	}
	name := options.Name
	if name == "" {
		name = DefaultPipeName
	}
	if !strings.HasPrefix(name, `\\.\pipe\`) || len(name) == len(`\\.\pipe\`) {
		return nil, fmt.Errorf("service: %q is not a local named pipe path (want \\\\.\\pipe\\<name>)", name)
	}
	descriptor, err := pipeSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: descriptor})
	if err != nil {
		return nil, fmt.Errorf("service: listen on %s: %w", name, err)
	}
	maxFrame := options.MaxFrameBytes
	if maxFrame <= 0 {
		maxFrame = DefaultPipeFrameBytes
	}
	return &PipeServer{
		name:       name,
		listener:   listener,
		api:        options.API,
		beginReq:   options.BeginRequest,
		maxFrame:   maxFrame,
		descriptor: descriptor,
		closed:     make(chan struct{}),
		conns:      make(map[net.Conn]struct{}),
	}, nil
}

// Name reports the full pipe path.
func (s *PipeServer) Name() string { return s.name }

// SecurityDescriptor reports the SDDL the listener was created with. The daemon
// exposes it from `errkb doctor` later; tests use it to prove the ACL.
func (s *PipeServer) SecurityDescriptor() string { return s.descriptor }

// Serve accepts connections until the context is cancelled or Close is called.
// Every connection runs on its own goroutine and answers its frames in order.
func (s *PipeServer) Serve(ctx context.Context) error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return nil
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("service: accept a pipe connection: %w", err)
		}
		if !s.track(conn) {
			_ = conn.Close()
			return nil
		}
		go s.serveConn(ctx, conn)
	}
}

// Close stops the listener and every open connection. Frames already read are
// answered first; a connection blocked in a read is unblocked by the close.
func (s *PipeServer) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.listener.Close()
		s.connsMu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.connsMu.Unlock()
	})
	return nil
}

// track registers a connection for Close. It returns false once the server is
// closing, so no new work starts during shutdown.
func (s *PipeServer) track(conn net.Conn) bool {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *PipeServer) untrack(conn net.Conn) {
	s.connsMu.Lock()
	delete(s.conns, conn)
	s.connsMu.Unlock()
}

// serveConn answers one connection's frames sequentially. Request and response
// frames on a connection never interleave, which is what makes concurrent
// clients safe: each connection's id/response pairing is its own stream.
func (s *PipeServer) serveConn(ctx context.Context, conn net.Conn) {
	defer func() {
		s.untrack(conn)
		_ = conn.Close()
	}()
	reader := bufio.NewReaderSize(conn, pipeReadBuffer)
	for {
		frame, err := readPipeFrame(reader, s.maxFrame)
		switch {
		case errors.Is(err, io.EOF):
			return
		case errors.Is(err, errPipeFrameTooLarge):
			_ = writePipeFrame(conn, s.api.rejectFrame("", "",
				APIErrorf(CodeInvalidRequest, "request frame exceeds %d bytes", s.maxFrame)))
			return
		case err != nil:
			// A client that disappears mid-frame is not an error worth logging.
			return
		}
		if len(frame) == 0 {
			continue
		}
		var release func()
		if s.beginReq != nil {
			release, err = s.beginReq()
			if err != nil {
				requestID, operation := peekRequestIdentity(frame)
				_ = writePipeFrame(conn, s.api.rejectFrame(requestID, operation,
					&APIError{Code: CodeUnavailable, Message: "the daemon is shutting down", Retryable: true}))
				return
			}
		}
		response := s.api.Handle(ctx, frame)
		if release != nil {
			release()
		}
		if err := writePipeFrame(conn, response); err != nil {
			return
		}
	}
}

// readPipeFrame reads one newline-terminated JSON frame. An oversized frame is
// rejected before the newline arrives, so the server never buffers without a
// bound.
func readPipeFrame(reader *bufio.Reader, max int) ([]byte, error) {
	frame := make([]byte, 0, 4096)
	for {
		chunk, err := reader.ReadSlice('\n')
		frame = append(frame, chunk...)
		if len(frame) > max+1 {
			return nil, errPipeFrameTooLarge
		}
		switch {
		case err == nil:
			frame = bytes.TrimSuffix(frame, []byte("\n"))
			frame = bytes.TrimSuffix(frame, []byte("\r"))
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

// writePipeFrame writes one response frame with the trailing newline.
func writePipeFrame(conn net.Conn, frame []byte) error {
	buffer := make([]byte, 0, len(frame)+1)
	buffer = append(buffer, frame...)
	buffer = append(buffer, '\n')
	_, err := conn.Write(buffer)
	return err
}

// pipeSecurityDescriptor builds an owner-only DACL: the current user and SYSTEM,
// with inheritance disabled. It is the only isolation available between Windows
// users on one machine, so the card forbids Everyone and friends.
func pipeSecurityDescriptor() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("service: open the current process token: %w", err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("service: read the current user: %w", err)
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", user.User.Sid.String()), nil
}
