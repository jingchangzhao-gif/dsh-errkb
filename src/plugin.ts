// The DSH adapter (EKB-038): this file no longer owns a knowledge base. Every
// failure becomes one observation, which the connector hands to the core over
// the local Named Pipe; the core classifies, redacts, fingerprints, stores and
// matches. The recorder interface below is what the injection layer and the
// tools are built on; it is a thin client, not a store.
//
// What the adapter must not do (§3.2): no classification, no redaction, no
// fingerprinting, no matching, no file writes. A failure the core cannot store
// (the daemon is down) is queued as the raw observation by createConnector()
// and replayed in order when the core returns; the queue keeps only redacted
// request payloads and caps itself (see src/state.ts).
//
// Injection (T13) still runs here: a hit becomes a one-line notice in the next
// step, a repeated failure earns the fix prompt, and the system-prompt section
// and session digest are unchanged. Fix trust is process-local (memory) — the
// core's errkb_trust() is the shared authority, and outcomes are reported
// through the feedback operation (EKB-033 deferral, EKB-038c decision).
import type { Context, Events } from "@deepseek-ai/cordis";
import { createUserMessage } from "@deepseek-ai/dsh-llm";
import type { UserMessage } from "@deepseek-ai/dsh-llm";
import type {
  PostToolDecision,
  ToolExecution,
  ToolExecutionResult,
} from "@deepseek-ai/dsh-tools";
import { safeErrorText } from "./capture";
import type { CaptureInput } from "./capture";
import type {
  Connector,
  CoreCandidate,
  CoreListItem,
  CoreListResponse,
  CoreLookupResponse,
  RawObservation,
} from "./connector";
import { createConnector } from "./connector";
import {
  CapTracker,
  Injector,
  estimateTokens,
  SYSTEM_PROMPT_HINT,
  SYSTEM_PROMPT_SECTION,
  noticeSource,
  sessionDigestText,
} from "./inject";
import type {
  CapLimits,
  InjectMode,
  Notice,
  NoticeKind,
  NoticeSource,
  SessionDigestMode,
} from "./inject";
import type { Exclusion, Hit, IndexedEntry, MatchResult } from "./match";
import { ResolutionTracker, callOutcome } from "./resolve-detect";
import type { CaptureFixMode } from "./resolve-detect";
import type { ObservationQueue } from "./state";
import type { Entry, EntryStatus } from "./store";

/** At most one failure is logged per this many milliseconds. */
export const FAILURE_LOG_INTERVAL_MS = 60_000;

/** Sessions that keep a transient counter; the least recently active goes first. */
export const MAX_SESSIONS = 64;

/** The counter key for payloads that name no agent. */
export const NO_SESSION = "";

/** Recorder settings; every one has a default. */
export interface RecorderOptions {
  /** Knowledge-base project the observations belong to; `default` when unset. */
  projectKey?: string;
  /** Connector row name; `dsh` by default. */
  connectorId?: string;
  /** The session id used when the host names no agent; `dsh` by default. */
  sessionId?: string;
  /** The capture sources to forward (the `capture` setting). */
  capture?: readonly string[];
  /** Capture command results that reported a non-zero exit code. */
  captureExitCodes?: boolean;
  /**
   * Which LLM providers' request failures to record; `["*"]` keeps every one.
   * A payload without a provider is kept too, because dropping it would lose
   * an error the adapter cannot classify.
   */
  providers?: readonly string[];
}

export const DEFAULT_RECORDER_OPTIONS: RecorderOptions = {
  projectKey: "default",
  connectorId: "dsh",
  sessionId: "dsh",
  capture: ["agent", "tool", "command", "llm"],
  captureExitCodes: true,
  providers: ["*"],
};

export interface RecorderLogger {
  warn(format: string, ...param: unknown[]): void;
}

/** Everything a recorder needs; the core and queue are injectable. */
export interface RecorderDeps {
  options?: Partial<RecorderOptions>;
  logger?: RecorderLogger;
  /** The pipe client; defaults to a real one over `\\.\pipe\errkb`. */
  connector?: (sessionId: string) => Connector;
  /** The observation queue, when the connector is not injected. */
  queue?: ObservationQueue;
  /** Builds the connector for one session; the wiring supplies the options. */
  createConnector?: (sessionId: string) => Connector;
}

/** What one captured error led to. */
export type RecordOutcome =
  /** The core created a new issue; `id` is its UUID. */
  | { kind: "appended"; id: string }
  /** The core counted an occurrence of a known issue. */
  | { kind: "hit"; id: string; hits: number }
  /** Queued for replay (or captured a transient the core did not store). */
  | { kind: "counted" }
  /** The capture was not forwarded (a successful command). */
  | { kind: "timeout" }
  /** The write failed; the failure was counted and maybe logged. */
  | { kind: "failed" };

/** What one recordFix() led to. */
export type FixOutcome =
  /** The core now carries the fix; `entry` reflects it. */
  | { kind: "fixed"; entry: Entry }
  /** No issue has that ID. */
  | { kind: "unknown" }
  | { kind: "timeout" }
  /** The write failed; the failure was counted and maybe logged. */
  | { kind: "failed" };

/** Running totals since the recorder was created. */
export interface RecorderStats {
  appended: number;
  hits: number;
  counted: number;
  timeouts: number;
  failures: number;
}

/** The `agent/error` payload fields the recorder reads. */
export interface AgentErrorPayload {
  agent?: { readonly id: string };
  error: unknown;
}

/** The `agent/request-error` payload, exactly as dsh-agent declares it. */
export type RequestErrorPayload = Parameters<Events["agent/request-error"]>[0];

