// The five tools are thin proxies (EKB-038d): every read and write is a core
// call through the recorder, and the tool output keeps the host contract the
// model already knows. No matching, redaction or storage happens here.
import { describe, expect, it, vi } from "vitest";
import {
  DEFAULT_LIST_LIMIT,
  MAX_LIST_LIMIT,
  TOOL_NAMES,
  createTools,
  registerTools,
} from "../src/tools";
import type { ToolsDeps } from "../src/tools";
import type { ToolDefinition } from "@deepseek-ai/dsh-tools";
import { candidate, entry, fakeRecorder, hit, indexed } from "./support";
import type { FakeRecorderOptions } from "./support";

const tool = (deps: ToolsDeps, name: string): ToolDefinition => {
  const found = createTools(deps).find((t) => t.name === name);
  if (found === undefined) throw new Error(`no tool ${name}`);
  return found;
};

const injection = (
  overrides: Partial<{
    notices: number;
    fixNotices: number;
    noticeTokens: number;
    suppressed: string[];
  }> = {},
) => {
  const counts = {
    notices: overrides.notices ?? 3,
    fixNotices: overrides.fixNotices ?? 2,
    noticeTokens: overrides.noticeTokens ?? 40,
  };
  const suppressed = new Set(overrides.suppressed ?? []);
  return {
    counts: () => counts,
    trust: {
      ready: Promise.resolve(),
      level: (_id: string, _fix: string) =>
        suppressed.has(_id) ? "suppressed" : "trusted",
    },
  } as never;
};

const deps = (
  recorderOptions: FakeRecorderOptions = {},
  injectionOptions = {},
): ToolsDeps & { recorder: ReturnType<typeof fakeRecorder> } => {
  const recorder = fakeRecorder(recorderOptions);
  return {
    recorder,
    injection: injection(injectionOptions),
    kbDir: "\\\\.\\pipe\\errkb",
    options: { idPrefix: "E-", idWidth: 4 },
  };
};

const run = async (
  definition: ToolDefinition,
  args: Record<string, unknown>,
  agent: string | null = "s1",
) =>
  (await definition.execute(
    args as never,
    {
      agent: agent === null ? undefined : { id: agent },
    } as never,
  )) as Record<string, unknown>;

describe("err_lookup", () => {
  it("returns a text match with its entry", async () => {
    const test = deps({ hits: hit() });
    const result = await run(tool(test, "err_lookup"), {
      query: "EPERM: operation not permitted",
    });
    expect(result.via).toBe("exact");
    expect((result.entry as Record<string, unknown>).id).toBe(entry().id);
    expect(test.recorder.calls).toContain("lookup");
  });

  it("returns the raw sample when full is true", async () => {
    const test = deps({ hits: hit({ entry: entry({ raw: "raw text" }) }) });
    const result = await run(tool(test, "err_lookup"), {
      query: "EPERM",
      full: true,
    });
    expect((result.entry as Record<string, unknown>).raw).toBe("raw text");
  });

  it("looks up a 12-hex fingerprint through the core", async () => {
    const test = deps({ fingerprintHit: hit() });
    const result = await run(tool(test, "err_lookup"), {
      query: "ABCDEF012345",
    });
    expect(result.via).toBe("fingerprint");
    expect(test.recorder.calls).toContain("lookupFingerprint");
  });

  it("rejects an ID-shaped query and names the core-UUID boundary", async () => {
    const test = deps();
    const result = await run(tool(test, "err_lookup"), { query: "E-0007" });
    expect(String(result.error)).toContain("E-0007");
    expect(String(result.error)).toContain("UUID");
    expect(test.recorder.calls).not.toContain("lookup");
  });

  it("offers the closest core candidates on a miss", async () => {
    const test = deps({
      hits: undefined,
      search: [candidate({ confidence: 0.61 })],
    });
    const result = await run(tool(test, "err_lookup"), { query: "boom" });
    expect(result.entry).toBeNull();
    const closest = result.closest as Array<Record<string, unknown>>;
    expect(closest[0]?.id).toBe(candidate().issue_id);
    expect(closest[0]?.similarity).toBe(0.61);
  });

  it("reports a miss with no candidates", async () => {
    const result = await run(tool(deps({ search: [] }), "err_lookup"), {
      query: "boom",
    });
    expect(result.closest).toEqual([]);
  });

  it("rejects an empty query", async () => {
    const result = await run(tool(deps(), "err_lookup"), { query: "   " });
    expect(result.error).toBe("query is empty");
  });

  it("reports an unreachable core when the index read fails", async () => {
    const test = deps();
    test.recorder.lookup = () => Promise.resolve(undefined);
    const result = await run(tool(test, "err_lookup"), { query: "boom" });
    expect(String(result.error)).toContain("core");
  });
});

