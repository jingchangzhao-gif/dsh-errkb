package capture_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// tailRecorder stores every event handed to it and validates it first, so a
// recording bug shows up as a test failure instead of a stored row.
type tailRecorder struct {
	events []contract.ErrorEvent
	errs   []error
}

func (recorder *tailRecorder) record(_ context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
	if err := event.Validate(); err != nil {
		recorder.errs = append(recorder.errs, err)
		return database.RecordResult{}, err
	}
	recorder.events = append(recorder.events, event)
	return database.RecordResult{}, nil
}

// tailTestOptions keeps the duplicate window long enough that the test, not
// the clock, decides when a burst is emitted.
func tailTestOptions(t *testing.T, path string) capture.TailOptions {
	t.Helper()
	return capture.TailOptions{
		Path:          path,
		PollInterval:  10 * time.Millisecond,
		MaxBackoff:    40 * time.Millisecond,
		DuplicateHold: time.Hour,
		MaxLineBytes:  1024,
		ProjectKey:    "tail-project",
		ConnectorID:   "11111111-1111-4111-8111-111111111111",
		SessionID:     "22222222-2222-4222-8222-222222222222",
	}
}

// utf16Bytes encodes text with an optional BOM.
func utf16Bytes(text string, order binary.ByteOrder, bom bool) []byte {
	units := utf16.Encode([]rune(text))
	buffer := make([]byte, 0, len(units)*2+2)
	if bom {
		prefix := make([]byte, 2)
		order.PutUint16(prefix, 0xFEFF)
		buffer = append(buffer, prefix...)
	}
	for _, unit := range units {
		pair := make([]byte, 2)
		order.PutUint16(pair, unit)
		buffer = append(buffer, pair...)
	}
	return buffer
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func appendText(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("append %s: %v", path, err)
	}
}

// tailOnce polls and flushes one burst so a test does not depend on timers.
func tailOnce(t *testing.T, tailer *capture.Tailer) {
	t.Helper()
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	tailer.Flush()
}

func newTailer(t *testing.T, options capture.TailOptions) *capture.Tailer {
	t.Helper()
	tailer, err := capture.NewTailer(options)
	if err != nil {
		t.Fatalf("new tailer: %v", err)
	}
	return tailer
}

func TestTailDecodesUTF8WithAndWithoutBOM(t *testing.T) {
	t.Run("with BOM", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.log")
		writeBytes(t, path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("错误：构建失败\n")...))
		var lines []capture.TailLine
		options := tailTestOptions(t, path)
		options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
		tailer := newTailer(t, options)
		tailOnce(t, tailer)
		if len(lines) != 1 || lines[0].Text != "错误：构建失败" || lines[0].Count != 1 {
			t.Fatalf("lines = %+v, want the Chinese line decoded once", lines)
		}
		if stats := tailer.Stats(); stats.Encoding != capture.EncodingUTF8 || stats.LinesRead != 1 {
			t.Fatalf("stats = %+v, want utf-8 and one line", stats)
		}
	})
	t.Run("without BOM", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "app.log")
		writeBytes(t, path, []byte("EPERM: operation not permitted\n"))
		var lines []capture.TailLine
		options := tailTestOptions(t, path)
		options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
		tailer := newTailer(t, options)
		tailOnce(t, tailer)
		if len(lines) != 1 || lines[0].Text != "EPERM: operation not permitted" {
			t.Fatalf("lines = %+v", lines)
		}
	})
}

func TestTailDecodesUTF16WithBOM(t *testing.T) {
	text := "错误：构建失败\n第二行\n"
	for _, testCase := range []struct {
		name  string
		order binary.ByteOrder
		want  capture.Encoding
	}{
		{name: "little endian", order: binary.LittleEndian, want: capture.EncodingUTF16LE},
		{name: "big endian", order: binary.BigEndian, want: capture.EncodingUTF16BE},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "utf16.log")
			writeBytes(t, path, utf16Bytes(text, testCase.order, true))
			var lines []capture.TailLine
			options := tailTestOptions(t, path)
			options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
			tailer := newTailer(t, options)
			tailOnce(t, tailer)
			if len(lines) != 2 || lines[0].Text != "错误：构建失败" || lines[1].Text != "第二行" {
				t.Fatalf("lines = %+v, want the two decoded Chinese lines", lines)
			}
			if stats := tailer.Stats(); stats.Encoding != testCase.want || stats.InvalidBytes != 0 {
				t.Fatalf("stats = %+v, want %s and no invalid bytes", stats, testCase.want)
			}
		})
	}
}

func TestTailInfersUTF16WithoutBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-bom.log")
	writeBytes(t, path, utf16Bytes("error: build failed\n", binary.LittleEndian, false))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(lines) != 1 || lines[0].Text != "error: build failed" {
		t.Fatalf("lines = %+v", lines)
	}
	if stats := tailer.Stats(); stats.Encoding != capture.EncodingUTF16LE {
		t.Fatalf("encoding = %q, want utf-16le", stats.Encoding)
	}
}

func TestTailHandlesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("first line\nsecond line\n"))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(lines) != 2 {
		t.Fatalf("initial lines = %+v", lines)
	}
	lines = nil
	// A rewritten file is shorter than the offset: it has to be read from
	// the head again, otherwise the new first line would be missed.
	writeBytes(t, path, []byte("third line\n"))
	tailOnce(t, tailer)
	if len(lines) != 1 || lines[0].Text != "third line" {
		t.Fatalf("lines after truncation = %+v", lines)
	}
	if stats := tailer.Stats(); stats.Truncations != 1 {
		t.Fatalf("truncations = %d, want 1", stats.Truncations)
	}
}

func TestTailHandlesRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("old line\n"))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	lines = nil
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	writeBytes(t, path, []byte("new line\n"))
	tailOnce(t, tailer)
	if len(lines) != 1 || lines[0].Text != "new line" {
		t.Fatalf("lines after rotation = %+v, want only the new file", lines)
	}
	if stats := tailer.Stats(); stats.Rotations != 1 {
		t.Fatalf("rotations = %d, want 1", stats.Rotations)
	}
}

func TestTailBuffersHalfLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("error: build fail"))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	if _, err := tailer.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("a half line must not be emitted: %+v", lines)
	}
	appendText(t, path, "ed after linking\n")
	tailOnce(t, tailer)
	if len(lines) != 1 || lines[0].Text != "error: build failed after linking" {
		t.Fatalf("lines = %+v, want the joined line", lines)
	}
}

func TestTailSuppressesDuplicateBurstsWithExactCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("boom-1\nboom-1\nboom-1\nboom-1\nboom-1\nboom-2\n"))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(lines) != 2 {
		t.Fatalf("bursts = %+v, want two bursts", lines)
	}
	if lines[0].Text != "boom-1" || lines[0].Count != 5 {
		t.Fatalf("first burst = %+v, want boom-1 x5", lines[0])
	}
	if lines[1].Text != "boom-2" || lines[1].Count != 1 {
		t.Fatalf("second burst = %+v, want boom-2 x1", lines[1])
	}
	if stats := tailer.Stats(); stats.LinesRead != 6 || stats.Emissions != 2 {
		t.Fatalf("stats = %+v, want six lines read as two bursts", stats)
	}
}

func TestTailInvalidBytesDoNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.log")
	writeBytes(t, path, append([]byte{0x80, 0x81}, []byte(" error: build failed\n")...))
	var lines []capture.TailLine
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(lines) != 1 || !strings.Contains(lines[0].Text, "error: build failed") {
		t.Fatalf("lines = %+v", lines)
	}
	if stats := tailer.Stats(); stats.InvalidBytes < 2 {
		t.Fatalf("invalid bytes = %d, want at least 2", stats.InvalidBytes)
	}
}

func TestTailBacksOffWhenTheFileIsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet.log")
	tailer := newTailer(t, tailTestOptions(t, path))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := tailer.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	stats := tailer.Stats()
	if stats.Polls < 2 {
		t.Fatalf("polls = %d, want the loop to keep trying", stats.Polls)
	}
	if stats.Polls > 20 {
		t.Fatalf("polls = %d, want backoff instead of a busy loop", stats.Polls)
	}
}

func TestTailRecordsOnlyClassifiedFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("just an informational log line\nerror: build failed after linking\ndial tcp 127.0.0.1:5432: i/o timeout\n"))
	var lines []capture.TailLine
	recorder := &tailRecorder{}
	options := tailTestOptions(t, path)
	options.Sink = func(line capture.TailLine) { lines = append(lines, line) }
	options.Recorder = recorder.record
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(lines) != 3 {
		t.Fatalf("sink saw %+v, want every emitted burst", lines)
	}
	if len(recorder.errs) != 0 {
		t.Fatalf("validation errors: %v", recorder.errs)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("recorded %d events, want only the build failure", len(recorder.events))
	}
	event := recorder.events[0]
	if event.Channel != capture.ChannelC || event.Category != "command-exit" {
		t.Fatalf("channel/category = %q/%q", event.Channel, event.Category)
	}
	if len(event.Fingerprint) != 12 || strings.ToLower(event.Fingerprint) != event.Fingerprint {
		t.Fatalf("fingerprint = %q, want 12 lowercase hex characters", event.Fingerprint)
	}
	if event.Severity == "" || event.Severity == "0" {
		t.Fatalf("severity = %q", event.Severity)
	}
	if !strings.HasPrefix(event.Evidence, "log tail ") {
		t.Fatalf("evidence = %q, want the log-tail provenance", event.Evidence)
	}
	if stats := tailer.Stats(); stats.Recorded != 1 || stats.Skipped != 2 {
		t.Fatalf("stats = %+v, want one recorded and two skipped", stats)
	}
}

func TestTailRedactsSecretsBeforeRecording(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	secret := "Zq7" + "Xw2Lp9Rt4Vb8Nm3Kd6Hs1"
	writeBytes(t, path, []byte("error: build failed with password="+secret+"\n"))
	recorder := &tailRecorder{}
	options := tailTestOptions(t, path)
	options.Recorder = recorder.record
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	if len(recorder.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(recorder.events))
	}
	event := recorder.events[0]
	for field, value := range map[string]string{
		"headline":   event.Headline,
		"normalized": event.NormalizedMessage,
		"sample":     event.RedactedSample,
		"evidence":   event.Evidence,
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s still holds the secret: %q", field, value)
		}
	}
	if !strings.Contains(event.RedactedSample, "<secret>") {
		t.Fatalf("sample = %q, want the secret replaced", event.RedactedSample)
	}
}

func TestTailRecordsFailuresToTheDatabase(t *testing.T) {
	db := newCaptureDatabase(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	writeBytes(t, path, []byte("error: build failed after linking\n"))
	options := tailTestOptions(t, path)
	options.ConnectorID = db.connectorID
	options.SessionID = db.sessionID
	options.Recorder = func(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
		return database.RecordEvent(ctx, db.conn, event)
	}
	tailer := newTailer(t, options)
	tailOnce(t, tailer)
	ctx := context.Background()
	var events, issues int
	if err := db.conn.QueryRow(ctx, "select count(*) from events").Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := db.conn.QueryRow(ctx, "select count(*) from issues").Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if events != 1 || issues != 1 {
		t.Fatalf("events/issues = %d/%d, want 1/1", events, issues)
	}
}
