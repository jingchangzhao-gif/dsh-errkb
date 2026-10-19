package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// The queue buffers events while PostgreSQL is unreachable. It is deliberately
// small and boring: one JSON file per event, written atomically, replayed
// through errkb_record_event, which is idempotent on event_id. The queue never
// decides whether a replay "already happened": deleting the file after a
// successful record is not a correctness requirement, because recording the
// same event_id twice is a no-op in the database (EKB-020). A crash between
// the database commit and the file delete therefore replays one event and
// loses nothing, which is exactly the crash-consistency property EKB-033 asks
// for.
const (
	// DefaultQueueMaxEvents caps the number of buffered events.
	DefaultQueueMaxEvents = 1000
	// DefaultQueueMaxBytes caps the bytes buffered on disk.
	DefaultQueueMaxBytes = 32 << 20
	// DefaultQueueMaxAge caps how long an event may wait for a replay.
	DefaultQueueMaxAge = 72 * time.Hour
	// DefaultQueuePollInterval is the first recovery-probe interval.
	DefaultQueuePollInterval = 2 * time.Second
	// DefaultQueueMaxBackoff caps the recovery-probe interval.
	DefaultQueueMaxBackoff = 30 * time.Second

	queueFileVersion = 1
	queueFileSuffix  = ".json"
	queueFilePath    = 0o600
	queueDirPerm     = 0o700
)

// QueueOptions configures an offline queue. Dir is required; the caps fall
// back to their defaults when they are zero or negative.
type QueueOptions struct {
	Dir       string
	MaxEvents int
	MaxBytes  int64
	MaxAge    time.Duration
	// Now is a test seam; production leaves it nil and uses time.Now.
	Now func() time.Time
}

// QueueStats reports what the queue did, so an operator can tell "the outage
// dropped events" from "the outage buffered them". Pending and Bytes describe
// the directory as of the last enqueue or replay.
type QueueStats struct {
	Enqueued     int64
	Replayed     int64
	Dropped      int64
	Expired      int64
	ReplayErrors int64
	Pending      int
	Bytes        int64
	LastError    string
}

// ReplayReport summarises one replay pass.
type ReplayReport struct {
	Replayed  int
	Remaining int
	Errors    int
}

// queuedEvent is the on-disk shape. EnqueuedAt is kept in the document rather
// than derived from the file system, because the file name only has to sort and
// because mtimes are not reliable enough to enforce the age cap.
type queuedEvent struct {
	Version    int                 `json:"version"`
	EnqueuedAt time.Time           `json:"enqueued_at"`
	Event      contract.ErrorEvent `json:"event"`
}

// Queue is an offline buffer of already-redacted events. It is safe for
// concurrent use; the file system is the only storage.
type Queue struct {
	dir       string
	maxEvents int
	maxBytes  int64
	maxAge    time.Duration
	now       func() time.Time

	mu    sync.Mutex
	stats QueueStats
}

// OpenQueue prepares the spool directory. Enqueue itself never blocks on a
// database or a network call: an unreachable database is the normal state that
// this type exists for, so the caller only pays one small file write.
func OpenQueue(options QueueOptions) (*Queue, error) {
	if strings.TrimSpace(options.Dir) == "" {
		return nil, errors.New("queue: a spool directory is required")
	}
	maxEvents := options.MaxEvents
	if maxEvents <= 0 {
		maxEvents = DefaultQueueMaxEvents
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultQueueMaxBytes
	}
	maxAge := options.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultQueueMaxAge
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(options.Dir, queueDirPerm); err != nil {
		return nil, fmt.Errorf("queue: create the spool directory: %w", err)
	}
	return &Queue{
		dir:       options.Dir,
		maxEvents: maxEvents,
		maxBytes:  maxBytes,
		maxAge:    maxAge,
		now:       now,
	}, nil
}

// Dir is the spool directory, for logging and health output.
func (q *Queue) Dir() string { return q.dir }

