// Machine-local state: `state.json` in the knowledge base directory (§4.3).
//
// ERRORS.md is append-only knowledge, shared across devices through git: a
// block is written once and rewritten only when someone edits its fix, status
// or notes. What changes on every repeat of an error - its hit count and when
// it was last seen - lives here instead, so two devices that both hit the same
// error never touch the same block and their histories merge cleanly. The file
// is machine-local and never committed; the counts may differ between devices,
// only the knowledge has to agree.
//
// The Hits and Last seen written in a block are its baseline: 1 and the time
// of creation, or whatever a human typed there. The effective values everyone
// shows are the baseline plus this machine's delta, and the later of the two
// times (effectiveEntry()). Deleting state.json is therefore harmless: the
// counts fall back to the baselines.
//
// Shape (version 1):
//
//   { "version": 1,
//     "entries": { "E-0007": { "hits": 3, "lastSeen": "2026-09-14 09:12" } },
//     "trust": { "E-0007": { "injected": 2, "recurredAfterInject": 0,
//                            "succeeded": 1, "fixSig": "…" } } }
//
// `entries` is keyed by entry ID, which never changes. `trust` holds FixTrust's
// records (inject.ts; the platform migration is docs/implementation-plan.md EKB-021), also per ID.
//
// A missing file is an empty state. A file that is not JSON, or not version 1,
// reads as empty too and never throws; it is left alone until the next write,
// which saves it aside as `state.corrupt-<ts>.json` before replacing it - the
// same treatment ERRORS.md gets. A record of the wrong shape inside a valid
// file is dropped on its own.
//
// Writes take the knowledge base's `.lock`, the same one the store takes, and
// replace the file atomically (temp file + rename). The recorder also runs them
// on its per-knowledge-base write chain, so within one process they are
// serialized anyway; the lock is for two processes (a web and a headless
// harness) on one machine, which would otherwise lose increments to each
// other's read-modify-write.
//
// Like store.ts, the logic takes its machine as an argument: the filesystem
// and the clock are injected.
import { dirname, join } from "node:path";
import type { TrustRecord } from "./inject";
import type { KbFiles } from "./paths";
import {
  DEFAULT_STORE_OPTIONS,
  nodeStoreFs,
  systemClock,
  withFileLock,
  writeFileAtomic,
} from "./store";
import type { Entry, LockOptions, StoreClock, StoreFs } from "./store";

/** The only version this code reads and writes. */
export const STATE_VERSION = 1;

/** This machine's counters for one entry, on top of its ERRORS.md baseline. */
export interface HitCounter {
  /** Repeats seen on this machine since the block was written. */
  hits: number;
  /** When this machine last saw it, in the block's display form. */
  lastSeen: string;
}

/** The whole of `state.json`. */
export interface MachineState {
  version: typeof STATE_VERSION;
  entries: Record<string, HitCounter>;
  trust: Record<string, TrustRecord>;
}

/** A state with nothing in it. */
export function emptyState(): MachineState {
  return { version: STATE_VERSION, entries: {}, trust: {} };
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const isCount = (value: unknown): value is number =>
  typeof value === "number" && Number.isInteger(value) && value >= 0;

function isHitCounter(value: unknown): value is HitCounter {
  return (
    isObject(value) && isCount(value.hits) && typeof value.lastSeen === "string"
  );
}

function isTrustRecord(value: unknown): value is TrustRecord {
  return (
    isObject(value) &&
    isCount(value.injected) &&
    isCount(value.recurredAfterInject) &&
    isCount(value.succeeded) &&
    typeof value.fixSig === "string"
  );
}

/**
 * The records of a map that pass `valid`, copied field by field. A missing
 * map is empty; anything else that is not an object makes the file invalid.
 */
function records<T>(
  value: unknown,
  valid: (record: unknown) => record is T,
  pick: (record: T) => T,
): Record<string, T> | undefined {
  if (value === undefined) return {};
  if (!isObject(value)) return undefined;
  return Object.fromEntries(
    Object.entries(value)
      .filter((pair): pair is [string, T] => valid(pair[1]))
      .map(([id, record]) => [id, pick(record)]),
  );
}

/**
 * Parse `state.json`.
 *
 * @param text - the file's contents.
 * @returns the state, or undefined when the text is not JSON, not an object,
 *   not version 1, or has an `entries` or `trust` that is not an object.
 */
export function parseState(text: string): MachineState | undefined {
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    return undefined;
  }
  if (!isObject(data) || data.version !== STATE_VERSION) return undefined;
  const entries = records(data.entries, isHitCounter, (r) => ({
    hits: r.hits,
    lastSeen: r.lastSeen,
  }));
  const trust = records(data.trust, isTrustRecord, (r) => ({
    injected: r.injected,
    recurredAfterInject: r.recurredAfterInject,
    succeeded: r.succeeded,
    fixSig: r.fixSig,
  }));
  if (entries === undefined || trust === undefined) return undefined;
  return { version: STATE_VERSION, entries, trust };
}

