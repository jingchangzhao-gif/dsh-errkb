package capture

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// ChannelC is the §3.1 level of a log tail: the process was not wrapped, so
// the only observable evidence is the file it writes.
const ChannelC = "C"

// DefaultTailMaxLineBytes bounds one line of a log before it is truncated.
// A binary file or a minified bundle can otherwise produce a single endless
// "line" and the tailer would keep it all in memory.
const DefaultTailMaxLineBytes = 8 << 10

const (
	defaultTailPollInterval = 200 * time.Millisecond
	defaultTailMaxBackoff   = 5 * time.Second
	defaultTailHold         = time.Second
	tailReadChunk           = 64 << 10
)

// Encoding is the one a tailed file was found to use. Windows leans on
// UTF-16LE for many logs (PowerShell's Out-File default), so the detector
// looks at the BOM first and then at the byte pattern.
type Encoding string

const (
	EncodingUTF8    Encoding = "utf-8"
	EncodingUTF16LE Encoding = "utf-16le"
	EncodingUTF16BE Encoding = "utf-16be"
)

// TailLine is one emitted burst: the text plus how many identical consecutive
// lines it stands for. A burst of five identical lines is emitted once with
// Count 5, so the total count stays exact without emitting five events.
type TailLine struct {
	Text  string
	Count int64
}

// TailOptions describes one tailed file.
type TailOptions struct {
	// Path is the log file to follow.
	Path string
	// PollInterval is the normal wait between polls.
	PollInterval time.Duration
	// MaxBackoff caps the wait while the file is missing or idle.
	MaxBackoff time.Duration
	// DuplicateHold bounds how long a pending burst may wait for a different
	// line before it is flushed anyway.
	DuplicateHold time.Duration
	// MaxLineBytes truncates a single line before it becomes an event.
	MaxLineBytes int
	// ProjectKey, ConnectorID and SessionID identify the persisted rows. All
	// three are required when Recorder is set.
	ProjectKey  string
	ConnectorID string
	SessionID   string
	// Recorder persists classified failures. Nil keeps the tailer observe-only.
	Recorder func(context.Context, contract.ErrorEvent) (database.RecordResult, error)
	// Sink observes every emitted burst, including lines that are not recorded.
	Sink func(TailLine)
}

// TailStats is the observable state of a tailer. It never drops a line
// silently: every line read lands in LinesRead and every non-recorded one in
// Skipped or RecordErrors.
type TailStats struct {
	Polls         int64
	BytesRead     int64
	LinesRead     int64
	InvalidBytes  int64
	Rotations     int64
	Truncations   int64
	Emissions     int64
	Recorded      int64
	Skipped       int64
	RecordErrors  int64
	Encoding      Encoding
	LastPollError string
}

// Tailer follows one file. Poll is safe to call repeatedly; Run adds the
// polling loop and the backoff. A Tailer is not safe for concurrent use.
type Tailer struct {
	options     TailOptions
	fileInfo    os.FileInfo
	offset      int64
	encoding    Encoding
	detected    bool
	rawBuffer   []byte
	textBuffer  string
	pending     *tailPending
	readBuffer  []byte
	lastPollErr string
	stats       TailStats
}

type tailPending struct {
	text  string
	count int64
	since time.Time
}

