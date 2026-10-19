// Tests for the thin connector (EKB-038c): the pipe transport, the observation
// adapter, the offline queue and the notice helper. No test needs a real
// daemon: the transport is injected and the queue runs on a temp directory.
import { EventEmitter } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Socket } from "node:net";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  CONNECTOR_CHANNEL,
  CoreError,
  createConnector,
  createConnectorQueue,
  createCore,
  createPipeTransport,
  observationFromAgentError,
  observationFromToolResult,
} from "../src/connector";
import type { Core, CoreCandidate, RawObservation } from "../src/connector";
import { filesIn } from "../src/paths";
import { createObservationQueue } from "../src/state";
import type { QueuedCapture } from "../src/state";

function observation(message: string): RawObservation {
  return {
    observed_at: "2026-10-19T09:00:00Z",
    source: "agent",
    message,
  };
}

function captureRequest(observationId: string): QueuedCapture {
  return {
    schema_version: "v1",
    connector_id: "dsh",
    session_id: "s-1",
    observation_id: observationId,
    observation: {
      observed_at: "2026-10-19T09:00:00.000Z",
      source: "agent",
      message: "boom",
    },
  };
}

class FakeSocket extends EventEmitter {
  written: string[] = [];
  destroyed = false;
  constructor(
    private readonly answer: (
      request: Record<string, unknown>,
    ) => string | undefined,
  ) {
    super();
  }
  setEncoding(): void {}
  write(line: string): boolean {
    this.written.push(line);
    const request = JSON.parse(line) as Record<string, unknown>;
    const reply = this.answer(request);
    if (reply !== undefined)
      queueMicrotask(() => {
        this.emit("data", reply);
      });
    return true;
  }
  destroy(): void {
    this.destroyed = true;
  }
}

function envelope(
  request: Record<string, unknown>,
  body: Record<string, unknown>,
): string {
  return `${JSON.stringify({
    schema_version: "v1",
    request_id: request.request_id,
    operation: request.operation,
    ...body,
  })}\n`;
}

describe("pipe transport", () => {
  it("sends one JSON line and resolves the result", async () => {
    const seen: Record<string, unknown>[] = [];
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket((request) => {
          seen.push(request);
          return envelope(request, { result: { created: true } });
        }) as unknown as Socket,
      requestId: () => "req-1",
    });
    const answer = await transport.call("capture", { connector_id: "dsh" });
    expect(answer).toEqual({ created: true });
    expect(seen).toHaveLength(1);
    expect(seen[0]).toMatchObject({
      schema_version: "v1",
      request_id: "req-1",
      operation: "capture",
      payload: { connector_id: "dsh" },
    });
  });

  it("maps the error branch to a CoreError", async () => {
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket((request) =>
          envelope(request, {
            error: { code: "unavailable", message: "down", retryable: true },
          }),
        ) as unknown as Socket,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      name: "CoreError",
      code: "unavailable",
      retryable: true,
    });
  });

  it("times out instead of hanging when the daemon does not answer", async () => {
    const transport = createPipeTransport({
      connect: () => new FakeSocket(() => undefined) as unknown as Socket,
      timeoutMs: 30,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "unavailable",
      retryable: true,
    });
  });

  it("rejects a response that answers another request", async () => {
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket(
          () =>
            `${JSON.stringify({
              schema_version: "v1",
              request_id: "someone-else",
              operation: "lookup",
              result: {},
            })}\n`,
        ) as unknown as Socket,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "internal",
    });
  });
});

function fakeCore(overrides: Partial<Record<keyof Core, unknown>> = {}): Core {
  const core: Core = {
    capture: async () => ({
      event_id: "e-1",
      occurrence_id: "o-1",
      issue_id: "i-1",
      fingerprint: "0123456789ab",
      created: true,
    }),
    lookup: async () => ({}),
    search: async () => ({}),
    feedback: async () => ({}),
    recordSolution: async () => ({}),
    setStatus: async () => ({}),
    list: async () => ({}),
    stats: async () => ({}),
  };
  return Object.assign(core, overrides) as Core;
}

