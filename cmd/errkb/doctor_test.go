//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// doctorTestArguments points the diagnosis at a closed port so the CLI tests
// exercise the failing path without needing a database.
func doctorTestArguments(t *testing.T) []string {
	t.Helper()
	return []string{
		"--dsn", "postgres://postgres@127.0.0.1:1/errkb?sslmode=disable&connect_timeout=1",
		"--migrations", "../../db/migrations",
		"--config", filepath.Join(t.TempDir(), "missing.yml"),
		"--queue-dir", filepath.Join(t.TempDir(), "queue"),
		"--pipe", `\\.\pipe\errkb-cli-doctor-test`,
	}
}

func TestDoctorJSONMatchesTheReportContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := append([]string{"doctor", "--json"}, doctorTestArguments(t)...)
	code := run(args, &stdout, &stderr)
	if code != exitInternal {
		t.Fatalf("exit code is %d, want %d (a failing diagnosis): %s", code, exitInternal, stderr.String())
	}

	var report service.DoctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode the JSON report: %v (%s)", err, stdout.String())
	}
	if len(report.Checks) != 9 {
		t.Fatalf("the JSON report has %d checks, want 9", len(report.Checks))
	}
	if report.Status != service.DoctorFailing {
		t.Fatalf("the JSON status is %q, want failing", report.Status)
	}
	if report.Health == nil {
		t.Fatal("the JSON report does not embed the health document")
	}
	if report.Version != version || report.GOOS == "" || report.GOARCH == "" {
		t.Fatalf("the JSON report does not carry the build identity: %+v", report)
	}
}

func TestDoctorHumanOutputCarriesTheFixHints(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := append([]string{"doctor"}, doctorTestArguments(t)...)
	code := run(args, &stdout, &stderr)
	if code != exitInternal {
		t.Fatalf("exit code is %d, want %d", code, exitInternal)
	}
	text := stdout.String()
	for _, fragment := range []string{"errkb doctor", "status: failing", "database", "fix:"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("the human output lacks %q:\n%s", fragment, text)
		}
	}
}
