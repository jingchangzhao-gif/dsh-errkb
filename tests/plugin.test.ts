// The recorder is a thin client (EKB-038d): every failure becomes one
// observation for the core, every read is a core call, and nothing is stored
// or matched locally. These tests drive it with a fake connector.
import type {
  ToolExecution,
  ToolExecutionResult,
} from "@deepseek-ai/dsh-tools";
import { describe, expect, it } from "vitest";
import { CoreError } from "../src/connector";
import {
  DEFAULT_INJECTION_OPTIONS,
  DEFAULT_RECORDER_OPTIONS,
  MAX_PENDING,
  MAX_SESSIONS,
  commandFrom,
  createRecorder,
  exitCode,
  registerListeners,
  toolInput,
} from "../src/plugin";
import type { RecorderOptions } from "../src/plugin";
import { candidate, fakeConnector, fakeRecorder, settle } from "./support";

const options = (
  overrides: Partial<RecorderOptions> = {},
): RecorderOptions => ({
  ...DEFAULT_RECORDER_OPTIONS,
  ...overrides,
});

const toolExec = (overrides: Partial<ToolExecution> = {}) =>
  ({
    name: "shell",
    arguments: { command: "pnpm build" },
    agent: { id: "s1" },
    ...overrides,
  }) as unknown as ToolExecution;

const toolResult = (overrides: Partial<ToolExecutionResult> = {}) =>
  ({
    isError: false,
    content: [{ type: "text", text: "ok" }],
    ...overrides,
  }) as unknown as ToolExecutionResult;

