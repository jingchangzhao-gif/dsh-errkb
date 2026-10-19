// Connector-local state: the offline observation queue (EKB-038d).
//
// The thin DSH adapter owns no knowledge. It captures raw observations, and the
// daemon classifies, redacts, fingerprints and stores them. While the daemon is
// down the adapter keeps the raw observations in `state.json`, in enqueue order,
// and replays them on reconnect; `observation_id` is the idempotency key, so a
// replay after a lost acknowledgement cannot count the same failure twice.
//
// The database is the only source of hit counts and fix trust (`errkb_trust()`).
// A version 1 file (local hit counters and FixTrust records) migrates to an
// empty queue. The file still goes through the knowledge base lock and atomic
// replacement.

import { dirname } from "node:path";

import type { KbFiles } from "./paths";
import {
  DEFAULT_STORE_OPTIONS,
  nodeStoreFs,
  systemClock,
  withFileLock,
  writeFileAtomic,
} from "./store";
import type { StoreClock, StoreFs } from "./store";
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