/** What the request-error waterfall returns: a retry veto, or undefined. */
export type RequestErrorAction = Awaited<
  ReturnType<Events["agent/request-error"]>
>;

/** Settles with a write's outcome; never rejects. */
export type PendingWrite = Promise<RecordOutcome>;

/** Listener bodies and their bookkeeping. */
export interface Recorder {
  /** Forward one error as an observation; never throws. */
  capture(input: CaptureInput, session?: string): PendingWrite;
  /** The `agent/error` listener body; never throws. */
  agentError(payload: AgentErrorPayload): PendingWrite;
  /**
   * The `tools/result` listener body: records failures only, and never throws.
   * A successful command returns undefined (nothing to capture).
   */
  toolResult(
    exec: Readonly<ToolExecution>,
    result: Readonly<ToolExecutionResult>,
  ): PendingWrite | undefined;
  /**
   * The `agent/request-error` observation: record one LLM request failure;
   * never throws. The waterfall forwarding is the caller's job, so the
   * observation never decides whether the request retries.
   */
  requestError(payload: RequestErrorPayload): PendingWrite;
  /**
   * Report one outcome for an issue's known fix (the resolution path): the
   * only trust path, so it goes to the core and never rejects.
   *
   * @returns true when an outcome was recorded (the issue has a fix).
   */
  feedback(issueId: string, outcome: "success" | "failure"): Promise<boolean>;
  /**
   * Ask the core for the best candidate for one capture input (the T13 hot
   * path). Reads never write anything.
   *
   * @returns the match, or undefined when the read failed or nothing matched.
   */
  lookup(input: CaptureInput): Promise<MatchResult | undefined>;
  /** Look one issue up by its core id (the fix prompt path). */
  lookupIssue(id: string): Promise<Hit | undefined>;
  /** Look one issue up by its 12-hex fingerprint (err_lookup's fast path). */
  lookupFingerprint(fingerprint: string): Promise<Hit | undefined>;
  /** The closest core candidates for free text (err_lookup on a miss). */
  search(query: string, limit?: number): Promise<CoreCandidate[] | undefined>;
  /** The indexed core entries, for the digest and the fix prompt. */
  entries(): Promise<IndexedEntry[] | undefined>;
  /** Record a fix for an issue (EKB-015 err_record). Never rejects. */
  recordFix(id: string, fix: string): Promise<FixOutcome>;
  /** Change one issue's status (err_record/err_forget). Never rejects. */
  setStatus(id: string, status: EntryStatus): Promise<FixOutcome>;
  /** Count a failure and log it, at most once a minute; never throws. */
  fail(error: unknown): void;
  /** Settles once every in-flight capture has settled. */
  idle(): Promise<void>;
  /** Outcomes in the order their writes finished. */
  readonly outcomes: readonly RecordOutcome[];
  readonly stats: Readonly<RecorderStats>;
  /** The connector this layer captures through (queue, replay, notices). */
  readonly connector: Connector;
  /** Replay queued observations; the daemon may be back. Never rejects. */
  replay(): Promise<void>;
  readonly projectKey: string;
}

// ---------------------------------------------------------------------------
// Reading hook payloads

/** The text of a tool result: its text blocks, one per line. */
export function resultText(content: readonly { type: string }[]): string {
  return content
    .filter((block): block is { type: "text"; text: string } => {
      return (
        block.type === "text" &&
        typeof (block as { text?: unknown }).text === "string"
      );
    })
    .map((block) => block.text)
    .join("\n");
}

// The argument names shell tools use for the command line. The shell tool is
// not among the installed packages, so its schema could not be checked; these
// cover the common spellings, and a call without any of them is still
// recorded, only without the command.
const COMMAND_KEYS = ["command", "cmd", "script"] as const;

/**
 * The command a tool call ran, from its parsed arguments: a string argument
 * as is, or the first of `command`, `cmd` and `script` that is a string or a
 * list of strings (joined with spaces).
 *
 * @param args - `exec.arguments`, deep-frozen JSON.
 * @returns the command, or `undefined` when the arguments name none.
 */
export function commandFrom(args: unknown): string | undefined {
  if (typeof args === "string") return args;
  if (typeof args !== "object" || args === null) return undefined;
  for (const key of COMMAND_KEYS) {
    const value = (args as Record<string, unknown>)[key];
    if (typeof value === "string") return value;
    if (Array.isArray(value) && value.every((v) => typeof v === "string"))
      return value.join(" ");
  }
  return undefined;
}

/**
 * The capture input for one tool outcome: a failure as a tool error, anything
 * else as command output, whose exit code decides whether it is forwarded.
 */
export function toolInput(
  exec: Readonly<ToolExecution>,
  result: Readonly<ToolExecutionResult>,
): CaptureInput {
  if (result.isError) {
    const code = result.error.info?.code;
    return {
      kind: "tool",
      toolName: exec.name,
      isError: true,
      message: result.error.message,
      ...(code === undefined ? {} : { code }),
    };
  }
  const command = commandFrom(exec.arguments);
  return {
    kind: "command",
    toolName: exec.name,
    text: resultText(result.content),
    ...(command === undefined ? {} : { command }),
  };
}

/** A host agent's id, when the value exposes a readable one. */
export function agentId(value: unknown): string | undefined {
  try {
    const agent = (value as { agent?: { id?: unknown } } | undefined)?.agent;
    return typeof agent?.id === "string" && agent.id !== ""
      ? agent.id
      : undefined;
  } catch {
    // Host payloads may guard service access behind an inject declaration.
    return undefined;
  }
}

// ---------------------------------------------------------------------------
// The recorder

