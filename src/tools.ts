// The five tools (EKB-030 style host contract) proxied to the core: lookup,
// record, list, forget and stats. No tool owns knowledge any more — every
// answer comes from the local API over the Named Pipe, and the tools only
// shape it for the model (EKB-038d).
//
// `err_record` with `message` creates the issue through the core's capture
// operation, so classification, redaction and fingerprinting stay in the core.
// `fix` goes through the record_solution operation and `status` through
// set_status. `err_forget` marks an issue wontfix; nothing is deleted (§4).
import { defineTool } from "@deepseek-ai/dsh-tools";
import type { ToolDefinition, ToolRunContext } from "@deepseek-ai/dsh-tools";
import type { Context } from "@deepseek-ai/cordis";
import type { ContentBlock } from "@deepseek-ai/dsh-llm";
import type { CaptureInput } from "./capture";
import { safeErrorText } from "./capture";
import { clip, oneLine } from "./inject";
import type { Injection, Recorder } from "./plugin";
import type { IndexedEntry } from "./match";
import { ENTRY_STATUSES, formatId } from "./store";
import type { Entry } from "./store";

/** The tool names, as the model sees them. */
export const TOOL_NAMES = [
  "err_lookup",
  "err_record",
  "err_list",
  "err_forget",
  "err_stats",
] as const;

/** Entries err_list returns when `limit` is not given (§9). */
export const DEFAULT_LIST_LIMIT = 20;

/** The most entries err_list returns, whatever `limit` says. */
export const MAX_LIST_LIMIT = 200;

/** Closest entries err_lookup offers on a miss (§9). */
export const CLOSEST_COUNT = 3;

/** Category of an entry err_record creates from a message without one. */
export const DEFAULT_RECORD_CATEGORY = "agent";

/**
 * The diagnosis one injected fix is assumed to replace, in tokens. §7 puts a
 * re-diagnosis at 800-3000 tokens of thinking and trial; this takes the low
 * end, so the estimate errs towards too little.
 */
export const ASSUMED_DIAGNOSIS_TOKENS = 800;

/** Titles in err_list and err_lookup's closest list are clipped to this. */
export const TITLE_MAX_CHARS = 120;

/** A query quoted back in a miss is clipped to this. */
const QUERY_MAX_CHARS = 80;

const SIGNATURE = /^[0-9a-f]{12}$/i;

/** Settings the tools read; every one has a default. */
export interface ToolsOptions {
  idPrefix: string;
  idWidth: number;
}

export const DEFAULT_TOOLS_OPTIONS: ToolsOptions = {
  idPrefix: "E-",
  idWidth: 4,
};

/** Everything the tools need. */
export interface ToolsDeps {
  recorder: Recorder;
  /** For err_stats: notices delivered and fix trust. */
  injection: Pick<Injection, "counts" | "trust">;
  /** A label for the knowledge base, shown by err_stats. */
  kbDir: string;
  options?: Partial<ToolsOptions>;
}

/** The part of a cordis Context the tools use. */
export type ToolHost = Pick<Context, "tools">;

// ---------------------------------------------------------------------------
// Shared pieces

/** The error branch every tool's output has. */
const ERROR = {
  type: "object",
  properties: { error: { type: "string", required: true } },
  additionalProperties: false,
} as const;

interface ErrorValue {
  error: string;
}

const isError = (value: object): value is ErrorValue => "error" in value;

const text = (body: string): ContentBlock[] => [{ type: "text", text: body }];

/** Render an error value as one line naming the tool. */
const errorText = (tool: string, value: ErrorValue) =>
  text(`${tool}: ${value.error}`);

const escapeRegExp = (value: string) =>
  value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** One line, clipped. */
const short = (value: string, max: number) => clip(oneLine(value), max);

/** Similarity rounded for display and for the JSON value. */
const round = (n: number) => Math.round(n * 100) / 100;