describe("createRecorder", () => {
  it("forwards one appended observation and counts it", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    const outcome = await recorder.capture({
      kind: "tool",
      toolName: "shell",
      isError: true,
      message: "EPERM",
    });
    expect(outcome).toEqual({
      kind: "appended",
      id: "00000000-0000-4000-8000-000000000003",
    });
    expect(connector.captured[0]?.source).toBe("tool");
    expect(connector.captured[0]?.message).toBe("EPERM");
    expect(connector.captured[0]?.tool_name).toBe("shell");
    expect(recorder.stats.appended).toBe(1);
    expect(recorder.stats.failures).toBe(0);
  });

  it("counts a hit when the core did not create the issue", async () => {
    const connector = fakeConnector(
      {},
      {
        capture: () =>
          Promise.resolve({
            queued: false,
            created: false,
            issueId: "00000000-0000-4000-8000-000000000003",
          }),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    expect(
      await recorder.capture({ kind: "agent", error: new Error("x") }),
    ).toEqual({
      kind: "hit",
      id: "00000000-0000-4000-8000-000000000003",
      hits: 1,
    });
    expect(recorder.stats.hits).toBe(1);
  });

  it("counts a queued observation as stored later", async () => {
    const connector = fakeConnector(
      {},
      {
        capture: () => Promise.resolve({ queued: true }),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    expect(await recorder.capture({ kind: "agent", error: "x" })).toEqual({
      kind: "counted",
    });
    expect(recorder.stats.counted).toBe(1);
  });

  it("counts a failed capture without throwing", async () => {
    const connector = fakeConnector(
      {},
      {
        capture: () => Promise.reject(new Error("pipe broke")),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    expect(await recorder.capture({ kind: "agent", error: "x" })).toEqual({
      kind: "failed",
    });
    expect(recorder.stats.failures).toBe(1);
  });

  it("skips a source the capture setting does not name", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ capture: ["tool"] }),
      createConnector: () => connector,
    });
    expect(await recorder.capture({ kind: "agent", error: "x" })).toEqual({
      kind: "timeout",
    });
    expect(connector.captured).toHaveLength(0);
  });

  it("never forwards a successful command", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    expect(
      recorder.toolResult(
        toolExec(),
        toolResult({
          content: [{ type: "text", text: "built\n[exit code: 0]" }],
        }),
      ),
    ).toBeUndefined();
    expect(connector.captured).toHaveLength(0);
  });

  it("forwards a failing command with its exit code", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    recorder.toolResult(
      toolExec(),
      toolResult({ content: [{ type: "text", text: "boom\n[exit code: 2]" }] }),
    );
    await recorder.idle();
    expect(connector.captured[0]?.source).toBe("command-exit");
    expect(connector.captured[0]?.exit_code).toBe(2);
    expect(connector.captured[0]?.command).toBe("pnpm build");
  });

  it("honours captureExitCodes: false", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ captureExitCodes: false }),
      createConnector: () => connector,
    });
    expect(
      recorder.toolResult(
        toolExec(),
        toolResult({ content: [{ type: "text", text: "[exit code: 2]" }] }),
      ),
    ).toBeUndefined();
    expect(connector.captured).toHaveLength(0);
  });

  it("forwards a tool error result as a tool observation", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    recorder.toolResult(
      toolExec(),
      toolResult({
        isError: true,
        error: {
          message: "read failed",
          info: { name: "read", code: "EACCES" },
        },
      }),
    );
    await recorder.idle();
    expect(connector.captured[0]?.source).toBe("tool");
    expect(connector.captured[0]?.error_code).toBe("EACCES");
  });

  it("forwards an agent error with its session", async () => {
    const connectors = new Map<string, ReturnType<typeof fakeConnector>>();
    const recorder = createRecorder({
      createConnector: (session) => {
        const connector = fakeConnector();
        connectors.set(session, connector);
        return connector;
      },
    });
    await recorder.agentError({ agent: { id: "s2" }, error: "nope" });
    expect(connectors.get("s2")?.captured[0]?.source).toBe("agent");
    expect(connectors.get("s2")?.captured[0]?.message).toContain("nope");
  });

  it("maps a core candidate to the hit the injection layer understands", async () => {
    const connector = fakeConnector({ candidate: candidate() });
    const recorder = createRecorder({ createConnector: () => connector });
    const hit = await recorder.lookup({
      kind: "tool",
      toolName: "shell",
      isError: true,
      message: "EPERM",
    });
    expect(hit?.matched).toBe(true);
    if (hit?.matched === true) {
      expect(hit.via).toBe("exact");
      expect(hit.approximate).toBe(false);
      expect(hit.entry.hits).toBe(3);
      expect(hit.entry.fix).toBe("Close the open handle first.");
      expect(hit.injectable).toBe(true);
    }
    expect(connector.core.calls[0]?.operation).toBe("lookup");
    expect(connector.core.calls[0]?.payload.project_key).toBe("default");
  });

  it("marks an approximate candidate and its reason", async () => {
    const connector = fakeConnector({
      candidate: candidate({
        match_reason: "jaccard_rerank",
        approximate: true,
        confidence: 0.75,
        solution: undefined,
      }),
    });
    const recorder = createRecorder({ createConnector: () => connector });
    const hit = await recorder.lookup({
      kind: "agent",
      error: new Error("EPERM"),
    });
    expect(hit?.matched).toBe(true);
    if (hit?.matched === true) {
      expect(hit.via).toBe("fuzzy");
      expect(hit.approximate).toBe(true);
      expect(hit.similarity).toBe(0.75);
      expect(hit.entry.status).toBe("open");
    }
  });

  it("reports a code-matched candidate through via=code", async () => {
    const connector = fakeConnector({
      candidate: candidate({
        match_reason: "error_code",
        approximate: false,
        solution: undefined,
      }),
    });
    const recorder = createRecorder({ createConnector: () => connector });
    const hit = await recorder.lookup({
      kind: "tool",
      toolName: "shell",
      isError: true,
      message: "x",
    });
    expect(hit?.matched === true && hit.via).toBe("code");
  });

  it("returns an unmatched result for an empty candidate list", async () => {
    const recorder = createRecorder({
      createConnector: () => fakeConnector(),
    });
    expect(
      await recorder.lookup({ kind: "agent", error: new Error("x") }),
    ).toEqual({ matched: false });
  });

  it("returns undefined when the lookup call fails", async () => {
    const connector = fakeConnector({
      reject: (operation) =>
        operation === "lookup"
          ? new CoreError("unavailable", "down")
          : undefined,
    });
    const recorder = createRecorder({ createConnector: () => connector });
    expect(
      await recorder.lookup({ kind: "agent", error: new Error("x") }),
    ).toBeUndefined();
    expect(recorder.stats.failures).toBe(1);
  });

  it("looks an issue up by id and by fingerprint", async () => {
    const connector = fakeConnector({ candidate: candidate() });
    const recorder = createRecorder({ createConnector: () => connector });
    const byId = await recorder.lookupIssue(candidate().issue_id);
    const byFingerprint = await recorder.lookupFingerprint("0123456789ab");
    expect(byId?.matched).toBe(true);
    expect(byFingerprint?.matched).toBe(true);
    expect(connector.core.calls[0]?.payload.issue).toBe(candidate().issue_id);
    expect(connector.core.calls[1]?.payload.fingerprint).toBe("0123456789ab");
  });

  it("passes a search query through with the three-candidate ceiling", async () => {
    const connector = fakeConnector({ candidate: candidate() });
    const recorder = createRecorder({ createConnector: () => connector });
    const found = await recorder.search("EPERM rename", 10);
    expect(found).toHaveLength(1);
    expect(connector.core.calls[0]?.operation).toBe("search");
    expect(connector.core.calls[0]?.payload.limit).toBe(3);
  });

  it("returns undefined when the search fails", async () => {
    const connector = fakeConnector({
      reject: (operation) =>
        operation === "search" ? new Error("down") : undefined,
    });
    const recorder = createRecorder({ createConnector: () => connector });
    expect(await recorder.search("x")).toBeUndefined();
  });

  it("maps list rows to indexed entries with hits and exclusions", async () => {
    const connector = fakeConnector({
      items: [
        {
          id: "00000000-0000-4000-8000-000000000003",
          title: "EPERM: rename",
          updated_at: "2026-10-19T10:00:00Z",
          hits: 4,
          status: "wontfix",
          fix: "not a fix",
        },
      ],
    });
    const recorder = createRecorder({ createConnector: () => connector });
    const index = await recorder.entries();
    expect(index).toHaveLength(1);
    expect(index?.[0]?.entry.hits).toBe(4);
    expect(index?.[0]?.entry.status).toBe("wontfix");
    expect(index?.[0]?.entry.fix).toBe("not a fix");
    expect(index?.[0]?.excluded).toBe("wontfix");
  });

  it("returns undefined when the list call fails", async () => {
    const connector = fakeConnector({
      reject: (operation) =>
        operation === "list" ? new Error("down") : undefined,
    });
    const recorder = createRecorder({ createConnector: () => connector });
    expect(await recorder.entries()).toBeUndefined();
  });

  it("records a fix through record_solution", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    const outcome = await recorder.recordFix(
      "00000000-0000-4000-8000-000000000003",
      "Close the handle.",
    );
    expect(outcome.kind).toBe("fixed");
    expect(connector.core.calls[0]?.operation).toBe("record_solution");
    expect(connector.core.calls[0]?.payload.body).toBe("Close the handle.");
  });

  it("rejects an empty fix and counts a failed write", async () => {
    const recorder = createRecorder({
      createConnector: () => fakeConnector(),
    });
    expect(await recorder.recordFix("id", "  ")).toEqual({ kind: "timeout" });
    const failing = fakeConnector({
      reject: (operation) =>
        operation === "record_solution" ? new Error("down") : undefined,
    });
    const other = createRecorder({ createConnector: () => failing });
    expect(await other.recordFix("id", "fix")).toEqual({ kind: "failed" });
  });

  it("changes a status through set_status", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    const outcome = await recorder.setStatus("abc", "wontfix");
    expect(outcome.kind).toBe("fixed");
    expect(connector.core.calls[0]?.payload.status).toBe("wontfix");
  });

  it("counts a failed status write", async () => {
    const connector = fakeConnector({
      reject: (operation) =>
        operation === "set_status" ? new Error("down") : undefined,
    });
    const recorder = createRecorder({ createConnector: () => connector });
    expect((await recorder.setStatus("abc", "open")).kind).toBe("failed");
  });

  it("replays the queue without throwing", async () => {
    const connector = fakeConnector(
      {},
      {
        replay: () => Promise.reject(new Error("still down")),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    await recorder.replay();
    expect(recorder.stats.failures).toBe(1);
  });

  it("waits for capture to settle in idle()", async () => {
    let release: (() => void) | undefined;
    const connector = fakeConnector(
      {},
      {
        capture: () =>
          new Promise((resolve) => {
            release = () =>
              resolve({
                queued: false,
                created: true,
                issueId: "00000000-0000-4000-8000-000000000003",
              });
          }),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    void recorder.capture({ kind: "agent", error: new Error("x") });
    const idle = recorder.idle();
    release?.();
    await idle;
    expect(recorder.stats.appended).toBe(1);
  });

  it("exposes its defaults", () => {
    expect(DEFAULT_INJECTION_OPTIONS.inject).toBe("hit-only");
    expect(DEFAULT_RECORDER_OPTIONS.connectorId).toBe("dsh");
    expect(MAX_PENDING).toBe(4);
    expect(MAX_SESSIONS).toBe(64);
  });

  it("reads the exit code from the last marker", () => {
    expect(exitCode("x\n[exit code: 1]\ny\n[exit code: 3]")).toBe(3);
    expect(exitCode("no marker")).toBeUndefined();
  });

  it("turns a tool result into a command input", () => {
    const input = toolInput(toolExec(), toolResult());
    expect(input.kind).toBe("command");
    if (input.kind === "command") expect(input.command).toBe("pnpm build");
  });

  it("passes a session id through to a per-session connector", async () => {
    const seen: string[] = [];
    const recorder = createRecorder({
      createConnector: (session) => {
        seen.push(session);
        return fakeConnector();
      },
    });
    await recorder.capture({ kind: "agent", error: "x" }, "s7");
    await settle();
    expect(seen).toEqual(["s7"]);
  });
});

describe("adapter edges", () => {
  it("returns a timeout when the source is not captured", async () => {
    const recorder = createRecorder({
      options: options({ capture: ["agent"] }),
      createConnector: () => fakeConnector(),
    });
    expect(
      await recorder.capture({
        kind: "tool",
        isError: true,
        toolName: "shell",
        message: "EPERM",
      }),
    ).toEqual({ kind: "timeout" });
    expect(recorder.stats.timeouts).toBe(1);
  });

  it("maps an llm failure and joins a command array", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({ createConnector: () => connector });
    await recorder.capture({
      kind: "llm",
      code: "RATE_LIMIT",
      message: "slow down",
    });
    expect(connector.captured[0]?.source).toBe("llm");
    expect(connector.captured[0]?.error_code).toBe("RATE_LIMIT");
    expect(commandFrom({ command: ["pnpm", "build"] })).toBe("pnpm build");
    expect(commandFrom({ command: 7 })).toBeUndefined();
    await recorder.capture({
      kind: "command",
      toolName: "shell",
      text: "[exit code: 1]",
      command: "pnpm build",
    });
    expect(connector.captured[1]?.command).toBe("pnpm build");
  });

  it("counts a failed capture", async () => {
    const connector = fakeConnector(
      {},
      {
        capture: () => Promise.reject(new Error("exploded")),
      },
    );
    const recorder = createRecorder({ createConnector: () => connector });
    expect(
      await recorder.capture({ kind: "agent", error: new Error("x") }),
    ).toEqual({
      kind: "failed",
    });
    expect(recorder.stats.failures).toBe(1);
  });

  it("swallows core lookup failures", async () => {
    const connector = fakeConnector();
    connector.core.lookup = () => Promise.reject(new Error("exploded"));
    const recorder = createRecorder({ createConnector: () => connector });
    expect(
      await recorder.lookup({ kind: "agent", error: new Error("x") }),
    ).toBeUndefined();
    expect(await recorder.lookupIssue("id")).toBeUndefined();
  });

  it("keeps excluded statuses in the index view", async () => {
    const item = (status: string | undefined) => ({
      id: "00000000-0000-4000-8000-000000000003",
      title: "t",
      updated_at: "2026-01-01T00:00:00Z",
      hits: 1,
      status,
      fix: "",
    });
    const connector = fakeConnector({
      items: [item("wontfix"), item(undefined)],
    });
    const recorder = createRecorder({ createConnector: () => connector });
    const index = await recorder.entries();
    expect(index?.[0]?.excluded).toBe("wontfix");
    expect(index?.[1]?.entry.status).toBe("open");
  });

  it("opens the default connector lazily and honours a provided one", () => {
    const lazy = createRecorder({ options: options() });
    expect(lazy.connector).toBeDefined();
    const connector = fakeConnector();
    const provided = createRecorder({ connector: () => connector });
    expect(provided.connector).toBe(connector);
  });

  it("reports listener errors without throwing", () => {
    const make = () => {
      const listeners = new Map<string, Function[]>();
      const ctx = {
        on: (name: string, fn: Function) => {
          listeners.set(name, [...(listeners.get(name) ?? []), fn]);
          return () => true;
        },
      } as never;
      return { ctx, listeners };
    };
    const { ctx, listeners } = make();
    const recorder = fakeRecorder();
    registerListeners(ctx, recorder);
    listeners.get("agent/error")?.[0]?.({
      agent: { id: "s1" },
      turn: 1,
      step: 1,
      error: new Error("x"),
    });
    listeners.get("tools/result")?.[0]?.(
      { name: "shell", arguments: {}, agent: { id: "s1" } },
      { isError: false, content: [] },
    );
    expect(recorder.calls).toContain("agentError");
    expect(recorder.calls).toContain("toolResult");
  });
});
describe("core edge paths", () => {
  it("looks a fingerprint up and swallows a failing core", async () => {
    const connector = fakeConnector({ candidate: candidate() });
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    const found = await recorder.lookupFingerprint("0123456789ab");
    expect(found?.id).toBe(candidate().issue_id);
    expect(connector.core.calls.at(-1)).toMatchObject({
      operation: "lookup",
      payload: { fingerprint: "0123456789ab" },
    });
    connector.core.lookup = () =>
      Promise.reject(new Error("core down")) as never;
    expect(await recorder.lookupFingerprint("0123456789ab")).toBeUndefined();
    expect(recorder.stats.failures).toBe(1);
  });

  it("forwards the error code of an llm lookup", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    await recorder.lookup({
      kind: "llm",
      message: "rate limited",
      code: "RATE_LIMIT",
    } as never);
    expect(connector.core.calls.at(-1)?.payload.error_code).toBe("RATE_LIMIT");
  });
});