const exitCodePattern = /\[exit code: (\d+)\]/g;

/** The last `[exit code: N]` in `text`, when there is one. */
export function exitCode(text: string): number | undefined {
  let code: number | undefined;
  for (const match of text.matchAll(exitCodePattern))
    code = Number.parseInt(match[1] as string, 10);
  return code;
}

/** One observation for a capture input, or undefined when it is not a failure. */
function observationFor(
  input: CaptureInput,
  captureExitCodes: boolean,
): RawObservation | undefined {
  const at = new Date().toISOString();
  switch (input.kind) {
    case "agent": {
      const text = safeErrorText(input.error);
      return {
        observed_at: at,
        source: "agent",
        message: text.message,
        ...(text.code === undefined ? {} : { error_code: text.code }),
      };
    }
    case "llm": {
      return {
        observed_at: at,
        source: "llm",
        message: input.message,
        ...(input.code === undefined ? {} : { error_code: input.code }),
      };
    }
    case "tool": {
      return {
        observed_at: at,
        source: "tool",
        message: input.message,
        ...(input.toolName === undefined ? {} : { tool_name: input.toolName }),
        ...(input.code === undefined ? {} : { error_code: input.code }),
      };
    }
    case "command": {
      const code = exitCode(input.text);
      if (!captureExitCodes || code === undefined || code === 0)
        return undefined;
      return {
        observed_at: at,
        source: "command-exit",
        message: input.text,
        exit_code: code,
        ...(input.command === undefined ? {} : { command: input.command }),
        ...(input.toolName === undefined ? {} : { tool_name: input.toolName }),
      };
    }
  }
}

/** A synthetic entry for a candidate or a list row (the adapter's view). */
function entryView(
  id: string,
  title: string,
  fingerprint: string,
  hits: number,
  fix: string,
  status: EntryStatus,
): Entry {
  return {
    id,
    title,
    meta: { sig: fingerprint },
    fingerprint,
    category: "",
    firstSeen: "",
    lastSeen: "",
    hits,
    trigger: "",
    raw: "",
    fix,
    status,
    notes: "",
  };
}

function statusOf(raw: unknown): EntryStatus {
  return raw === "fixed" || raw === "wontfix" || raw === "misjudged"
    ? (raw as EntryStatus)
    : "open";
}

/** Map a core candidate to the hit shape the injection layer understands. */
function hitFrom(candidate: CoreCandidate, projectKey: string): Hit {
  const fix = candidate.solution?.body ?? "";
  const status: EntryStatus = fix.trim() === "" ? "open" : "fixed";
  const entry = entryView(
    candidate.issue_id,
    candidate.title,
    candidate.fingerprint,
    candidate.hits ?? 0,
    fix,
    status,
  );
  // The solution identity travels with the view so feedback() can name the
  // exact version; Entry predates solutions, so it rides as an extra field.
  const version = candidate.solution?.solution_version_id;
  if (version !== undefined)
    Object.assign(entry, { solutionVersionId: version });
  const exact =
    !candidate.approximate &&
    (candidate.match_reason === "exact_fingerprint" ||
      candidate.match_reason === "issue_id");
  const via: Hit["via"] =
    candidate.match_reason === "error_code"
      ? "code"
      : exact
        ? "exact"
        : "fuzzy";
  const similarity = exact
    ? 1
    : Math.max(0, Math.min(1, candidate.confidence ?? 0));
  void projectKey;
  return {
    matched: true,
    id: entry.id,
    entry,
    via,
    approximate: candidate.approximate,
    similarity,
    injectable: true,
  };
}