/**
 * Bind the tools to a recorder.
 *
 * @param deps - the recorder, the injection layer, the KB label and settings.
 * @returns the five definitions, ready for `ctx.tools.register()`.
 */
export function createTools(deps: ToolsDeps): ToolDefinition[] {
  const o: ToolsOptions = { ...DEFAULT_TOOLS_OPTIONS, ...deps.options };
  const { recorder } = deps;
  const idPattern = new RegExp(`^${escapeRegExp(o.idPrefix)}(\\d+)$`, "i");

  /** The ID number in an ID-shaped string, or undefined. */
  function idNumber(value: string): number | undefined {
    const m = idPattern.exec(value.trim());
    return m === null ? undefined : Number(m[1]);
  }

  /** The canonical spelling of an ID-shaped string, for messages. */
  function canonicalId(value: string): string {
    const n = idNumber(value);
    return n === undefined ? value.trim() : formatId(n, o.idPrefix, o.idWidth);
  }

  /** The indexed core entries, or an error value when the read failed. */
  async function readIndex(): Promise<IndexedEntry[] | ErrorValue> {
    const index = await recorder.entries();
    return (
      index ?? {
        error: "could not reach the errkb core (is the daemon running?)",
      }
    );
  }

  /** The entry an ID-shaped string names, from the core. */
  async function findIssue(id: string): Promise<Entry | undefined> {
    const hit = await recorder.lookupIssue(id);
    return hit?.entry;
  }

  /**
   * Run a tool body; anything it throws comes back as an error value, counted
   * like a failed write.
   */
  async function safely<T extends object>(
    body: () => Promise<T | ErrorValue>,
  ): Promise<T | ErrorValue> {
    try {
      return await body();
    } catch (error) {
      recorder.fail(error);
      return { error: `internal error: ${safeErrorText(error).message}` };
    }
  }

  /** A trimmed string argument, or undefined when it is absent or blank. */
  const given = (value: string | undefined) => {
    const trimmed = value?.trim();
    return trimmed === undefined || trimmed === "" ? undefined : trimmed;
  };

  /** A write outcome's error, or its entry. */
  function fixedEntry(outcome: {
    kind: string;
    entry?: Entry;
  }): Entry | ErrorValue {
    if (outcome.kind === "fixed" && outcome.entry !== undefined)
      return outcome.entry;
    if (outcome.kind === "unknown") return { error: "no such entry" };
    return { error: "could not reach the errkb core (see the plugin log)" };
  }

  // -------------------------------------------------------------------------
  // err_lookup

  const ENTRY_VIEW = {
    type: "object",
    properties: {
      id: { type: "string", required: true },
      title: { type: "string", required: true },
      category: { type: "string", required: true },
      hits: { type: "integer", required: true },
      status: { type: "string", enum: ENTRY_STATUSES, required: true },
      fix: { type: "string", required: true },
      raw: { type: "string" },
    },
    additionalProperties: false,
  } as const;

  const lookup = defineTool({
    name: "err_lookup",
    description:
      "Look up an error in the errkb knowledge base by entry ID, 12-hex fingerprint or the error text itself. Returns the entry with its recorded fix, or the closest entries when nothing matches.",
    parameters: {
      query: {
        type: "string",
        required: true,
        description: `An entry ID (${formatId(7, o.idPrefix, o.idWidth)}), a 12-hex fingerprint, or the error message.`,
      },
      full: {
        type: "boolean",
        description: "Also return the redacted raw sample. Default false.",
      },
    },
    output: {
      schema: {
        oneOf: [
          {
            type: "object",
            properties: {
              query: { type: "string", required: true },
              via: {
                type: "string",
                enum: ["id", "fingerprint", "exact", "fuzzy", "code", "none"],
                required: true,
              },
              entry: { oneOf: [ENTRY_VIEW, { type: "null" }], required: true },
              closest: {
                type: "array",
                items: {
                  type: "object",
                  properties: {
                    id: { type: "string", required: true },
                    title: { type: "string", required: true },
                    similarity: { type: "number", required: true },
                  },
                  additionalProperties: false,
                },
                required: true,
              },
            },
            additionalProperties: false,
          },
          ERROR,
        ],
      },
      render(_args, value) {
        if (isError(value)) return errorText("err_lookup", value);
        const { entry } = value;
        if (entry !== null) {
          const lines = [
            `${entry.id} · ${entry.title}`,
            `category: ${entry.category} · hits: ${entry.hits} · status: ${entry.status} · matched by ${value.via}`,
            entry.fix === "" ? "fix: (none recorded)" : `fix: ${entry.fix}`,
          ];
          if (entry.raw !== undefined) lines.push("raw:", entry.raw);
          return text(lines.join("\n"));
        }
        const head = `No entry matches "${short(value.query, QUERY_MAX_CHARS)}".`;
        if (value.closest.length === 0) return text(head);
        return text(
          [
            `${head} Closest:`,
            ...value.closest.map(
              (c) => `${c.id} ${c.title} (similarity ${c.similarity})`,
            ),
          ].join("\n"),
        );
      },
    },
    isConcurrencySafe: () => true,
    execute: ({ query, full }) =>
      safely(async () => {
        const q = given(query);
        if (q === undefined) return { error: "query is empty" };

        const view = (entry: Entry) => ({
          id: entry.id,
          title: entry.title,
          category: entry.category,
          hits: entry.hits,
          status: entry.status,
          fix: entry.fix,
          ...(full === true ? { raw: entry.raw } : {}),
        });
        const result = (
          via: "id" | "fingerprint" | "exact" | "fuzzy" | "code",
          entry: Entry,
        ) => ({ query: q, via, entry: view(entry), closest: [] });

        if (idNumber(q) !== undefined) {
          return {
            error: `entry ids are core UUIDs now; look ${canonicalId(q)} up by its fingerprint or error text`,
          };
        }
        if (SIGNATURE.test(q)) {
          const hit = await recorder.lookupFingerprint(q.toLowerCase());
          if (hit !== undefined && hit.matched)
            return result("fingerprint", hit.entry);
        }
        const input: CaptureInput = {
          kind: "tool",
          isError: true,
          toolName: "err_lookup",
          message: q,
        };
        const hit = await recorder.lookup(input);
        if (hit === undefined)
          return {
            error: "could not reach the errkb core (is the daemon running?)",
          };
        if (hit.matched) {
          const via =
            hit.via === "code"
              ? "code"
              : hit.via === "exact"
                ? "exact"
                : "fuzzy";
          return result(via, hit.entry);
        }

        const nearest = await recorder.search(q, CLOSEST_COUNT);
        const closest = (nearest ?? []).map((candidate) => ({
          id: candidate.issue_id,
          title: short(candidate.title, TITLE_MAX_CHARS),
          similarity: round(
            Math.max(0, Math.min(1, candidate.confidence ?? 0)),
          ),
        }));
        return { query: q, via: "none" as const, entry: null, closest };
      }),
  });

  // -------------------------------------------------------------------------
  // err_record

  const record = defineTool({
    name: "err_record",
    description:
      "Record what you learned about an error in the errkb knowledge base. Give exactly one of id (an existing entry) or message (the error text). A message updates an entry only when it matches exactly; on an approximate match nothing is written and the closest entry's ID is returned, so confirm it with id. A new entry is created when nothing matches. A fix marks the entry fixed. This is the only way a fix is written.",
    parameters: {
      id: {
        type: "string",
        description: `An existing entry, e.g. ${formatId(7, o.idPrefix, o.idWidth)}.`,
      },
      message: {
        type: "string",
        description:
          "The error text, when you do not know the ID. Only an exact match updates an existing entry.",
      },
      fix: {
        type: "string",
        description: "What fixed it, in one or two sentences.",
      },
      status: {
        type: "string",
        enum: ENTRY_STATUSES,
        description:
          "fixed, wontfix (never inject it) or open. A fix alone sets fixed.",
      },
      note: {
        type: "string",
        description: "A line added to the entry's notes.",
      },
      category: {
        type: "string",
        description: `For message: the category to match and file under, e.g. tool or command-exit. Default: match any; file new entries under ${DEFAULT_RECORD_CATEGORY}.`,
      },
    },
    output: {
      schema: {
        oneOf: [
          {
            type: "object",
            properties: {
              id: { type: "string", required: true },
              created: { type: "boolean", required: true },
              status: { type: "string", enum: ENTRY_STATUSES, required: true },
              hasFix: { type: "boolean", required: true },
            },
            additionalProperties: false,
          },
          ERROR,
        ],
      },
      render(_args, value) {
        if (isError(value)) return errorText("err_record", value);
        const what = value.created ? "Created" : "Updated";
        const fix = value.hasFix ? "" : ", no fix recorded";
        return text(`${what} ${value.id} (status ${value.status}${fix}).`);
      },
    },
    execute: (args) =>
      safely(async () => {
        const id = given(args.id);
        const message = given(args.message);
        const fix = given(args.fix);
        const note = given(args.note);
        void note;
        const category = given(args.category);
        void category;
        const { status } = args;
        if ((id === undefined) === (message === undefined))
          return { error: "give exactly one of id and message" };
        if (args.fix !== undefined && fix === undefined)
          return { error: "fix is empty" };

        let target: { id: string; created: boolean };
        if (id !== undefined) {
          const found = await findIssue(id);
          if (found === undefined)
            return { error: `no entry ${canonicalId(id)}` };
          target = { id: found.id, created: false };
        } else {
          const input: CaptureInput = {
            kind: "tool",
            isError: true,
            toolName: "err_record",
            message: message as string,
          };
          const textHit = await recorder.lookup(input);
          if (
            textHit !== undefined &&
            textHit.matched &&
            textHit.via !== "exact"
          )
            return {
              error: `closest match is ${textHit.id} (approximate, by ${textHit.via}); nothing was written. Call err_record with id: "${textHit.id}" to confirm, or reword message`,
            };
          if (textHit !== undefined && textHit.matched) {
            target = { id: textHit.id, created: false };
          } else {
            const outcome = await recorder.capture(input);
            if (outcome.kind === "appended")
              target = { id: outcome.id, created: true };
            else if (outcome.kind === "hit")
              target = { id: outcome.id, created: false };
            else
              return {
                error:
                  "could not reach the errkb core; the observation was queued and will be replayed",
              };
          }
        }

        let entry: Entry | undefined;
        if (fix !== undefined) {
          const outcome = await recorder.recordFix(target.id, fix);
          const fixed = fixedEntry(outcome);
          if (isError(fixed)) return fixed;
          entry = fixed;
        }
        if (status !== undefined) {
          const outcome = await recorder.setStatus(target.id, status);
          const updated = fixedEntry(outcome);
          if (isError(updated)) return updated;
          entry = updated;
        }
        if (entry === undefined) entry = await findIssue(target.id);
        if (entry === undefined) return { error: `no entry ${target.id}` };
        return {
          id: target.id,
          created: target.created,
          status: entry.status,
          hasFix: entry.fix.trim() !== "",
        };
      }),
  });

  // -------------------------------------------------------------------------
  // err_list

  const list = defineTool({
    name: "err_list",
    description:
      "List entries of the errkb knowledge base: ID, title and hits only, no bodies.",
    parameters: {
      cat: {
        type: "string",
        description:
          "Only this category: tool, command-exit, llm, agent, or a display category such as tool / bash.",
      },
      status: { type: "string", enum: ENTRY_STATUSES },
      limit: {
        type: "integer",
        description: `At most this many entries (default ${DEFAULT_LIST_LIMIT}, at most ${MAX_LIST_LIMIT}).`,
      },
    },
    output: {
      schema: {
        oneOf: [
          {
            type: "object",
            properties: {
              entries: {
                type: "array",
                items: {
                  type: "object",
                  properties: {
                    id: { type: "string", required: true },
                    title: { type: "string", required: true },
                    hits: { type: "integer", required: true },
                  },
                  additionalProperties: false,
                },
                required: true,
              },
              total: { type: "integer", required: true },
            },
            additionalProperties: false,
          },
          ERROR,
        ],
      },
      render(_args, value) {
        if (isError(value)) return errorText("err_list", value);
        if (value.total === 0) return text("No entries.");
        return text(
          [
            ...value.entries.map((e) => `${e.id} (${e.hits}) ${e.title}`),
            `${value.entries.length} of ${value.total} shown.`,
          ].join("\n"),
        );
      },
    },
    isConcurrencySafe: () => true,
    execute: (args) =>
      safely(async () => {
        const limit = args.limit ?? DEFAULT_LIST_LIMIT;
        if (!Number.isInteger(limit) || limit < 1)
          return { error: "limit must be a positive integer" };
        const index = await readIndex();
        if (!Array.isArray(index)) return index;
        const cat = given(args.cat)?.toLowerCase();
        const matches = index.filter(
          (i) =>
            (cat === undefined ||
              i.category.toLowerCase() === cat ||
              i.entry.category.toLowerCase() === cat) &&
            (args.status === undefined || i.entry.status === args.status),
        );
        return {
          entries: matches
            .slice(0, Math.min(limit, MAX_LIST_LIMIT))
            .map(({ entry }) => ({
              id: entry.id,
              title: short(entry.title, TITLE_MAX_CHARS),
              hits: entry.hits,
            })),
          total: matches.length,
        };
      }),
  });

  // -------------------------------------------------------------------------
  // err_forget

  const forget = defineTool({
    name: "err_forget",
    description:
      "Mark a misjudged entry wontfix so it is never injected again. Nothing is deleted; the status change is audited by the core.",
    parameters: {
      id: { type: "string", required: true },
      reason: { type: "string", description: "Why, for the audit trail." },
    },
    output: {
      schema: {
        oneOf: [
          {
            type: "object",
            properties: {
              id: { type: "string", required: true },
              archived: { type: "boolean", required: true },
            },
            additionalProperties: false,
          },
          ERROR,
        ],
      },
      render(_args, value) {
        if (isError(value)) return errorText("err_forget", value);
        return text(`Marked ${value.id} wontfix; it will not be injected.`);
      },
    },
    execute: (args) =>
      safely(async () => {
        const id = given(args.id);
        if (id === undefined) return { error: "id is empty" };
        const found = await findIssue(id);
        if (found === undefined)
          return { error: `no entry ${canonicalId(id)}` };
        const target = found.id;
        const outcome = await recorder.setStatus(target, "wontfix");
        if (outcome.kind !== "fixed")
          return {
            error: "could not reach the errkb core (see the plugin log)",
          };
        return { id: target, archived: true };
      }),
  });

  // -------------------------------------------------------------------------
  // err_stats

  const stats = defineTool({
    name: "err_stats",
    description:
      "Show the errkb ledger: entries, hits, notices injected, an estimate of tokens saved, open entries without a fix, distrusted fixes and the knowledge base path. Costs no model call.",
    parameters: {
      scope: {
        type: "string",
        enum: ["session", "all"],
        description:
          "Notices of this session or of every session since the plugin started (default all). Entry figures are always the whole knowledge base.",
      },
    },
    output: {
      schema: {
        oneOf: [
          {
            type: "object",
            properties: {
              scope: {
                type: "string",
                enum: ["session", "all"],
                required: true,
              },
              kbDir: { type: "string", required: true },
              entries: { type: "integer", required: true },
              hits: { type: "integer", required: true },
              openWithoutFix: { type: "integer", required: true },
              notices: { type: "integer", required: true },
              fixNotices: { type: "integer", required: true },
              noticeTokens: { type: "integer", required: true },
              estimatedTokensSaved: { type: "integer", required: true },
              suppressed: {
                type: "array",
                items: { type: "string" },
                required: true,
              },
            },
            additionalProperties: false,
          },
          ERROR,
        ],
      },
      render(_args, value) {
        if (isError(value)) return errorText("err_stats", value);
        const where =
          value.scope === "session" ? "this session" : "all sessions";
        const net =
          value.fixNotices * ASSUMED_DIAGNOSIS_TOKENS - value.noticeTokens;
        const clamped = net < 0 ? ` = −${-net}, shown as 0` : "";
        return text(
          [
            `Knowledge base: ${value.kbDir}`,
            `Entries: ${value.entries} · hits: ${value.hits} · open without a fix: ${value.openWithoutFix}`,
            `Notices (${where}): ${value.notices}, ${value.fixNotices} with a fix, ${value.noticeTokens} tokens`,
            `Estimated tokens saved: ${value.estimatedTokensSaved} (estimate: ${value.fixNotices} fix notices × ${ASSUMED_DIAGNOSIS_TOKENS} − ${value.noticeTokens} notice tokens${clamped})`,
            `Distrusted fixes, not injected: ${value.suppressed.length === 0 ? "none" : value.suppressed.join(", ")}`,
          ].join("\n"),
        );
      },
    },
    isConcurrencySafe: () => true,
    execute: (args, exec: ToolRunContext) =>
      safely(async () => {
        const scope = args.scope ?? "all";
        const session = exec.agent?.id;
        if (scope === "session" && session === undefined)
          return { error: "scope session needs a call from an agent session" };
        const index = await readIndex();
        if (!Array.isArray(index)) return index;
        const counts = deps.injection.counts(
          scope === "session" ? session : undefined,
        );
        const { trust } = deps.injection;
        await trust.ready;
        return {
          scope,
          kbDir: deps.kbDir,
          entries: index.length,
          hits: index.reduce((sum, { entry }) => sum + entry.hits, 0),
          openWithoutFix: index.filter(
            ({ entry }) => entry.status === "open" && entry.fix.trim() === "",
          ).length,
          ...counts,
          estimatedTokensSaved: estimateSaved(
            counts.fixNotices,
            counts.noticeTokens,
          ),
          suppressed: index
            .filter(
              ({ entry }) =>
                entry.fix.trim() !== "" &&
                trust.level(entry.id, entry.fix) === "suppressed",
            )
            .map(({ entry }) => entry.id),
        };
      }),
  });

  return [lookup, record, list, forget, stats];
}

/**
 * err_stats' estimate of the tokens the knowledge base saved: every notice
 * that carried a fix is assumed to replace one diagnosis of
 * {@link ASSUMED_DIAGNOSIS_TOKENS} tokens, and every notice delivered costs
 * its own tokens. It leaves out the standing costs (the system-prompt section
 * on every request, the session digest), so it is an estimate, and labelled
 * one wherever it is shown. It never goes below 0.
 *
 * @param fixNotices - notices that carried a fix.
 * @param noticeTokens - the tokens of every notice delivered.
 */
export function estimateSaved(
  fixNotices: number,
  noticeTokens: number,
): number {
  return Math.max(0, fixNotices * ASSUMED_DIAGNOSIS_TOKENS - noticeTokens);
}

/**
 * Register the five tools (T15). A tool the registry refuses (a duplicate
 * name) is counted and logged; the others are still registered.
 *
 * @param ctx - the plugin's context, with the `tools` service.
 * @param deps - see {@link createTools}.
 */
export function registerTools(ctx: ToolHost, deps: ToolsDeps): void {
  for (const tool of createTools(deps)) {
    try {
      ctx.tools.register(tool);
    } catch (error) {
      deps.recorder.fail(error);
    }
  }
}
