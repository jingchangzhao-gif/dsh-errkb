// The injection layer still lives in plugin.ts (EKB-038d): it turns a core
// lookup into one notice in the next step, asks once for a missing fix, and
// keeps the system-prompt section and session digest. It runs on the thin
// recorder, so its tests use a fake recorder — no knowledge-base files.
import type { Context } from "@deepseek-ai/cordis";
import type {
  PostToolDecision,
  ToolExecution,
  ToolExecutionResult,
} from "@deepseek-ai/dsh-tools";
import type { UserMessage } from "@deepseek-ai/dsh-llm";
import { describe, expect, it } from "vitest";
import {
  SYSTEM_PROMPT_HINT,
  SYSTEM_PROMPT_SECTION,
  WORDING,
} from "../src/inject";
import {
  DEFAULT_INJECTION_OPTIONS,
  MAX_PENDING,
  createInjection,
  registerInjection,
  registerListeners,
} from "../src/plugin";
import type {
  Injection,
  InjectionOptions,
  PreStepDecision,
} from "../src/plugin";
import { askFixText } from "../src/resolve-detect";
import { entry, fakeRecorder, hit, indexed, settle } from "./support";
import type { FakeRecorderOptions } from "./support";

type Listener = (...args: unknown[]) => unknown;

/** A fake agent: an id, and the messages agent.inject() received. */
function agent(id = "s1") {
  const injected: UserMessage[] = [];
  return {
    id,
    injected,
    inject(message: UserMessage) {
      injected.push(message);
    },
  };
}

/** A context that records registrations and plays the host. */
function fakeCtx(options: { sectionThrows?: boolean } = {}) {
  const listeners = new Map<string, Listener[]>();
  const sections: Array<{ name: string; order: number; text: string }> = [];
  const warnings: unknown[][] = [];
  const ctx = {
    on(name: string, listener: Listener) {
      listeners.set(name, [...(listeners.get(name) ?? []), listener]);
      return () => true;
    },
    tools: { register: () => () => undefined },
    systemPrompt: {
      section(section: { name: string; order: number; text: string }) {
        if (options.sectionThrows) throw new Error("duplicate section");
        sections.push(section);
        return () => undefined;
      },
    },
    logger: {
      info: () => undefined,
      warn: (...args: unknown[]) => {
        warnings.push(args);
      },
      error: () => undefined,
      debug: () => undefined,
    },
  };
  const only = (name: string) => {
    const found = listeners.get(name) ?? [];
    expect(found).toHaveLength(1);
    return found[0] as Listener;
  };
  return {
    ctx: ctx as unknown as Context,
    listeners,
    sections,
    warnings,
    emit(name: string, ...args: unknown[]) {
      for (const listener of listeners.get(name) ?? []) listener(...args);
    },
    postExecute(
      exec: Readonly<ToolExecution>,
      result: Readonly<ToolExecutionResult>,
      downstream: PostToolDecision = { kind: "accept" },
    ) {
      return only("tools/post-execute")(exec, result, () =>
        Promise.resolve(downstream),
      ) as Promise<PostToolDecision>;
    },
    preStep(
      payload: { agent: { id: string }; turn: number },
      downstream: PreStepDecision = { kind: "enter", messages: [] },
    ) {
      return only("agent/pre-step")(payload, () =>
        Promise.resolve(downstream),
      ) as Promise<PreStepDecision>;
    },
    async sessionStart(payload: { agent: { id: string } }) {
      for (const listener of listeners.get("agent/session-start") ?? [])
        await listener(payload);
    },
  };
}

const exec = (overrides: Partial<ToolExecution> = {}) =>
  ({
    name: "shell",
    arguments: { command: "pnpm build" },
    agent: { id: "s1" },
    ...overrides,
  }) as unknown as ToolExecution;

const failed = (text = "EPERM: operation not permitted") =>
  ({
    isError: true,
    error: { message: text },
  }) as unknown as ToolExecutionResult;

const succeeded = (text = "done") =>
  ({
    isError: false,
    content: [{ type: "text", text }],
  }) as unknown as ToolExecutionResult;

const injectionFor = (
  recorderOptions: FakeRecorderOptions = {},
  options: Partial<InjectionOptions> = {},
) => {
  const recorder = fakeRecorder(recorderOptions);
  const injection = createInjection({
    recorder,
    options: { ...DEFAULT_INJECTION_OPTIONS, ...options },
  });
  return { recorder, injection };
};