/** Build the recorder the injection layer and the tools run on. */
export function createRecorder(deps: RecorderDeps = {}): Recorder {
  const options: RecorderOptions = {
    ...DEFAULT_RECORDER_OPTIONS,
    ...deps.options,
  };
  const projectKey = options.projectKey ?? "default";
  const connectorId = options.connectorId ?? "dsh";
  const defaultSession = options.sessionId ?? "dsh";
  const captureSources = new Set(options.capture ?? []);
  const captureExitCodes = options.captureExitCodes !== false;
  const outcomes: RecordOutcome[] = [];
  const stats: RecorderStats = {
    appended: 0,
    hits: 0,
    counted: 0,
    timeouts: 0,
    failures: 0,
  };
  const inFlight = new Set<Promise<unknown>>();
  let lastLog = 0;

  // The same request failure can reach the adapter twice: dsh-llm fires
  // `agent/request-error` for the LLM request, and the failing tool call that
  // carried it may also arrive through `tools/result`. The core keys
  // occurrences by event id, so without a client-side guard such a pair would
  // count one failure twice. The window is deliberately short: a real retry is
  // slower than this, so it still counts.
  // shortcut: identity is the code plus the first message line within 1.5 s,
  // not a host-provided event id (the host does not expose one); upgrade if one
  // appears.
  const DEDUPE_WINDOW_MS = 1_500;
  const recentFailures = new Map<string, number>();
  const providers = new Set(
    (options.providers ?? ["*"]).map((provider) =>
      provider.trim().toLowerCase(),
    ),
  );

  /** Whether this provider's request failures are captured. */
  function providerAllowed(provider: string): boolean {
    if (providers.size === 0 || providers.has("*")) return true;
    return providers.has(provider.trim().toLowerCase());
  }

  /** True when this observation was already forwarded a moment ago. */
  function duplicate(
    observation: RawObservation,
    session: string | undefined,
  ): boolean {
    const firstLine = observation.message.split(/\r?\n/, 1)[0]?.trim() ?? "";
    // One failure can arrive twice: `agent/request-error` for the LLM request
    // and `agent/error` for the same message, sometimes with different codes.
    // The message text is the part both share, so it is the dedupe key; the
    // window is short enough that two genuine failures of identical text are
    // still counted separately when they are more than a moment apart.
    const key = `${session ?? defaultSession}\u0000${firstLine}`;
    const now = Date.now();
    const seen = recentFailures.get(key);
    for (const [known, at] of recentFailures)
      if (now - at >= DEDUPE_WINDOW_MS) recentFailures.delete(known);
    if (seen !== undefined && now - seen < DEDUPE_WINDOW_MS) return true;
    recentFailures.set(key, now);
    return false;
  }

  const connectors = new Map<string, Connector>();

  /** One connector per host session, sharing the core and the queue. */
  function connectorFor(session?: string): Connector {
    const key = session ?? defaultSession;
    const existing = connectors.get(key);
    if (existing !== undefined) return existing;
    const provided = deps.connector?.(key);
    const built =
      provided ??
      (deps.createConnector !== undefined
        ? deps.createConnector(key)
        : createConnector({
            connectorId,
            sessionId: key,
            projectKey,
            ...(deps.queue === undefined ? {} : { queue: deps.queue }),
          }));
    connectors.set(key, built);
    return built;
  }

  /** Count and append one outcome; never throws. */
  function record(outcome: RecordOutcome): RecordOutcome {
    outcomes.push(outcome);
    switch (outcome.kind) {
      case "appended":
        stats.appended++;
        break;
      case "hit":
        stats.hits++;
        break;
      case "counted":
        stats.counted++;
        break;
      case "timeout":
        stats.timeouts++;
        break;
    }
    return outcome;
  }

  function fail(error: unknown): void {
    stats.failures++;
    const now = Date.now();
    if (now - lastLog < FAILURE_LOG_INTERVAL_MS) return;
    lastLog = now;
    deps.logger?.warn(
      "errkb: %s",
      error instanceof Error ? error.message : String(error),
    );
  }

  /** Forward one observation; never rejects. */
  function forward(
    observation: RawObservation | undefined,
    session?: string,
  ): PendingWrite {
    if (observation === undefined)
      return Promise.resolve(record({ kind: "counted" }));
    if (duplicate(observation, session))
      return Promise.resolve(record({ kind: "counted" }));
    const pending = (async (): Promise<RecordOutcome> => {
      try {
        const outcome = await connectorFor(session).capture(observation);
        if (outcome.queued === true) return record({ kind: "counted" });
        if (outcome.issueId === undefined) return record({ kind: "counted" });
        if (outcome.created === true)
          return record({ kind: "appended", id: outcome.issueId });
        return record({ kind: "hit", id: outcome.issueId, hits: 1 });
      } catch (error) {
        fail(error);
        outcomes.push({ kind: "failed" });
        return { kind: "failed" };
      }
    })();
    inFlight.add(pending);
    void pending.finally(() => inFlight.delete(pending));
    return pending;
  }

  function capture(input: CaptureInput, session?: string): PendingWrite {
    if (!captureSources.has(input.kind)) {
      return Promise.resolve(record({ kind: "timeout" }));
    }
    return forward(observationFor(input, captureExitCodes), session);
  }

  /** Map one core response to a match; undefined when nothing matched. */
  function matchOf(
    response: CoreLookupResponse | undefined,
  ): MatchResult | undefined {
    const best = response?.candidates?.[0];
    if (best === undefined) return { matched: false };
    return hitFrom(best, projectKey);
  }

  async function lookup(input: CaptureInput): Promise<MatchResult | undefined> {
    try {
      const observation = observationFor(input, captureExitCodes);
      if (observation === undefined) return undefined;
      const connector = connectorFor();
      const response = (await connector.core.lookup({
        schema_version: "v1",
        project_key: projectKey,
        normalized_message: observation.message,
        ...(observation.error_code === undefined
          ? {}
          : { error_code: observation.error_code }),
      })) as CoreLookupResponse;
      return matchOf(response);
    } catch (error) {
      fail(error);
      return undefined;
    }
  }

  async function lookupIssue(id: string): Promise<Hit | undefined> {
    try {
      const connector = connectorFor();
      const response = (await connector.core.lookup({
        schema_version: "v1",
        project_key: projectKey,
        issue: id,
      })) as CoreLookupResponse;
      const best = response.candidates?.[0];
      return best === undefined ? undefined : hitFrom(best, projectKey);
    } catch (error) {
      fail(error);
      return undefined;
    }
  }

  async function lookupFingerprint(
    fingerprint: string,
  ): Promise<Hit | undefined> {
    try {
      const connector = connectorFor();
      const response = (await connector.core.lookup({
        schema_version: "v1",
        project_key: projectKey,
        fingerprint,
      })) as CoreLookupResponse;
      const best = response.candidates?.[0];
      return best === undefined ? undefined : hitFrom(best, projectKey);
    } catch (error) {
      fail(error);
      return undefined;
    }
  }

  async function search(
    query: string,
    limit = 3,
  ): Promise<CoreCandidate[] | undefined> {
    try {
      const connector = connectorFor();
      const response = (await connector.core.search({
        schema_version: "v1",
        query,
        project_key: projectKey,
        limit: Math.max(1, Math.min(limit, 3)),
      })) as { candidates?: CoreCandidate[] };
      return response.candidates ?? [];
    } catch (error) {
      fail(error);
      return undefined;
    }
  }

  async function entries(): Promise<IndexedEntry[] | undefined> {
    try {
      const connector = connectorFor();
      const response = (await connector.core.list({
        schema_version: "v1",
        kind: "issue",
        // The list contract caps a page at 100 (core MaxListLimit).
        limit: 100,
        project_key: projectKey,
      })) as CoreListResponse;
      const items = response.items ?? [];
      return items.map((item: CoreListItem) => {
        const status = statusOf(item.status);
        const excludedStatus: string = item.status ?? "";
        const entry = entryView(
          item.id,
          item.title,
          "",
          item.hits ?? 0,
          item.fix ?? "",
          status,
        );
        return {
          entry,
          sig: entry.fingerprint,
          category: entry.category,
          code: undefined,
          proj: projectKey,
          tokens: new Set<string>(),
          length: entry.title.length,
          excluded:
            excludedStatus === "wontfix" || excludedStatus === "misjudged"
              ? (excludedStatus as Exclusion)
              : undefined,
        };
      });
    } catch (error) {
      fail(error);
      return undefined;
    }
  }

  function fixed(id: string, fix: string): FixOutcome {
    return {
      kind: "fixed",
      entry: entryView(id, "", "", 0, fix, "fixed"),
    };
  }

  async function recordFix(id: string, fix: string): Promise<FixOutcome> {
    if (fix.trim() === "") return { kind: "timeout" };
    try {
      const connector = connectorFor();
      await connector.core.recordSolution({
        schema_version: "v1",
        issue_id: id,
        body: fix,
        source: "dsh",
        actor: "dsh",
      });
      return fixed(id, fix);
    } catch (error) {
      fail(error);
      return { kind: "failed" };
    }
  }

  async function setStatus(
    id: string,
    status: EntryStatus,
  ): Promise<FixOutcome> {
    try {
      const connector = connectorFor();
      await connector.core.setStatus({
        schema_version: "v1",
        issue_id: id,
        status,
      });
      return {
        kind: "fixed",
        entry: entryView(id, "", "", 0, "", status),
      };
    } catch (error) {
      fail(error);
      return { kind: "failed" };
    }
  }

  async function replay(): Promise<void> {
    try {
      await connectorFor().replay();
    } catch (error) {
      fail(error);
    }
  }

  const after: Recorder = {
    capture,
    agentError(payload) {
      return forward(
        {
          observed_at: new Date().toISOString(),
          source: "agent",
          message: safeErrorText(payload.error).message,
        },
        agentId(payload),
      );
    },
    requestError(payload) {
      const observation = observationFor(
        {
          kind: "llm",
          code: payload.failure.code,
          message: payload.failure.message,
        },
        captureExitCodes,
      );
      const session = agentId(payload);
      if (observation === undefined)
        return Promise.resolve(record({ kind: "timeout" }));
      if (!captureSources.has("llm"))
        return Promise.resolve(record({ kind: "timeout" }));
      if (!providerAllowed(payload.provider)) {
        // Mark the message as seen even though it is not recorded: the same
        // failure arrives a second time through agent/error, which carries no
        // provider, and "providers" decides whether the failure is kept at all.
        duplicate(observation, session);
        return Promise.resolve(record({ kind: "timeout" }));
      }
      return forward(observation, session);
    },
    toolResult(exec, result) {
      const input = toolInput(exec, result);
      if (input.kind === "tool") {
        return forward(observationFor(input, captureExitCodes), agentId(exec));
      }
      const observation = observationFor(input, captureExitCodes);
      if (observation === undefined) return undefined;
      return forward(observation, agentId(exec));
    },
    lookup,
    lookupIssue,
    lookupFingerprint,
    search,
    entries,
    recordFix,
    setStatus,
    async feedback(issueId, outcome) {
      try {
        const found = await lookupIssue(issueId);
        const version = (found?.entry as Entry & { solutionVersionId?: string })
          ?.solutionVersionId;
        if (found === undefined || version === undefined) return false;
        await connectorFor().core.feedback({
          schema_version: "v1",
          issue_id: issueId,
          solution_version_id: version,
          outcome,
        });
        return true;
      } catch (error) {
        fail(error);
        return false;
      }
    },
    fail,
    async idle() {
      while (inFlight.size > 0) await Promise.allSettled(inFlight);
    },
    outcomes,
    stats,
    get connector() {
      return connectorFor();
    },
    replay,
    projectKey,
  };
  return after;
}

