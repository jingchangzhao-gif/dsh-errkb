// The thin DSH connector (EKB-038c): the piece that replaces the local
// pipeline with a Named Pipe client. It captures raw observations, hands them
// to the daemon (`capture`), queues them while the daemon is down and replays
// them in order once it is back. Everything that turns an observation into
// knowledge - classification, redaction, fingerprints, retrieval, trust - runs
// in the core; nothing in this file may reimplement it (plan §3.2).
//
// This module is deliberately library-level: `src/index.ts` still wires the
// legacy recorder, and the host wiring plus the tool proxy land with the swap
// (EKB-038d). That keeps every intermediate commit green while the adapter, the
// queue and the client are built and tested.
//
// Wire format (internal/contract/local_api.go): one JSON object per line -
//   {"schema_version":"v1","request_id":"…","operation":"capture","payload":{…}}
// answered by
//   {"schema_version":"v1","request_id":"…","operation":"capture","result":{…}}
// or {"…","error":{"code":"unavailable","message":"…","retryable":true}}.
import { randomUUID } from "node:crypto";
import { connect as dial } from "node:net";
import type { Socket } from "node:net";
import { extractHeadline } from "./capture";
import { oneLine, WORDING } from "./inject";
import { createObservationQueue } from "./state";
import type { ObservationQueue, QueuedCapture, QueueStats } from "./state";

/** The connector SDK version on every request (spec/connector.cue). */
export const CONNECTOR_SDK_VERSION = "v1";

/** The default local pipe name (EKB-029). */
export const DEFAULT_PIPE = String.raw`\\.\pipe\errkb`;

/** How long one pipe call may wait for its answer. */
export const DEFAULT_PIPE_TIMEOUT_MS = 10_000;

/** The hook/plugin capture channel (§3.1): a structured host event. */
export const CONNECTOR_CHANNEL = "A";

/** The local API operations the connector uses. */
export type Operation =
  | "capture"
  | "lookup"
  | "search"
  | "record"
  | "feedback"
  | "list"
  | "stats"
  | "record_solution"
  | "set_status";

/** A failure as the host reports it, before the core classifies it. */
export interface RawObservation {
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
export interface CaptureOutcome {
  queued: boolean;
  created?: boolean;
  issueId?: string;
  fingerprint?: string;
}

/** What one replay pass did. */
export interface ReplayReport {
  replayed: number;
  remaining: number;
  /** The error that stopped the pass, if any. */
  error?: string;
}

/** A candidate as the core publishes it (spec/mcp-tools.cue #Candidate). */
export interface CoreCandidate {
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
export interface CoreListItem {
  id: string;
  title: string;
  updated_at: string;
  hits?: number;
  status?: string;
  fix?: string;
}

/** The `lookup` result. */
export interface CoreLookupResponse {
  candidates?: CoreCandidate[];
}

/** The `list` result. */
export interface CoreListResponse {
  items?: CoreListItem[];
  next_cursor?: string;
  total?: number;
}

/** One problem the connector answers locally: the reason and its wording. */
export interface ConnectorNotice {
  /** `hit` or `near` for a reusable fix, `no-fix` when only the issue is known. */
  kind: "hit" | "near" | "no-fix";
  text: string;
}

/** A core failure. `unavailable` (or a retryable error) while the daemon is down. */
export class CoreError extends Error {
  /** The closed error code: invalid_request/not_found/unavailable/conflict/internal. */
  readonly code: string;
  /** Whether replaying the same request may succeed later. */
  readonly retryable: boolean;

  /** @param code - the closed core error code. */
  constructor(code: string, message: string, retryable = false) {
    super(message);
    this.name = "CoreError";
    this.code = code;
    this.retryable = retryable;
  }

