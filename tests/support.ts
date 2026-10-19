// Shared fakes for the adapter tests (EKB-038d). Nothing here touches the
// real pipe, a database or a knowledge-base directory: the core, the queue and
// the recorder are all in memory, so the tests describe the adapter contract.
import type { CaptureInput } from "../src/capture";
import type {
  CaptureOutcome,
  Connector,
  ConnectorNotice,
  Core,
  CoreCandidate,
  CoreListItem,
  RawObservation,
  ReplayReport,
} from "../src/connector";
import type { Hit, IndexedEntry, MatchResult } from "../src/match";
import type {
  FixOutcome,
  Recorder,
  RecordOutcome,
  RecorderStats,
} from "../src/plugin";
import type {
  ObservationQueue,
  QueueStats,
  QueuedCapture,
  QueuedObservation,
} from "../src/state";
import type { Entry, EntryStatus } from "../src/store";

/** One call the fake core received. */
export interface CoreCall {
  operation: string;
  payload: Record<string, unknown>;
}

/** The fake core's programmable answers. */
export interface FakeCoreOptions {
  candidate?: CoreCandidate;
  items?: CoreListItem[];
  reject?: (operation: string) => Error | undefined;
}

/** A core that records its calls and answers from `options`. */
export function fakeCore(options: FakeCoreOptions = {}): Core & {
  calls: CoreCall[];
} {
  const calls: CoreCall[] = [];
  const answer = (operation: string, payload: unknown) => {
    const record = payload as Record<string, unknown>;
    const reject = options.reject?.(operation);
    if (reject !== undefined) return Promise.reject(reject);
    calls.push({ operation, payload: record });
    switch (operation) {
      case "capture":
        return Promise.resolve({
          event_id: "00000000-0000-4000-8000-000000000001",
          occurrence_id: "00000000-0000-4000-8000-000000000002",
          issue_id: "00000000-0000-4000-8000-000000000003",
          fingerprint: "0123456789ab",
          created: true,
        });
      case "lookup":
      case "search":
        return Promise.resolve(
          options.candidate === undefined
            ? { candidates: [] }
            : { candidates: [options.candidate] },
        );
      case "list":
        return Promise.resolve({
          items: options.items ?? [],
          next_cursor: undefined,
        });
      case "record_solution":
        return Promise.resolve({
          solution_id: "00000000-0000-4000-8000-000000000004",
          solution_version_id: "00000000-0000-4000-8000-000000000005",
          version: 1,
          recorded: true,
        });
      case "set_status":
        return Promise.resolve({
          issue_id: record.issue_id,
          status: record.status,
        });
      default:
        return Promise.resolve({});
    }
  };
  return {
    calls,
    capture: (request) => answer("capture", request) as never,
    lookup: (payload) => answer("lookup", payload) as never,
    search: (payload) => answer("search", payload) as never,
    feedback: (payload) => answer("feedback", payload) as never,
    recordSolution: (payload) => answer("record_solution", payload) as never,
    setStatus: (payload) => answer("set_status", payload) as never,
    list: (payload) => answer("list", payload) as never,
    stats: (payload) => answer("stats", payload) as never,
  };
}

/** A memory queue: the connector contract without disk. */
export function memoryQueue(): ObservationQueue & { items: QueuedCapture[] } {
  const items: QueuedCapture[] = [];
  let dropped = 0;
  return {
    items,
    enqueue(request: QueuedCapture) {
      items.push(request);
      return Promise.resolve({
        pending: items.length,
        bytes: 0,
        dropped,
      } satisfies QueueStats);
    },
    list() {
      return Promise.resolve(
        items.map(
          (request) =>
            ({
              observationId: request.observation_id,
              queuedAt: new Date(0).toISOString(),
              request,
            }) satisfies QueuedObservation,
        ),
      );
    },
    remove(observationId: string) {
      const at = items.findIndex((i) => i.observation_id === observationId);
      if (at >= 0) items.splice(at, 1);
      return Promise.resolve();
    },
    stats(): Promise<QueueStats> {
      return Promise.resolve({
        pending: items.length,
        bytes: 0,
        dropped,
      });
    },
  };
}

/** A connector over a fake core and a memory queue. */
export function fakeConnector(
  options: FakeCoreOptions = {},
  overrides: Partial<Connector> = {},
): Connector & {
  captured: RawObservation[];
  core: ReturnType<typeof fakeCore>;
} {
  const core = fakeCore(options);
  const captured: RawObservation[] = [];
  const connector: Connector & {
    captured: RawObservation[];
    core: ReturnType<typeof fakeCore>;
  } = {
    core: core as ReturnType<typeof fakeCore>,
    queue: memoryQueue(),
    captured,
    capture(observation: RawObservation) {
      captured.push(observation);
      return Promise.resolve({
        queued: false,
        created: true,
        issueId: "00000000-0000-4000-8000-000000000003",
        fingerprint: "0123456789ab",
      } satisfies CaptureOutcome);
    },
    replay(): Promise<ReplayReport> {
      return Promise.resolve({ replayed: 0, remaining: 0 });
    },
    pending() {
      return Promise.resolve({ pending: 0, bytes: 0, dropped: 0 });
    },
    notice(): ConnectorNotice | undefined {
      return undefined;
    },
    ...overrides,
  } as unknown as Connector & {
    captured: RawObservation[];
    core: ReturnType<typeof fakeCore>;
  };
  connector.core = core;
  return connector;
}

