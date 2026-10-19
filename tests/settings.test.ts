import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Context } from "@deepseek-ai/cordis";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  Config,
  ID_PREFIX_PATTERN,
  SETTING_BOUNDS,
  apply,
  injectionOptions,
  normalizeSettings,
} from "../src/index";
import { DEFAULT_INJECTION_OPTIONS } from "../src/plugin";
import { effectiveTransientThreshold } from "../src/capture";

/** normalizeSettings() on the defaults plus `over`, with its warnings. */
function normalize(over: Partial<Config>) {
  const warnings: string[] = [];
  const config = normalizeSettings(Config({ ...over }), (m) =>
    warnings.push(m),
  );
  return { config, warnings };
}

describe("normalizeSettings", () => {
  it("leaves the defaults alone and warns about nothing", () => {
    const { config, warnings } = normalize({});
    expect(config).toEqual(Config({}));
    expect(warnings).toEqual([]);
  });

  it("works without a warn callback", () => {
    expect(normalizeSettings(Config({ idWidth: 0 })).idWidth).toBe(1);
  });

  it("does not mutate the settings it was given", () => {
    const given = Config({ maxEntries: -1 });
    normalizeSettings(given);
    expect(given.maxEntries).toBe(-1);
  });

  it.each([
    // [setting, given, used]
    ["fuzzyThreshold", 0.5, 0.5],
    ["fuzzyThreshold", 1, 1],
    ["fuzzyThreshold", 0.49, 0.5],
    ["fuzzyThreshold", 5, 1],
    ["fuzzyThreshold", -1, 0.5],
    ["fuzzyThreshold", Number.NaN, 0.72],
    ["idWidth", 1, 1],
    ["idWidth", 9, 9],
    ["idWidth", 0, 1],
    ["idWidth", 10, 9],
    ["idWidth", 4.7, 4],
    ["idWidth", Number.POSITIVE_INFINITY, 9],
    ["maxEntries", 1, 1],
    ["maxEntries", 0, 1],
    ["maxEntries", -1, 1],
    ["maxEntries", 250.9, 250],
    ["maxEntries", Number.NaN, 200],
    ["maxSampleChars", 0, 0],
    ["maxSampleChars", -5, 0],
    ["maxSampleChars", 99.5, 99],
    ["transientThreshold", 1, 1],
    ["transientThreshold", 0, 1],
    ["transientThreshold", -3, 1],
    ["transientThreshold", 2.2, 3],
    ["transientThreshold", Number.NaN, 5],
  ] as const)("%s: %s becomes %s", (key, given, used) => {
    const { config, warnings } = normalize({ [key]: given });
    expect(config[key]).toBe(used);
    if (Object.is(given, used)) {
      expect(warnings).toEqual([]);
    } else {
      expect(warnings).toEqual([
        `err-kb: setting ${key}=${String(given)} is out of range; using ${String(used)}`,
      ]);
    }
  });

  it("uses capture.ts's rule for transientThreshold", () => {
    for (const n of [-1, 0, 0.1, 1, 4.5, 7])
      expect(
        normalize({ transientThreshold: n }).config.transientThreshold,
      ).toBe(effectiveTransientThreshold(n));
  });

  it.each(["E-", "ERR_", "kb-E-", "X", "e-"])("accepts idPrefix %j", (p) => {
    const { config, warnings } = normalize({ idPrefix: p });
    expect(config.idPrefix).toBe(p);
    expect(warnings).toEqual([]);
  });

  it.each(["", "E1", "E 0", "E·", "E-\n", "E.", "错-"])(
    "replaces idPrefix %j with the default",
    (p) => {
      const { config, warnings } = normalize({ idPrefix: p });
      expect(config.idPrefix).toBe("E-");
      expect(warnings).toEqual([
        `err-kb: setting idPrefix=${JSON.stringify(p)} is out of range; using "E-"`,
      ]);
    },
  );

  it("warns once per changed setting and keeps the rest", () => {
    const { config, warnings } = normalize({
      fuzzyThreshold: 5,
      idWidth: 0,
      maxEntries: 10,
    });
    expect(config).toMatchObject({
      fuzzyThreshold: 1,
      idWidth: 1,
      maxEntries: 10,
    });
    expect(warnings).toHaveLength(2);
    expect(warnings[0]).toContain("fuzzyThreshold=5");
    expect(warnings[1]).toContain("idWidth=0");
  });

  it("documents the bounds it enforces", () => {
    expect(SETTING_BOUNDS).toEqual({
      fuzzyThreshold: { min: 0.5, max: 1, integer: false },
      idWidth: { min: 1, max: 9, integer: true },
      maxEntries: { min: 1, max: Infinity, integer: true },
      maxSampleChars: { min: 0, max: Infinity, integer: true },
      pipeTimeoutMs: { min: 1, max: 600_000, integer: true },
      offlineMaxEvents: { min: 1, max: 100_000, integer: true },
      offlineMaxBytes: { min: 1, max: 1024 * 1024 * 1024, integer: true },
    });
    expect(ID_PREFIX_PATTERN.test(Config({}).idPrefix)).toBe(true);
  });

  it.each([
    ["pipeTimeoutMs", 0, 1],
    ["pipeTimeoutMs", 10.9, 10],
    ["pipeTimeoutMs", 1e9, 600_000],
    ["offlineMaxEvents", 0, 1],
    ["offlineMaxEvents", 1e9, 100_000],
    ["offlineMaxBytes", 0, 1],
    ["offlineMaxBytes", 1e12, 1024 * 1024 * 1024],
  ] as const)("clamps %s: %s becomes %s", (key, given, used) => {
    const { config } = normalize({ [key]: given });
    expect(config[key]).toBe(used);
  });
});