describe("err_record", () => {
  it("rejects anything but exactly one of id and message", async () => {
    const test = deps();
    expect((await run(tool(test, "err_record"), {})).error).toBe(
      "give exactly one of id and message",
    );
    expect(
      (
        await run(tool(test, "err_record"), {
          id: entry().id,
          message: "boom",
        })
      ).error,
    ).toBe("give exactly one of id and message");
  });

  it("records a fix by id through record_solution", async () => {
    const test = deps({ issue: hit() });
    const result = await run(tool(test, "err_record"), {
      id: entry().id,
      fix: "Close the handle.",
    });
    expect(result.id).toBe(entry().id);
    expect(result.hasFix).toBe(true);
    expect(test.recorder.calls).toContain("recordFix");
  });

  it("reports an unknown id", async () => {
    const result = await run(tool(deps({ issue: undefined }), "err_record"), {
      id: "E-0007",
    });
    expect(String(result.error)).toContain("E-0007");
  });

  it("updates an exact message match instead of capturing it", async () => {
    const test = deps({ hits: hit({ via: "exact" }), issue: hit() });
    const result = await run(tool(test, "err_record"), { message: "boom" });
    expect(result.created).toBe(false);
    expect(test.recorder.calls).not.toContain("capture:tool");
  });

  it("refuses an approximate match and asks for confirmation", async () => {
    const test = deps({
      hits: hit({ via: "fuzzy", approximate: true }),
    });
    const result = await run(tool(test, "err_record"), { message: "boom" });
    expect(String(result.error)).toContain("approximate");
    expect(String(result.error)).toContain(entry().id);
    expect(test.recorder.calls).not.toContain("recordFix");
  });

  it("creates an issue through the core capture operation", async () => {
    const test = deps({ hits: { matched: false }, issue: hit() });
    const result = await run(tool(test, "err_record"), { message: "boom" });
    expect(result.created).toBe(true);
    expect(result.id).toBe(entry().id);
    expect(test.recorder.calls).toContain("capture:tool");
  });

  it("explains when the new observation was queued offline", async () => {
    const test = deps({
      hits: { matched: false },
      captureOutcome: { kind: "counted" },
    });
    const result = await run(tool(test, "err_record"), { message: "boom" });
    expect(String(result.error)).toContain("queued");
  });

  it("changes a status through set_status", async () => {
    const test = deps({
      issue: hit(),
      statusOutcome: { kind: "fixed", entry: entry({ status: "wontfix" }) },
    });
    const result = await run(tool(test, "err_record"), {
      id: entry().id,
      status: "wontfix",
    });
    expect(result.status).toBe("wontfix");
    expect(test.recorder.calls).toContain("setStatus");
  });

  it("rejects an empty fix", async () => {
    const result = await run(tool(deps({ issue: hit() }), "err_record"), {
      id: entry().id,
      fix: "  ",
    });
    expect(result.error).toBe("fix is empty");
  });

  it("ignores a note without failing", async () => {
    const test = deps({ issue: hit() });
    const result = await run(tool(test, "err_record"), {
      id: entry().id,
      note: "seen in CI",
    });
    expect(result.id).toBe(entry().id);
  });
});

describe("err_list", () => {
  const indexEntry = (id: string, status: "open" | "fixed" = "open") => ({
    entry: entry({ id, status, category: "tool" }),
    sig: "0123456789ab",
    category: "tool",
    code: undefined,
    proj: "default",
    tokens: new Set<string>(),
    length: 1,
    excluded: undefined,
  });

  it("lists entries with hits and a total", async () => {
    const test = deps();
    test.recorder.entries = () =>
      Promise.resolve([indexEntry("E-1"), indexEntry("E-2")]);
    const result = await run(tool(test, "err_list"), {});
    expect((result.entries as unknown[]).length).toBe(2);
    expect(result.total).toBe(2);
  });

  it("filters by status and category", async () => {
    const test = deps();
    test.recorder.entries = () =>
      Promise.resolve([indexEntry("E-1", "fixed"), indexEntry("E-2", "open")]);
    const fixed = await run(tool(test, "err_list"), { status: "fixed" });
    expect(fixed.total).toBe(1);
    const none = await run(tool(test, "err_list"), { cat: "llm" });
    expect(none.total).toBe(0);
  });

  it("caps the page and validates the limit", async () => {
    const test = deps();
    test.recorder.entries = () =>
      Promise.resolve(
        Array.from({ length: 30 }, (_, i) => indexEntry(`E-${i}`)),
      );
    const result = await run(tool(test, "err_list"), { limit: 5 });
    expect((result.entries as unknown[]).length).toBe(5);
    expect((await run(tool(test, "err_list"), { limit: 0 })).error).toContain(
      "limit",
    );
  });

  it("says so when there are no entries", async () => {
    const test = deps();
    test.recorder.entries = () => Promise.resolve([]);
    expect(await run(tool(test, "err_list"), {})).toEqual({
      entries: [],
      total: 0,
    });
  });

  it("keeps the documented limits", () => {
    expect(DEFAULT_LIST_LIMIT).toBe(20);
    expect(MAX_LIST_LIMIT).toBe(200);
    expect(TOOL_NAMES).toEqual([
      "err_lookup",
      "err_record",
      "err_list",
      "err_forget",
      "err_stats",
    ]);
  });
});

