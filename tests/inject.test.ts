import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { boundContextSummary } from "@deepseek-ai/dsh-llm";
import { describe, expect, it } from "vitest";
import {
  CAUSE_MAX_CHARS,
  CAUSE_MIN_CHARS,
  CapTracker,
  DEFAULT_CAP_LIMITS,
  ELLIPSIS,
  FixTrust,
  INJECT_MODES,
  Injector,
  NOTICE_MAX_CHARS,
  NOTICE_MAX_TOKENS,
  PLUGIN_NAME,
  SUMMARY_MAX_CHARS,
  DIGEST_MAX_TITLES,
  DIGEST_TITLE_MAX_CHARS,
  SESSION_DIGEST_MODES,
  SYSTEM_PROMPT_HINT,
  SYSTEM_PROMPT_SECTION,
  WORDING,
  clip,
  estimateTokens,
  fitNotice,
  fixSig,
  memoryTrustStore,
  noticeSource,
  noticeText,
  oneLine,
  sessionDigestText,
  trustLevel,
  withinCaps,
} from "../src/inject";
import type { InjectMode, NoticeEvent, TrustState } from "../src/inject";
import { name } from "../src/index";
import type { Hit } from "../src/match";
import type { Entry } from "../src/store";

// ---------------------------------------------------------------------------
// Fixtures

const FIX =
  "close the locking process and re-run; if it persists, use pnpm install --config.node-linker=hoisted.";
const CAUSE = "node_modules locked by an editor during pnpm install";

function entry(id: string, overrides: Partial<Entry> = {}): Entry {
  return {
    id,
    title: "[tool:pwsh] EPERM: operation not permitted, rename",
    meta: { sig: "3f2a1c9d0b71", cat: "tool" },
    fingerprint: "3f2a1c9d0b71",
    category: "tool / pwsh",
    firstSeen: "2026-09-14 09:12",
    lastSeen: "2026-09-14 15:40",
    hits: 5,
    trigger: CAUSE,
    raw: "EPERM: operation not permitted, rename '<path>'",
    fix: FIX,
    status: "open",
    notes: "",
    ...overrides,
  };
}

function hit(
  id = "E-0007",
  overrides: Partial<Entry> = {},
  extra: Partial<Hit> = {},
): NoticeEvent {
  return {
    kind: "hit",
    hit: {
      matched: true,
      id,
      entry: entry(id, overrides),
      via: "exact",
      approximate: false,
      similarity: 1,
      injectable: true,
      ...extra,
    },
  };
}

const near = (id = "E-0007", overrides: Partial<Entry> = {}) =>
  hit(id, overrides, { via: "fuzzy", approximate: true, similarity: 0.8 });

const miss = (id = "E-0011"): NoticeEvent => ({ kind: "miss", id });

const cjk = (n: number) => "关闭占用该目录的编辑器后重跑".repeat(n);

// ---------------------------------------------------------------------------

describe("estimateTokens", () => {
  it("counts ASCII at three characters a token, rounded up", () => {
    expect(estimateTokens("")).toBe(0);
    expect(estimateTokens("abc")).toBe(1);
    expect(estimateTokens("abcd")).toBe(2);
    expect(estimateTokens("a\tb\nc")).toBe(2);
  });

  it("counts every CJK character as a token", () => {
    expect(estimateTokens("拒绝访问")).toBe(4);
    expect(estimateTokens("EPERM 拒绝访问")).toBe(2 + 4);
  });

  it("counts a character outside the BMP once", () => {
    expect("𠮷".length).toBe(2);
    expect(estimateTokens("𠮷")).toBe(1);
    expect(estimateTokens("ab𠮷c")).toBe(1 + 1);
  });

  it("overestimates English: 120 tokens is at most 360 ASCII characters", () => {
    expect(estimateTokens("x".repeat(360))).toBe(120);
    expect(estimateTokens("x".repeat(361))).toBe(121);
  });
});