// ---------------------------------------------------------------------------
// Injection

export const MAX_PENDING = 4;

/** Injection settings; every one has a default. */
export interface InjectionOptions {
  inject: InjectMode;
  captureFix: CaptureFixMode;
  sessionDigest: SessionDigestMode;
  systemPromptHint: boolean;
}

export const DEFAULT_INJECTION_OPTIONS: InjectionOptions = {
  inject: "hit-only",
  captureFix: "prompt-once",
  sessionDigest: "counts",
  systemPromptHint: true,
};

/** Everything the injection layer needs. */
export interface InjectionDeps {
  recorder: Recorder;
  options?: Partial<InjectionOptions>;
  /** Lower notice budgets, for every session. */
  caps?: Partial<CapLimits>;
}

/** `agent/pre-step`'s payload, as dsh-agent declares it. */
export type PreStepPayload = Parameters<Events["agent/pre-step"]>[0];

/** `PreStepDecision` from dsh-agent, through the event's return type. */
export type PreStepDecision = Awaited<ReturnType<Events["agent/pre-step"]>>;

/** `agent/session-start`'s payload, as dsh-agent declares it. */
export type SessionStartPayload = Parameters<Events["agent/session-start"]>[0];

/** `PromptSection` from dsh-system-prompt, through `SystemPrompt.section`. */
export type PromptSection = Parameters<Context["systemPrompt"]["section"]>[0];

