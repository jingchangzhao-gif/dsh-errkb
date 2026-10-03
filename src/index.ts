// dsh-errkb plugin entry. This file declares the plugin's surface and wires it:
// apply() resolves the knowledge base, binds a recorder to it, and registers the
// two capture listeners (T11) together with the four injection points (T13),
// which also feed resolution detection (T14), and the five tools (T15).
// The pipeline itself lives in src/plugin.ts; every registration point still
// to come is marked with the task that owns it.
import type { Context } from "@deepseek-ai/cordis";
import z from "@deepseek-ai/schemastery";
import { defaultProbe, formatKbLog, resolveKbDir } from "./paths";
import { INJECT_MODES, SESSION_DIGEST_MODES } from "./inject";
import type { InjectMode, SessionDigestMode } from "./inject";
import { CAPTURE_FIX_MODES } from "./resolve-detect";
import type { CaptureFixMode } from "./resolve-detect";
import {
  DEFAULT_INJECTION_OPTIONS,
  createInjection,
  createRecorder,
  registerInjection,
} from "./plugin";
import type { InjectionOptions, RecorderOptions } from "./plugin";
import { registerTools } from "./tools";
import { effectiveTransientThreshold } from "./capture";

// The plugin's runtime name, matching `id` in cordis.patch.yml. The installed
// harness plugins follow the same rule: dsh-spill-policy exports "spill-policy",
// dsh-persona exports "persona", and the third-party dsh-pr-watch exports
// "pr-watch" next to `id: pr-watch` in its own patch.
export const name = "err-kb";

// Real service names, copied from installed plugins rather than invented: the
// `tools` service backs the five tools registered in T15, and `systemPrompt`
// backs the standing section registered in T13.
export const inject = ["tools", "systemPrompt"];

// Every default mirrors the Settings table in README.md item for item. T11
// reads the capture and store settings (see recorderOptions), T13 reads
// inject, sessionDigest and systemPromptHint and T14 reads captureFix (see
// injectionOptions); providers and exportDir are still only declared. inject,
// captureFix and sessionDigest stay strings so an existing profile with a typo
// still loads: a value outside the documented set (inject:
// hit-only|always|off, captureFix: prompt-once|off, sessionDigest:
// off|counts|index) falls back to the default.
// `share` is read, and anything but "private" counts as "public", the safer
// of the two. `labels` is the exception: it is new, so it starts out as the
// union it documents (§17 Q2). The store reads both label sets whatever this
// says; it only decides the language of blocks it writes. The numeric settings
// and idPrefix are clamped to their documented ranges by normalizeSettings(),
// with a warning, rather than rejected here: a rejected value would stop apply.
export const Config = z.object({
  kbDir: z.string().default(""),
  idPrefix: z.string().default("E-"),
  idWidth: z.number().default(4),
  capture: z.array(z.string()).default(["tool", "command", "llm", "agent"]),
  captureExitCodes: z.boolean().default(true),
  transientThreshold: z.number().default(5),
  fuzzyThreshold: z.number().default(0.72),
  captureFix: z.string().default("prompt-once"),
  inject: z.string().default("hit-only"),
  sessionDigest: z.string().default("counts"),
  systemPromptHint: z.boolean().default(true),
  providers: z.array(z.string()).default(["*"]),
  share: z.string().default("public"),
  maxEntries: z.number().default(200),
  maxSampleChars: z.number().default(500),
  exportDir: z.string().default(""),
  labels: z.union(["en", "zh"]).default("en"),
});

/** The plugin's settings, as apply() receives them. */
export type Config = Schemastery.TypeT<typeof Config>;

/**
 * The `idPrefix` shape: letters, digits, `-` and `_`, not ending in a digit, so
 * the trailing digits of an ID are its number alone (match.ts and tools.ts
 * both read them that way) and a prefix never spans a header's ` · `.
 */
export const ID_PREFIX_PATTERN = /^[A-Za-z0-9_-]*[A-Za-z_-]$/;

/** One numeric setting's documented range and whether it is a whole number. */
interface NumericBounds {
  min: number;
  max: number;
  integer: boolean;
}

/** The documented ranges, checked by normalizeSettings(). */
export const SETTING_BOUNDS = {
  fuzzyThreshold: { min: 0.5, max: 1, integer: false },
  idWidth: { min: 1, max: 9, integer: true },
  maxEntries: { min: 1, max: Infinity, integer: true },
  maxSampleChars: { min: 0, max: Infinity, integer: true },
} as const satisfies Record<string, NumericBounds>;