describe("text helpers", () => {
  it("withinCaps checks both the character and the token cap", () => {
    expect(withinCaps("x".repeat(360))).toBe(true);
    expect(withinCaps("x".repeat(361))).toBe(false);
    expect(withinCaps("拒".repeat(NOTICE_MAX_TOKENS))).toBe(true);
    expect(withinCaps("拒".repeat(NOTICE_MAX_TOKENS + 1))).toBe(false);
  });

  it("oneLine collapses whitespace and newlines", () => {
    expect(oneLine("  a\n\n b\t c ")).toBe("a b c");
  });

  it("clip cuts by code point and marks the cut", () => {
    expect(clip("abcdef", 6)).toBe("abcdef");
    expect(clip("abcdef", 4)).toBe(`abc${ELLIPSIS}`);
    expect(clip("ab   cdef", 4)).toBe(`ab${ELLIPSIS}`);
    expect(clip("拒绝访问", 3)).toBe(`拒绝${ELLIPSIS}`);
    expect(clip("𠮷𠮷𠮷", 2)).toBe(`𠮷${ELLIPSIS}`);
    expect(clip("abc", 0)).toBe("");
    expect(clip("", 0)).toBe("");
  });
});

describe("fitNotice", () => {
  const render = (c: string, f: string) => `[h] ${c} | ${f} END`;

  it("leaves short text alone, apart from the cause cap", () => {
    expect(fitNotice(render, "c", "f")).toBe("[h] c | f END");
    const long = "c".repeat(CAUSE_MAX_CHARS + 20);
    expect(fitNotice(render, long, "f")).toBe(
      `[h] ${clip(long, CAUSE_MAX_CHARS)} | f END`,
    );
  });

  it("shrinks the cause before the fix", () => {
    const cause = "c".repeat(CAUSE_MAX_CHARS);
    const fix = "f".repeat(250);
    const text = fitNotice(render, cause, fix);
    expect(withinCaps(text)).toBe(true);
    expect(text).toContain(` | ${fix} END`);
    expect(text).toContain(ELLIPSIS);
  });

  it("then shrinks the fix, keeping the template whole", () => {
    const text = fitNotice(render, cjk(10), cjk(10));
    expect(withinCaps(text)).toBe(true);
    expect(text.startsWith("[h] ")).toBe(true);
    expect(text.endsWith(`${ELLIPSIS} END`)).toBe(true);
    expect(text).toContain(`${clip(cjk(10), CAUSE_MIN_CHARS)} |`);
  });

  it("clips the whole body when the template alone is too long", () => {
    const huge = (c: string, f: string) => `${"x".repeat(500)} ${c} ${f}`;
    const text = fitNotice(huge, "c", "f");
    expect(withinCaps(text)).toBe(true);
    expect(text.endsWith(ELLIPSIS)).toBe(true);
  });
});

