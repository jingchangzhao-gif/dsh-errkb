//go:build windows

// The `errkb run` wrapper (EKB-031) plus the closed loop the product exists for
// (EKB-035): run the command with live output, record a non-transient failure
// through the daemon, then ask for the known fix and print it.
//
// The CLI adds no judgement: the event is built by internal/capture, the match
// comes from the database, and only an exact fingerprint match may be reused.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// runOutput is the --json shape of one wrapped run. It is a CLI envelope, not
// a contract type: the record and lookup members are the EKB-008 payloads
// unmodified so scripts still parse them with the same reader.
type runOutput struct {
	ExitCode    int                      `json:"exit_code"`
	Record      *contract.RecordResponse `json:"record,omitempty"`
	Lookup      *contract.LookupResponse `json:"lookup,omitempty"`
	RecordError string                   `json:"record_error,omitempty"`
}

// runRun implements `errkb run [flags] -- <command> [args...]`.
func runRun(args []string, jsonMode bool, stdout, stderr io.Writer, stdin io.Reader, env *cliEnv) int {
	split := 0
	for index, arg := range args {
		if arg == "--" {
			split = index
			break
		}
	}
	if split == 0 {
		fmt.Fprintln(stderr, "errkb run: the command must follow `--`, e.g. errkb run -- pnpm build")
		return exitUsage
	}
	flagArgs, command := args[:split], args[split+1:]
	if len(command) == 0 {
		fmt.Fprintln(stderr, "errkb run: a command is required after `--`")
		return exitUsage
	}

	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	connector := flags.String("connector", "", "connector id the event belongs to (required)")
	session := flags.String("session", "", "session id the event belongs to (required)")
	project := flags.String("project", "default", "project key the issue is scoped to")
	captureBytes := flags.Int("capture-bytes", 0, "retained bytes per stream (default 65536)")
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(flags.Args()) != 0 {
		fmt.Fprintln(stderr, "errkb run: flags belong before `--`")
		return exitUsage
	}
	if strings.TrimSpace(*connector) == "" || strings.TrimSpace(*session) == "" {
		fmt.Fprintln(stderr, "errkb run: --connector and --session are required (the daemon needs rows to hang the event on)")
		return exitUsage
	}

	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()

	record := func(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
		// The wrapper speaks the connector capture operation (EKB-038a): connector
		// and session are identities the core creates on first use, and the core
		// owns the redaction/fingerprint pipeline. Sending the finished event over
		// `record` would require the CLI to know UUIDs it never has.
		response, err := client.Capture(ctx, contract.CaptureRequest{
			SchemaVersion: contract.ConnectorSDKVersion,
			ConnectorID:   event.ConnectorID,
			SessionID:     event.SessionID,
			// The wrapper is capture channel B (§3.1); the capture operation would
			// otherwise default to the hook channel A.
			Channel: "B",
			Observation: contract.ConnectorObservation{
				ObservedAt:      event.ObservedAt,
				Source:          "command-exit",
				Message:         event.NormalizedMessage,
				ErrorCode:       event.ErrorCode,
				Command:         event.Command,
				ExitCode:        event.ExitCode,
				ToolName:        event.ToolName,
				OS:              event.OS,
				Shell:           event.Shell,
				RuntimeVersions: event.RuntimeVersions,
				ProjectKey:      event.ProjectKey,
				Evidence:        event.Evidence,
				Confidence:      event.Confidence,
			},
		})
		if err != nil {
			return database.RecordResult{}, err
		}
		return database.RecordResult{
			EventID:      response.EventID,
			OccurrenceID: response.OccurrenceID,
			IssueID:      response.IssueID,
			Fingerprint:  response.Fingerprint,
			Created:      response.Created,
		}, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := capture.Run(runCtx, capture.RunOptions{
		Command:      command,
		Env:          os.Environ(),
		Stdin:        stdin,
		Stdout:       stdout,
		Stderr:       stderr,
		CaptureLimit: *captureBytes,
		ProjectKey:   *project,
		ConnectorID:  *connector,
		SessionID:    *session,
		Recorder:     record,
	})
	if err != nil {
		fmt.Fprintf(stderr, "errkb run: %v\n", err)
		return exitInternal
	}

	output := runOutput{ExitCode: result.ExitCode, RecordError: result.RecordError}
	if result.Recorded != nil {
		output.Record = &contract.RecordResponse{
			SchemaVersion: contract.ToolSchemaVersion,
			EventID:       result.Recorded.EventID,
			OccurrenceID:  result.Recorded.OccurrenceID,
			IssueID:       result.Recorded.IssueID,
			Fingerprint:   result.Recorded.Fingerprint,
			Created:       result.Recorded.Created,
		}
	}

	if result.Recorded != nil {
		ctx, cancelLookup := context.WithTimeout(context.Background(), cliTimeout)
		lookup, lookupErr := client.Lookup(ctx, contract.LookupRequest{
			SchemaVersion: contract.ToolSchemaVersion,
			Fingerprint:   result.Recorded.Fingerprint,
			ProjectKey:    *project,
		})
		cancelLookup()
		if lookupErr == nil {
			output.Lookup = &lookup
		} else {
			fmt.Fprintf(stderr, "errkb run: the command failed and was recorded, but the lookup failed: %v\n", lookupErr)
		}
	}

	if jsonMode {
		if code := env.writeJSON(output); code != exitOK {
			return result.ExitCode
		}
		return result.ExitCode
	}

	switch {
	case result.Transient:
		fmt.Fprintf(stdout, "errkb: failure looks transient (%s); not recorded, rerun when the service recovers\n", result.Classification.Category)
	case result.Recorded == nil && result.RecordError != "":
		fmt.Fprintf(stderr, "errkb: the command failed but the event was not recorded: %s\n", result.RecordError)
	case result.Recorded == nil && result.ExitCode != 0:
		fmt.Fprintln(stdout, "errkb: the command failed but no event was produced")
	}

	if result.Recorded != nil {
		fmt.Fprintf(stdout, "\nerrkb: failure recorded as issue %s (%s)\n", result.Recorded.IssueID, result.Recorded.Fingerprint)
		if output.Lookup != nil {
			if len(output.Lookup.Candidates) == 0 {
				fmt.Fprintln(stdout, "errkb: no known fix yet")
			} else {
				fmt.Fprintln(stdout, "errkb: candidates from the knowledge base")
				if code := env.renderCandidates(output.Lookup.Candidates, nil); code != exitOK && code != exitNotFound {
					return code
				}
			}
		}
	}
	return result.ExitCode
}
