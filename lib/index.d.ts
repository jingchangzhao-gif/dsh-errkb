import z from "@deepseek-ai/schemastery";
import { Socket } from "node:net";
import { Context } from "@deepseek-ai/cordis";

//#region src/paths.d.ts
/** Absolute paths of the six files inside one knowledge base directory. */
interface KbFiles {
  errors: string;
  archive: string;
  index: string;
  state: string;
  machine: string;
  lock: string;
}
//#endregion
//#region src/store.d.ts
/** The filesystem operations the store needs, injected for testability. */
interface StoreFs {
  /** File contents, or undefined when the file does not exist. */
  readFile(path: string): Promise<string | undefined>;
  writeFile(path: string, data: string): Promise<void>;
  appendFile(path: string, data: string): Promise<void>;
  rename(from: string, to: string): Promise<void>;
  /** Create a file only if it does not exist (`wx`); false when it does. */
  createExclusive(path: string, data: string): Promise<boolean>;
  /** Modification time in ms, or undefined when the file does not exist. */
  mtimeMs(path: string): Promise<number | undefined>;
  /** Remove a file; a missing file is not an error. */
  remove(path: string): Promise<void>;
  /** Names in a directory; a missing directory lists as empty. */
  list(dir: string): Promise<string[]>;
  mkdir(dir: string): Promise<void>;
}
/** Time and randomness, injected for testability. */
interface StoreClock {
  now(): Date;
  sleep(ms: number): Promise<void>;
  random(): number;
}
//#endregion
//#region src/state.d.ts
/** How many observations and bytes the queue keeps before dropping the oldest. */
interface QueueLimits {
  maxEvents: number;
  maxBytes: number;
}
/** One capture request; mirrors the pipe capture envelope (spec/connector.cue). */
interface QueuedCapture {
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
interface QueuedObservation {
  observationId: string;
  queuedAt: string;
  request: QueuedCapture;
}
/** What `stats()` reports: what is waiting and what was dropped by the caps. */
interface QueueStats {
  pending: number;
  bytes: number;
  dropped: number;
}
/** The connector-local queue. */
interface ObservationQueue {
  enqueue(request: QueuedCapture, at?: Date): Promise<QueueStats>;
  list(): Promise<QueuedObservation[]>;
  remove(observationId: string): Promise<void>;
  stats(): Promise<QueueStats>;
}
/** Dependencies of `createObservationQueue`. */
interface ObservationQueueDeps {
  /** `files.state` is the queue file; `files.lock` is the knowledge base lock. */
  files: Pick<KbFiles, "state" | "lock">;
  options?: Partial<QueueLimits>;
  fs?: StoreFs;
  clock?: StoreClock;
}
/** Build the connector-local observation queue. */
declare function createObservationQueue(deps: ObservationQueueDeps): ObservationQueue;
//#endregion
//#region src/connector.d.ts
/** The connector SDK version on every request (spec/connector.cue). */
declare const CONNECTOR_SDK_VERSION = "v1";
/** The default local pipe name (EKB-029). */
declare const DEFAULT_PIPE: string;
/** How long one pipe call may wait for its answer. */
declare const DEFAULT_PIPE_TIMEOUT_MS = 10000;
/** The hook/plugin capture channel (§3.1): a structured host event. */
declare const CONNECTOR_CHANNEL = "A";
/** The local API operations the connector uses. */
type Operation = "capture" | "lookup" | "search" | "record" | "feedback" | "list" | "stats" | "record_solution" | "set_status";
/** A failure as the host reports it, before the core classifies it. */
interface RawObservation {
  observed_at: string;
  source: "tool" | "command-exit" | "llm" | "agent";
  message: string;
  error_code?: string;
  command?: string;
  exit_code?: number;
  tool_name?: string;
  os?: string;
  shell?: string;
  runtime_versions?: Record<string, string>;
  project_key?: string;
  evidence?: string;
  confidence?: number;
}
/** What `capture()` did: stored now, or queued for a later replay. */
interface CaptureOutcome {
  queued: boolean;
  created?: boolean;
  issueId?: string;
  fingerprint?: string;
}
/** What one replay pass did. */
interface ReplayReport {
  replayed: number;
  remaining: number;
  /** The error that stopped the pass, if any. */
  error?: string;
}
/** A candidate as the core publishes it (spec/mcp-tools.cue #Candidate). */
interface CoreCandidate {
  issue_id: string;
  fingerprint: string;
  title: string;
  match_reason: string;
  match_detail: string;
  approximate: boolean;
  confidence?: number | null;
  /** How many times this issue was seen (EKB-038d additive contract field). */
  hits?: number;
  solution?: {
    solution_version_id: string;
    body: string;
    source: string;
    applies_to: string;
    trust: number;
  };
}
/** One row as the core's `list` returns it (spec/mcp-tools.cue #ListItem). */
interface CoreListItem {
  id: string;
  title: string;
  updated_at: string;
  hits?: number;
  status?: string;
  fix?: string;
}
/** The `lookup` result. */
interface CoreLookupResponse {
  candidates?: CoreCandidate[];
}
/** The `list` result. */
interface CoreListResponse {
  items?: CoreListItem[];
  next_cursor?: string;
  total?: number;
}
/** One problem the connector answers locally: the reason and its wording. */
interface ConnectorNotice {
  /** `hit` or `near` for a reusable fix, `no-fix` when only the issue is known. */
  kind: "hit" | "near" | "no-fix";
  text: string;
}
/** A core failure. `unavailable` (or a retryable error) while the daemon is down. */
declare class CoreError extends Error {
  /** The closed error code: invalid_request/not_found/unavailable/conflict/internal. */
  readonly code: string;
  /** Whether replaying the same request may succeed later. */
  readonly retryable: boolean;
  /** @param code - the closed core error code. */
  constructor(code: string, message: string, retryable?: boolean);
  /** True when the daemon is down (queue the observation instead of failing). */
  get unavailable(): boolean;
}
/** The pipe transport: one newline-delimited JSON request per call. */
interface Transport {
  call(operation: Operation, payload: unknown): Promise<unknown>;
}
/** Options of {@link createPipeTransport}. */
interface PipeTransportOptions {
  /** The pipe to dial; defaults to {@link DEFAULT_PIPE}. */
  pipe?: string;
  /** Per-call timeout; defaults to {@link DEFAULT_PIPE_TIMEOUT_MS}. */
  timeoutMs?: number;
  /** The dialer; injectable so tests never need a real pipe. */
  connect?: (path: string) => Socket;
  /** Request-id source; injectable for deterministic tests. */
  requestId?: () => string;
}
/**
 * A transport that dials the daemon for every call. The daemon may restart at
 * any time, so the connector never holds a long-lived connection; the pipe is
 * cheap on Windows and this keeps "daemon came back" handling trivial.
 */
declare function createPipeTransport(options?: PipeTransportOptions): Transport;
/** The core, as the adapter uses it. */
interface Core {
  capture(request: QueuedCapture): Promise<{
    event_id: string;
    occurrence_id: string;
    issue_id: string;
    fingerprint: string;
    created: boolean;
  }>;
  lookup(payload: Record<string, unknown>): Promise<{
    candidates?: CoreCandidate[];
  }>;
  search(payload: Record<string, unknown>): Promise<{
    candidates?: CoreCandidate[];
  }>;
  feedback(payload: Record<string, unknown>): Promise<unknown>;
  recordSolution(payload: Record<string, unknown>): Promise<unknown>;
  setStatus(payload: Record<string, unknown>): Promise<unknown>;
  list(payload: Record<string, unknown>): Promise<unknown>;
  stats(payload: Record<string, unknown>): Promise<unknown>;
}
/** A typed view over a {@link Transport}. */
declare function createCore(transport: Transport): Core;
/** How the adapter identifies itself to the core. */
interface ConnectorIdentity {
  /** The connector row name, e.g. `dsh`. */
  connectorId: string;
  /** The host session id; one per DSH session. */
  sessionId: string;
  /** The project the failures belong to; defaults to `default`. */
  projectKey?: string;
  /** The capture channel; defaults to {@link CONNECTOR_CHANNEL}. */
  channel?: string;
  /** Observation-id source; injectable for deterministic tests. */
  newObservationId?: () => string;
  /** Clock; injectable for deterministic tests. */
  now?: () => Date;
}
/** The adapter the host uses: capture, replay and the read/write calls. */
interface Connector {
  readonly core: Core;
  readonly queue?: ObservationQueue;
  /** Capture one observation, queueing it when the daemon is down. */
  capture(observation: RawObservation): Promise<CaptureOutcome>;
  /** Replay queued observations in order; stops at the first failure. */
  replay(): Promise<ReplayReport>;
  /** What is waiting in the queue. */
  pending(): Promise<QueueStats>;
  /** Render an injected notice from the best candidate, or undefined. */
  notice(candidates: readonly CoreCandidate[]): ConnectorNotice | undefined;
}
/** Options of {@link createConnector}. */
interface ConnectorOptions extends ConnectorIdentity {
  /** The core; defaults to a pipe transport over {@link DEFAULT_PIPE}. */
  core?: Core;
  /** The offline queue; without one a daemon outage fails the capture. */
  queue?: ObservationQueue;
  /** Where the runtime versions in an observation come from. */
  runtimeVersions?: Record<string, string>;
}
/** Build the thin connector adapter. */
declare function createConnector(options: ConnectorOptions): Connector;
/** What a DSH agent/error payload looks like (subset the adapter reads). */
interface AgentErrorPayload {
  agent?: {
    id?: string;
  };
  error?: unknown;
}
/**
 * Turn an agent failure into a raw observation. The core decides the category,
 * severity, transient flag, fingerprint and the redacted texts.
 */
declare function observationFromAgentError(payload: AgentErrorPayload, options?: {
  now?: () => Date;
  projectKey?: string;
  runtimeVersions?: Record<string, string>;
}): RawObservation;
/** What a DSH tool execution looks like (subset the adapter reads). */
interface ToolExecutionLike {
  tool?: {
    name?: string;
  };
  call?: {
    name?: string;
  };
  input?: Record<string, unknown>;
}
/** What a DSH tool result looks like (subset the adapter reads). */
interface ToolResultLike {
  isError?: boolean;
  text?: string;
  content?: unknown;
  output?: unknown;
}
/**
 * Turn a failed tool result into a raw observation. A successful result
 * returns undefined: the knowledge base records failures, not every call.
 */
declare function observationFromToolResult(exec: ToolExecutionLike, result: ToolResultLike, options?: {
  now?: () => Date;
  projectKey?: string;
  runtimeVersions?: Record<string, string>;
}): RawObservation | undefined;
/**
 * Build the connector-local queue over `state.json`.
 *
 * @param files - `state` is the queue file, `lock` the knowledge base lock.
 * @param options - the caps and injectable filesystem/clock (tests).
 */
declare function createConnectorQueue(files: Parameters<typeof createObservationQueue>[0]["files"], options?: Parameters<typeof createObservationQueue>[0]["options"]): ObservationQueue;
//#endregion
//#region src/inject.d.ts
/** Values of the `inject` setting. */
declare const INJECT_MODES: readonly ["hit-only", "always", "off"];
/** `hit-only` keeps misses silent; `always` announces them; `off` says nothing. */
type InjectMode = (typeof INJECT_MODES)[number];
/** Values of the `sessionDigest` setting. */
declare const SESSION_DIGEST_MODES: readonly ["off", "counts", "index"];
/** `off` says nothing; `counts` one line; `index` adds up to 10 titles. */
type SessionDigestMode = (typeof SESSION_DIGEST_MODES)[number];
//#endregion
//#region src/resolve-detect.d.ts
/** Values of the `captureFix` setting. */
declare const CAPTURE_FIX_MODES: readonly ["prompt-once", "off"];
/** `prompt-once` asks once per entry and session for a missing fix; `off` never. */
type CaptureFixMode = (typeof CAPTURE_FIX_MODES)[number];
//#endregion
//#region src/plugin.d.ts
/** Recorder settings; every one has a default. */
interface RecorderOptions {
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
/** Injection settings; every one has a default. */
interface InjectionOptions {
  inject: InjectMode;
  captureFix: CaptureFixMode;
  sessionDigest: SessionDigestMode;
  systemPromptHint: boolean;
}
//#endregion
//#region src/index.d.ts
declare const name = "err-kb";
declare const inject: string[];
declare const Config: z<Schemastery.ObjectS<{
  pipe: z<string, string>;
  pipeTimeoutMs: z<number, number>;
  offlineMaxEvents: z<number, number>;
  offlineMaxBytes: z<number, number>;
  connectorId: z<string, string>;
  projectKey: z<string, string>;
  capture: z<string[], string[]>;
  captureExitCodes: z<boolean, boolean>;
  transientThreshold: z<number, number>;
  fuzzyThreshold: z<number, number>;
  captureFix: z<string, string>;
  inject: z<string, string>;
  sessionDigest: z<string, string>;
  systemPromptHint: z<boolean, boolean>;
  kbDir: z<string, string>;
  idPrefix: z<string, string>;
  idWidth: z<number, number>;
  providers: z<string[], string[]>;
  share: z<string, string>;
  maxEntries: z<number, number>;
  maxSampleChars: z<number, number>;
  exportDir: z<string, string>;
  labels: z<"en" | "zh", "en" | "zh">;
}>, Schemastery.ObjectT<{
  pipe: z<string, string>;
  pipeTimeoutMs: z<number, number>;
  offlineMaxEvents: z<number, number>;
  offlineMaxBytes: z<number, number>;
  connectorId: z<string, string>;
  projectKey: z<string, string>;
  capture: z<string[], string[]>;
  captureExitCodes: z<boolean, boolean>;
  transientThreshold: z<number, number>;
  fuzzyThreshold: z<number, number>;
  captureFix: z<string, string>;
  inject: z<string, string>;
  sessionDigest: z<string, string>;
  systemPromptHint: z<boolean, boolean>;
  kbDir: z<string, string>;
  idPrefix: z<string, string>;
  idWidth: z<number, number>;
  providers: z<string[], string[]>;
  share: z<string, string>;
  maxEntries: z<number, number>;
  maxSampleChars: z<number, number>;
  exportDir: z<string, string>;
  labels: z<"en" | "zh", "en" | "zh">;
}>>;
/** The plugin's settings, as apply() receives them. */
type Config = Schemastery.TypeT<typeof Config>;
/**
 * The `idPrefix` shape: letters, digits, `-` and `_`, not ending in a digit, so
 * the trailing digits of an ID are its number alone and a prefix never spans a
 * header's ` · `.
 */
declare const ID_PREFIX_PATTERN: RegExp;
/** The documented ranges, checked by normalizeSettings(). */
declare const SETTING_BOUNDS: {
  readonly fuzzyThreshold: {
    readonly min: 0.5;
    readonly max: 1;
    readonly integer: false;
  };
  readonly idWidth: {
    readonly min: 1;
    readonly max: 9;
    readonly integer: true;
  };
  readonly maxEntries: {
    readonly min: 1;
    readonly max: number;
    readonly integer: true;
  };
  readonly maxSampleChars: {
    readonly min: 0;
    readonly max: number;
    readonly integer: true;
  };
  readonly pipeTimeoutMs: {
    readonly min: 1;
    readonly max: 600000;
    readonly integer: true;
  };
  readonly offlineMaxEvents: {
    readonly min: 1;
    readonly max: 100000;
    readonly integer: true;
  };
  readonly offlineMaxBytes: {
    readonly min: 1;
    readonly max: number;
    readonly integer: true;
  };
};
/**
 * Keep the numeric settings and `idPrefix` inside their documented ranges, so
 * a typo degrades instead of silently breaking matching or the connector.
 * `transientThreshold` follows capture.ts's own rule
 * (effectiveTransientThreshold), reported here so the change is visible.
 *
 * @param config - the plugin's settings.
 * @param warn - called once per setting that changed.
 * @returns the settings with every out-of-range value replaced.
 */
declare function normalizeSettings(config: Config, warn?: (message: string) => void): Config;
/**
 * The recorder settings a configuration selects: which sources are captured,
 * and which connector/project the observations belong to.
 *
 * @param config - the plugin's settings.
 * @returns settings for createRecorder().
 */
declare function recorderOptions(config: Config): RecorderOptions;
/**
 * The injection settings a configuration selects.
 *
 * @param config - the plugin's settings.
 * @returns the settings for createInjection(); an unknown `inject`,
 *   `captureFix` or `sessionDigest` value takes its default.
 */
declare function injectionOptions(config: Config): InjectionOptions;
declare function apply(ctx: Context, given: Config): void;
//#endregion
export { AgentErrorPayload, CONNECTOR_CHANNEL, CONNECTOR_SDK_VERSION, CaptureOutcome, Config, Connector, ConnectorIdentity, ConnectorNotice, ConnectorOptions, Core, CoreCandidate, CoreError, CoreListItem, CoreListResponse, CoreLookupResponse, DEFAULT_PIPE, DEFAULT_PIPE_TIMEOUT_MS, ID_PREFIX_PATTERN, Operation, PipeTransportOptions, RawObservation, ReplayReport, SETTING_BOUNDS, ToolExecutionLike, ToolResultLike, Transport, apply, createConnector, createConnectorQueue, createCore, createPipeTransport, inject, injectionOptions, name, normalizeSettings, observationFromAgentError, observationFromToolResult, recorderOptions };
//# sourceMappingURL=index.d.ts.map