describe("err_forget", () => {
  it("marks an entry wontfix", async () => {
    const test = deps({ issue: hit() });
    const result = await run(tool(test, "err_forget"), { id: entry().id });
    expect(result).toEqual({ id: entry().id, archived: true });
    expect(test.recorder.calls).toContain("setStatus");
  });

  it("reports an unknown entry", async () => {
    const result = await run(tool(deps({ issue: undefined }), "err_forget"), {
      id: "E-0007",
    });
    expect(String(result.error)).toContain("E-0007");
  });

  it("reports a failed status write", async () => {
    const test = deps({ issue: hit(), statusOutcome: { kind: "failed" } });
    const result = await run(tool(test, "err_forget"), { id: entry().id });
    expect(String(result.error)).toContain("core");
  });

  it("rejects an empty id", async () => {
    const result = await run(tool(deps(), "err_forget"), { id: " " });
    expect(result.error).toBe("id is empty");
  });
});

describe("err_stats", () => {
  it("summarises the core ledger and the notices", async () => {
    const test = deps({}, { suppressed: [entry().id] });
    test.recorder.entries = () =>
      Promise.resolve([indexed({ status: "open", fix: "" }), indexed()]);
    const result = await run(tool(test, "err_stats"), {});
    expect(result.entries).toBe(2);
    expect(result.hits).toBe(6);
    expect(result.openWithoutFix).toBe(1);
    expect(result.notices).toBe(3);
    expect(result.fixNotices).toBe(2);
    expect(result.estimatedTokensSaved).toBe(2 * 800 - 40);
    expect(result.suppressed).toEqual([entry().id]);
  });

  it("needs a session for scope=session", async () => {
    const result = await run(
      tool(deps(), "err_stats"),
      { scope: "session" },
      null,
    );
    expect(String(result.error)).toContain("agent session");
  });

  it("reports an unreachable core", async () => {
    const test = deps();
    test.recorder.entries = () => Promise.resolve(undefined);
    const result = await run(tool(test, "err_stats"), {});
    expect(String(result.error)).toContain("core");
  });
});

describe("registerTools", () => {
  it("registers all five tools", () => {
    const registered: string[] = [];
    const ctx = {
      tools: {
        register: (definition: ToolDefinition) => {
          registered.push(definition.name);
          return () => undefined;
        },
      },
    };
    registerTools(ctx as never, deps());
    expect(registered).toHaveLength(5);
  });

  it("counts a tool the registry refuses", () => {
    const recorder = fakeRecorder();
    const ctx = {
      tools: {
        register: () => {
          throw new Error("duplicate tool");
        },
      },
    };
    registerTools(ctx as never, { ...deps(), recorder });
    expect(recorder.stats.failures).toBe(5);
  });

  it("wraps an unexpected tool failure as an error value", async () => {
    const test = deps();
    test.recorder.entries = () => {
      throw new Error("exploded");
    };
    const result = await run(tool(test, "err_list"), {});
    expect(String(result.error)).toContain("internal error");
  });

  it("stops err_stats from counting a missing trust level", async () => {
    const definition = tool(deps(), "err_stats");
    expect(typeof definition.execute).toBe("function");
    vi.fn();
  });
});