/** The listener bodies of the four injection points. */
export interface Injection {
  readonly options: InjectionOptions;
  /** `agent/error`: record, and queue the notice for the next step. */
  agentError(payload: AgentErrorPayload): void;
  /**
   * `tools/result`: record, route a miss's new ID to the next step, and feed
   * resolution detection (T14).
   */
  toolResult(
    exec: Readonly<ToolExecution>,
    result: Readonly<ToolExecutionResult>,
  ): void;
  /** `tools/post-execute`: the downstream decision plus a notice. */
  postExecute(
    exec: Readonly<ToolExecution>,
    result: Readonly<ToolExecutionResult>,
    next: () => Promise<PostToolDecision>,
  ): Promise<PostToolDecision>;
  /** `agent/pre-step`: the downstream decision plus a pending notice. */
  preStep(
    payload: PreStepPayload,
    next: () => Promise<PreStepDecision>,
  ): Promise<PreStepDecision>;
  /** `agent/session-start`: fresh session state and the digest. */
  sessionStart(payload: SessionStartPayload): Promise<void>;
  /** The system-prompt section, or undefined under `systemPromptHint: false`. */
  section(): PromptSection | undefined;
  /**
   * Notices delivered so far (T15's `err_stats`): one session's since it
   * started, or every session's since the plugin started.
   */
  counts(session?: string): InjectionCounts;
}

/** Notices delivered, for `err_stats` (T15). */
export interface InjectionCounts {
  /** Every notice that reached the model. */
  notices: number;
  /** The notices that carried a recorded fix: `hit`, `near` or `doubted`. */
  fixNotices: number;
  /** Their bodies' size, by estimateTokens(). */
  noticeTokens: number;
}

const emptyCounts = (): InjectionCounts => ({
  notices: 0,
  fixNotices: 0,
  noticeTokens: 0,
});

const FIX_NOTICES: readonly NoticeKind[] = ["hit", "near", "doubted"];

/** One notice waiting for the next step. */
interface Pending {
  /** The lookup; a read, so the next step waits for it. */
  read: Promise<void>;
  hit?: Hit;
  /** The lookup missed and `inject: 'always'` wants the new ID announced. */
  miss: boolean;
  /** The write settled; `id` is set when it appended an entry. */
  written: boolean;
  id?: string;
  /** The fix prompt for this entry, which looks resolved (T14). */
  ask?: string;
  /** The resolver's turn the prompt was queued in; it lapses with the window. */
  turn?: number;
}

interface SessionState {
  injector: Injector;
  resolver: ResolutionTracker;
  turn: number | undefined;
  pending: Pending[];
  /**
   * Resolution events, handled one at a time in arrival order: a failure
   * waits for its write to name the entry before a later success is checked
   * against it. Never rejects.
   */
  resolving: Promise<void>;
  /** What this session was told, for `err_stats` (T15). */
  counts: InjectionCounts;
}

/** A user message carrying `text`, attributed to the plugin. */
function pluginMessage(text: string, source: NoticeSource): UserMessage {
  return createUserMessage({ content: [{ type: "text", text }], source });
}

/**
 * A hit whose hit count includes the occurrence being reported: the core is
 * about to add it, and "known (1 hit)" for the second sighting would undersell.
 */
function counted(hit: Hit): Hit {
  return { ...hit, entry: { ...hit.entry, hits: hit.entry.hits + 1 } };
}

/**
 * Bind the four injection points to a recorder.
 *
 * @param deps - the recorder, settings, trust store and caps.
 * @returns listener bodies that never throw into a turn.
 */