describe("noticeText", () => {
  it("a hit carries cause, fix and the try-first wording", () => {
    expect(noticeText(hit())).toEqual({
      kind: "hit",
      text: `[errkb] E-0007 known (5 hits) | cause: ${CAUSE} | fix: ${FIX} ${WORDING.hit}`,
    });
  });

  it("the hit wording never forbids re-diagnosis or research", () => {
    expect(WORDING.hit).toBe(
      "Known fix: try this first, before re-diagnosing or researching.",
    );
    expect(noticeText(hit()).text).not.toMatch(/do not/i);
  });

  it("says 1 hit, singular", () => {
    expect(noticeText(hit("E-0001", { hits: 1 })).text).toContain(
      "E-0001 known (1 hit) |",
    );
  });

  it("leaves the cause out when the entry has no trigger", () => {
    expect(noticeText(hit("E-0007", { trigger: "  " })).text).toBe(
      `[errkb] E-0007 known (5 hits) | fix: ${FIX} ${WORDING.hit}`,
    );
  });

  it("joins a multi-line fix into one line", () => {
    const text = noticeText(
      hit("E-0007", { fix: "step one\n\nstep two" }),
    ).text;
    expect(text).toContain("| fix: step one step two Known fix:");
    expect(text).not.toContain("\n");
  });

  it("a near hit says approximate match, verify first, and nothing stronger", () => {
    const { kind, text } = noticeText(near());
    expect(kind).toBe("near");
    expect(text).toBe(
      `[errkb] E-0007 known (5 hits) | cause: ${CAUSE} | fix: ${FIX} Approximate match, verify first.`,
    );
    expect(text).not.toContain(WORDING.hit);
    expect(text).not.toMatch(/re-diagnos/);
  });

  it("an entry without a fix gets the short notice", () => {
    expect(noticeText(hit("E-0007", { fix: " \n " }))).toEqual({
      kind: "no-fix",
      text: "[errkb] E-0007 seen before (5 hits), no fix recorded yet.",
    });
    expect(noticeText(near("E-0007", { fix: "" })).text).toBe(
      "[errkb] E-0007 seen before (5 hits, approximate match), no fix recorded yet.",
    );
    expect(
      estimateTokens(noticeText(hit("E-0007", { fix: "" })).text),
    ).toBeLessThanOrEqual(20);
  });

  it("a miss is recorded with no fix yet", () => {
    expect(noticeText(miss())).toEqual({
      kind: "miss",
      text: "[errkb] recorded as E-0011 (no fix yet).",
    });
  });

  it("a doubted fix says it failed here last time", () => {
    const { kind, text } = noticeText(hit(), "doubted");
    expect(kind).toBe("doubted");
    expect(
      text.endsWith(" This fix failed here last time; verify before applying."),
    ).toBe(true);
    expect(text).not.toContain(WORDING.hit);
  });

  it("a doubted near hit says both", () => {
    const { kind, text } = noticeText(near(), "doubted");
    expect(kind).toBe("doubted");
    expect(text.endsWith(`${WORDING.approximate} ${WORDING.doubted}`)).toBe(
      true,
    );
  });

  it("a doubted entry without a fix is still the short notice", () => {
    expect(noticeText(hit("E-0007", { fix: "" }), "doubted").kind).toBe(
      "no-fix",
    );
  });
});

describe("caps on the body", () => {
  it("keeps a long English fix within 400 characters and 120 tokens", () => {
    const fix = "re-run pnpm install after closing the editor ".repeat(20);
    const { text } = noticeText(
      hit("E-0007", { fix, trigger: CAUSE.repeat(5) }),
    );
    expect(Array.from(text).length).toBeLessThanOrEqual(NOTICE_MAX_CHARS);
    expect(estimateTokens(text)).toBeLessThanOrEqual(NOTICE_MAX_TOKENS);
    expect(text.endsWith(WORDING.hit)).toBe(true);
  });

  it("keeps a CJK-heavy fix within 120 tokens, ending with the instruction", () => {
    const { text } = noticeText(
      hit("E-0007", { fix: cjk(30), trigger: cjk(10) }),
    );
    expect(estimateTokens(text)).toBeLessThanOrEqual(NOTICE_MAX_TOKENS);
    expect(Array.from(text).length).toBeLessThanOrEqual(NOTICE_MAX_CHARS);
    expect(text).toContain(`${ELLIPSIS} ${WORDING.hit}`);
    // The cause gave way first, to its floor.
    expect(text).toContain(`cause: ${clip(cjk(10), CAUSE_MIN_CHARS)} |`);
  });

  it("keeps every wording within the caps", () => {
    const heavy = { fix: cjk(30), trigger: cjk(10) };
    for (const event of [hit("E-0007", heavy), near("E-0007", heavy)])
      for (const trust of ["trusted", "doubted"] as const)
        expect(withinCaps(noticeText(event, trust).text)).toBe(true);
  });

  it("clips an absurd ID rather than break the cap", () => {
    const id = `E-${"9".repeat(600)}`;
    for (const event of [hit(id), hit(id, { fix: "" }), miss(id)]) {
      const { text } = noticeText(event);
      expect(withinCaps(text)).toBe(true);
      expect(text.endsWith(ELLIPSIS)).toBe(true);
    }
  });
});