const textOf = (message: UserMessage): string =>
  (message.content as Array<{ text: string }>)
    .map((block) => block.text)
    .join("\n");

const injectedTexts = (messages: readonly UserMessage[] | undefined) =>
  (messages ?? []).map(textOf);

const messagesOf = (decision: PreStepDecision): readonly UserMessage[] =>
  decision.kind === "enter" ? decision.messages : [];

describe("createInjection hits", () => {
  it("delivers a hit notice with the fix after a tool call", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, fakeRecorder());
    const decision = await host.postExecute(exec(), failed());
    const contexts = injectedTexts(decision.additionalContexts);
    expect(contexts).toHaveLength(1);
    expect(contexts[0]).toContain("errkb");
    expect(contexts[0]).toContain(entry().id);
    expect(contexts[0]).toContain("Close the open handle first.");
  });

  it("counts the occurrence in the notice", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({
      hits: hit({ entry: entry({ hits: 2 }) }),
    });
    registerInjection(host.ctx, injection, fakeRecorder());
    const decision = await host.postExecute(exec(), failed());
    expect(injectedTexts(decision.additionalContexts)[0]).toContain("3 hits");
    expect(injection.counts("s1").notices).toBe(1);
    expect(injection.counts().notices).toBe(1);
  });

  it("offers a pending agent-error hit at the next pre-step", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, fakeRecorder());
    injection.agentError({ agent: { id: "s1" }, error: "boom" });
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(injectedTexts(messagesOf(decision))).toHaveLength(1);
    expect(injectedTexts(messagesOf(decision))[0]).toContain(entry().id);
  });

  it("returns the downstream decision untouched when nothing matched", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: undefined });
    registerInjection(host.ctx, injection, fakeRecorder());
    const decision = await host.postExecute(exec(), failed(), {
      kind: "accept",
    });
    expect(decision).toEqual({ kind: "accept" });
  });

  it("does nothing under inject: off", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor(
      { hits: hit() },
      { inject: "off" },
    );
    registerInjection(host.ctx, injection, recorder);
    injection.agentError({ agent: { id: "s1" }, error: "boom" });
    await settle();
    expect(recorder.calls).toContain("agentError");
    expect(recorder.calls).not.toContain("lookup");
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(messagesOf(decision)).toEqual([]);
  });

  it("records an agent error that names no agent", async () => {
    const { recorder, injection } = injectionFor({ hits: hit() });
    injection.agentError({ error: "no agent" });
    await settle();
    expect(recorder.calls).toContain("agentError");
    expect(recorder.calls).not.toContain("lookup");
  });

  it("keeps at most MAX_PENDING notices", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, fakeRecorder());
    for (let i = 0; i < MAX_PENDING + 2; i += 1)
      injection.agentError({ agent: { id: "s1" }, error: `boom ${i}` });
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(injectedTexts(messagesOf(decision))).toHaveLength(1);
    const second = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(injectedTexts(messagesOf(second)).length).toBeLessThanOrEqual(
      MAX_PENDING,
    );
  });

  it("skips the notice when a pre-step is not an enter", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, fakeRecorder());
    injection.agentError({ agent: { id: "s1" }, error: "boom" });
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 }, {
      kind: "skip",
      messages: [],
    } as never);
    expect(decision).toEqual({ kind: "skip", messages: [] });
  });

  it("announces a recorded miss under inject: always", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor(
      {
        hits: { matched: false },
        captureOutcome: { kind: "appended", id: entry().id },
      },
      { inject: "always" },
    );
    registerInjection(host.ctx, injection, recorder);
    const execution = exec();
    const answer = await host.postExecute(execution, failed());
    expect(answer).toEqual({ kind: "accept" });
    host.emit("tools/result", execution, failed());
    await settle();
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(injectedTexts(messagesOf(decision))[0]).toContain(entry().id);
  });
});