// Stats returns a copy of the counters. Pending and Bytes are refreshed by
// enqueue and replay, not by Stats itself.
func (q *Queue) Stats() QueueStats {
	if q == nil {
		return QueueStats{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stats
}

// Enqueue redacts the event, validates it and writes it to the spool
// directory. The redaction pass is the last gate before plaintext could reach
// the disk (§6): producers already redact, but the queue refuses to store an
// event that still looks sensitive instead of trusting its caller.
//
// It returns false when the same event_id is already buffered (a replay of the
// same event), which keeps the queue from growing on retries.
func (q *Queue) Enqueue(event contract.ErrorEvent) (bool, error) {
	if q == nil {
		return false, errors.New("queue: nil queue")
	}
	event = redactQueuedEvent(event)
	if err := event.Validate(); err != nil {
		return false, fmt.Errorf("queue: redact before enqueue: %w", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	names, err := q.list()
	if err != nil {
		return false, err
	}
	if queueContains(names, event.EventID) {
		q.refresh(names)
		return false, nil
	}

	now := q.now().UTC()
	payload, err := json.Marshal(queuedEvent{Version: queueFileVersion, EnqueuedAt: now, Event: event})
	if err != nil {
		return false, fmt.Errorf("queue: encode the event: %w", err)
	}
	path := filepath.Join(q.dir, queueFileName(now, event.EventID))
	if err := writeAtomically(path, payload); err != nil {
		// Disk full or a permission failure: report it fast and let the caller
		// log it, rather than retrying inside the capture path.
		return false, fmt.Errorf("queue: write the event: %w", err)
	}
	q.stats.Enqueued++

	names, err = q.list()
	if err != nil {
		return true, err
	}
	if err := q.sweep(now, names); err != nil {
		return true, err
	}
	return true, nil
}

// Replay re-records every buffered event through the recorder, oldest first,
// and deletes each file the database accepted. Recording is idempotent on the
// database side, so an interrupted pass (or a crash between commit and delete)
// replays at most one event and duplicates nothing.
//
// A failing recorder stops the pass: the database is still unreachable, and
// the rest of the queue must survive for the next probe.
func (q *Queue) Replay(ctx context.Context, recorder func(context.Context, contract.ErrorEvent) (database.RecordResult, error)) (ReplayReport, error) {
	report := ReplayReport{}
	if q == nil {
		return report, errors.New("queue: nil queue")
	}
	if recorder == nil {
		return report, errors.New("queue: a recorder is required")
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	names, err := q.list()
	if err != nil {
		return report, err
	}
	now := q.now().UTC()
	if err := q.sweep(now, names); err != nil {
		return report, err
	}
	names, err = q.list()
	if err != nil {
		return report, err
	}

	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		path := filepath.Join(q.dir, name)
		queued, err := readQueuedEvent(path)
		if err != nil {
			// A file that cannot be decoded cannot be replayed either. Move it
			// aside instead of deleting it, so the bytes can be inspected.
			aside := path + ".corrupt"
			if renameErr := os.Rename(path, aside); renameErr != nil {
				return report, fmt.Errorf("queue: move the corrupt entry aside: %w", renameErr)
			}
			q.stats.ReplayErrors++
			q.stats.LastError = fmt.Sprintf("queue: corrupt entry %s: %v", name, err)
			report.Errors++
			continue
		}
		if _, err := recorder(ctx, queued.Event); err != nil {
			q.stats.ReplayErrors++
			q.stats.LastError = fmt.Sprintf("queue: replay %s: %v", name, err)
			report.Errors++
			break
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, fmt.Errorf("queue: remove the replayed entry: %w", err)
		}
		q.stats.Replayed++
		report.Replayed++
	}

	remaining, err := q.list()
	if err != nil {
		return report, err
	}
	q.refresh(remaining)
	report.Remaining = q.stats.Pending
	return report, nil
}

// QueueRunOptions configures the recovery loop.
type QueueRunOptions struct {
	Recorder func(context.Context, contract.ErrorEvent) (database.RecordResult, error)
	// Available probes the database. When it returns nil the queue replays;
	// a non-nil error just backs the loop off.
	Available    func(context.Context) error
	PollInterval time.Duration
	MaxBackoff   time.Duration
}

// Run probes for a working database and replays when it is reachable. It backs
// off on failures so an outage costs one cheap probe per interval, and returns
// nil when ctx is done.
func (q *Queue) Run(ctx context.Context, options QueueRunOptions) error {
	if q == nil {
		return errors.New("queue: nil queue")
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = DefaultQueuePollInterval
	}
	maxBackoff := options.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = DefaultQueueMaxBackoff
	}
	if maxBackoff < interval {
		maxBackoff = interval
	}
	delay := interval
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		ready := true
		if options.Available != nil {
			if err := options.Available(ctx); err != nil {
				ready = false
			}
		}
		if ready {
			report, err := q.Replay(ctx, options.Recorder)
			if err != nil && ctx.Err() == nil {
				q.stats.LastError = err.Error()
			}
			if report.Replayed > 0 {
				delay = interval
			} else {
				delay = nextBackoff(delay, maxBackoff)
			}
		} else {
			delay = nextBackoff(delay, maxBackoff)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// list returns the queue file names in replay order (oldest first).
func (q *Queue) list() ([]string, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("queue: read the spool directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), queueFileSuffix) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

// sweep expires old entries and drops the oldest ones while the queue is over
// either cap. Dropping is the explicit over-limit policy: the newest failures
// are the ones most worth replaying.
func (q *Queue) sweep(now time.Time, names []string) error {
	type entry struct {
		name string
		size int64
		at   time.Time
	}
	entries := make([]entry, 0, len(names))
	var total int64
	for _, name := range names {
		info, err := os.Stat(filepath.Join(q.dir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("queue: stat %s: %w", name, err)
		}
		at := q.enqueuedAt(name, info.ModTime())
		if now.Sub(at) >= q.maxAge {
			if err := os.Remove(filepath.Join(q.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("queue: expire %s: %w", name, err)
			}
			q.stats.Expired++
			continue
		}
		entries = append(entries, entry{name: name, size: info.Size(), at: at})
		total += info.Size()
	}
	// names is sorted, but the stat loop can skip vanished files; sort again
	// so the drop policy is still "oldest first". The newest entry is never
	// dropped: one oversized event keeps the queue over the byte budget until
	// the next replay, because losing the failure that just happened is worse
	// than a temporarily over-budget spool.
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	for len(entries) > q.maxEvents || (total > q.maxBytes && len(entries) > 1) {
		oldest := entries[0]
		if err := os.Remove(filepath.Join(q.dir, oldest.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("queue: drop %s: %w", oldest.name, err)
		}
		entries = entries[1:]
		total -= oldest.size
		q.stats.Dropped++
	}
	q.stats.Pending = len(entries)
	q.stats.Bytes = total
	return nil
}

// refresh recomputes Pending and Bytes without dropping anything.
func (q *Queue) refresh(names []string) {
	var total int64
	pending := 0
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(q.dir, name)); err == nil {
			total += info.Size()
			pending++
		}
	}
	q.stats.Pending = pending
	q.stats.Bytes = total
}

// enqueuedAt reads the time from the file name. The name is authoritative
// because it is written atomically with the payload and sorts lexicographically.
func (q *Queue) enqueuedAt(name string, fallback time.Time) time.Time {
	if len(name) < 20 {
		return fallback
	}
	var nanos int64
	if _, err := fmt.Sscanf(name[:20], "%d", &nanos); err != nil || nanos <= 0 {
		return fallback
	}
	return time.Unix(0, nanos).UTC()
}

func queueFileName(at time.Time, eventID string) string {
	return fmt.Sprintf("%020d-%s%s", at.UnixNano(), eventID, queueFileSuffix)
}

func queueContains(names []string, eventID string) bool {
	suffix := "-" + eventID + queueFileSuffix
	for _, name := range names {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func readQueuedEvent(path string) (queuedEvent, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return queuedEvent{}, err
	}
	var queued queuedEvent
	if err := json.Unmarshal(payload, &queued); err != nil {
		return queuedEvent{}, err
	}
	if queued.Version != queueFileVersion {
		return queuedEvent{}, fmt.Errorf("unsupported queue file version %d", queued.Version)
	}
	if err := queued.Event.Validate(); err != nil {
		return queuedEvent{}, err
	}
	return queued, nil
}

func writeAtomically(path string, payload []byte) error {
	temp := path + ".tmp"
	if err := os.WriteFile(temp, payload, queueFilePath); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// redactQueuedEvent is the queue's own redaction pass. Producers already redact
// their captures, but the queue is the last gate before content reaches the
// disk, so it redacts again and then validates: an event that still looks
// sensitive is rejected rather than stored.
func redactQueuedEvent(event contract.ErrorEvent) contract.ErrorEvent {
	event.Headline = redact.Text(event.Headline, redact.Public)
	event.NormalizedMessage = redact.Text(event.NormalizedMessage, redact.Public)
	event.RedactedSample = redact.Sample(event.RedactedSample, redact.Public)
	event.Evidence = redact.Text(event.Evidence, redact.Public)
	event.Command = redact.Text(event.Command, redact.Public)
	event.ErrorCode = redact.Text(event.ErrorCode, redact.Public)
	event.ToolName = redact.Text(event.ToolName, redact.Public)
	return event
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}