describe("noticeSource", () => {
  it("has exactly the dsh-llm plugin notice shape", () => {
    expect(noticeSource("short")).toStrictEqual({
      kind: "plugin",
      plugin: "err-kb",
      form: "notice",
      summary: "short",
    });
    expect(PLUGIN_NAME).toBe(name);
  });

  it("bounds the summary to 120 characters with dsh-llm's own function", () => {
    expect(SUMMARY_MAX_CHARS).toBe(120);
    const text = noticeText(hit()).text;
    expect(text.length).toBeGreaterThan(120);
    const { summary } = noticeSource(text);
    expect(summary).toBe(boundContextSummary(text));
    expect(summary.length).toBe(120);
    expect(summary.endsWith(ELLIPSIS)).toBe(true);
    expect(noticeSource("x".repeat(120)).summary).toBe("x".repeat(120));
    expect(noticeSource("x".repeat(121)).summary.length).toBe(120);
  });
});

describe("CapTracker", () => {
  it("defaults to 1 per step, 3 per turn, 2 per ID, 1 for a fixed entry", () => {
    expect(DEFAULT_CAP_LIMITS).toEqual({
      perStep: 1,
      perTurn: 3,
      perIdPerSession: 2,
      fixedPerSession: 1,
    });
    expect(new CapTracker().limits).toEqual(DEFAULT_CAP_LIMITS);
  });

  it("allows at most one notice per step", () => {
    const caps = new CapTracker();
    expect(caps.tryEmit("E-0001")).toBe(true);
    expect(caps.tryEmit("E-0002")).toBe(false);
    caps.beginStep();
    expect(caps.tryEmit("E-0002")).toBe(true);
  });

  it("allows at most three notices per turn", () => {
    const caps = new CapTracker();
    const emitted = ["E-1", "E-2", "E-3", "E-4"].map((id) => {
      caps.beginStep();
      return caps.tryEmit(id);
    });
    expect(emitted).toEqual([true, true, true, false]);
    caps.beginTurn();
    expect(caps.tryEmit("E-4")).toBe(true);
  });

  it("allows at most two notices per ID per session, across turns", () => {
    const caps = new CapTracker();
    const emitted = [1, 2, 3].map(() => {
      caps.beginTurn();
      return caps.tryEmit("E-0007");
    });
    expect(emitted).toEqual([true, true, false]);
  });

  it("lets a fixed entry speak once per session", () => {
    const caps = new CapTracker();
    expect(caps.tryEmit("E-0007", true)).toBe(true);
    caps.beginTurn();
    expect(caps.tryEmit("E-0007", true)).toBe(false);
  });

  it("takes nothing from any budget when it refuses", () => {
    const caps = new CapTracker();
    expect(caps.tryEmit("E-0007", true)).toBe(true);
    caps.beginStep();
    expect(caps.tryEmit("E-0007", true)).toBe(false);
    // The refusal did not use up this step.
    expect(caps.tryEmit("E-0008")).toBe(true);
  });

  it("only lowers limits", () => {
    const caps = new CapTracker({ perTurn: 1, perStep: 5 });
    expect(caps.limits).toEqual({ ...DEFAULT_CAP_LIMITS, perTurn: 1 });
    expect(caps.tryEmit("E-1")).toBe(true);
    caps.beginStep();
    expect(caps.tryEmit("E-2")).toBe(false);
  });

  it("keeps sessions apart: one tracker each", () => {
    const a = new CapTracker();
    const b = new CapTracker();
    a.tryEmit("E-0007");
    a.beginTurn();
    a.tryEmit("E-0007");
    expect(b.tryEmit("E-0007")).toBe(true);
  });
});