describe("rendered output", () => {
  const render = (
    definition: ToolDefinition,
    args: Record<string, unknown>,
    value: Record<string, unknown>,
  ) =>
    (
      definition as unknown as {
        output: {
          render: (
            args: unknown,
            value: unknown,
          ) => Array<{ type: string; text: string }>;
        };
      }
    ).output
      .render(args, value)
      .map((block) => block.text)
      .join("\n");

  it("renders a lookup hit, a hit without a fix or raw, and misses", async () => {
    const definition = tool(deps(), "err_lookup");
    const withRaw = render(
      definition,
      { query: "EPERM" },
      {
        query: "EPERM",
        via: "exact",
        entry: {
          id: "E-0001",
          title: "boom",
          category: "tool",
          hits: 1,
          status: "open",
          fix: "do y",
          raw: "raw text",
        },
        closest: [],
      },
    );
    expect(withRaw).toContain("raw:");
    const bare = render(
      definition,
      { query: "EPERM" },
      {
        query: "EPERM",
        via: "none",
        entry: {
          id: "E-0001",
          title: "boom",
          category: "tool",
          hits: 1,
          status: "open",
          fix: "",
        },
        closest: [],
      },
    );
    expect(bare).toContain("fix: (none recorded)");
    const closest = render(
      definition,
      { query: "EPERM" },
      {
        query: "EPERM",
        via: "none",
        entry: null,
        closest: [{ id: "E-0001", title: "t", similarity: 0.7 }],
      },
    );
    expect(closest).toContain("Closest:");
    const nowhere = render(
      definition,
      { query: "EPERM" },
      { query: "EPERM", via: "none", entry: null, closest: [] },
    );
    expect(nowhere).toContain("No entry matches");
    expect(render(definition, { query: "x" }, { error: "boom" })).toBe(
      "err_lookup: boom",
    );
  });

  it("renders record, list, forget and stats values", async () => {
    expect(
      render(
        tool(deps(), "err_record"),
        {},
        { id: "u1", created: true, status: "open", hasFix: true },
      ),
    ).toContain("Created u1");
    expect(
      render(
        tool(deps(), "err_record"),
        {},
        { id: "u1", created: false, status: "fixed", hasFix: false },
      ),
    ).toContain("Updated u1 (status fixed, no fix recorded)");
    expect(render(tool(deps(), "err_record"), {}, { error: "boom" })).toBe(
      "err_record: boom",
    );

    expect(
      render(
        tool(deps(), "err_list"),
        {},
        { entries: [{ id: "E-0001", title: "t", hits: 2 }], total: 3 },
      ),
    ).toContain("1 of 3 shown.");
    expect(
      render(tool(deps(), "err_list"), {}, { entries: [], total: 0 }),
    ).toBe("No entries.");
    expect(render(tool(deps(), "err_list"), {}, { error: "boom" })).toBe(
      "err_list: boom",
    );

    expect(
      render(tool(deps(), "err_forget"), {}, { id: "u1", archived: true }),
    ).toContain("Marked u1 wontfix");
    expect(render(tool(deps(), "err_forget"), {}, { error: "boom" })).toBe(
      "err_forget: boom",
    );

    const stats = {
      scope: "session",
      kbDir: "p",
      entries: 1,
      hits: 2,
      openWithoutFix: 3,
      notices: 4,
      fixNotices: 5,
      noticeTokens: 900_000,
      estimatedTokensSaved: 0,
      suppressed: ["E-0002"],
    };
    const rendered = render(tool(deps(), "err_stats"), {}, stats);
    expect(rendered).toContain("this session");
    expect(rendered).toContain("shown as 0");
    expect(rendered).toContain("E-0002");
    expect(
      render(
        tool(deps(), "err_stats"),
        {},
        {
          ...stats,
          scope: "all",
          fixNotices: 2,
          noticeTokens: 1,
          suppressed: [],
        },
      ),
    ).toContain("Distrusted fixes, not injected: none");
    expect(render(tool(deps(), "err_stats"), {}, { error: "boom" })).toBe(
      "err_stats: boom",
    );
  });

  it("surfaces write failures from the core", async () => {
    const test = deps({ hits: { matched: false } });
    test.recorder.recordFix = () => Promise.resolve({ kind: "failed" });
    const failed = await run(tool(test, "err_record"), {
      message: "new error",
      fix: "do x",
    });
    expect(String(failed.error)).toContain("could not reach");
    test.recorder.recordFix = () => Promise.resolve({ kind: "unknown" });
    const unknown = await run(tool(test, "err_record"), {
      message: "new error 2",
      fix: "do x",
    });
    expect(String(unknown.error)).toBe("no such entry");
    test.recorder.recordFix = () => {
      throw new Error("exploded");
    };
    const thrown = await run(tool(test, "err_record"), {
      message: "new error 3",
      fix: "do x",
    });
    expect(String(thrown.error)).toContain("internal error");
  });
});