/** What a candidate or list row looks like for the fake recorder. */
export const candidate = (
  overrides: Partial<CoreCandidate> = {},
): CoreCandidate => ({
  issue_id: "00000000-0000-4000-8000-000000000003",
  fingerprint: "0123456789ab",
  title: "EPERM: operation not permitted, rename",
  match_reason: "exact_fingerprint",
  match_detail: "",
  approximate: false,
  hits: 3,
  solution: {
    solution_version_id: "00000000-0000-4000-8000-000000000005",
    body: "Close the open handle first.",
    source: "dsh",
    applies_to: "os=windows",
    trust: 0.5,
  },
  ...overrides,
});

/** An entry the fake recorder's lookups return. */
export const entry = (overrides: Partial<Entry> = {}): Entry => ({
  id: "00000000-0000-4000-8000-000000000003",
  title: "EPERM: operation not permitted, rename",
  meta: { sig: "0123456789ab" },
  fingerprint: "0123456789ab",
  category: "",
  firstSeen: "",
  lastSeen: "",
  hits: 3,
  trigger: "",
  raw: "",
  fix: "Close the open handle first.",
  status: "fixed",
  notes: "",
  ...overrides,
});

/** An index entry built from {@link entry}. */
export function indexed(overrides: Partial<Entry> = {}): IndexedEntry {
  const built = entry(overrides);
  return {
    entry: built,
    sig: built.fingerprint,
    category: built.category,
    code: undefined,
    proj: "default",
    tokens: new Set<string>(),
    length: built.title.length,
    excluded: undefined,
  };
}

/** The hit shape a lookup resolves to. */
export const hit = (overrides: Partial<Hit> = {}): Hit => ({
  matched: true,
  id: "00000000-0000-4000-8000-000000000003",
  entry: entry(),
  via: "exact",
  approximate: false,
  similarity: 1,
  injectable: true,
  ...overrides,
});

/** Programmable behaviour of {@link fakeRecorder}. */
export interface FakeRecorderOptions {
  hits?: MatchResult | undefined;
  issue?: Hit | undefined;
  fingerprintHit?: Hit | undefined;
  entries?: IndexedEntry[];
  captureOutcome?: RecordOutcome;
  fixOutcome?: FixOutcome;
  statusOutcome?: FixOutcome;
  search?: CoreCandidate[] | undefined;
}

/** A recorder the injection layer and the tools run on, in memory. */
export function fakeRecorder(options: FakeRecorderOptions = {}): Recorder & {
  calls: string[];
  captured: RawObservation[];
} {
  const calls: string[] = [];
  const captured: RawObservation[] = [];
  const outcomes: RecordOutcome[] = [];
  const stats: RecorderStats = {
    appended: 0,
    hits: 0,
    counted: 0,
    timeouts: 0,
    failures: 0,
  };
  const index = options.entries ?? [indexed()];
  const recorder: Recorder & { calls: string[]; captured: RawObservation[] } = {
    calls,
    captured,
    capture(input: CaptureInput) {
      calls.push(`capture:${input.kind}`);
      captured.push({
        observed_at: new Date(0).toISOString(),
        source: "tool",
        message: "x",
      });
      const outcome = options.captureOutcome ?? {
        kind: "appended",
        id: entry().id,
      };
      outcomes.push(outcome);
      return Promise.resolve(outcome);
    },
    agentError() {
      calls.push("agentError");
      return Promise.resolve({ kind: "appended", id: entry().id });
    },
    requestError() {
      calls.push("requestError");
      return Promise.resolve({ kind: "appended", id: entry().id });
    },
    toolResult() {
      calls.push("toolResult");
      return Promise.resolve({ kind: "appended", id: entry().id });
    },
    lookup() {
      calls.push("lookup");
      return Promise.resolve(options.hits ?? { matched: false });
    },
    lookupIssue() {
      calls.push("lookupIssue");
      return Promise.resolve(options.issue);
    },
    lookupFingerprint() {
      calls.push("lookupFingerprint");
      return Promise.resolve(options.fingerprintHit ?? hit());
    },
    search() {
      calls.push("search");
      return Promise.resolve(options.search ?? []);
    },
    entries() {
      calls.push("entries");
      return Promise.resolve(index as IndexedEntry[]);
    },
    recordFix() {
      calls.push("recordFix");
      return Promise.resolve(
        options.fixOutcome ?? { kind: "fixed", entry: entry() },
      );
    },
    setStatus() {
      calls.push("setStatus");
      return Promise.resolve(
        options.statusOutcome ?? { kind: "fixed", entry: entry() },
      );
    },
    feedback() {
      calls.push("feedback");
      return Promise.resolve(true);
    },
    fail() {
      stats.failures++;
    },
    idle() {
      return Promise.resolve();
    },
    outcomes,
    stats,
    connector: fakeConnector(),
    replay() {
      calls.push("replay");
      return Promise.resolve();
    },
    projectKey: "default",
  };
  return recorder;
}

/** Settle pending microtasks. */
export const settle = () =>
  new Promise<void>((resolve) => setImmediate(resolve));

/** `status` as a writable entry status. */
export const status = (value: string): EntryStatus => value as EntryStatus;