describe("fix trust", () => {
  it("trustLevel: trusted, doubted after 1, suppressed after 2 with no success", () => {
    const r = (recurredAfterInject: number, succeeded = 0) => ({
      injected: 3,
      recurredAfterInject,
      succeeded,
      fixSig: "x",
    });
    expect(trustLevel(undefined)).toBe("trusted");
    expect(trustLevel(r(0))).toBe("trusted");
    expect(trustLevel(r(1))).toBe("doubted");
    expect(trustLevel(r(2))).toBe("suppressed");
    expect(trustLevel(r(5, 1))).toBe("doubted");
  });

  it("fixSig ignores whitespace and changes with the fix", () => {
    expect(fixSig("a  b\n")).toBe(fixSig("a b"));
    expect(fixSig("a b")).not.toBe(fixSig("a c"));
    expect(fixSig("a")).toMatch(/^[0-9a-f]{12}$/);
  });

  it("counts a recurrence only after an injection in the same turn", () => {
    const trust = new FixTrust();
    trust.seen("E-0007", FIX);
    expect(trust.record("E-0007", FIX)).toBeUndefined();
    trust.injected("E-0007", FIX);
    trust.beginTurn();
    trust.seen("E-0007", FIX);
    expect(trust.record("E-0007", FIX)).toMatchObject({
      injected: 1,
      recurredAfterInject: 0,
    });
    trust.injected("E-0007", FIX);
    trust.seen("E-0007", FIX);
    trust.seen("E-0007", FIX);
    expect(trust.record("E-0007", FIX)).toMatchObject({
      injected: 2,
      recurredAfterInject: 1,
    });
    expect(trust.level("E-0007", FIX)).toBe("doubted");
  });

  it("starts over when the fix text is edited", () => {
    const trust = new FixTrust();
    trust.injected("E-0007", FIX);
    trust.seen("E-0007", FIX);
    expect(trust.level("E-0007", FIX)).toBe("doubted");
    expect(trust.level("E-0007", "a better fix")).toBe("trusted");
    trust.injected("E-0007", "a better fix");
    expect(trust.snapshot().entries["E-0007"]).toEqual({
      injected: 1,
      recurredAfterInject: 0,
      succeeded: 0,
      fixSig: fixSig("a better fix"),
    });
  });

  it("a success lifts suppression", () => {
    const trust = new FixTrust();
    trust.succeeded("E-0007", FIX);
    expect(trust.record("E-0007", FIX)).toBeUndefined();
    for (let i = 0; i < 2; i++) {
      trust.injected("E-0007", FIX);
      trust.seen("E-0007", FIX);
    }
    expect(trust.level("E-0007", FIX)).toBe("suppressed");
    trust.succeeded("E-0007", FIX);
    expect(trust.level("E-0007", FIX)).toBe("doubted");
  });

  it("keeps plain, serializable state behind its store", () => {
    const store = memoryTrustStore();
    const trust = new FixTrust(store);
    trust.injected("E-0007", FIX);
    trust.seen("E-0007", FIX);
    const saved: TrustState = store.load();
    expect(JSON.parse(JSON.stringify(saved))).toEqual(saved);
    expect(saved).toEqual(trust.snapshot());
    expect(new FixTrust(store).level("E-0007", FIX)).toBe("doubted");
  });

  it("memoryTrustStore copies in and out", () => {
    const initial: TrustState = { entries: {} };
    const store = memoryTrustStore(initial);
    initial.entries["E-1"] = {
      injected: 1,
      recurredAfterInject: 0,
      succeeded: 0,
      fixSig: "x",
    };
    expect(store.load()).toEqual({ entries: {} });
    const loaded = store.load();
    loaded.entries["E-2"] = initial.entries["E-1"];
    expect(store.load()).toEqual({ entries: {} });
    expect(memoryTrustStore().load()).toEqual({ entries: {} });
  });
});