/** `value` inside `bounds`; a non-finite value takes `fallback`. */
function clampSetting(
  value: number,
  bounds: NumericBounds,
  fallback: number,
): number {
  if (Number.isNaN(value)) return fallback;
  const whole =
    bounds.integer && Number.isFinite(value) ? Math.floor(value) : value;
  return Math.min(bounds.max, Math.max(bounds.min, whole));
}

/**
 * Keep the numeric settings and `idPrefix` inside their documented ranges, so
 * a typo degrades instead of silently breaking matching, archiving or IDs.
 * `transientThreshold` follows capture.ts's own rule
 * (effectiveTransientThreshold), reported here so the change is visible.
 *
 * @param config - the plugin's settings.
 * @param warn - called once per setting that changed, naming it, the value
 *   given and the value used.
 * @returns the settings with every out-of-range value replaced.
 */
export function normalizeSettings(
  config: Config,
  warn: (message: string) => void = () => undefined,
): Config {
  const defaults = Config({});
  const out: Config = { ...config };
  const show = (value: unknown) =>
    typeof value === "string" ? JSON.stringify(value) : String(value);
  const report = (key: string, given: unknown, used: unknown) =>
    warn(
      `err-kb: setting ${key}=${show(given)} is out of range; using ${show(used)}`,
    );
  for (const key of Object.keys(SETTING_BOUNDS) as Array<
    keyof typeof SETTING_BOUNDS
  >) {
    const given = config[key];
    const used = clampSetting(given, SETTING_BOUNDS[key], defaults[key]);
    if (!Object.is(used, given)) {
      out[key] = used;
      report(key, given, used);
    }
  }
  const threshold = effectiveTransientThreshold(config.transientThreshold);
  if (threshold !== config.transientThreshold) {
    out.transientThreshold = threshold;
    report("transientThreshold", config.transientThreshold, threshold);
  }
  if (!ID_PREFIX_PATTERN.test(config.idPrefix)) {
    out.idPrefix = defaults.idPrefix;
    report("idPrefix", config.idPrefix, defaults.idPrefix);
  }
  return out;
}

/**
 * The recorder settings a configuration selects.
 *
 * @param config - the plugin's settings.
 * @returns capture, matching and store settings for createRecorder().
 */
export function recorderOptions(config: Config): RecorderOptions {
  return {
    capture: config.capture,
    captureExitCodes: config.captureExitCodes,
    transientThreshold: config.transientThreshold,
    fuzzyThreshold: config.fuzzyThreshold,
    share: config.share === "private" ? "private" : "public",
    maxEntries: config.maxEntries,
    maxSampleChars: config.maxSampleChars,
    labels: config.labels,
    idPrefix: config.idPrefix,
    idWidth: config.idWidth,
  };
}

/** `value` when it is one of `allowed`, else `fallback`. */
function oneOf<T extends string>(
  value: string,
  allowed: readonly T[],
  fallback: T,
): T {
  return (allowed as readonly string[]).includes(value)
    ? (value as T)
    : fallback;
}

/**
 * The injection settings a configuration selects.
 *
 * @param config - the plugin's settings.
 * @returns the settings for createInjection(); an unknown `inject`,
 *   `captureFix` or `sessionDigest` value takes its default.
 */
export function injectionOptions(config: Config): InjectionOptions {
  return {
    inject: oneOf<InjectMode>(
      config.inject,
      INJECT_MODES,
      DEFAULT_INJECTION_OPTIONS.inject,
    ),
    captureFix: oneOf<CaptureFixMode>(
      config.captureFix,
      CAPTURE_FIX_MODES,
      DEFAULT_INJECTION_OPTIONS.captureFix,
    ),
    sessionDigest: oneOf<SessionDigestMode>(
      config.sessionDigest,
      SESSION_DIGEST_MODES,
      DEFAULT_INJECTION_OPTIONS.sessionDigest,
    ),
    systemPromptHint: config.systemPromptHint,
  };
}

// One startup line with the resolved directory and its tier (T05), then the
// two capture listeners (T11), the four injection points (T13) and the five
// tools (T15).
export function apply(ctx: Context, given: Config) {
  const config = normalizeSettings(given, (message) =>
    ctx.logger.warn(message),
  );
  const kb = resolveKbDir(config.kbDir, defaultProbe());
  ctx.logger.info(formatKbLog(kb));
  const recorder = createRecorder({
    files: kb.files,
    logger: ctx.logger,
    options: recorderOptions(config),
  });
  const injection = createInjection({
    recorder,
    options: injectionOptions(config),
  });
  registerInjection(ctx, injection, recorder);
  registerTools(ctx, {
    recorder,
    injection,
    kbDir: kb.dir,
    options: {
      idPrefix: config.idPrefix,
      idWidth: config.idWidth,
      fuzzyThreshold: config.fuzzyThreshold,
    },
  });

  // TODO(T16): register the agent/request-error listener here.
}
