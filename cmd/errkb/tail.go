//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// runTail follows a log file and records the failures it contains through the
// daemon (channel C). The file mechanics (rotation, truncation, duplicate
// bursts, UTF-16 detection) live in capture.Tailer; this command only supplies
// the pipe recorder and the flags.
func runTail(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tail", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "log file to follow")
	connector := flags.String("connector", "log", "connector row name for the captured rows")
	session := flags.String("session", "tail", "session row name for the captured rows")
	project := flags.String("project", "default", "project key for the captured rows")
	poll := flags.Duration("poll", 200*time.Millisecond, "poll interval")
	once := flags.Bool("once", false, "stop after the first recorded failure")
	timeout := flags.Duration("timeout", 0, "stop after this duration (0 = run until interrupted)")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *path == "" {
		fmt.Fprintln(stderr, "errkb tail: --file is required")
		return exitUsage
	}
	absolutePath, err := filepath.Abs(*path)
	if err != nil {
		fmt.Fprintf(stderr, "errkb tail: resolve the log path: %v\n", err)
		return exitInternal
	}

	env := &cliEnv{json: false, stdout: stdout, stderr: stderr, stdin: os.Stdin, dial: dialClient}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()

	var recorded atomic.Int64
	var captureErrors atomic.Int64
	recorder := func(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
		message := event.RedactedSample
		if message == "" {
			message = event.Headline
		}
		response, err := client.Capture(ctx, contract.CaptureRequest{
			SchemaVersion: contract.ConnectorSDKVersion,
			ConnectorID:   *connector,
			SessionID:     *session,
			Channel:       capture.ChannelC,
			Observation: contract.ConnectorObservation{
				ObservedAt: time.Now().UTC().Format(time.RFC3339),
				Source:     "command-exit",
				Message:    message,
				ProjectKey: *project,
				Evidence:   event.Evidence,
			},
		})
		if err != nil {
			if captureErrors.Add(1) <= 3 {
				fmt.Fprintf(stderr, "tail: record: %v\n", err)
			}
			return database.RecordResult{}, err
		}
		reported := recorded.Add(1)
		if reported == 1 || !*once {
			fmt.Fprintf(stdout, "tail: recorded as issue %s (%s) from %s\n", response.IssueID, response.Fingerprint, filepath.Base(absolutePath))
		}
		return database.RecordResult{
			EventID:      response.EventID,
			OccurrenceID: response.OccurrenceID,
			IssueID:      response.IssueID,
			Fingerprint:  response.Fingerprint,
			Created:      response.Created,
		}, nil
	}
	tailer, err := capture.NewTailer(capture.TailOptions{
		Path:         absolutePath,
		PollInterval: *poll,
		ProjectKey:   *project,
		ConnectorID:  *connector,
		SessionID:    *session,
		Recorder:     recorder,
	})
	if err != nil {
		fmt.Fprintf(stderr, "errkb tail: %v\n", err)
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var deadline <-chan time.Time
	if *timeout > 0 {
		timer := time.NewTimer(*timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	report := func() {
		stats := tailer.Stats()
		fmt.Fprintf(stdout, "tail: %d line(s) read, %d recorded, %d skipped, %d error(s)\n", stats.LinesRead, stats.Recorded, stats.Skipped, stats.RecordErrors)
	}
	if *once {
		for {
			if _, err := tailer.Poll(ctx); err != nil {
				fmt.Fprintf(stderr, "errkb tail: %v\n", err)
				return exitInternal
			}
			if recorded.Load() > 0 {
				return exitOK
			}
			select {
			case <-ctx.Done():
				return exitOK
			case <-deadline:
				fmt.Fprintln(stderr, "errkb tail: no failure was recorded before the timeout")
				report()
				return exitInternal
			case <-time.After(*poll):
			}
		}
	}
	if deadline != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(*timeout))
		_ = deadline
		defer cancel()
	}
	if err := tailer.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "errkb tail: %v\n", err)
		report()
		return exitInternal
	}
	report()
	return exitOK
}