describe("Injector", () => {
  it("defaults to hit-only", () => {
    expect(new Injector().mode).toBe("hit-only");
    expect(INJECT_MODES).toEqual(["hit-only", "always", "off"]);
  });

  it("emits a hit with its source", () => {
    const notice = new Injector().offer(hit());
    expect(notice).toEqual({
      id: "E-0007",
      kind: "hit",
      text: noticeText(hit()).text,
      source: noticeSource(noticeText(hit()).text),
    });
  });

  it("handles a miss according to the inject setting", () => {
    const by = (mode: InjectMode) => new Injector({ mode }).offer(miss());
    expect(by("hit-only")).toBeUndefined();
    expect(by("off")).toBeUndefined();
    expect(by("always")).toMatchObject({
      id: "E-0011",
      kind: "miss",
      text: "[errkb] recorded as E-0011 (no fix yet).",
      source: { kind: "plugin", plugin: "err-kb", form: "notice" },
    });
  });

  it("ask(): the one-shot fix prompt, under the step and turn caps (T14)", () => {
    const injector = new Injector({ caps: new CapTracker() });
    const asked = injector.ask("E-0011");
    expect(asked).toEqual({
      id: "E-0011",
      kind: "ask-fix",
      text: "[errkb] E-0011 looks resolved. Record the fix with err_record in one sentence so it can be reused.",
      source: noticeSource(asked?.text as string),
    });
    // One notice per step.
    expect(injector.ask("E-0012")).toBeUndefined();
    expect(new Injector({ mode: "off" }).ask("E-0011")).toBeUndefined();
  });

  it("ask() has its own per-ID budget, apart from the entry's notices", () => {
    const injector = new Injector();
    const noFix = hit("E-0007", { fix: "" });
    for (let i = 0; i < 2; i++) {
      injector.beginTurn();
      expect(injector.offer(noFix)?.kind).toBe("no-fix");
    }
    injector.beginTurn();
    expect(injector.offer(noFix)).toBeUndefined();
    expect(injector.ask("E-0007")?.kind).toBe("ask-fix");
  });

  it("says nothing at all when inject is off", () => {
    const injector = new Injector({ mode: "off" });
    expect(injector.offer(hit())).toBeUndefined();
    expect(injector.offer(near())).toBeUndefined();
    expect(injector.trust.snapshot()).toEqual({ entries: {} });
  });

  it("emits hits under always too", () => {
    expect(new Injector({ mode: "always" }).offer(hit())?.kind).toBe("hit");
  });

  it("never speaks for a non-injectable entry", () => {
    const injector = new Injector({ mode: "always" });
    for (const excluded of ["wontfix", "misjudged"] as const)
      expect(
        injector.offer(
          hit(
            "E-0007",
            { status: excluded === "wontfix" ? "wontfix" : "open" },
            {
              injectable: false,
              excluded,
            },
          ),
        ),
      ).toBeUndefined();
    // Silence spent no budget.
    expect(injector.offer(hit("E-0008"))).toBeDefined();
  });

  it("emits a no-fix hit without touching trust", () => {
    const injector = new Injector();
    const notice = injector.offer(hit("E-0007", { fix: "" }));
    expect(notice?.kind).toBe("no-fix");
    expect(injector.trust.snapshot()).toEqual({ entries: {} });
  });

  it("applies the caps: 1 per step, 3 per turn", () => {
    const injector = new Injector({ mode: "always" });
    injector.beginTurn();
    expect(injector.offer(hit("E-1"))).toBeDefined();
    expect(injector.offer(miss("E-2"))).toBeUndefined();
    const later = ["E-3", "E-4", "E-5"].map((id) => {
      injector.beginStep();
      return injector.offer(hit(id)) !== undefined;
    });
    expect(later).toEqual([true, true, false]);
  });

  it("lets a fixed entry speak once per session", () => {
    const injector = new Injector();
    const fixed = () => hit("E-0007", { status: "fixed" });
    expect(injector.offer(fixed())).toBeDefined();
    injector.beginTurn();
    expect(injector.offer(fixed())).toBeUndefined();
  });

  it("speaks at most twice per ID per session", () => {
    const injector = new Injector();
    const spoken = [1, 2, 3].map(() => {
      injector.beginTurn();
      return injector.offer(hit()) !== undefined;
    });
    expect(spoken).toEqual([true, true, false]);
  });

  it("turns doubtful after a recurrence in the turn, then stops at two", () => {
    // One trust store outlives the sessions, as state.json will.
    const trust = new FixTrust();
    const session = () => new Injector({ trust });

    const first = session();
    first.beginTurn();
    expect(first.offer(hit())?.kind).toBe("hit");
    first.beginStep();
    // Same error again in the same turn: the fix did not hold.
    const second = first.offer(hit());
    expect(second?.kind).toBe("doubted");
    expect(second?.text.endsWith(WORDING.doubted)).toBe(true);
    expect(second?.text).not.toContain(WORDING.hit);
    first.beginStep();
    // A second recurrence: suppressed on this machine.
    expect(first.offer(hit())).toBeUndefined();
    expect(trust.snapshot().entries["E-0007"]).toMatchObject({
      injected: 2,
      recurredAfterInject: 2,
      succeeded: 0,
    });

    const next = session();
    next.beginTurn();
    expect(next.offer(hit())).toBeUndefined();
    // Another entry is unaffected.
    expect(next.offer(hit("E-0008"))?.kind).toBe("hit");
  });

  it("does not count a recurrence across turns", () => {
    const injector = new Injector();
    injector.beginTurn();
    injector.offer(hit());
    injector.beginTurn();
    expect(injector.offer(hit())?.kind).toBe("hit");
  });

  it("a recurring near hit is doubtful as well as approximate", () => {
    const injector = new Injector();
    injector.offer(near());
    injector.beginStep();
    const notice = injector.offer(near());
    expect(notice?.kind).toBe("doubted");
    expect(
      notice?.text.endsWith(`${WORDING.approximate} ${WORDING.doubted}`),
    ).toBe(true);
  });

  it("every notice it emits is inside the caps", () => {
    const injector = new Injector({ mode: "always" });
    const events = [
      hit("E-1", { fix: cjk(40), trigger: cjk(20) }),
      near("E-2", { fix: "x ".repeat(400) }),
      miss("E-3"),
    ];
    for (const event of events) {
      injector.beginStep();
      const notice = injector.offer(event);
      expect(notice).toBeDefined();
      expect(withinCaps(notice!.text)).toBe(true);
      expect(notice!.source.summary.length).toBeLessThanOrEqual(120);
    }
  });
});