  /** True when the daemon is down (queue the observation instead of failing). */
  get unavailable(): boolean {
    return this.code === "unavailable" || this.retryable;
  }
}

/** The pipe transport: one newline-delimited JSON request per call. */
export interface Transport {
  call(operation: Operation, payload: unknown): Promise<unknown>;
}

/** Options of {@link createPipeTransport}. */
export interface PipeTransportOptions {
  /** The pipe to dial; defaults to {@link DEFAULT_PIPE}. */
  pipe?: string;
  /** Per-call timeout; defaults to {@link DEFAULT_PIPE_TIMEOUT_MS}. */
  timeoutMs?: number;
  /** The dialer; injectable so tests never need a real pipe. */
  connect?: (path: string) => Socket;
  /** Request-id source; injectable for deterministic tests. */
  requestId?: () => string;
}

interface PipeEnvelope {
  schema_version?: unknown;
  request_id?: unknown;
  operation?: unknown;
  result?: unknown;
  error?: { code?: unknown; message?: unknown; retryable?: unknown };
}

/**
 * A transport that dials the daemon for every call. The daemon may restart at
 * any time, so the connector never holds a long-lived connection; the pipe is
 * cheap on Windows and this keeps "daemon came back" handling trivial.
 */
export function createPipeTransport(
  options: PipeTransportOptions = {},
): Transport {
  const pipe = options.pipe ?? DEFAULT_PIPE;
  const timeoutMs = options.timeoutMs ?? DEFAULT_PIPE_TIMEOUT_MS;
  const connect = options.connect ?? dial;
  const nextId = options.requestId ?? (() => randomUUID());

  return {
    call(operation, payload) {
      const requestId = nextId();
      const line = `${JSON.stringify({
        schema_version: CONNECTOR_SDK_VERSION,
        request_id: requestId,
        operation,
        payload,
      })}\n`;

      return new Promise((resolve, reject) => {
        const socket = connect(pipe);
        let settled = false;
        let buffer = "";
        const finish = (action: () => void) => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          socket.destroy();
          action();
        };
        const timer = setTimeout(
          () =>
            finish(() =>
              reject(
                new CoreError(
                  "unavailable",
                  `the errkb daemon did not answer within ${timeoutMs} ms`,
                  true,
                ),
              ),
            ),
          timeoutMs,
        );

        socket.setEncoding("utf8");
        socket.on("data", (chunk: string) => {
          buffer += chunk;
          const end = buffer.indexOf("\n");
          if (end === -1) return;
          const text = buffer.slice(0, end);
          finish(() => {
            try {
              resolve(decodeResponse(text, requestId, operation));
            } catch (error) {
              reject(error);
            }
          });
        });
        socket.on("error", (error: Error) =>
          finish(() =>
            reject(
              new CoreError(
                "unavailable",
                `the errkb daemon is not reachable on ${pipe}: ${error.message}`,
                true,
              ),
            ),
          ),
        );
        socket.on("close", () =>
          finish(() =>
            reject(
              new CoreError(
                "unavailable",
                "the errkb daemon closed the pipe before answering",
                true,
              ),
            ),
          ),
        );
        socket.write(line);
      });
    },
  };
}

/** Decode and validate one response line. */
function decodeResponse(
  text: string,
  requestId: string,
  operation: Operation,
): unknown {
  let envelope: PipeEnvelope;
  try {
    envelope = JSON.parse(text) as PipeEnvelope;
  } catch {
    throw new CoreError(
      "internal",
      "the errkb daemon returned a response that is not JSON",
    );
  }
  if (envelope.schema_version !== CONNECTOR_SDK_VERSION)
    throw new CoreError(
      "internal",
      `the errkb daemon answered with schema ${String(envelope.schema_version)}, want ${CONNECTOR_SDK_VERSION}`,
    );
  if (envelope.request_id !== requestId)
    throw new CoreError(
      "internal",
      "the errkb daemon answered a different request",
    );
  if (envelope.operation !== operation)
    throw new CoreError(
      "internal",
      `the errkb daemon answered ${String(envelope.operation)} for ${operation}`,
    );
  if (envelope.error !== undefined) {
    const code = String(envelope.error.code ?? "internal");
    const message = String(envelope.error.message ?? "the errkb daemon failed");
    throw new CoreError(code, message, envelope.error.retryable === true);
  }
  if (envelope.result === undefined)
    throw new CoreError(
      "internal",
      "the errkb daemon answered without a result",
    );
  return envelope.result;
}

/** The core, as the adapter uses it. */
export interface Core {
  capture(request: QueuedCapture): Promise<{
    event_id: string;
    occurrence_id: string;
    issue_id: string;
    fingerprint: string;
    created: boolean;
    /** Set when the core spooled the event because its database was down. */
    queued?: boolean;
  }>;
  lookup(
    payload: Record<string, unknown>,
  ): Promise<{ candidates?: CoreCandidate[] }>;
  search(
    payload: Record<string, unknown>,
  ): Promise<{ candidates?: CoreCandidate[] }>;
  feedback(payload: Record<string, unknown>): Promise<unknown>;
  recordSolution(payload: Record<string, unknown>): Promise<unknown>;
  setStatus(payload: Record<string, unknown>): Promise<unknown>;
  list(payload: Record<string, unknown>): Promise<unknown>;
  stats(payload: Record<string, unknown>): Promise<unknown>;
}

/** A typed view over a {@link Transport}. */
export function createCore(transport: Transport): Core {
  const call = <T>(operation: Operation, payload: unknown) =>
    transport.call(operation, payload) as Promise<T>;
  return {
    capture: (request) => call("capture", request),
    lookup: (payload) => call("lookup", payload),
    search: (payload) => call("search", payload),
    feedback: (payload) => call("feedback", payload),
    recordSolution: (payload) => call("record_solution", payload),
    setStatus: (payload) => call("set_status", payload),
    list: (payload) => call("list", payload),
    stats: (payload) => call("stats", payload),
  };
}

/** How the adapter identifies itself to the core. */
export interface ConnectorIdentity {
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
export interface Connector {
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
export interface ConnectorOptions extends ConnectorIdentity {
  /** The core; defaults to a pipe transport over {@link DEFAULT_PIPE}. */
  core?: Core;
  /** The offline queue; without one a daemon outage fails the capture. */
  queue?: ObservationQueue;
  /** Where the runtime versions in an observation come from. */
  runtimeVersions?: Record<string, string>;
}

/** Build the thin connector adapter. */
export function createConnector(options: ConnectorOptions): Connector {
  const core = options.core ?? createCore(createPipeTransport());
  const queue = options.queue;
  const connectorId = options.connectorId;
  const sessionId = options.sessionId;
  const projectKey = options.projectKey ?? "default";
  const channel = options.channel ?? CONNECTOR_CHANNEL;
  const newObservationId = options.newObservationId ?? (() => randomUUID());
  const now = options.now ?? (() => new Date());

  const requestFor = (
    observation: RawObservation,
    observationId: string,
  ): QueuedCapture => ({
    schema_version: CONNECTOR_SDK_VERSION,
    connector_id: connectorId,
    session_id: sessionId,
    observation_id: observationId,
    channel,
    observation: {
      ...observation,
      project_key: observation.project_key ?? projectKey,
    },
  });

  async function send(request: QueuedCapture): Promise<CaptureOutcome> {
    const answer = await core.capture(request);
    return {
      queued: false,
      created: answer.created,
      issueId: answer.issue_id,
      fingerprint: answer.fingerprint,
    };
  }

  return {
    core,
    queue,

    async capture(observation) {
      const request = requestFor(observation, newObservationId());
      try {
        return await send(request);
      } catch (error) {
        if (error instanceof CoreError && error.unavailable) {
          if (queue === undefined)
            throw new CoreError(
              "unavailable",
              "the errkb daemon is down and no offline queue is configured",
              true,
            );
          await queue.enqueue(request, now());
          return { queued: true };
        }
        throw error;
      }
    },

    async replay() {
      if (queue === undefined) return { replayed: 0, remaining: 0 };
      const pending = await queue.list();
      let replayed = 0;
      for (const item of pending) {
        try {
          await send(item.request);
        } catch (error) {
          return {
            replayed,
            remaining: pending.length - replayed,
            error: error instanceof Error ? error.message : String(error),
          };
        }
        await queue.remove(item.observationId);
        replayed += 1;
      }
      return { replayed, remaining: 0 };
    },

    pending: () =>
      queue === undefined
        ? Promise.resolve({ pending: 0, bytes: 0, dropped: 0 })
        : queue.stats(),

    notice(candidates) {
      const best = candidates[0];
      if (best === undefined) return undefined;
      const fix = best.solution?.body ?? "";
      const approximate = best.approximate;
      if (fix.trim() === "")
        return {
          kind: "no-fix",
          text: oneLine(
            `[errkb] ${best.issue_id} seen before (${best.match_reason}), no fix recorded yet.`,
          ),
        };
      const closing = approximate ? WORDING.approximate : WORDING.hit;
      return {
        kind: approximate ? "near" : "hit",
        text: oneLine(
          `[errkb] ${best.issue_id} known | fix: ${fix}${approximate ? ` ${closing}` : ` ${closing}`}`,
        ),
      };
    },
  };
}

/** What a DSH agent/error payload looks like (subset the adapter reads). */
export interface AgentErrorPayload {
  agent?: { id?: string };
  error?: unknown;
}

/** Extract one line of text from whatever an agent failure carries. */
function errorText(value: unknown): string {
  if (typeof value === "string") return value;
  if (value instanceof Error) return value.message;
  if (typeof value === "object" && value !== null) {
    const message = (value as { message?: unknown }).message;
    if (typeof message === "string") return message;
  }
  return String(value);
}

/**
 * Turn an agent failure into a raw observation. The core decides the category,
 * severity, transient flag, fingerprint and the redacted texts.
 */
export function observationFromAgentError(
  payload: AgentErrorPayload,
  options: {
    now?: () => Date;
    projectKey?: string;
    runtimeVersions?: Record<string, string>;
  } = {},
): RawObservation {
  const now = options.now ?? (() => new Date());
  return {
    observed_at: now().toISOString(),
    source: "agent",
    message: errorText(payload.error),
    ...(payload.agent?.id === undefined ? {} : { tool_name: payload.agent.id }),
    ...(options.projectKey === undefined
      ? {}
      : { project_key: options.projectKey }),
    ...(options.runtimeVersions === undefined
      ? {}
      : { runtime_versions: options.runtimeVersions }),
    evidence: "dsh agent/error",
  };
}

/** What a DSH tool execution looks like (subset the adapter reads). */
export interface ToolExecutionLike {
  tool?: { name?: string };
  call?: { name?: string };
  input?: Record<string, unknown>;
}

/** What a DSH tool result looks like (subset the adapter reads). */
export interface ToolResultLike {
  isError?: boolean;
  text?: string;
  content?: unknown;
  output?: unknown;
}

/** The text of a tool result, whether it carries text or blocks. */
function toolResultText(result: ToolResultLike): string {
  if (typeof result.text === "string") return result.text;
  if (typeof result.content === "string") return result.content;
  if (Array.isArray(result.content)) {
    const parts: string[] = [];
    for (const block of result.content) {
      if (typeof block === "object" && block !== null) {
        const text = (block as { text?: unknown }).text;
        if (typeof text === "string") parts.push(text);
      }
    }
    if (parts.length > 0) return parts.join("\n");
  }
  if (typeof result.output === "string") return result.output;
  return JSON.stringify(result.output ?? result.content ?? "");
}

/** The command a tool execution ran, when its input names one. */
function commandOf(exec: ToolExecutionLike): string | undefined {
  const input = exec.input ?? {};
  const keys = ["command", "cmd", "script"];
  for (const key of keys) {
    const value = input[key];
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return undefined;
}

/**
 * Turn a failed tool result into a raw observation. A successful result
 * returns undefined: the knowledge base records failures, not every call.
 */
export function observationFromToolResult(
  exec: ToolExecutionLike,
  result: ToolResultLike,
  options: {
    now?: () => Date;
    projectKey?: string;
    runtimeVersions?: Record<string, string>;
  } = {},
): RawObservation | undefined {
  const text = toolResultText(result);
  const exit = /\[exit code: (\d+)\]/.exec(text);
  const exitCode = exit === null ? undefined : Number(exit[1]);
  const failed =
    result.isError === true || (exitCode !== undefined && exitCode !== 0);
  if (!failed) return undefined;
  const now = options.now ?? (() => new Date());
  const command = commandOf(exec);
  const toolName = exec.tool?.name ?? exec.call?.name;
  const headline = extractHeadline(text);
  return {
    observed_at: now().toISOString(),
    source: "tool",
    message: text,
    ...(headline.code === undefined ? {} : { error_code: headline.code }),
    ...(command === undefined ? {} : { command }),
    ...(exitCode === undefined ? {} : { exit_code: exitCode }),
    ...(toolName === undefined ? {} : { tool_name: toolName }),
    ...(options.projectKey === undefined
      ? {}
      : { project_key: options.projectKey }),
    ...(options.runtimeVersions === undefined
      ? {}
      : { runtime_versions: options.runtimeVersions }),
    evidence: "dsh tools/result",
  };
}

/**
 * Build the connector-local queue over `state.json`.
 *
 * @param files - `state` is the queue file, `lock` the knowledge base lock.
 * @param options - the caps and injectable filesystem/clock (tests).
 */
export function createConnectorQueue(
  files: Parameters<typeof createObservationQueue>[0]["files"],
  options: Parameters<typeof createObservationQueue>[0]["options"] = {},
): ObservationQueue {
  return createObservationQueue({ files, options });
}