describe("fix prompt", () => {
  it("asks once for a missing fix after a success", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor({
      hits: { matched: false },
      captureOutcome: { kind: "appended", id: entry().id },
      issue: hit({ entry: entry({ fix: "" }) }),
    });
    registerInjection(host.ctx, injection, recorder);
    await host.preStep({ agent: { id: "s1" }, turn: 1 });
    const execution = exec();
    await host.postExecute(execution, failed());
    host.emit("tools/result", execution, failed());
    await settle();
    host.emit("tools/result", execution, succeeded());
    await settle();
    await settle();
    expect(recorder.calls).toContain("lookupIssue");
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(injectedTexts(messagesOf(decision))[0]).toContain(
      askFixText(entry().id),
    );
  });

  it("does not ask when captureFix is off", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor(
      {
        hits: { matched: false },
        captureOutcome: { kind: "appended", id: entry().id },
        issue: hit({ entry: entry({ fix: "" }) }),
      },
      { captureFix: "off" },
    );
    registerInjection(host.ctx, injection, recorder);
    await host.preStep({ agent: { id: "s1" }, turn: 1 });
    void recorder.toolResult(exec(), failed());
    await settle();
    host.emit("tools/result", exec(), succeeded());
    await settle();
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(messagesOf(decision)).toEqual([]);
  });

  it("does not ask when the entry already has a fix", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor({
      hits: { matched: false },
      captureOutcome: { kind: "appended", id: entry().id },
      issue: hit(),
    });
    registerInjection(host.ctx, injection, recorder);
    await host.preStep({ agent: { id: "s1" }, turn: 1 });
    void recorder.toolResult(exec(), failed());
    await settle();
    host.emit("tools/result", exec(), succeeded());
    await settle();
    await settle();
    const decision = await host.preStep({ agent: { id: "s1" }, turn: 1 });
    expect(messagesOf(decision)).toEqual([]);
  });
});

describe("session digest and section", () => {
  it("injects the digest at session start", async () => {
    const a = agent();
    const { injection } = injectionFor({
      entries: [indexed(), indexed({ id: "E-2", title: "second" })],
    });
    await injection.sessionStart({ agent: a, source: "startup" } as never);
    expect(a.injected).toHaveLength(1);
    expect(textOf(a.injected[0] as UserMessage)).toContain("2");
  });

  it("skips the digest when sessionDigest is off", async () => {
    const a = agent();
    const { injection } = injectionFor({}, { sessionDigest: "off" });
    await injection.sessionStart({ agent: a, source: "startup" } as never);
    expect(a.injected).toHaveLength(0);
  });

  it("skips the digest when the index read fails", async () => {
    const a = agent();
    const recorder = fakeRecorder();
    recorder.entries = () => Promise.resolve(undefined);
    const injection = createInjection({ recorder });
    await injection.sessionStart({ agent: a, source: "startup" } as never);
    expect(a.injected).toHaveLength(0);
  });

  it("exposes the system-prompt section", () => {
    const { injection } = injectionFor();
    expect(injection.section()).toEqual({
      ...SYSTEM_PROMPT_SECTION,
      text: SYSTEM_PROMPT_HINT,
    });
    const off = injectionFor({}, { systemPromptHint: false }).injection;
    expect(off.section()).toBeUndefined();
  });
});

describe("registration", () => {
  it("registers the capture listeners without request-error", () => {
    const host = fakeCtx();
    const recorder = fakeRecorder();
    registerListeners(host.ctx, recorder);
    expect(host.listeners.get("agent/error")).toHaveLength(1);
    expect(host.listeners.get("tools/result")).toHaveLength(1);
    expect(host.listeners.has("agent/request-error")).toBe(false);
    host.emit("agent/error", { agent: { id: "s1" }, error: "x" });
    expect(recorder.calls).toContain("agentError");
    void recorder.toolResult(exec(), failed());
    expect(recorder.calls).toContain("toolResult");
  });

  it("registers the hooks, the section and the replay", async () => {
    const host = fakeCtx();
    const recorder = fakeRecorder({ entries: [] });
    const { injection } = injectionFor();
    registerInjection(host.ctx, injection, recorder);
    expect(host.listeners.get("tools/post-execute")).toHaveLength(1);
    expect(host.listeners.get("agent/pre-step")).toHaveLength(1);
    expect(host.listeners.get("agent/session-start")).toHaveLength(2);
    expect(host.sections).toHaveLength(1);
    await host.sessionStart({
      agent: { id: "s1" },
      source: "startup",
    } as never);
    expect(recorder.calls).toContain("replay");
  });

  it("counts a section that cannot be registered", () => {
    const host = fakeCtx({ sectionThrows: true });
    const recorder = fakeRecorder();
    const { injection } = injectionFor();
    registerInjection(host.ctx, injection, recorder);
    expect(recorder.stats.failures).toBe(1);
  });

  it("skips registration under systemPromptHint: false", () => {
    const host = fakeCtx();
    const recorder = fakeRecorder();
    const injection: Injection = injectionFor(
      {},
      { systemPromptHint: false },
    ).injection;
    registerInjection(host.ctx, injection, recorder);
    expect(host.sections).toHaveLength(0);
  });

  it("reports per-session and total counts", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, fakeRecorder());
    await host.postExecute(exec(), failed());
    await host.postExecute(exec({ agent: { id: "s2" } as never }), failed());
    expect(injection.counts("s1").notices).toBe(1);
    expect(injection.counts("s2").notices).toBe(1);
    expect(injection.counts("missing")).toEqual({
      notices: 0,
      fixNotices: 0,
      noticeTokens: 0,
    });
    expect(injection.counts().notices).toBe(2);
    expect(injection.counts().fixNotices).toBe(2);
    expect(injection.counts().noticeTokens).toBeGreaterThan(0);
  });

  it("mentions the approximate wording for a fuzzy hit", async () => {
    const host = fakeCtx();
    const { injection } = injectionFor({
      hits: hit({ via: "fuzzy", approximate: true, similarity: 0.8 }),
    });
    registerInjection(host.ctx, injection, fakeRecorder());
    const decision = await host.postExecute(exec(), failed());
    expect(injectedTexts(decision.additionalContexts)[0]).toContain(
      WORDING.approximate,
    );
  });
});