export function createInjection(deps: InjectionDeps): Injection {
  const o: InjectionOptions = { ...DEFAULT_INJECTION_OPTIONS, ...deps.options };
  const { recorder } = deps;
  const sessions = new Map<string, SessionState>();
  // A tool miss whose write will name the ID, keyed by the execution object
  // that `tools/post-execute` and `tools/result` both receive (the registry
  // freezes it in place before `tools/result`; identity is kept).
  const awaitingId = new WeakMap<object, SessionState>();
  const injecting = o.inject !== "off";
  const totals = emptyCounts();

  /** The message for a notice about to reach the model, counted. */
  function deliver(state: SessionState, notice: Notice): UserMessage {
    const tokens = estimateTokens(notice.text);
    const fix = FIX_NOTICES.includes(notice.kind) ? 1 : 0;
    for (const counts of [state.counts, totals]) {
      counts.notices++;
      counts.fixNotices += fix;
      counts.noticeTokens += tokens;
    }
    return pluginMessage(notice.text, notice.source);
  }

  /** This session's state, kept among the most recently active ones. */
  function stateFor(session: string, fresh = false): SessionState {
    const state = (!fresh && sessions.get(session)) || {
      injector: new Injector({
        mode: o.inject,
        caps: new CapTracker(deps.caps),
        scope: session,
      }),
      resolver: new ResolutionTracker(),
      turn: undefined,
      pending: [],
      resolving: Promise.resolve(),
      counts: emptyCounts(),
    };
    sessions.delete(session);
    sessions.set(session, state);
    if (sessions.size > MAX_SESSIONS)
      sessions.delete(sessions.keys().next().value as string);
    return state;
  }

  /**
   * `start()`, with a handler attached before anything else can run: a throw
   * or a rejection is counted and settles as undefined. Every promise the
   * hooks start and do not await at once goes through here, so none is ever
   * left to reject unhandled while `next()` runs (§13).
   */
  function guard<T>(start: () => Promise<T>): Promise<T | undefined> {
    return start().catch((error: unknown) => {
      recorder.fail(error);
      return undefined;
    });
  }

  /** Queue a notice for the next step; never rejects. */
  function track(
    state: SessionState,
    item: Pending,
    write: PendingWrite | undefined,
  ): void {
    if (write !== undefined)
      void guard(() => write).then((outcome) => {
        item.written = true;
        if (outcome?.kind === "appended") item.id = outcome.id;
      });
    else item.written = true;
    state.pending.push(item);
    if (state.pending.length > MAX_PENDING) state.pending.shift();
  }

  /**
   * Look a tool outcome up and offer it; never rejects. Reading the payload
   * is inside the try: a malformed result (`isError` with no `error`) throws
   * there, and this runs while `next()` is still pending.
   */
  async function toolNotice(
    exec: Readonly<ToolExecution>,
    result: Readonly<ToolExecutionResult>,
    state: SessionState,
  ): Promise<Notice | undefined> {
    try {
      const found = await recorder.lookup(toolInput(exec, result));
      if (found === undefined) return undefined;
      if (found.matched)
        return state.injector.offer({ kind: "hit", hit: counted(found) });
      if (o.inject === "always") awaitingId.set(exec, state);
      return undefined;
    } catch (error) {
      recorder.fail(error);
      return undefined;
    }
  }

  /** Queue a resolution step behind the session's earlier ones. */
  function resolving(state: SessionState, step: () => Promise<void>): void {
    state.resolving = state.resolving.then(() => guard(step)).then(() => {});
  }

  /**
   * Feed one tool outcome to resolution detection (T14). A failure is watched
   * once its write names the entry; a success resolves what it matches. The
   * turn is taken now, when the call happened.
   */
  function detect(
    state: SessionState,
    exec: Readonly<ToolExecution>,
    result: Readonly<ToolExecutionResult>,
    write: PendingWrite | undefined,
  ): void {
    const input = toolInput(exec, result);
    const outcome = callOutcome({
      toolName: exec.name,
      isError: input.kind === "tool",
      text: input.kind === "command" ? input.text : "",
      ...(input.kind === "command" && input.command !== undefined
        ? { command: input.command }
        : {}),
    });
    const turn = state.resolver.turn;
    if (!outcome.ok) {
      if (write === undefined) return;
      resolving(state, async () => {
        const written = await write;
        if (written.kind === "appended" || written.kind === "hit")
          state.resolver.occurred(written.id, outcome.key, turn);
      });
      return;
    }
    resolving(state, async () => {
      for (const id of state.resolver.succeeded(outcome.keys, turn))
        await resolved(state, id, turn);
    });
  }

  /**
   * `id` looks resolved: its fix earned a success, and an entry without one
   * gets the one-shot prompt on the next step, under `captureFix:
   * 'prompt-once'`.
   */
  async function resolved(
    state: SessionState,
    id: string,
    turn: number,
  ): Promise<void> {
    const found = await recorder.lookupIssue(id);
    if (found === undefined) return;
    const { entry } = found;
    // Outcome is the only trust path: the core's errkb_trust() owns trust.
    void recorder.feedback(id, "success");
    if (
      !injecting ||
      o.captureFix !== "prompt-once" ||
      entry.fix.trim() !== "" ||
      !state.resolver.ask(id)
    )
      return;
    // Trust is the core's (errkb_trust); this prompt only asks for the fix.
    track(
      state,
      { read: Promise.resolve(), miss: false, written: true, ask: id, turn },
      undefined,
    );
  }

  /** The first pending notice the caps allow; the rest are dropped. */
  async function drain(state: SessionState): Promise<Notice | undefined> {
    const items = state.pending.slice();
    await Promise.all(items.map((item) => item.read));
    const kept: Pending[] = [];
    let notice: Notice | undefined;
    for (const item of items) {
      if (item.hit !== undefined) {
        notice ??= state.injector.offer({ kind: "hit", hit: item.hit }, true);
      } else if (item.id !== undefined) {
        notice ??= state.injector.offer({ kind: "miss", id: item.id });
      } else if (
        item.ask !== undefined &&
        state.resolver.open(item.turn as number)
      ) {
        // A prompt the caps refuse now waits for a later step, until the
        // window it was asked in closes; then it is dropped unsaid.
        const asked =
          notice === undefined ? state.injector.ask(item.ask) : undefined;
        if (asked !== undefined) notice = asked;
        else kept.push(item);
      }
    }
    state.pending = state.pending.filter(
      (item) => !items.includes(item) || kept.includes(item),
    );
    return notice;
  }

  return {
    options: o,

    agentError(payload) {
      try {
        const session = agentId(payload);
        if (!injecting || session === undefined) {
          void recorder.agentError(payload);
          return;
        }
        const state = stateFor(session);
        // Queued before the write, so the error cannot hit its own new entry.
        const found = guard(() =>
          recorder.lookup({ kind: "agent", error: payload.error }),
        );
        const write = recorder.agentError(payload);
        const item: Pending = {
          read: Promise.resolve(),
          miss: false,
          written: false,
        };
        item.read = found.then((result) => {
          try {
            if (result === undefined) return;
            if (!result.matched) {
              item.miss = o.inject === "always";
              return;
            }
            state.injector.observe(result);
            item.hit = counted(result);
          } catch (error) {
            recorder.fail(error);
          }
        });
        track(state, item, write);
      } catch (error) {
        recorder.fail(error);
      }
    },

    toolResult(exec, result) {
      try {
        const write = recorder.toolResult(exec, result);
        const state = awaitingId.get(exec);
        if (state !== undefined) {
          awaitingId.delete(exec);
          track(
            state,
            { read: Promise.resolve(), miss: true, written: false },
            write,
          );
        }
        const session = agentId(exec);
        if (session !== undefined)
          detect(stateFor(session), exec, result, write);
      } catch (error) {
        recorder.fail(error);
      }
    },

    async postExecute(exec, result, next) {
      let pending: Promise<Notice | undefined> | undefined;
      let state: SessionState | undefined;
      try {
        const session = agentId(exec);
        if (injecting && session !== undefined) {
          state = stateFor(session);
          const current = state;
          // Guarded as it is created, not when it is awaited after next().
          pending = guard(() => toolNotice(exec, result, current));
        }
      } catch (error) {
        recorder.fail(error);
      }
      const decision = await next();
      if (pending === undefined || state === undefined) return decision;
      try {
        const notice = await pending;
        if (notice === undefined) return decision;
        return {
          ...decision,
          additionalContexts: [
            ...(decision.additionalContexts ?? []),
            deliver(state, notice),
          ],
        };
      } catch (error) {
        recorder.fail(error);
        return decision;
      }
    },

    async preStep(payload, next) {
      let state: SessionState | undefined;
      try {
        const session = agentId(payload);
        if (session === undefined) return next();
        state = stateFor(session);
        if (state.turn !== payload.turn) {
          state.turn = payload.turn;
          state.injector.beginTurn();
          state.resolver.beginTurn();
        }
        state.injector.beginStep();
      } catch (error) {
        recorder.fail(error);
      }
      const decision = await next();
      if (
        state === undefined ||
        decision.kind !== "enter" ||
        state.pending.length === 0
      )
        return decision;
      try {
        const notice = await drain(state);
        if (notice === undefined) return decision;
        return {
          ...decision,
          messages: [...decision.messages, deliver(state, notice)],
        };
      } catch (error) {
        recorder.fail(error);
        return decision;
      }
    },

    async sessionStart({ agent }) {
      try {
        stateFor(agent.id, true);
        if (o.sessionDigest === "off") return;
        const index = await recorder.entries();
        if (index === undefined) return;
        const text = sessionDigestText(
          index.map(({ entry, excluded }) => ({
            id: entry.id,
            title: entry.title,
            hits: entry.hits,
            injectable: excluded === undefined,
          })),
          o.sessionDigest,
        );
        if (text === undefined) return;
        const head = text.split("\n", 1)[0] as string;
        agent.inject(pluginMessage(text, noticeSource(head)));
      } catch (error) {
        recorder.fail(error);
      }
    },

    counts(session) {
      if (session === undefined) return { ...totals };
      const state = sessions.get(session);
      return state === undefined ? emptyCounts() : { ...state.counts };
    },

    section() {
      return o.systemPromptHint
        ? { ...SYSTEM_PROMPT_SECTION, text: SYSTEM_PROMPT_HINT }
        : undefined;
    },
  };
}