// ---------------------------------------------------------------------------
// T13: scoped trust, observe(), the digest and the standing section

describe("fix trust: a store that loads asynchronously (state.json)", () => {
  const record = (fix: string, recurred = 0) => ({
    injected: 1,
    recurredAfterInject: recurred,
    succeeded: 0,
    fixSig: fixSig(fix),
  });

  it("saved records join once read; one that changed meanwhile is kept", async () => {
    let finish: (state: TrustState | undefined) => void = () => undefined;
    const saves: TrustState[] = [];
    const trust = new FixTrust({
      load: () => ({ entries: {} }),
      save: (state) => saves.push(structuredClone(state)),
      loaded: new Promise((resolve) => {
        finish = resolve;
      }),
    });
    // Before the read lands, this process injects E-0001's fix.
    trust.injected("E-0001", "fresh");
    finish({
      entries: { "E-0001": record("stale", 2), "E-0002": record("b", 2) },
    });
    await trust.ready;
    expect(trust.record("E-0001", "fresh")).toMatchObject({ injected: 1 });
    expect(trust.level("E-0002", "b")).toBe("suppressed");
    expect(saves).toHaveLength(1);
  });

  it("nothing saved: the state stays as load() gave it", async () => {
    const trust = new FixTrust({
      load: () => ({ entries: { "E-0001": record("a") } }),
      save: () => undefined,
      loaded: Promise.resolve(undefined),
    });
    await trust.ready;
    expect(trust.snapshot().entries).toEqual({ "E-0001": record("a") });
  });
});

describe("fix trust scopes (T13)", () => {
  it("a recurrence counts only in the scope that injected the fix", () => {
    const trust = new FixTrust();
    trust.injected("E-0007", FIX, "a");
    trust.seen("E-0007", FIX, "b");
    expect(trust.record("E-0007", FIX)?.recurredAfterInject).toBe(0);
    trust.beginTurn("b");
    trust.seen("E-0007", FIX, "a");
    expect(trust.record("E-0007", FIX)?.recurredAfterInject).toBe(1);
  });

  it("a new turn in one scope leaves another scope's injections alone", () => {
    const trust = new FixTrust();
    trust.injected("E-0007", FIX, "a");
    trust.injected("E-0008", FIX, "b");
    trust.beginTurn("a");
    trust.seen("E-0007", FIX, "a");
    trust.seen("E-0008", FIX, "b");
    expect(trust.record("E-0007", FIX)?.recurredAfterInject).toBe(0);
    expect(trust.record("E-0008", FIX)?.recurredAfterInject).toBe(1);
  });

  it("an observed hit is not counted again when it is offered", () => {
    const trust = new FixTrust();
    const injector = new Injector({ trust, scope: "s" });
    const event = hit() as Extract<NoticeEvent, { kind: "hit" }>;
    expect(injector.offer(event)?.kind).toBe("hit");
    injector.caps.beginStep();
    injector.observe(event.hit);
    expect(injector.offer(event, true)?.kind).toBe("doubted");
    expect(trust.record("E-0007", FIX)?.recurredAfterInject).toBe(1);
  });
});