describe("edge paths", () => {
  it("counts a rejected write without throwing", async () => {
    const recorder = fakeRecorder();
    recorder.agentError = () => Promise.reject(new Error("core down"));
    const injection = createInjection({ recorder });
    injection.agentError({ agent: { id: "s1" }, error: "boom" });
    await settle();
    expect(recorder.stats.failures).toBeGreaterThanOrEqual(1);
  });

  it("keeps the decision when a tool lookup rejects", async () => {
    const host = fakeCtx();
    const recorder = fakeRecorder({ hits: hit() });
    recorder.lookup = () => Promise.reject(new Error("core down"));
    const injection = createInjection({ recorder });
    registerInjection(host.ctx, injection, recorder);
    const decision = await host.postExecute(exec(), failed());
    expect(decision).toEqual({ kind: "accept" });
  });

  it("counts a result whose match flag throws", async () => {
    const recorder = fakeRecorder();
    recorder.lookup = () =>
      Promise.resolve(
        Object.defineProperty({}, "matched", {
          get() {
            throw new Error("bad result");
          },
        }) as never,
      );
    createInjection({ recorder }).agentError({
      agent: { id: "s1" },
      error: "boom",
    });
    await settle();
    expect(recorder.stats.failures).toBe(1);
  });

  it("survives a payload whose agent getter throws", async () => {
    const recorder = fakeRecorder();
    const injection = createInjection({ recorder });
    injection.agentError(
      Object.defineProperty({}, "agent", {
        get() {
          throw new Error("no agent");
        },
      }) as never,
    );
    await settle();
    expect(recorder.stats.failures).toBe(1);
  });

  it("survives a failing tool-result write", async () => {
    const host = fakeCtx();
    const recorder = fakeRecorder();
    recorder.toolResult = () => {
      throw new Error("write failed");
    };
    const injection = createInjection({ recorder });
    registerInjection(host.ctx, injection, recorder);
    host.emit("tools/result", exec(), failed());
    expect(recorder.stats.failures).toBe(1);
  });

  it("survives an execution whose agent getter throws", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, recorder);
    const bad = Object.defineProperty(
      { name: "shell", arguments: { command: "pnpm build" } },
      "agent",
      {
        get() {
          throw new Error("no agent");
        },
      },
    ) as never;
    const decision = await host.postExecute(bad, failed());
    expect(decision).toEqual({ kind: "accept" });
    expect(recorder.stats.failures).toBe(1);
  });

  it("survives a decision whose contexts getter throws", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, recorder);
    const downstream = Object.defineProperty(
      { kind: "accept" },
      "additionalContexts",
      {
        get() {
          throw new Error("bad decision");
        },
      },
    ) as PostToolDecision;
    const decision = await host.postExecute(exec(), failed(), downstream);
    expect(decision).toEqual(downstream);
  });

  it("survives a pre-step whose agent getter throws", async () => {
    const host = fakeCtx();
    const { recorder, injection } = injectionFor({ hits: hit() });
    registerInjection(host.ctx, injection, recorder);
    const payload = Object.defineProperty({ turn: 1 }, "agent", {
      get() {
        throw new Error("no agent");
      },
    }) as never;
    const decision = await host.preStep(payload);
    expect(decision.kind).toBe("enter");
    expect(recorder.stats.failures).toBe(1);
  });
});