/** Render a state as `state.json`: indented JSON and a final newline. */
export function renderState(state: MachineState): string {
  return `${JSON.stringify(state, null, 2)}\n`;
}

/**
 * The later of two last-seen times. Both are in the block's display form
 * (`2026-09-14 09:12`, UTC), which sorts as text; an empty one loses.
 */
export function laterSeen(a: string, b: string): string {
  return b > a ? b : a;
}

/**
 * An entry as every display shows it: the hits written in its block plus this
 * machine's delta, and the later of the two last-seen times. Without a
 * counter it is the entry itself.
 *
 * @param entry - the entry as parsed from ERRORS.md: the baseline.
 * @param counter - this machine's counters for it, if any.
 */
export function effectiveEntry(
  entry: Entry,
  counter: HitCounter | undefined,
): Entry {
  if (counter === undefined) return entry;
  return {
    ...entry,
    hits: entry.hits + counter.hits,
    lastSeen: laterSeen(entry.lastSeen, counter.lastSeen),
  };
}

/**
 * Count one more repeat of `id`, seen at `at`.
 *
 * @param state - changed in place.
 * @returns the entry's counter after the change.
 */
export function addHit(
  state: MachineState,
  id: string,
  at: string,
): HitCounter {
  const counter = state.entries[id] ?? { hits: 0, lastSeen: "" };
  const next = {
    hits: counter.hits + 1,
    lastSeen: laterSeen(counter.lastSeen, at),
  };
  state.entries[id] = next;
  return next;
}

/**
 * Name for the copy of a `state.json` that could not be read, like
 * corruptFileName() for ERRORS.md.
 *
 * @param now - timestamp to embed.
 * @returns a file name, never a path.
 */
export function corruptStateFileName(now: Date): string {
  return `state.corrupt-${now.toISOString().replace(/[:.]/g, "-")}.json`;
}

const CORRUPT_COPY = /^state\.corrupt-.*\.json$/;

/** What a read of `state.json` found. */
export interface StateRead {
  state: MachineState;
  /** The file exists but could not be read; `state` is empty. */
  corrupt: boolean;
}

/** What a write of `state.json` led to. */
export interface StateWrite {
  state: MachineState;
  /** File name of the saved-aside copy, when this write made one. */
  savedAs?: string;
}

/** `state.json` bound to one knowledge base directory. */
export interface StateFile {
  /** The current state; never throws for a missing or corrupt file. */
  read(): Promise<StateRead>;
  /**
   * Change the state under the lock and write it back atomically. A corrupt
   * file is saved aside first, then replaced by `mutate` applied to an empty
   * state.
   */
  update(mutate: (state: MachineState) => void): Promise<StateWrite>;
}

/**
 * Bind `state.json` to a knowledge base directory.
 *
 * @param files - paths from resolveKbDir().
 * @param options - the lock settings; anything missing takes the store's default.
 * @param fs - filesystem; defaults to the real one.
 * @param clock - time; defaults to the real one.
 */