interface FakeQueue {
  enqueue: ReturnType<typeof vi.fn>;
  list: ReturnType<typeof vi.fn>;
  remove: ReturnType<typeof vi.fn>;
  stats: ReturnType<typeof vi.fn>;
  items: Map<string, { observationId: string; request: QueuedCapture }>;
}

function fakeQueue(): FakeQueue {
  const items = new Map<
    string,
    { observationId: string; request: QueuedCapture }
  >();
  const queue = {
    items,
    enqueue: vi.fn(async (request: QueuedCapture) => {
      items.set(request.observation_id, {
        observationId: request.observation_id,
        request,
      });
      return { pending: items.size, bytes: 0, dropped: 0 };
    }),
    list: vi.fn(async () =>
      [...items.values()].map((item) => ({
        observationId: item.observationId,
        queuedAt: "2026-10-19T09:00:00Z",
        request: item.request,
      })),
    ),
    remove: vi.fn(async (observationId: string) => {
      items.delete(observationId);
    }),
    stats: vi.fn(async () => ({ pending: items.size, bytes: 0, dropped: 0 })),
  };
  return queue as unknown as FakeQueue;
}

describe("connector adapter", () => {
  it("captures a normalised observation and returns the issue identity", async () => {
    const calls: QueuedCapture[] = [];
    const core = fakeCore({
      capture: async (request: QueuedCapture) => {
        calls.push(request);
        return {
          event_id: "e-1",
          occurrence_id: "o-1",
          issue_id: "i-1",
          fingerprint: "0123456789ab",
          created: true,
        };
      },
    });
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core,
      queue: fakeQueue() as never,
      newObservationId: () => "obs-1",
      now: () => new Date("2026-10-19T09:00:00Z"),
    });
    const outcome = await connector.capture(observation("boom"));
    expect(outcome).toEqual({
      queued: false,
      created: true,
      issueId: "i-1",
      fingerprint: "0123456789ab",
    });
    expect(calls[0]).toMatchObject({
      schema_version: "v1",
      connector_id: "dsh",
      session_id: "s-1",
      observation_id: "obs-1",
      channel: CONNECTOR_CHANNEL,
      observation: { project_key: "default", message: "boom" },
    });
  });

  it("queues the observation while the daemon is down", async () => {
    const queue = fakeQueue();
    const core = fakeCore({
      capture: async () => {
        throw new CoreError("unavailable", "down", true);
      },
    });
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core,
      queue: queue as never,
      newObservationId: () => "obs-1",
    });
    const outcome = await connector.capture(observation("boom"));
    expect(outcome).toEqual({ queued: true });
    expect(queue.enqueue).toHaveBeenCalledTimes(1);
    expect(queue.items.get("obs-1")?.request.observation.message).toBe("boom");
  });

  it("fails a non-retryable core error instead of queueing", async () => {
    const queue = fakeQueue();
    const core = fakeCore({
      capture: async () => {
        throw new CoreError("conflict", "already recorded", false);
      },
    });
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core,
      queue: queue as never,
    });
    await expect(connector.capture(observation("boom"))).rejects.toThrow(
      "already recorded",
    );
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it("replays queued observations in order and stops at the first failure", async () => {
    const queue = fakeQueue();
    await queue.enqueue(captureRequest("obs-1") as never);
    await queue.enqueue(captureRequest("obs-2") as never);
    let calls = 0;
    const core = fakeCore({
      capture: async () => {
        calls += 1;
        if (calls === 2) throw new CoreError("unavailable", "down", true);
        return {
          event_id: "e",
          occurrence_id: "o",
          issue_id: "i",
          fingerprint: "0123456789ab",
          created: false,
        };
      },
    });
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core,
      queue: queue as never,
    });
    const report = await connector.replay();
    expect(report.replayed).toBe(1);
    expect(report.remaining).toBe(1);
    expect(report.error).toContain("down");
    expect(queue.items.has("obs-1")).toBe(false);
    expect(queue.items.has("obs-2")).toBe(true);
  });

  it("renders a hit notice with the fix and a near notice for approximations", () => {
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core: fakeCore(),
      queue: fakeQueue() as never,
    });
    const hit: CoreCandidate = {
      issue_id: "i-1",
      fingerprint: "0123456789ab",
      title: "EPERM: operation not permitted, rename",
      match_reason: "exact_fingerprint",
      match_detail: "score 1.00",
      approximate: false,
      solution: {
        solution_version_id: "v-1",
        body: "close the file before renaming it",
        source: "dsh",
        applies_to: "os=windows",
        trust: 0.75,
      },
    };
    expect(connector.notice([hit])?.kind).toBe("hit");
    expect(connector.notice([hit])?.text).toContain("close the file");
    expect(connector.notice([{ ...hit, approximate: true }])?.kind).toBe(
      "near",
    );
    expect(connector.notice([{ ...hit, solution: undefined }])?.kind).toBe(
      "no-fix",
    );
    expect(connector.notice([])).toBeUndefined();
  });
});