describe("apply with out-of-range settings", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "errkb-settings-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("starts, and warns once per setting it had to change", () => {
    const warnings: unknown[][] = [];
    const ctx = {
      on: () => () => true,
      tools: { register: () => () => undefined },
      systemPrompt: { section: () => () => undefined },
      logger: {
        info: () => undefined,
        warn: (...args: unknown[]) => warnings.push(args),
        error: () => undefined,
        debug: () => undefined,
      },
    } as unknown as Context;
    expect(() =>
      apply(
        ctx,
        Config({
          kbDir: dir,
          fuzzyThreshold: 5,
          maxEntries: -1,
          idWidth: 0,
          maxSampleChars: -5,
          idPrefix: "",
        }),
      ),
    ).not.toThrow();
    expect(warnings.map((w) => String(w[0]))).toEqual([
      "err-kb: setting fuzzyThreshold=5 is out of range; using 1",
      "err-kb: setting idWidth=0 is out of range; using 1",
      "err-kb: setting maxEntries=-1 is out of range; using 1",
      "err-kb: setting maxSampleChars=-5 is out of range; using 0",
      'err-kb: setting idPrefix="" is out of range; using "E-"',
    ]);
  });
});

describe("injectionOptions fallbacks", () => {
  it("uses the defaults for unknown mode strings", () => {
    const options = injectionOptions(
      Config({
        inject: "bogus" as never,
        captureFix: "bogus" as never,
        sessionDigest: "bogus" as never,
      }),
    );
    expect(options).toEqual(DEFAULT_INJECTION_OPTIONS);
  });
});

describe("apply wiring", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "errkb-wiring-"));
  });
  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("queues a captured failure on the offline queue", async () => {
    const listeners = new Map<string, Function[]>();
    const sink = {
      info: () => undefined,
      warn: () => undefined,
      error: () => undefined,
      debug: () => undefined,
    };
    const ctx = {
      on: (name: string, fn: Function) => {
        listeners.set(name, [...(listeners.get(name) ?? []), fn]);
        return () => true;
      },
      tools: { register: () => () => undefined },
      systemPrompt: { section: () => () => undefined },
      logger: sink,
    } as unknown as Context;
    apply(ctx, Config({ kbDir: dir }));
    for (const fn of listeners.get("agent/error") ?? []) {
      fn({ agent: { id: "s1" }, turn: 1, step: 1, error: new Error("EPERM") });
    }
    const statePath = join(dir, "state.json");
    let document: { queue: Record<string, unknown>[] } | undefined;
    for (let i = 0; i < 40 && document === undefined; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      document = await readFile(statePath, "utf8")
        .then(
          (text) => JSON.parse(text) as { queue: Record<string, unknown>[] },
        )
        .catch(() => undefined);
    }
    expect(document?.queue).toHaveLength(1);
    expect(String(JSON.stringify(document?.queue[0]))).toContain("EPERM");
  });
});