// NewTailer validates the options and returns a tailer. It does not touch the
// file yet: the first Poll opens it.
func NewTailer(options TailOptions) (*Tailer, error) {
	if strings.TrimSpace(options.Path) == "" {
		return nil, errors.New("tail: a log path is required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultTailPollInterval
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = defaultTailMaxBackoff
	}
	if options.DuplicateHold <= 0 {
		options.DuplicateHold = defaultTailHold
	}
	if options.MaxLineBytes <= 0 {
		options.MaxLineBytes = DefaultTailMaxLineBytes
	}
	if options.Recorder != nil {
		if options.ProjectKey == "" {
			return nil, errors.New("tail: a project key is required when recording")
		}
		if options.ConnectorID == "" || options.SessionID == "" {
			return nil, errors.New("tail: a connector and a session are required when recording")
		}
	}
	return &Tailer{options: options, readBuffer: make([]byte, tailReadChunk)}, nil
}

// Stats returns a snapshot of the counters.
func (tailer *Tailer) Stats() TailStats {
	stats := tailer.stats
	stats.Encoding = tailer.encoding
	stats.LastPollError = tailer.lastPollErr
	return stats
}

// Poll reads whatever is new, emits complete bursts and flushes a pending
// burst if it has waited longer than DuplicateHold. A missing file is not an
// error: it is the normal state right before a log is created, and Run backs
// off instead of busy-looping.
func (tailer *Tailer) Poll(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tailer.stats.Polls++
	emitted, err := tailer.pollOnce(ctx)
	if err != nil {
		tailer.lastPollErr = err.Error()
		return emitted, err
	}
	tailer.lastPollErr = ""
	if tailer.pending != nil && time.Since(tailer.pending.since) >= tailer.options.DuplicateHold {
		emitted += tailer.flushPending(ctx)
	}
	return emitted, nil
}

func (tailer *Tailer) pollOnce(ctx context.Context) (int, error) {
	// Stat through the open handle, not through the path: on Windows only the
	// handle carries the file ID, and the path alone cannot tell a rotated
	// (renamed away, recreated) file from the same file that grew.
	file, err := os.Open(tailer.options.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("tail %s: open: %w", tailer.options.Path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("tail %s: inspect: %w", tailer.options.Path, err)
	}
	rotated := tailer.fileInfo != nil && !os.SameFile(tailer.fileInfo, info)
	if rotated {
		tailer.resetFile()
		tailer.stats.Rotations++
	}
	if tailer.fileInfo == nil {
		tailer.fileInfo = info
	}
	if info.Size() < tailer.offset {
		// The same file got shorter: it was truncated or rewritten in place.
		// Reading at the old offset would miss everything until it grows again.
		tailer.resetFile()
		tailer.stats.Truncations++
	}

	n, readErr := file.ReadAt(tailer.readBuffer, tailer.offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return 0, fmt.Errorf("tail %s: read: %w", tailer.options.Path, readErr)
	}
	if n == 0 {
		return 0, nil
	}
	tailer.offset += int64(n)
	tailer.stats.BytesRead += int64(n)
	tailer.rawBuffer = append(tailer.rawBuffer, tailer.readBuffer[:n]...)

	text, invalid := tailer.decodeAvailable(false)
	tailer.stats.InvalidBytes += invalid
	emitted := 0
	if text != "" {
		emitted = tailer.consumeText(ctx, text)
	}
	return emitted, nil
}

// Run polls until the context ends. Between polls it backs off while the file
// is idle or missing, so a tailer for a log that never appears does not spin.
func (tailer *Tailer) Run(ctx context.Context) error {
	interval := tailer.options.PollInterval
	for {
		before := tailer.stats.BytesRead
		if _, err := tailer.Poll(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				tailer.flushPending(context.Background())
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			tailer.flushPending(context.Background())
			return nil
		case <-time.After(interval):
		}
		if tailer.stats.BytesRead == before {
			interval *= 2
			if interval > tailer.options.MaxBackoff {
				interval = tailer.options.MaxBackoff
			}
		} else {
			interval = tailer.options.PollInterval
		}
	}
}

// Flush emits the pending burst immediately.
func (tailer *Tailer) Flush() int {
	return tailer.flushPending(context.Background())
}

// resetFile forgets every per-file byte so a new (rotated, truncated or
// replaced) file is read from its own beginning, including its encoding.
func (tailer *Tailer) resetFile() {
	tailer.fileInfo = nil
	tailer.offset = 0
	tailer.encoding = ""
	tailer.detected = false
	tailer.rawBuffer = nil
	tailer.textBuffer = ""
}

// decodeAvailable detects the encoding once per file and turns raw bytes into
// text, keeping whatever cannot be decoded yet for the next poll.
func (tailer *Tailer) decodeAvailable(atEOF bool) (string, int64) {
	var invalid int64
	if !tailer.detected {
		if len(tailer.rawBuffer) < 2 {
			return "", 0
		}
		encoding, skip := detectTailEncoding(tailer.rawBuffer)
		tailer.encoding = encoding
		tailer.detected = true
		tailer.stats.Encoding = encoding
		tailer.rawBuffer = tailer.rawBuffer[skip:]
	}
	var text string
	var consumed int
	switch tailer.encoding {
	case EncodingUTF16LE:
		text, consumed, invalid = decodeUTF16Tail(tailer.rawBuffer, binary.LittleEndian, atEOF)
	case EncodingUTF16BE:
		text, consumed, invalid = decodeUTF16Tail(tailer.rawBuffer, binary.BigEndian, atEOF)
	default:
		text, consumed, invalid = decodeUTF8Tail(tailer.rawBuffer, atEOF)
	}
	tailer.rawBuffer = append(tailer.rawBuffer[:0], tailer.rawBuffer[consumed:]...)
	return text, invalid
}

// consumeText splits complete lines out of the decoded text and keeps the
// trailing partial line for the next poll.
func (tailer *Tailer) consumeText(ctx context.Context, text string) int {
	tailer.textBuffer += text
	emitted := 0
	for {
		index := strings.IndexByte(tailer.textBuffer, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(tailer.textBuffer[:index], "\r")
		tailer.textBuffer = tailer.textBuffer[index+1:]
		emitted += tailer.acceptLine(ctx, line)
	}
	// A file without newlines (or a binary one) could otherwise grow the
	// partial line forever; keep only the tail that could still become a line.
	if len(tailer.textBuffer) > 2*tailer.options.MaxLineBytes {
		tailer.textBuffer = tailer.textBuffer[len(tailer.textBuffer)-tailer.options.MaxLineBytes:]
	}
	return emitted
}

// acceptLine merges a line into the pending burst. Identical consecutive
// lines are counted, not emitted: that is the bounded duplicate suppression.
func (tailer *Tailer) acceptLine(ctx context.Context, line string) int {
	if strings.TrimSpace(line) == "" {
		return 0
	}
	tailer.stats.LinesRead++
	line = truncateLine(line, tailer.options.MaxLineBytes)
	if tailer.pending != nil && tailer.pending.text == line {
		tailer.pending.count++
		return 0
	}
	emitted := tailer.flushPending(ctx)
	tailer.pending = &tailPending{text: line, count: 1, since: time.Now()}
	return emitted
}

// flushPending emits the pending burst, if any.
func (tailer *Tailer) flushPending(ctx context.Context) int {
	if tailer.pending == nil {
		return 0
	}
	pending := tailer.pending
	tailer.pending = nil
	tailer.emit(ctx, pending.text, pending.count)
	return 1
}

func (tailer *Tailer) emit(ctx context.Context, line string, count int64) {
	tailer.stats.Emissions++
	if tailer.options.Sink != nil {
		tailer.options.Sink(TailLine{Text: line, Count: count})
	}
	tailer.record(ctx, line, count)
}

// record classifies the burst and hands a validated event to the recorder.
// Lines that look like ordinary log noise (classification unknown) and
// transient failures stay out of the permanent record path.
func (tailer *Tailer) record(ctx context.Context, line string, count int64) {
	if tailer.options.Recorder == nil {
		return
	}
	// Source "log": unlike a wrapped command there is no exit code, so the
	// category comes from the line's own evidence. Source "command" would
	// force every line into the build class.
	classification := core.Classify(core.ClassifyInput{Source: "log", Message: line})
	if classification.Category == core.CategoryUnknown || classification.Transient {
		tailer.stats.Skipped++
		return
	}
	headline := core.ExtractHeadline(line)
	redactedLine := redact.Text(headline.Line, redact.Public)
	normalized := core.Normalize(redactedLine)
	fingerprint := core.Signature(commandCategory, normalized)
	summary := summarizeEnvironment()
	severity := classification.Severity
	transient := false
	confidence := 0.5
	evidence := "log tail " + redact.Text(filepath.Base(tailer.options.Path), redact.Public)
	if count > 1 {
		evidence += fmt.Sprintf(" (repeated %d times)", count)
	}
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           newEventID(),
		ConnectorID:       tailer.options.ConnectorID,
		SessionID:         tailer.options.SessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           ChannelC,
		Category:          commandCategory,
		Severity:          string(severity),
		Transient:         &transient,
		Headline:          redactedLine,
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(line, redact.Public),
		ErrorCode:         headline.Code,
		OS:                summary.OS,
		Shell:             summary.Shell,
		RuntimeVersions:   summary.RuntimeVersions,
		ProjectKey:        tailer.options.ProjectKey,
		Fingerprint:       fingerprint,
		Evidence:          evidence,
		Confidence:        &confidence,
	}
	if err := event.Validate(); err != nil {
		tailer.stats.RecordErrors++
		return
	}
	if _, err := tailer.options.Recorder(ctx, event); err != nil {
		tailer.stats.RecordErrors++
		return
	}
	tailer.stats.Recorded++
}

// detectTailEncoding looks for a BOM first and then for the NUL-byte pattern
// of UTF-16 without a BOM (ASCII text in UTF-16 is half NUL bytes). Anything
// else is treated as UTF-8, and invalid bytes are replaced instead of failing.
func detectTailEncoding(raw []byte) (Encoding, int) {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return EncodingUTF8, 3
	}
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		return EncodingUTF16LE, 2
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return EncodingUTF16BE, 2
	}
	if len(raw) < 16 {
		return EncodingUTF8, 0
	}
	sample := raw
	if len(sample) > 512 {
		sample = sample[:512]
	}
	pairs := 0
	evenZeros := 0
	oddZeros := 0
	for index := 0; index+1 < len(sample); index += 2 {
		pairs++
		if sample[index] == 0 {
			evenZeros++
		}
		if sample[index+1] == 0 {
			oddZeros++
		}
	}
	if pairs > 0 {
		if oddZeros*4 > pairs && evenZeros*8 < pairs {
			return EncodingUTF16LE, 0
		}
		if evenZeros*4 > pairs && oddZeros*8 < pairs {
			return EncodingUTF16BE, 0
		}
	}
	return EncodingUTF8, 0
}