describe("request-error capture", () => {
  const failure = (code: string, message: string, provider = "deepseek") =>
    ({
      agent: { id: "s1" },
      provider,
      failure: { code, message },
    }) as never;

  it("forwards a request failure as one llm observation", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    const outcome = await recorder.requestError(
      failure("RATE_LIMIT", "429 Too Many Requests"),
    );
    expect(connector.captured).toHaveLength(1);
    expect(connector.captured[0]).toMatchObject({
      source: "llm",
      error_code: "RATE_LIMIT",
    });
    expect(connector.captured[0]?.message).toContain("429");
    expect(outcome).toEqual({
      kind: "appended",
      id: "00000000-0000-4000-8000-000000000003",
    });
  });

  it("skips a provider outside the allowlist", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ providers: ["openai"] }),
      createConnector: () => connector,
    });
    expect(
      await recorder.requestError(failure("AUTH", "bad key", "deepseek")),
    ).toEqual({ kind: "timeout" });
    expect(connector.captured).toHaveLength(0);
  });

  it("does not let the paired agent error bypass a provider filter", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ providers: ["openai"] }),
      createConnector: () => connector,
    });
    expect(
      await recorder.requestError(failure("AUTH", "bad key", "deepseek")),
    ).toEqual({ kind: "timeout" });
    const paired = await recorder.agentError({
      agent: { id: "s1" },
      error: "bad key",
    });
    expect(paired).toEqual({ kind: "counted" });
    expect(connector.captured).toHaveLength(0);
  });

  it("captures every provider for the wildcard allowlist", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ providers: ["*"] }),
      createConnector: () => connector,
    });
    await recorder.requestError(failure("AUTH", "bad key", "anything"));
    expect(connector.captured).toHaveLength(1);
  });

  it("skips request failures when the llm source is not captured", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options({ capture: ["tool"] }),
      createConnector: () => connector,
    });
    expect(await recorder.requestError(failure("AUTH", "bad key"))).toEqual({
      kind: "timeout",
    });
    expect(connector.captured).toHaveLength(0);
  });

  it("counts a tool result and a request error of the same failure once", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    const first = await recorder.toolResult(
      { name: "shell", arguments: {}, agent: { id: "s1" } } as never,
      {
        isError: true,
        content: [],
        error: {
          message: "EPERM: operation not permitted",
          info: { name: "shell", code: "EPERM" },
        },
      } as never,
    );
    const second = await recorder.requestError(
      failure("EPERM", "EPERM: operation not permitted"),
    );
    expect(first?.kind).toBe("appended");
    expect(second).toEqual({ kind: "counted" });
    expect(connector.captured).toHaveLength(1);
    expect(recorder.stats.counted).toBe(1);
  });

  it("counts two sources of the same message once even when the codes differ", async () => {
    // Live: agent/request-error reported INVALID_REQUEST while agent/error
    // reported the same provider text with no code; the message is the key.
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    const first = await recorder.requestError(
      failure("INVALID_REQUEST", "the model was rejected"),
    );
    const second = await recorder.capture(
      {
        kind: "llm",
        code: "UNKNOWN",
        message: "the model was rejected",
      },
      "s1",
    );
    expect(first.kind).toBe("appended");
    expect(second).toEqual({ kind: "counted" });
    expect(connector.captured).toHaveLength(1);
  });

  it("counts a different message of the same code separately", async () => {
    const connector = fakeConnector();
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    await recorder.requestError(failure("RATE_LIMIT", "first failure"));
    await recorder.requestError(failure("RATE_LIMIT", "second failure"));
    expect(connector.captured).toHaveLength(2);
  });

  it("reports the outcome of a solution to the core", async () => {
    const connector = fakeConnector({ candidate: candidate() });
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    expect(await recorder.feedback(candidate().issue_id, "success")).toBe(true);
    expect(connector.core.calls.at(-1)).toMatchObject({
      operation: "feedback",
      payload: {
        issue_id: candidate().issue_id,
        solution_version_id: "00000000-0000-4000-8000-000000000005",
        outcome: "success",
      },
    });
  });

  it("skips feedback when the issue has no recorded solution", async () => {
    const connector = fakeConnector({
      candidate: { ...candidate(), solution: undefined },
    });
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    expect(
      await recorder.feedback(
        "00000000-0000-4000-8000-000000000003",
        "failure",
      ),
    ).toBe(false);
    expect(
      connector.core.calls.some((call) => call.operation === "feedback"),
    ).toBe(false);
  });

  it("swallows a failing feedback write", async () => {
    const connector = fakeConnector({
      candidate: candidate(),
      reject: (operation) =>
        operation === "feedback" ? new Error("core down") : undefined,
    });
    const recorder = createRecorder({
      options: options(),
      createConnector: () => connector,
    });
    expect(await recorder.feedback(candidate().issue_id, "success")).toBe(
      false,
    );
    expect(recorder.stats.failures).toBe(1);
  });
});
