// dsh-errkb plugin entry. This file declares the plugin's surface and wires it:
// apply() builds the thin core client (EKB-038d), which forwards every failure
// as an observation over the local Named Pipe, replays queued observations when
// the daemon returns, and registers the two capture listeners (T11), the four
// injection points (T13), resolution detection (T14) and the five tools (T15).
// The core owns the knowledge base; this file owns only the DSH integration.
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
import { createConnector, createCore, createPipeTransport } from "./connector";
import { createObservationQueue } from "./state";

// The connector module stays exported for adapters that want the typed core
// client directly (it is also how the tests drive the adapter).
export * from "./connector";

// The plugin's runtime name, matching `id` in cordis.patch.yml.
export const name = "err-kb";

// Real service names: the `tools` service backs the five tools, and
// `systemPrompt` backs the standing section registered in T13.
export const inject = ["tools", "systemPrompt"];

// Every default mirrors the Settings table in README.md item for item. The
// knowledge-base settings (kbDir, maxEntries, maxSampleChars, exportDir) stay
// declared so existing profiles keep loading; they no longer select local
// storage — the core owns it. The connector settings select the pipe, the
// per-call timeout and the offline queue caps. `share`, `labels` and
// `providers` are read-only for compatibility; `transientThreshold`,
// `fuzzyThreshold`, `maxEntries`, `maxSampleChars` and `idPrefix` keep their
// documented clamps so a typo degrades instead of stopping apply.
export const Config = z.object({
  // Connector settings (EKB-038d).
  pipe: z.string().default("\\\\.\\pipe\\errkb"),
  pipeTimeoutMs: z.number().default(10_000),
  offlineMaxEvents: z.number().default(1_000),
  offlineMaxBytes: z.number().default(32 * 1024 * 1024),
  connectorId: z.string().default("dsh"),
  projectKey: z.string().default("default"),

  // Capture and injection settings (unchanged semantics).
  capture: z.array(z.string()).default(["tool", "command", "llm", "agent"]),
  captureExitCodes: z.boolean().default(true),
  transientThreshold: z.number().default(5),
  fuzzyThreshold: z.number().default(0.72),
  captureFix: z.string().default("prompt-once"),
  inject: z.string().default("hit-only"),
  sessionDigest: z.string().default("counts"),
  systemPromptHint: z.boolean().default(true),

  // Legacy knowledge-base settings, kept declared for profile compatibility.
  kbDir: z.string().default(""),
  idPrefix: z.string().default("E-"),
  idWidth: z.number().default(4),
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
 * the trailing digits of an ID are its number alone and a prefix never spans a
 * header's ` · `.
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
  pipeTimeoutMs: { min: 1, max: 600_000, integer: true },
  offlineMaxEvents: { min: 1, max: 100_000, integer: true },
  offlineMaxBytes: { min: 1, max: 1024 * 1024 * 1024, integer: true },
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
 * a typo degrades instead of silently breaking matching or the connector.
 * `transientThreshold` follows capture.ts's own rule
 * (effectiveTransientThreshold), reported here so the change is visible.
 *
 * @param config - the plugin's settings.
 * @param warn - called once per setting that changed.
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
 * The recorder settings a configuration selects: which sources are captured,
 * and which connector/project the observations belong to.
 *
 * @param config - the plugin's settings.
 * @returns settings for createRecorder().
 */
export function recorderOptions(config: Config): RecorderOptions {
  return {
    capture: config.capture,
    captureExitCodes: config.captureExitCodes,
    connectorId: config.connectorId,
    projectKey: config.projectKey,
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

// One startup line naming the core, then the two capture listeners (T11), the
// four injection points (T13) and the five tools (T15). The local knowledge
// base still resolves the queue directory (state.json + lock), so the offline
// queue lives with the plugin's other state.
export function apply(ctx: Context, given: Config) {
  const config = normalizeSettings(given, (message) =>
    ctx.logger.warn(message),
  );
  const kb = resolveKbDir(config.kbDir, defaultProbe());
  ctx.logger.info(formatKbLog(kb));
  ctx.logger.info(
    "err-kb: knowledge base is the local core at %s (connector %s)",
    config.pipe,
    config.connectorId,
  );
  const queue = createObservationQueue({
    files: { state: kb.files.state, lock: kb.files.lock },
    options: {
      maxEvents: config.offlineMaxEvents,
      maxBytes: config.offlineMaxBytes,
    },
  });
  const recorder = createRecorder({
    logger: ctx.logger,
    queue,
    options: recorderOptions(config),
    createConnector: (sessionId) =>
      createConnector({
        connectorId: config.connectorId,
        sessionId,
        projectKey: config.projectKey,
        queue,
        core: createCore(
          createPipeTransport({
            pipe: config.pipe,
            timeoutMs: config.pipeTimeoutMs,
          }),
        ),
      }),
  });
  const injection = createInjection({
    recorder,
    options: injectionOptions(config),
  });
  registerInjection(ctx, injection, recorder);
  registerTools(ctx, {
    recorder,
    injection,
    kbDir: config.pipe,
    options: {
      idPrefix: config.idPrefix,
      idWidth: config.idWidth,
    },
  });

  // TODO(T16): register the agent/request-error listener here.
}
