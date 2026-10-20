//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

func TestClientFailsFastWithoutDaemon(t *testing.T) {
	started := time.Now()
	client, err := DialPipe(context.Background(), newTestPipeName())
	if err == nil {
		_ = client.Close()
		t.Fatalf("DialPipe on a pipe nobody listens on returned no error")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("DialPipe error = %v, want one that wraps ErrServiceUnavailable", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("DialPipe took %s to report a missing daemon; the CLI must fail fast", elapsed)
	}
}

func TestClientCallsEveryOperation(t *testing.T) {
	backend := &fakeBackend{}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	client, err := DialPipe(context.Background(), name)
	if err != nil {
		t.Fatalf("DialPipe: %v", err)
	}
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	if _, err := client.Lookup(ctx, lookupPayload("0000000000aa")); err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if _, err := client.Search(ctx, searchPayload("boom")); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := client.Record(ctx, recordPayload("11111111-1111-4111-8111-111111111111")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := client.Feedback(ctx, feedbackPayload("note-1")); err != nil {
		t.Fatalf("Feedback: %v", err)
	}
	if _, err := client.List(ctx, listPayload("")); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := client.Stats(ctx, statsPayload()); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := []string{"lookup", "search", "record", "feedback", "list", "stats"}
	if got := append([]string(nil), backend.operations...); !equalStrings(got, want) {
		t.Fatalf("backend saw %v, want %v", got, want)
	}
}

func TestClientMapsBusinessErrors(t *testing.T) {
	backend := &fakeBackend{err: APIErrorf(CodeNotFound, "no such issue")}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	client, err := DialPipe(context.Background(), name)
	if err != nil {
		t.Fatalf("DialPipe: %v", err)
	}
	defer func() { _ = client.Close() }()
	_, err = client.Lookup(context.Background(), lookupPayload("0000000000aa"))
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("Lookup error = %v, want a *CallError", err)
	}
	if callErr.Code != CodeNotFound {
		t.Fatalf("CallError.Code = %q, want %q", callErr.Code, CodeNotFound)
	}
	if !strings.Contains(callErr.Error(), "no such issue") {
		t.Fatalf("CallError.Error() = %q, want the daemon message", callErr.Error())
	}
}

func TestClientRejectsADaemonThatBreaksTheContract(t *testing.T) {
	// The server re-validates what a backend hands it (EKB-029); a backend that
	// returns a wrong envelope version therefore surfaces as an internal error
	// instead of reaching the CLI as if it were valid.
	backend := &fakeBackend{lookup: &contract.LookupResponse{SchemaVersion: "v2"}}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	client, err := DialPipe(context.Background(), name)
	if err != nil {
		t.Fatalf("DialPipe: %v", err)
	}
	defer func() { _ = client.Close() }()
	_, err = client.Lookup(context.Background(), lookupPayload("0000000000aa"))
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != CodeInternal {
		t.Fatalf("Lookup error = %v, want an internal *CallError", err)
	}
}

func TestClientSerializesConcurrentCalls(t *testing.T) {
	backend := &fakeBackend{}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	client, err := DialPipe(context.Background(), name)
	if err != nil {
		t.Fatalf("DialPipe: %v", err)
	}
	defer func() { _ = client.Close() }()
	var wait sync.WaitGroup
	failures := make(chan error, 40)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for call := 0; call < 5; call++ {
				note := fmt.Sprintf("note-%d-%d", worker, call)
				response, err := client.Feedback(context.Background(), feedbackPayload(note))
				if err != nil {
					failures <- fmt.Errorf("worker %d call %d: %w", worker, call, err)
					return
				}
				if response.OutcomeID != note {
					failures <- fmt.Errorf("worker %d call %d got outcome %q, want its own note %q", worker, call, response.OutcomeID, note)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if len(backend.operations) != 40 {
		t.Fatalf("backend answered %d calls, want 40", len(backend.operations))
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
