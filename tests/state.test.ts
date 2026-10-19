// The connector-local observation queue (EKB-038d): the only state the DSH
// plugin keeps. It holds redacted capture requests while the daemon is down,
// in order, with a cap, and survives a restart. No authoritative hit counters
// or fix trust live here any more (the EKB-033 deferral).
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  DEFAULT_QUEUE_LIMITS,
  QUEUE_VERSION,
  createObservationQueue,
} from "../src/state";
import type { QueuedCapture } from "../src/state";

let dir: string;

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), "errkb-queue-"));
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

const files = () => ({
  state: join(dir, "state.json"),
  lock: join(dir, "errors.lock"),
});

const capture = (
  id: string,
  message = "EPERM: operation not permitted",
): QueuedCapture => ({
  schema_version: "v1",
  connector_id: "dsh",
  session_id: "s1",
  observation_id: id,
  channel: "A",
  observation: {
    observed_at: "2026-10-19T10:00:00.000Z",
    source: "tool",
    message,
  },
});

const at = (iso: string) => new Date(iso);

describe("createObservationQueue", () => {
  it("persists capture requests in order", async () => {
    const queue = createObservationQueue({ files: files() });
    await queue.enqueue(capture("obs-1"), at("2026-10-19T10:00:00Z"));
    await queue.enqueue(capture("obs-2"), at("2026-10-19T10:00:01Z"));
    const pending = await queue.list();
    expect(pending.map((p) => p.observationId)).toEqual(["obs-1", "obs-2"]);
    expect(pending[0]?.request.connector_id).toBe("dsh");
    expect((await queue.stats()).pending).toBe(2);
  });

  it("survives a restart", async () => {
    const first = createObservationQueue({ files: files() });
    await first.enqueue(capture("obs-1"));
    const second = createObservationQueue({ files: files() });
    expect((await second.list()).map((p) => p.observationId)).toEqual([
      "obs-1",
    ]);
  });

  it("ignores a repeated observation id", async () => {
    const queue = createObservationQueue({ files: files() });
    await queue.enqueue(capture("obs-1"));
    await queue.enqueue(capture("obs-1", "a different message"));
    const pending = await queue.list();
    expect(pending).toHaveLength(1);
    expect(pending[0]?.request.observation.message).toBe(
      "EPERM: operation not permitted",
    );
  });

  it("rejects an empty observation id", async () => {
    const queue = createObservationQueue({ files: files() });
    await expect(queue.enqueue(capture(""))).rejects.toThrow(/observation_id/);
  });

  it("removes one observation", async () => {
    const queue = createObservationQueue({ files: files() });
    await queue.enqueue(capture("obs-1"));
    await queue.enqueue(capture("obs-2"));
    await queue.remove("obs-1");
    expect((await queue.list()).map((p) => p.observationId)).toEqual(["obs-2"]);
  });

  it("drops the oldest entry at the event cap", async () => {
    const queue = createObservationQueue({
      files: files(),
      options: { maxEvents: 2 },
    });
    for (const id of ["obs-1", "obs-2", "obs-3"])
      await queue.enqueue(capture(id));
    const pending = await queue.list();
    expect(pending.map((p) => p.observationId)).toEqual(["obs-2", "obs-3"]);
    expect((await queue.stats()).dropped).toBe(1);
  });

  it("drops the oldest entry at the byte cap, keeping the newest", async () => {
    const queue = createObservationQueue({
      files: files(),
      options: { maxBytes: 900 },
    });
    await queue.enqueue(capture("obs-1", "x".repeat(400)));
    await queue.enqueue(capture("obs-2", "y".repeat(400)));
    expect((await queue.stats()).pending).toBe(1);
    expect((await queue.list())[0]?.observationId).toBe("obs-2");
  });

  it("treats a missing or corrupt queue file as empty", async () => {
    await writeFile(files().state, "{ not json", "utf8");
    const queue = createObservationQueue({ files: files() });
    expect(await queue.list()).toEqual([]);
    await queue.enqueue(capture("obs-1"));
    expect((await queue.list()).map((p) => p.observationId)).toEqual(["obs-1"]);
  });

  it("drops an obsolete version when the first observation is queued", async () => {
    await writeFile(
      files().state,
      JSON.stringify({ version: 1, entries: { "E-0001": { hits: 2 } } }),
      "utf8",
    );
    const queue = createObservationQueue({ files: files() });
    expect((await queue.stats()).pending).toBe(0);
    await queue.enqueue(capture("obs-1"));
    const stored = JSON.parse(await readFile(files().state, "utf8")) as {
      version: number;
      queue: unknown[];
    };
    expect(stored.version).toBe(QUEUE_VERSION);
    expect(stored.queue).toHaveLength(1);
  });

  it("serialises concurrent enqueues behind the lock", async () => {
    const queue = createObservationQueue({ files: files() });
    await Promise.all(
      Array.from({ length: 20 }, (_, i) => queue.enqueue(capture(`obs-${i}`))),
    );
    expect((await queue.list()).length).toBe(20);
    expect(DEFAULT_QUEUE_LIMITS.maxEvents).toBeGreaterThanOrEqual(20);
  });

  it("reports the caps' defaults", () => {
    expect(DEFAULT_QUEUE_LIMITS).toEqual({
      maxEvents: 1_000,
      maxBytes: 32 << 20,
    });
  });
});

describe("queue document shapes", () => {
  it("ignores a document whose queue is not a list", async () => {
    await writeFile(
      files().state,
      JSON.stringify({ version: QUEUE_VERSION, dropped: 0, queue: {} }),
    );
    const queue = createObservationQueue({ files: files() });
    expect(await queue.list()).toEqual([]);
    expect((await queue.stats()).pending).toBe(0);
  });

  it("ignores entries that do not look like queued captures", async () => {
    await writeFile(
      files().state,
      JSON.stringify({
        version: QUEUE_VERSION,
        dropped: 0,
        queue: [{ observationId: "obs-1", request: null }],
      }),
    );
    const queue = createObservationQueue({ files: files() });
    expect(await queue.list()).toEqual([]);
  });
});
