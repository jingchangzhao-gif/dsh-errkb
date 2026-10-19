//go:build windows

// The errkb mcp subcommand (EKB-037): MCP over stdio for hosts that speak the
// protocol. stdout carries protocol frames only; every diagnostic goes to
// stderr because one stray byte on stdout breaks the client's message stream.
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/mcp"
)

// pipeBackend answers the six MCP tools over the local daemon pipe. It dials
// per call: a long-lived MCP session must survive a daemon restart, and the
// pipe client closes with each call, which is also how the CLI talks.
type pipeBackend struct {
	dial func(context.Context) (apiClient, error)
}

var _ mcp.Backend = pipeBackend{}

func (b pipeBackend) with(ctx context.Context, call func(context.Context, apiClient) error) error {
	client, err := b.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return call(ctx, client)
}

func (b pipeBackend) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	var response contract.LookupResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Lookup(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	var response contract.SearchResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Search(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	var response contract.RecordResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Record(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	var response contract.FeedbackResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Feedback(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	var response contract.ListResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.List(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	var response contract.StatsResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Stats(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

// runMCP serves MCP over stdio until the host closes the stream.
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(stderr, "usage: errkb mcp   (MCP over stdio for LLM hosts; logs go to stderr)")
			return exitOK
		default:
			fmt.Fprintf(stderr, "errkb mcp: unknown argument %q\n", arg)
			return exitUsage
		}
	}
	server := mcp.NewServer(stdin, stdout, stderr, pipeBackend{dial: dialClient}, version)
	if err := server.Serve(context.Background()); err != nil {
		// Serve already wrote protocol errors to stdout; this line explains the
		// transport failure on stderr without touching the frame stream.
		fmt.Fprintf(stderr, "errkb mcp: %v\n", err)
		return exitInternal
	}
	return exitOK
}
