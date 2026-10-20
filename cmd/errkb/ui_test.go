//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpoolBacklogCountsEventFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"00000000000000000001-a.json", "00000000000000000002-b.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested.json"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	backlog, err := spoolBacklog(dir)
	if err != nil {
		t.Fatalf("spoolBacklog: %v", err)
	}
	if backlog != 2 {
		t.Fatalf("backlog = %d, want 2 (a directory named *.json is not a queued event)", backlog)
	}
}

func TestSpoolBacklogMissingDirectoryIsZero(t *testing.T) {
	backlog, err := spoolBacklog(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("spoolBacklog: %v", err)
	}
	if backlog != 0 {
		t.Fatalf("backlog = %d, want 0", backlog)
	}
}