// decodeUTF8Tail decodes as much as possible and stops at an incomplete
// multibyte rune so the next poll can finish it.
func decodeUTF8Tail(raw []byte, atEOF bool) (string, int, int64) {
	var builder strings.Builder
	var invalid int64
	index := 0
	for index < len(raw) {
		r, size := utf8.DecodeRune(raw[index:])
		if r == utf8.RuneError && size == 1 {
			if !atEOF && incompleteUTF8(raw[index:]) {
				break
			}
			builder.WriteRune(utf8.RuneError)
			invalid++
			index++
			continue
		}
		builder.WriteRune(r)
		index += size
	}
	return builder.String(), index, invalid
}

func incompleteUTF8(rest []byte) bool {
	lead := rest[0]
	need := 0
	switch {
	case lead&0x80 == 0:
		return false
	case lead&0xE0 == 0xC0:
		need = 2
	case lead&0xF0 == 0xE0:
		need = 3
	case lead&0xF8 == 0xF0:
		need = 4
	default:
		return false
	}
	return len(rest) < need
}

// decodeUTF16Tail decodes complete UTF-16 code units. A dangling high
// surrogate or an odd trailing byte waits for the next poll instead of being
// decoded into a wrong character.
func decodeUTF16Tail(raw []byte, order binary.ByteOrder, atEOF bool) (string, int, int64) {
	units := make([]uint16, 0, len(raw)/2)
	index := 0
	for index+1 < len(raw) {
		units = append(units, order.Uint16(raw[index:]))
		index += 2
	}
	var builder strings.Builder
	var invalid int64
	consumedUnits := 0
	for consumedUnits < len(units) {
		unit := units[consumedUnits]
		if !utf16.IsSurrogate(rune(unit)) {
			builder.WriteRune(rune(unit))
			consumedUnits++
			continue
		}
		if unit >= 0xD800 && unit <= 0xDBFF {
			if consumedUnits+1 >= len(units) {
				if atEOF {
					builder.WriteRune(utf8.RuneError)
					invalid++
					consumedUnits++
					continue
				}
				break
			}
			r := utf16.DecodeRune(rune(unit), rune(units[consumedUnits+1]))
			if r == utf8.RuneError {
				invalid++
			}
			builder.WriteRune(r)
			consumedUnits += 2
			continue
		}
		// A low surrogate without its high half.
		builder.WriteRune(utf8.RuneError)
		invalid++
		consumedUnits++
	}
	if atEOF && index < len(raw) {
		invalid++
	}
	return builder.String(), consumedUnits * 2, invalid
}

// truncateLine keeps a line bounded, counting runes so a multibyte character
// is never cut in half. A multibyte line can therefore stay up to about four
// times MaxLineBytes, which is still bounded.
func truncateLine(line string, maxBytes int) string {
	runes := []rune(line)
	if len(runes) <= maxBytes {
		return line
	}
	return string(runes[:maxBytes]) + "…"
}