describe("observation builders", () => {
  it("builds an agent observation with the host session identity", () => {
    const built = observationFromAgentError(
      { agent: { id: "agent-7" }, error: new Error("LLM quota exceeded") },
      { now: () => new Date("2026-10-19T09:00:00Z"), projectKey: "proj" },
    );
    expect(built).toMatchObject({
      observed_at: "2026-10-19T09:00:00.000Z",
      source: "agent",
      message: "LLM quota exceeded",
      tool_name: "agent-7",
      project_key: "proj",
    });
  });

  it("captures only failed tool results, with command and exit code", () => {
    expect(
      observationFromToolResult({ tool: { name: "pwsh" } }, { isError: false }),
    ).toBeUndefined();
    const built = observationFromToolResult(
      { tool: { name: "pwsh" }, input: { command: "pnpm build" } },
      { isError: true, text: "ERR_PNPM_NO_IMPORTER\n[exit code: 1]" },
    );
    expect(built).toMatchObject({
      source: "tool",
      command: "pnpm build",
      tool_name: "pwsh",
      exit_code: 1,
    });
    expect(built?.message).toContain("ERR_PNPM_NO_IMPORTER");
  });
});

describe("observation queue", () => {
  let dir: string;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "errkb-queue-"));
  });

  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("keeps raw observations in enqueue order and is idempotent", async () => {
    const files = filesIn(dir);
    const queue = createObservationQueue({ files });
    await queue.enqueue(captureRequest("obs-1"));
    await queue.enqueue(captureRequest("obs-2"));
    await queue.enqueue(captureRequest("obs-1"));
    expect((await queue.list()).map((item) => item.observationId)).toEqual([
      "obs-1",
      "obs-2",
    ]);
    await queue.remove("obs-1");
    expect((await queue.stats()).pending).toBe(1);
  });

  it("drops the oldest observation when a cap is exceeded", async () => {
    const files = filesIn(dir);
    const queue = createObservationQueue({
      files,
      options: { maxEvents: 2 },
    });
    await queue.enqueue(captureRequest("obs-1"));
    await queue.enqueue(captureRequest("obs-2"));
    await queue.enqueue(captureRequest("obs-3"));
    const ids = (await queue.list()).map((item) => item.observationId);
    expect(ids).toEqual(["obs-2", "obs-3"]);
    expect((await queue.stats()).dropped).toBe(1);
  });

  it("migrates a version 1 state file to an empty queue", async () => {
    const files = filesIn(dir);
    const v1 = JSON.stringify({ version: 1, entries: {}, trust: {} });
    await writeFile(files.state, v1);
    const queue = createObservationQueue({ files });
    expect(await queue.list()).toEqual([]);
    expect((await queue.stats()).dropped).toBe(0);
    await queue.enqueue(captureRequest("obs-1"));
    const text = await readFile(files.state, "utf8");
    expect(JSON.parse(text).version).toBe(2);
  });

  it("treats an unreadable file as an empty queue", async () => {
    const files = filesIn(dir);
    await writeFile(files.state, "{not json");
    const queue = createObservationQueue({ files });
    expect(await queue.list()).toEqual([]);
  });

  it("rejects an observation without an idempotency key", async () => {
    const files = filesIn(dir);
    const queue = createObservationQueue({ files });
    await expect(
      queue.enqueue({ ...captureRequest(""), observation_id: "" } as never),
    ).rejects.toThrow("observation_id");
  });
});