// ---------------------------------------------------------------------------
// Registration

/** The part of a cordis Context the wiring uses. */
export type ListenerHost = Pick<Context, "on">;

/** The part of a cordis Context the injection points use. */
export type InjectionHost = Pick<Context, "on" | "systemPrompt">;

/**
 * Register the three capture listeners (T11 + T16).
 *
 * @param ctx - the plugin's context.
 * @param recorder - from createRecorder().
 */
export function registerListeners(ctx: ListenerHost, recorder: Recorder): void {
  ctx.on("agent/error", (payload) => {
    void recorder.agentError(payload);
  });
  ctx.on("tools/result", (exec, result) => {
    void recorder.toolResult(exec, result);
    return undefined;
  });
  // `agent/request-error` is a waterfall (§13): the listener must await the
  // downstream chain and return its answer unchanged, and it must never take
  // the request over. The observation is awaited too: a fire-and-forget write
  // can be dropped when the host process exits right after a failed request.
  ctx.on("agent/request-error", async (payload, next) => {
    try {
      return await next();
    } finally {
      try {
        await recorder.requestError(payload);
      } catch {
        // Recording must never change the waterfall answer.
      }
    }
  });
}

/**
 * Register the capture listeners routed through the injection layer, the
 * three injection hooks and the system-prompt section (T11 + T13). A section
 * that cannot be registered (a duplicate name) is counted and logged; the
 * hooks still work without it.
 *
 * @param ctx - the plugin's context.
 * @param injection - from createInjection().
 * @param recorder - the recorder the injection layer was built on.
 */
export function registerInjection(
  ctx: InjectionHost,
  injection: Injection,
  recorder: Recorder,
): void {
  ctx.on("agent/error", (payload) => {
    injection.agentError(payload);
  });
  ctx.on("tools/result", (exec, result) => {
    injection.toolResult(exec, result);
    return undefined;
  });
  ctx.on("tools/post-execute", (exec, result, next) =>
    injection.postExecute(exec, result, next),
  );
  ctx.on("agent/pre-step", (payload, next) => injection.preStep(payload, next));
  // The promise goes back to the host: one that awaits its emit listeners
  // gets the digest queued before the first step; one that does not still
  // gets it at the next pre-step after the read. It never rejects.
  ctx.on("agent/session-start", (payload) => injection.sessionStart(payload));
  // Queued observations from a previous outage are replayed as a session
  // starts, so a restarted daemon drains the queue without a new failure.
  ctx.on("agent/session-start", () => {
    void recorder.replay();
    return undefined;
  });
  const section = injection.section();
  if (section === undefined) return;
  try {
    ctx.systemPrompt.section(section);
  } catch (error) {
    recorder.fail(error);
  }
}
