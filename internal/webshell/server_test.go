package webshell

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server, err := New(func(_ context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
		if request.Query == "boom" {
			return contract.SearchResponse{SchemaVersion: "v1", Candidates: []contract.Candidate{{
				IssueID: "issue-1", Fingerprint: "abcdefabcdef", Title: "boom", MatchReason: "exact_fingerprint", Approximate: false,
			}}}, nil
		}
		return contract.SearchResponse{SchemaVersion: "v1", Candidates: []contract.Candidate{}}, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server, httptest.NewServer(server.Handler())
}

func TestPageCarriesTheToken(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), server.Token()) {
		t.Fatal("the page does not carry the launch token")
	}
	if !strings.Contains(string(body), "/assets/app.js") {
		t.Fatal("the page does not load the embedded script")
	}
	if response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}
	if strings.Contains(string(body), "http://") || strings.Contains(string(body), "https://") {
		t.Fatal("the page references an external origin")
	}
}

func TestSearchRequiresTheToken(t *testing.T) {
	_, server := newTestServer(t)
	defer server.Close()
	response, err := http.Post(server.URL+"/api/search", "application/json", strings.NewReader(`{"query":"boom"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestSearchReturnsTheDaemonAnswer(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/search", strings.NewReader(`{"query":"boom"}`))
	request.Header.Set("X-Errkb-Token", server.Token())
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	var payload contract.SearchResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Candidates) != 1 || payload.Candidates[0].IssueID != "issue-1" {
		t.Fatalf("unexpected candidates: %+v", payload.Candidates)
	}
}

func TestSearchRejectsAnEmptyQuery(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/search", strings.NewReader(`{"query":"  "}`))
	request.Header.Set("X-Errkb-Token", server.Token())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestListenBindsLoopbackOnly(t *testing.T) {
	server, err := New(func(context.Context, contract.SearchRequest) (contract.SearchResponse, error) {
		return contract.SearchResponse{SchemaVersion: "v1"}, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listener, err := server.Listen(0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()
	if address := listener.Addr().String(); !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("listener address = %q, want loopback", address)
	}
	if !strings.Contains(server.URL(listener), "token="+server.Token()) {
		t.Fatal("the URL does not carry the token")
	}
	if strings.Contains(server.URL(listener), "0.0.0.0") {
		t.Fatal("the URL must not advertise a wildcard address")
	}
}