export function createStateFile(
  files: Pick<KbFiles, "state" | "lock">,
  options: Partial<LockOptions> = {},
  fs: StoreFs = nodeStoreFs(),
  clock: StoreClock = systemClock(),
): StateFile {
  const o: LockOptions = { ...DEFAULT_STORE_OPTIONS, ...options };
  const dir = dirname(files.state);

  async function load(): Promise<StateRead & { text?: string }> {
    const text = await fs.readFile(files.state);
    if (text === undefined) return { state: emptyState(), corrupt: false };
    const state = parseState(text);
    return state === undefined
      ? { state: emptyState(), corrupt: true, text }
      : { state, corrupt: false };
  }

  /** Save a corrupt file aside, unless an identical copy is already there. */
  async function saveAside(text: string): Promise<string | undefined> {
    for (const name of await fs.list(dir))
      if (
        CORRUPT_COPY.test(name) &&
        (await fs.readFile(join(dir, name))) === text
      )
        return undefined;
    const name = corruptStateFileName(clock.now());
    await fs.writeFile(join(dir, name), text);
    return name;
  }

  return {
    async read() {
      const { state, corrupt } = await load();
      return { state, corrupt };
    },

    update(mutate) {
      return withFileLock(files.lock, o, fs, clock, async () => {
        const { state, text } = await load();
        const savedAs = text === undefined ? undefined : await saveAside(text);
        mutate(state);
        await writeFileAtomic(fs, files.state, renderState(state));
        return savedAs === undefined ? { state } : { state, savedAs };
      });
    },
  };
}

// ---------------------------------------------------------------------------
// Connector observation queue (EKB-038c, the EKB-033 deferral).
//
// The thin DSH adapter owns no knowledge: it captures raw observations and the
// daemon classifies, redacts, fingerprints and stores them. While the daemon is
// down (`unavailable`), the adapter keeps the raw observations here in enqueue
// order and replays them on reconnect. `observation_id` is the idempotency key:
// replaying the same observation after a lost acknowledgement reuses it, so the
// core does not count the failure twice.
//
// Nothing authoritative lives here any more. A version 1 file (hit counters and
// FixTrust records) migrates to an empty queue: the database is the only source
// of hits, and fix trust is computed by `errkb_trust()` from recorded outcomes.
// The file still goes through the knowledge base lock and atomic replacement.

/** The queue document version. Version 1 files migrate to an empty queue. */
export const QUEUE_VERSION = 2;

/** How many observations and bytes the queue keeps before dropping the oldest. */
export interface QueueLimits {
  maxEvents: number;
  maxBytes: number;
}

/** Default caps: a few days of ordinary use fits comfortably. */
export const DEFAULT_QUEUE_LIMITS: QueueLimits = {
  maxEvents: 1_000,
  maxBytes: 32 << 20,
};

/** One capture request; mirrors the pipe capture envelope (spec/connector.cue). */
export interface QueuedCapture {
  schema_version: string;
  connector_id: string;
  session_id: string;
  observation_id: string;
  channel?: string;
  observation: {
    observed_at: string;
    source: string;
    message: string;
    [field: string]: unknown;
  };
}

/** One queued observation and when the adapter tried to send it. */
export interface QueuedObservation {
  observationId: string;
  queuedAt: string;
  request: QueuedCapture;
}

/** What `stats()` reports: what is waiting and what was dropped by the caps. */
export interface QueueStats {
  pending: number;
  bytes: number;
  dropped: number;
}

/** The connector-local queue. */
export interface ObservationQueue {
  enqueue(request: QueuedCapture, at?: Date): Promise<QueueStats>;
  list(): Promise<QueuedObservation[]>;
  remove(observationId: string): Promise<void>;
  stats(): Promise<QueueStats>;
}

/** Dependencies of `createObservationQueue`. */
export interface ObservationQueueDeps {
  /** `files.state` is the queue file; `files.lock` is the knowledge base lock. */
  files: Pick<KbFiles, "state" | "lock">;
  options?: Partial<QueueLimits>;
  fs?: StoreFs;
  clock?: StoreClock;
}

