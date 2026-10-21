package webshell

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

func writeServer(t *testing.T) *Server {
	t.Helper()
	server := detailServer(t, nil)
	server.EnableWrites(
		func(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
			return contract.RecordSolutionResponse{
				SchemaVersion:     "v1",
				SolutionID:        "solution-1",
				SolutionVersionID: "version-1",
				Version:           2,
				Recorded:          true,
			}, nil
		},
		func(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
			return contract.SetStatusResponse{SchemaVersion: "v1", IssueID: request.IssueID, Status: request.Status}, nil
		},
	)
	return server
}

func TestVersionWritePassesThrough(t *testing.T) {
	server := writeServer(t)
	var seen contract.RecordSolutionRequest
	server.EnableWrites(
		func(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
			seen = request
			return contract.RecordSolutionResponse{SchemaVersion: "v1", SolutionID: "s", SolutionVersionID: "v", Version: 2, Recorded: true}, nil
		},
		func(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
			return contract.SetStatusResponse{}, nil
		},
	)
	body := `{"body":"install the workspace deps","preconditions":"a clean checkout","source":"toolbox"}`
	request := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/versions", strings.NewReader(body))
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", response.Code, response.Body.String())
	}
	if seen.IssueID != "issue-1" || seen.Body != "install the workspace deps" || seen.Source != "toolbox" {
		t.Fatalf("daemon saw an unexpected request: %+v", seen)
	}
}

func TestIssueStatusWritePassesThrough(t *testing.T) {
	server := writeServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/status", strings.NewReader(`{"status":"wontfix"}`))
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"wontfix"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestWriteRejectsUnknownFields(t *testing.T) {
	server := writeServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/status", strings.NewReader(`{"status":"resolved","future":true}`))
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", response.Code)
	}
}

func TestWriteNeedsTheWiring(t *testing.T) {
	server := detailServer(t, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/status", strings.NewReader(`{"status":"resolved"}`))
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", response.Code)
	}
}

func TestWriteReportsTheCoreError(t *testing.T) {
	server := writeServer(t)
	server.EnableWrites(
		func(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
			return contract.RecordSolutionResponse{}, errors.New("invalid: solution.body: required")
		},
		func(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
			return contract.SetStatusResponse{}, nil
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/api/issues/issue-1/versions", strings.NewReader(`{"body":""}`))
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", response.Code, response.Body.String())
	}
}