describe("sessionDigestText", () => {
  const e = (
    id: string,
    hits: number,
    injectable = true,
    title = `title ${id}`,
  ) => ({
    id,
    title,
    hits,
    injectable,
  });

  it("knows the three modes", () => {
    expect(SESSION_DIGEST_MODES).toEqual(["off", "counts", "index"]);
  });

  it("counts: one line, singular and plural", () => {
    expect(sessionDigestText([e("E-0001", 1)], "counts")).toBe(
      "[errkb] 1 known error; known fixes are shown when an error repeats.",
    );
    expect(sessionDigestText([e("E-0001", 1), e("E-0002", 1)], "counts")).toBe(
      "[errkb] 2 known errors; known fixes are shown when an error repeats.",
    );
  });

  it("off, or nothing recorded: nothing", () => {
    expect(sessionDigestText([e("E-0001", 1)], "off")).toBeUndefined();
    expect(sessionDigestText([], "counts")).toBeUndefined();
    expect(sessionDigestText([], "index")).toBeUndefined();
  });

  it("index: most hits first, document order on ties, capped and clipped", () => {
    const entries = Array.from({ length: 14 }, (_, i) =>
      e(
        `E-${String(i + 1).padStart(4, "0")}`,
        i % 3,
        true,
        `t${i + 1} ${"x".repeat(100)}`,
      ),
    );
    const lines = (sessionDigestText(entries, "index") as string).split("\n");
    expect(lines).toHaveLength(DIGEST_MAX_TITLES + 1);
    expect(lines.slice(1, 4).map((l) => l.split(" ")[0])).toEqual([
      "E-0003",
      "E-0006",
      "E-0009",
    ]);
    const title = lines[1]?.replace(/^E-0003 /, "").replace(/ \(2 hits\)$/, "");
    expect(Array.from(title as string)).toHaveLength(DIGEST_TITLE_MAX_CHARS);
    expect(title?.endsWith(ELLIPSIS)).toBe(true);
  });

  it("index with only excluded entries is the count line alone", () => {
    expect(sessionDigestText([e("E-0001", 9, false)], "index")).toBe(
      "[errkb] 1 known error; known fixes are shown when an error repeats.",
    );
  });
});

describe("the standing section", () => {
  it("is plugin:errkb at 10400, about 50 tokens, with no template variables", () => {
    expect(SYSTEM_PROMPT_SECTION).toEqual({
      name: "plugin:errkb",
      order: 10400,
    });
    expect(estimateTokens(SYSTEM_PROMPT_HINT)).toBeLessThanOrEqual(90);
    expect(SYSTEM_PROMPT_HINT.split(/\s+/).length).toBeLessThanOrEqual(50);
    expect(SYSTEM_PROMPT_HINT).not.toMatch(/\{\{/);
    // T15: the hint points at the one tool that writes a fix.
    expect(SYSTEM_PROMPT_HINT).toContain(
      "record a working fix with err_record",
    );
  });
});

// ---------------------------------------------------------------------------
// Conformance corpus (EKB-009): the same cases the Go port must reproduce.

function corpus<T>(name: string): T[] {
  const file = resolve(
    fileURLToPath(new URL("..", import.meta.url)),
    "spec",
    "corpus",
    `${name}.json`,
  );
  return (JSON.parse(readFileSync(file, "utf8")) as { cases: T[] }).cases;
}

interface NoticeCase {
  case_id: string;
  source_test: string;
  input: { event: NoticeEvent; trust?: string };
  expected: { kind: string; text: string };
}

describe("conformance corpus: notice", () => {
  for (const c of corpus<NoticeCase>("notice")) {
    it(`${c.case_id} (${c.source_test})`, () => {
      expect(
        noticeText(
          c.input.event,
          c.input.trust as Parameters<typeof noticeText>[1],
        ),
      ).toEqual(c.expected);
    });
  }
});