describe("connector transport failures", () => {
  const failed = (
    setup: (socket: FakeSocket) => void,
  ): ReturnType<typeof createPipeTransport> =>
    createPipeTransport({
      connect: () => {
        const socket = new FakeSocket(() => undefined);
        setup(socket);
        return socket as unknown as Socket;
      },
    });

  it("reports the pipe as unavailable when the socket errors", async () => {
    const transport = failed((socket) =>
      queueMicrotask(() => socket.emit("error", new Error("ENOENT"))),
    );
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "unavailable",
      retryable: true,
    });
  });

  it("reports the pipe as unavailable when the socket closes early", async () => {
    const transport = failed((socket) =>
      queueMicrotask(() => socket.emit("close")),
    );
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "unavailable",
    });
  });

  it("rejects a non-JSON answer", async () => {
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket(() => "definitely not json\n") as unknown as Socket,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "internal",
    });
  });

  it("rejects an answer with the wrong schema version", async () => {
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket(
          (request) =>
            `${JSON.stringify({
              schema_version: "v2",
              request_id: request.request_id,
              operation: "lookup",
              result: {},
            })}\n`,
        ) as unknown as Socket,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "internal",
    });
  });

  it("rejects an answer to another operation", async () => {
    const transport = createPipeTransport({
      connect: () =>
        new FakeSocket(
          (request) =>
            `${JSON.stringify({
              schema_version: "v1",
              request_id: request.request_id,
              operation: "search",
              result: {},
            })}\n`,
        ) as unknown as Socket,
    });
    await expect(transport.call("lookup", {})).rejects.toMatchObject({
      code: "internal",
    });
  });
});

describe("core helpers", () => {
  it("routes every core method to its operation", async () => {
    const operations: string[] = [];
    const core = createCore({
      call: async (operation) => {
        operations.push(operation);
        return {};
      },
    });
    await core.capture(captureRequest("obs-1"));
    await core.lookup({});
    await core.search({});
    await core.feedback({});
    await core.recordSolution({});
    await core.setStatus({});
    await core.list({});
    await core.stats({});
    expect(operations).toEqual([
      "capture",
      "lookup",
      "search",
      "feedback",
      "record_solution",
      "set_status",
      "list",
      "stats",
    ]);
  });

  it("replays a queue that drains completely", async () => {
    const queue = fakeQueue();
    await queue.enqueue(captureRequest("obs-1") as never);
    await queue.enqueue(captureRequest("obs-2") as never);
    const connector = createConnector({
      connectorId: "dsh",
      sessionId: "s-1",
      core: fakeCore(),
      queue: queue as never,
    });
    expect(await connector.replay()).toEqual({ replayed: 2, remaining: 0 });
    expect(queue.items.size).toBe(0);
  });

  it("builds a queue over the knowledge base files", async () => {
    const dir = await mkdtemp(join(tmpdir(), "errkb-connector-queue-"));
    try {
      const queue = createConnectorQueue(filesIn(dir));
      await queue.enqueue(captureRequest("obs-1"));
      expect((await queue.stats()).pending).toBe(1);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("reads messages from objects, numbers and null", () => {
    const read = (error: unknown) =>
      observationFromAgentError({ error }).message;
    expect(read({ message: "from object" })).toBe("from object");
    expect(read(42)).toBe("42");
    expect(read(null)).toBe("null");
  });

  it("joins text content blocks and keeps the runtime versions", () => {
    const built = observationFromToolResult(
      { tool: { name: "pwsh" } },
      {
        isError: true,
        content: [
          { type: "text", text: "line one" },
          { type: "text", text: "line two" },
        ],
      },
      {
        projectKey: "proj",
        runtimeVersions: { node: "24.16.0" },
      },
    );
    expect(built?.message).toBe("line one\nline two");
    expect(built).toMatchObject({
      project_key: "proj",
      runtime_versions: { node: "24.16.0" },
    });
  });

  it("falls back to the serialized output for other result shapes", () => {
    const built = observationFromToolResult(
      { tool: { name: "pwsh" } },
      { isError: true, output: 123 },
    );
    expect(built?.message).toBe("123");
    expect(built?.command).toBeUndefined();
  });
});