interface QueueDocument {
  version: number;
  dropped: number;
  queue: QueuedObservation[];
}

/** Byte size used for the cap: the serialized request plus its bookkeeping. */
function queuedBytes(queued: QueuedObservation): number {
  return Buffer.byteLength(JSON.stringify(queued), "utf8");
}

/** Read the document, migrating a version 1 (or unreadable) file to empty. */
function parseQueueDocument(text: string | undefined): QueueDocument {
  const empty: QueueDocument = {
    version: QUEUE_VERSION,
    dropped: 0,
    queue: [],
  };
  if (text === undefined) return empty;
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return empty;
  }
  if (typeof parsed !== "object" || parsed === null) return empty;
  const doc = parsed as Partial<QueueDocument>;
  if (doc.version !== QUEUE_VERSION) return empty;
  const queue = Array.isArray(doc.queue)
    ? doc.queue.filter(
        (item): item is QueuedObservation =>
          typeof item === "object" &&
          item !== null &&
          typeof (item as QueuedObservation).observationId === "string" &&
          typeof item.request === "object" &&
          item.request !== null,
      )
    : [];
  const dropped =
    typeof doc.dropped === "number" && doc.dropped >= 0 ? doc.dropped : 0;
  return { version: QUEUE_VERSION, dropped, queue };
}

/** Build the connector-local observation queue. */
export function createObservationQueue(
  deps: ObservationQueueDeps,
): ObservationQueue {
  const { files } = deps;
  const limits: QueueLimits = { ...DEFAULT_QUEUE_LIMITS, ...deps.options };
  const fs = deps.fs ?? nodeStoreFs();
  const clock = deps.clock ?? systemClock();
  const o = DEFAULT_STORE_OPTIONS;
  const dir = dirname(files.state);

  const statsOf = (doc: QueueDocument): QueueStats => ({
    pending: doc.queue.length,
    bytes: doc.queue.reduce((total, item) => total + queuedBytes(item), 0),
    dropped: doc.dropped,
  });

  async function read(): Promise<QueueDocument> {
    return parseQueueDocument(await fs.readFile(files.state));
  }

  async function write(doc: QueueDocument): Promise<void> {
    await writeFileAtomic(
      fs,
      files.state,
      `${JSON.stringify(doc, undefined, 2)}\n`,
    );
  }

  /** Drop the oldest items until both caps hold; the newest is always kept. */
  function trim(doc: QueueDocument): QueueDocument {
    while (
      doc.queue.length > 1 &&
      (doc.queue.length > limits.maxEvents ||
        statsOf(doc).bytes > limits.maxBytes)
    ) {
      doc.queue.shift();
      doc.dropped += 1;
    }
    return doc;
  }

  return {
    enqueue(request, at = clock.now()) {
      if (request.observation_id === "")
        return Promise.reject(
          new Error("observation_id: required for the offline queue"),
        );
      return withFileLock(files.lock, o, fs, clock, async () => {
        const existing = await read();
        if (
          existing.queue.some(
            (item) => item.observationId === request.observation_id,
          )
        )
          return statsOf(existing);
        const doc = trim({
          ...existing,
          queue: [
            ...existing.queue,
            {
              observationId: request.observation_id,
              queuedAt: at.toISOString(),
              request,
            },
          ],
        });
        await write(doc);
        return statsOf(doc);
      });
    },

    list() {
      return withFileLock(files.lock, o, fs, clock, async () => {
        const doc = await read();
        await fs.mkdir(dir);
        return doc.queue;
      });
    },

    remove(observationId) {
      return withFileLock(files.lock, o, fs, clock, async () => {
        const doc = await read();
        const kept = doc.queue.filter(
          (item) => item.observationId !== observationId,
        );
        if (kept.length === doc.queue.length) return;
        await write({ ...doc, queue: kept });
      });
    },

    stats() {
      return withFileLock(files.lock, o, fs, clock, async () =>
        statsOf(await read()),
      );
    },
  };
}
