import z from "@deepseek-ai/schemastery";
import { accessSync, constants, statSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { boundContextSummary, createUserMessage } from "@deepseek-ai/dsh-llm";
import { connect } from "node:net";
import * as fsp from "node:fs/promises";
import { defineTool } from "@deepseek-ai/dsh-tools";
//#region src/paths.ts
/** The six file names the knowledge base directory holds. */
const KB_FILE = {
	errors: "ERRORS.md",
	archive: "ERRORS.archive.md",
	index: "errors.index.json",
	state: "state.json",
	machine: ".machine.json",
	lock: ".lock"
};
/** Environment variable that overrides the harness home. */
const DSH_HOME_ENV = "DSH_HOME";
/** Directory under the OS home that holds harness data when `$DSH_HOME` is unset. */
const DSH_HOME_DIR_NAME = ".dsh";
/** Directory under the plugin root used by tiers 1a and 2. */
const KB_DIR_NAME = "errors";
/** Directory under the harness home used by tier 3. */
const KB_FALLBACK_DIR_NAME = "errkb";
/**
* Expand a leading `~`, `~/` or `~\` against the OS home.
*
* This mirrors `@deepseek-ai/dsh-home-paths`, so a `$DSH_HOME` written as
* `~/dsh` lands where the harness would put it. It is deliberately *not* applied
* to the `kbDir` setting: that setting resolves against the plugin package root,
* and a `~` there is a configuration mistake we would rather surface as a
* literal directory name than silently reinterpret. No environment variables are
* interpolated anywhere.
*
* @param input - the raw path.
* @returns the expanded path, or the input unchanged when no supported prefix is present.
*/
function expandHomePath(input) {
	if (input === "~") return homedir();
	if (input.startsWith("~/") || input.startsWith("~\\")) return join(homedir(), input.slice(2));
	return input;
}
/**
* Resolve the harness home: `$DSH_HOME` when it is set and not blank, else
* `<os home>/.dsh`. The precedence and the blank-is-unset rule are copied from
* `@deepseek-ai/dsh-home-paths`, which also treats a whitespace-only value as
* unset so a blank override never resolves to the current directory.
*
* @param env - environment mapping to read; injected for tests.
* @returns an absolute harness home path.
*/
function resolveDshHome(env = process.env) {
	const fromEnv = env[DSH_HOME_ENV];
	return resolve(expandHomePath(fromEnv !== void 0 && fromEnv.trim().length > 0 ? fromEnv : join(homedir(), DSH_HOME_DIR_NAME)));
}
/**
* Derive the plugin package root from a module URL.
*
* The same file is loaded from `lib/index.js` once bundled and from
* `src/paths.ts` under the test runner, so both build directories are walked
* out of. A `link:` install needs no special case: Node resolves the symlink to
* the real repository path before this code runs.
*
* @param moduleUrl - `import.meta.url` of a module inside the package.
* @returns the package root.
*/
function pluginRootFrom(moduleUrl) {
	let current = dirname(fileURLToPath(moduleUrl));
	while (basename(current) === "lib" || basename(current) === "src") {
		const parent = dirname(current);
		/* v8 ignore next -- a filesystem root exists, so the walk stops before this guard */
		if (parent === current) break;
		current = parent;
	}
	return current;
}
/**
* Whether a path has a `node_modules` segment.
*
* Matching is by whole path segment, not by substring: `node_modules_backup`
* and `my_node_modules` must not match. Both separators are accepted, and the
* comparison is case-insensitive on Windows.
*
* @param dir - the path to test.
* @param platform - platform to apply; injected for tests.
* @returns true when a segment equals `node_modules`.
*/
function isInsideNodeModulesPath(dir, platform = process.platform) {
	return (platform === "win32" ? dir.toLowerCase() : dir).split(/[\\/]+/).includes("node_modules");
}
/**
* Whether a directory can be written to, climbing to the nearest existing
* ancestor when the directory itself does not exist yet.
*
* The climb stops at a regular file, because a path running through one can
* never become a directory. That case is not theoretical on Windows: `stat` on
* `<file>/child` reports ENOENT rather than ENOTDIR (measured on this machine),
* so a climb that only watched for ENOENT would land on the file and call it
* writable. Any other inspection error - including a permission denial - is a
* refusal rather than a climb.
*
* Platform note, measured on Windows: `fs.access(W_OK)` reflects the read-only
* attribute and the effective access token rather than the directory's ACL. A
* directory carrying the read-only attribute still reports writable and still
* accepts files, so this probe is evidence, not a promise; the store task keeps
* its write path recoverable for that reason.
*
* @param dir - directory that may or may not exist.
* @returns true when the directory, or its nearest existing ancestor, is a writable directory.
*/
function isWritableDir(dir) {
	let current = resolve(dir);
	for (;;) try {
		if (!statSync(current).isDirectory()) return false;
		accessSync(current, constants.W_OK);
		return true;
	} catch (error) {
		if (error.code !== "ENOENT") return false;
		const parent = dirname(current);
		/* v8 ignore next -- a filesystem root exists, so traversal resolves before this guard */
		if (parent === current) return false;
		current = parent;
	}
}
/**
* Build the six absolute file paths inside a knowledge base directory.
*
* @param dir - the resolved knowledge base directory.
* @returns the file paths.
*/
function filesIn(dir) {
	return {
		errors: join(dir, KB_FILE.errors),
		archive: join(dir, KB_FILE.archive),
		index: join(dir, KB_FILE.index),
		state: join(dir, KB_FILE.state),
		machine: join(dir, KB_FILE.machine),
		lock: join(dir, KB_FILE.lock)
	};
}
/**
* Resolve the knowledge base directory.
*
* Tier 1a: `kbDir` is a relative path -> `<pluginRoot>/<kbDir>`.
* Tier 1b: `kbDir` is an absolute path -> used as written.
* Tier 2:  `kbDir` is empty and the plugin root is writable and not inside
*          `node_modules` -> `<pluginRoot>/errors`.
* Tier 3:  otherwise (global install, read-only package directory, unwritable
*          disk) -> `<dshHome>/errkb`.
*
* A blank or whitespace-only `kbDir` counts as empty, matching the harness's
* treatment of a blank `$DSH_HOME`.
*
* @param kbDir - the configured directory, relative or absolute, or empty.
* @param probe - machine facts, injected.
* @returns the directory, the tier that produced it and the six file paths.
*/
function resolveKbDir(kbDir, probe) {
	const configured = kbDir.trim();
	if (configured.length > 0) {
		if (isAbsolute(configured)) return {
			dir: configured,
			tier: "config-absolute",
			files: filesIn(configured)
		};
		const dir = join(probe.pluginRoot, configured);
		return {
			dir,
			tier: "config-relative",
			files: filesIn(dir)
		};
	}
	if (probe.isWritable(probe.pluginRoot) && !probe.isInsideNodeModules(probe.pluginRoot)) {
		const dir = join(probe.pluginRoot, KB_DIR_NAME);
		return {
			dir,
			tier: "plugin-root",
			files: filesIn(dir)
		};
	}
	const dir = join(probe.dshHome, KB_FALLBACK_DIR_NAME);
	return {
		dir,
		tier: "dsh-home",
		files: filesIn(dir)
	};
}
/**
* The one startup line: directory plus tier, so "which tier did the global
* install take" is never a guess.
*
* @param resolved - the resolved knowledge base.
* @returns a single line, without a trailing newline.
*/
function formatKbLog(resolved) {
	return `[errkb] kb dir: ${resolved.dir} (tier: ${resolved.tier})`;
}
/**
* The only function here that reads the machine: package root, harness home,
* and the two probes.
*
* @returns a probe bound to this process.
*/
function defaultProbe() {
	return {
		pluginRoot: pluginRootFrom(import.meta.url),
		dshHome: resolveDshHome(),
		isWritable: isWritableDir,
		isInsideNodeModules: (dir) => isInsideNodeModulesPath(dir)
	};
}
//#endregion
//#region src/signature.ts
/** The placeholders normalization writes, one per class of run-to-run noise. */
const PLACEHOLDER = {
	ts: "<ts>",
	pid: "<pid>",
	port: "<port>",
	pos: "<pos>",
	uuid: "<uuid>",
	hash: "<hash>",
	tmp: "<tmp>",
	path: "<path>"
};
const ANSI = /\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;
const SEGMENT = String.raw`(?:<[a-z-]+>|[^\s'"\x60<>|:*?\\/()\[\]{},;])+`;
new RegExp([
	String.raw`(?<![\w\\])(?:\\\\\?\\)?[A-Za-z]:[\\/]${SEGMENT}(?:[\\/]${SEGMENT})*[\\/]?`,
	String.raw`(?<![\w\\])\\\\${SEGMENT}(?:\\${SEGMENT})+\\?`,
	String.raw`(?<![\w.:/\\<>~-])~[\\/]${SEGMENT}(?:[\\/]${SEGMENT})*[\\/]?`,
	String.raw`(?<![\w.:/\\<>~-])\/${SEGMENT}(?:\/${SEGMENT})+\/?`
].join("|"), "g");
PLACEHOLDER.uuid, PLACEHOLDER.hash, PLACEHOLDER.ts, PLACEHOLDER.ts, PLACEHOLDER.ts, PLACEHOLDER.pid, PLACEHOLDER.port, `${PLACEHOLDER.port}`, PLACEHOLDER.pos, PLACEHOLDER.pos, `${PLACEHOLDER.pos}`, `${PLACEHOLDER.pos}`, `${PLACEHOLDER.pos}`, `${PLACEHOLDER.tmp}`, PLACEHOLDER.tmp;
const DEFAULT_CAPTURE_OPTIONS = {
	capture: [
		"tool",
		"command",
		"llm",
		"agent"
	],
	captureExitCodes: true,
	transientThreshold: 5
};
/** Run a getter-like function; a throw yields `undefined`. */
function attempt(read) {
	try {
		return read();
	} catch {
		return;
	}
}
/** A string or finite number as text; anything else is ignored. */
function scalar(value) {
	if (typeof value === "string") return value === "" ? void 0 : value;
	if (typeof value === "number" && Number.isFinite(value)) return String(value);
}
/**
* Text for any value: JSON for plain data, `String()` otherwise, and a fixed
* placeholder when both throw (circular objects, hostile `toString`).
*/
function text$1(value) {
	if (typeof value === "function") return "[function]";
	if (typeof value === "object" && value !== null) {
		const json = attempt(() => JSON.stringify(value));
		if (typeof json === "string") return json;
	}
	return attempt(() => String(value)) ?? "[unprintable value]";
}
/**
* Read a thrown value safely (§6, `agent/error`): an Error gives its message,
* name and code; a string is the message; an object with a string `message`
* reads like an Error; anything else is stringified. Never throws - not on
* circular objects, throwing getters or a Proxy whose every trap throws.
*
* @param error - whatever was thrown.
* @returns the message, plus a name and code when there are any.
*/
function safeErrorText(error) {
	if (typeof error === "string") return { message: error };
	if (typeof error !== "object" || error === null) return { message: text$1(error) };
	const message = attempt(() => error.message);
	const name = scalar(attempt(() => error.name));
	const code = scalar(attempt(() => error.code));
	const extras = {
		...name === void 0 ? {} : { name },
		...code === void 0 ? {} : { code }
	};
	if (typeof message === "string") return {
		message,
		...extras
	};
	if (message !== void 0) return {
		message: text$1(message),
		...extras
	};
	return {
		message: text$1(error),
		...extras
	};
}
/**
* A line that names its error: a Node/pnpm code (`ERR_PNPM_…`), an errno
* (`EPERM`), an exception class (`ModuleNotFoundError`) or a TypeScript error
* (`error TS2307`). ERR_ codes may carry digits (`ERR_PNPM_FETCH_404`).
*/
const CODE = /\b(?:ERR_[A-Z0-9_]+|E[A-Z]{2,}|[A-Z]\w*Error|error TS\d+)\b/g;
const NOT_A_CODE = /* @__PURE__ */ new Set([
	"ERR",
	"ERROR",
	"ERRORS",
	"EXIT"
]);
const TRACEBACK = "Traceback (most recent call last):";
/** The first real code in a line, as stored: `error TS2307` → `TS2307`. */
function codeIn(line) {
	for (const [found] of line.matchAll(CODE)) {
		if (NOT_A_CODE.has(found)) continue;
		return found.startsWith("error ") ? found.slice(6) : found;
	}
}
/** Cut a line to `max` characters, the last one an ellipsis. */
function cap(line, max = 200) {
	const chars = Array.from(line);
	if (chars.length <= max) return line;
	return `${chars.slice(0, max - 1).join("").trimEnd()}…`;
}
/**
* Pick the one line of a (possibly multi-line) error text that identifies it
* (docs/implementation-plan.md EKB-012):
*
* 1. a Python traceback gives its last non-empty line, the exception itself;
* 2. otherwise the first line naming a code (see {@link CODE});
* 3. otherwise the last non-empty line.
*
* The traceback rule runs first because a traceback quotes source lines, and a
* quoted `raise ValueError(...)` would otherwise win rule 2. ANSI escapes are
* stripped, lines are trimmed and the result is capped at
* {@link HEADLINE_MAX_CHARS}.
*
* @param raw - the full text.
* @returns the headline, empty for blank text, and its code if it names one.
*/
function extractHeadline(raw) {
	const lines = raw.replace(ANSI, "").split(/\r?\n|\r/).map((line) => line.trim()).filter((line) => line !== "");
	const last = lines.at(-1) ?? "";
	const pick = (line) => {
		const code = codeIn(line);
		return {
			line: cap(line),
			...code === void 0 ? {} : { code }
		};
	};
	if (raw.includes(TRACEBACK)) return pick(last);
	return pick(lines.find((line) => codeIn(line) !== void 0) ?? last);
}
const EXIT_MARKER = /\[exit code: (\d+)\]/g;
/**
* The exit code a tool result reports, from its last `[exit code: N]` marker.
*
* @param text - the tool result text.
* @returns N, or `undefined` when there is no marker.
*/
function exitCode$1(text) {
	const last = Array.from(text.matchAll(EXIT_MARKER)).at(-1);
	return last === void 0 ? void 0 : Number(last[1]);
}
/**
* The `transientThreshold` actually applied: a whole number of at least 1.
* index.ts reports a setting this changes; this stays the one rule for it.
*/
function effectiveTransientThreshold(value) {
	if (!Number.isFinite(value)) return DEFAULT_CAPTURE_OPTIONS.transientThreshold;
	return Math.max(1, Math.ceil(value));
}
/** Values of the `captureFix` setting. */
const CAPTURE_FIX_MODES = ["prompt-once", "off"];
/**
* The one-shot prompt for an entry that looks resolved and has no fix. It
* names `err_record` (T15), the one tool that writes a fix.
*
* @param id - the entry.
*/
function askFixText(id) {
	return `[errkb] ${id} looks resolved. Record the fix with err_record in one sentence so it can be reused.`;
}
const toolKey = (toolName) => `tool:${toolName}`;
const commandKey = (command) => `command:${command.replace(/\s+/g, " ").trim()}`;
/**
* Classify one tool call for resolution. A tool failure is keyed by the tool,
* a non-zero exit by its command (by the tool when the call names none), and
* anything else is a success for both its tool and its command.
*
* @param call - the tool name, command and result.
*/
function callOutcome(call) {
	const command = call.command === void 0 || call.command.trim() === "" ? void 0 : call.command;
	if (call.isError) return {
		ok: false,
		key: toolKey(call.toolName)
	};
	const code = exitCode$1(call.text);
	if (code !== void 0 && code !== 0) return {
		ok: false,
		key: command === void 0 ? toolKey(call.toolName) : commandKey(command)
	};
	return {
		ok: true,
		keys: [toolKey(call.toolName), ...command === void 0 ? [] : [commandKey(command)]]
	};
}
/**
* One session's resolution state: the watched entries, the turn count, and
* the entries already asked for a fix. Call beginTurn() at every new turn.
* Every method that takes a turn defaults to the current one; the plugin
* passes the turn an event happened in, since it acts on it a little later.
*/
var ResolutionTracker = class {
	current = 0;
	watches = /* @__PURE__ */ new Map();
	asked = /* @__PURE__ */ new Set();
	/** The turns begun so far; 0 before the first. */
	get turn() {
		return this.current;
	}
	/** A new turn: watches older than the window lapse. */
	beginTurn() {
		this.current++;
		for (const [id, watch] of this.watches) if (!this.open(watch.turn)) this.watches.delete(id);
	}
	/** Whether something from `turn` is still inside the window. */
	open(turn) {
		return this.current - turn <= 1;
	}
	/**
	* A call failing under `key` was recorded against `id`: watch it, from
	* `turn`. A watch already on `id` is replaced - a recurrence.
	*/
	occurred(id, key, turn = this.current) {
		this.watches.delete(id);
		this.watches.set(id, {
			key,
			turn
		});
		if (this.watches.size > 32) this.watches.delete(this.watches.keys().next().value);
	}
	/**
	* A call succeeded under `keys` in `turn`.
	*
	* @returns the entries it resolves, in the order they were recorded; each
	*   stops being watched.
	*/
	succeeded(keys, turn = this.current) {
		const resolved = [];
		for (const [id, watch] of this.watches) {
			if (!keys.includes(watch.key)) continue;
			const after = turn - watch.turn;
			if (after < 0 || after > 1) continue;
			this.watches.delete(id);
			resolved.push(id);
		}
		return resolved;
	}
	/** The entries being watched, for tests and `err_stats`. */
	watched() {
		return [...this.watches.keys()];
	}
	/**
	* Take the one fix prompt `id` gets in this session.
	*
	* @returns true the first time for `id`, false ever after.
	*/
	ask(id) {
		if (this.asked.has(id)) return false;
		this.asked.add(id);
		return true;
	}
};
//#endregion
//#region src/inject.ts
/** The plugin name every notice source carries; matches `name` in index.ts. */
const PLUGIN_NAME = "err-kb";
/** Values of the `inject` setting. */
const INJECT_MODES = [
	"hit-only",
	"always",
	"off"
];
/** The closing sentences, exactly as the model reads them. */
const WORDING = {
	hit: "Known fix: try this first, before re-diagnosing or researching.",
	approximate: "Approximate match, verify first.",
	doubted: "This fix failed here last time; verify before applying."
};
const NON_ASCII = /[^ -~\t\n\r]/gu;
/**
* A conservative token estimate that needs no tokenizer: every non-ASCII code
* point counts as one token, ASCII as one per {@link ASCII_CHARS_PER_TOKEN}
* characters, rounded up. It overestimates English by about a quarter and
* Chinese by a little more, so a notice inside the cap is inside it for real.
*
* @param text - any text.
* @returns the estimated token count.
*/
function estimateTokens(text) {
	const wide = text.match(NON_ASCII)?.length ?? 0;
	const ascii = text.length - wide - surrogateUnits(text);
	return Math.ceil(ascii / 3) + wide;
}
function surrogateUnits(text) {
	return text.length - Array.from(text).length;
}
/**
* Whether a body is inside both caps.
*
* @param text - a notice body.
*/
function withinCaps(text) {
	return Array.from(text).length <= 400 && estimateTokens(text) <= 120;
}
/** Collapse runs of whitespace, newlines included, to one space. */
function oneLine(text) {
	return text.replace(/\s+/g, " ").trim();
}
/**
* Clip text to at most `max` code points, the last of them an ellipsis when
* anything was cut.
*
* @param text - the text.
* @param max - the limit in code points; 0 or less gives the empty string.
*/
function clip(text, max) {
	const chars = Array.from(text);
	if (chars.length <= max) return text;
	if (max <= 0) return "";
	return `${chars.slice(0, max - 1).join("").trimEnd()}…`;
}
function largest(low, high, ok) {
	if (!ok(low)) return void 0;
	while (low < high) {
		const mid = Math.ceil((low + high) / 2);
		if (ok(mid)) low = mid;
		else high = mid - 1;
	}
	return low;
}
/**
* Fit a cause and a fix into a template within both caps. The cause is first
* clipped to {@link CAUSE_MAX_CHARS}; if the result is still too long the cause
* shrinks towards {@link CAUSE_MIN_CHARS}, and only then does the fix shrink.
* Whatever the template adds stays whole. A template too long on its own (an
* absurd ID) is clipped as a last resort.
*
* @param render - builds the body from a clipped cause and fix.
* @param cause - the cause, one line.
* @param fix - the fix, one line.
* @returns a body inside both caps.
*/
function fitNotice(render, cause, fix) {
	const causeChars = Math.min(Array.from(cause).length, 100);
	const fixChars = Array.from(fix).length;
	const fits = (c, f) => withinCaps(render(clip(cause, c), clip(fix, f)));
	if (fits(causeChars, fixChars)) return render(clip(cause, causeChars), fix);
	const floor = Math.min(causeChars, 40);
	const c = largest(floor, causeChars, (n) => fits(n, fixChars));
	if (c !== void 0) return render(clip(cause, c), fix);
	const f = largest(0, fixChars, (n) => fits(floor, n));
	if (f !== void 0) return render(clip(cause, floor), clip(fix, f));
	return hardClip(render(clip(cause, floor), ""));
}
function hardClip(text) {
	return clip(text, largest(0, Array.from(text).length, (m) => withinCaps(clip(text, m))));
}
const hitsText = (hits) => `${hits} ${hits === 1 ? "hit" : "hits"}`;
/**
* The body of a notice. Pure; caps are applied here, counts are not.
*
* @param event - the hit or miss.
* @param trust - the fix's trust level; ignored for a miss or an entry
*   without a fix.
* @returns the kind and the body.
*/
function noticeText(event, trust = "trusted") {
	if (event.kind === "miss") return {
		kind: "miss",
		text: hardClip(`[errkb] recorded as ${event.id} (no fix yet).`)
	};
	const { hit } = event;
	const { id, entry } = hit;
	const fix = oneLine(entry.fix);
	const near = hit.approximate ? ", approximate match" : "";
	if (fix === "") return {
		kind: "no-fix",
		text: hardClip(`[errkb] ${id} seen before (${hitsText(entry.hits)}${near}), no fix recorded yet.`)
	};
	const doubted = trust !== "trusted";
	const closing = [...hit.approximate ? [WORDING.approximate] : [], ...doubted ? [WORDING.doubted] : []];
	if (closing.length === 0) closing.push(WORDING.hit);
	const kind = doubted ? "doubted" : hit.approximate ? "near" : "hit";
	const cause = oneLine(entry.trigger);
	const head = `[errkb] ${id} known (${hitsText(entry.hits)})`;
	const tail = closing.join(" ");
	const render = (c, f) => `${head}${c === "" ? "" : ` | cause: ${c}`} | fix: ${f} ${tail}`;
	return {
		kind,
		text: fitNotice(render, cause, fix)
	};
}
/**
* The message source for a notice body: `{kind: 'plugin', plugin: 'err-kb',
* form: 'notice', summary}`, the summary bounded by dsh-llm's
* `boundContextSummary`.
*
* @param text - the notice body.
*/
function noticeSource(text) {
	return {
		kind: "plugin",
		plugin: PLUGIN_NAME,
		form: "notice",
		summary: boundContextSummary(text)
	};
}
const DEFAULT_CAP_LIMITS = {
	perStep: 1,
	perTurn: 3,
	perIdPerSession: 2,
	fixedPerSession: 1
};
/**
* Counts one session's notices. Create one per session; call beginTurn() and
* beginStep() at those boundaries, and tryEmit() before each notice.
*/
var CapTracker = class {
	limits;
	step = 0;
	turn = 0;
	perId = /* @__PURE__ */ new Map();
	/** @param limits - lower limits; a value above the default is ignored. */
	constructor(limits = {}) {
		const l = { ...DEFAULT_CAP_LIMITS };
		for (const key of Object.keys(l)) l[key] = Math.min(l[key], limits[key] ?? l[key]);
		this.limits = l;
	}
	/** A new turn: the turn and step budgets start again. */
	beginTurn() {
		this.turn = 0;
		this.step = 0;
	}
	/** A new step within the turn: the step budget starts again. */
	beginStep() {
		this.step = 0;
	}
	/**
	* Take one notice from every budget, if every budget has one left.
	*
	* @param id - the entry the notice is about.
	* @param fixed - whether the entry's status is `fixed`.
	* @returns whether the notice may be emitted; nothing is taken when not.
	*/
	tryEmit(id, fixed = false) {
		const used = this.perId.get(id) ?? 0;
		const perId = fixed ? this.limits.fixedPerSession : this.limits.perIdPerSession;
		if (this.step >= this.limits.perStep || this.turn >= this.limits.perTurn || used >= perId) return false;
		this.step++;
		this.turn++;
		this.perId.set(id, used + 1);
		return true;
	}
};
/**
* A TrustStore in memory. It saves a deep copy, so later changes to the saved
* object do not leak in.
*
* @param initial - the state to start from.
*/
function memoryTrustStore(initial = { entries: {} }) {
	let state = structuredClone(initial);
	return {
		load: () => structuredClone(state),
		save: (next) => {
			state = structuredClone(next);
		}
	};
}
/**
* Hash a fix text, so an edited fix starts trusted again.
*
* @param fix - the entry's fix field.
*/
function fixSig(fix) {
	return createHash("sha256").update(oneLine(fix)).digest("hex").slice(0, 12);
}
/**
* The trust level a record gives.
*
* @param record - the record, if there is one.
*/
function trustLevel(record) {
	if (record === void 0) return "trusted";
	if (record.recurredAfterInject >= 2 && record.succeeded === 0) return "suppressed";
	return record.recurredAfterInject >= 1 ? "doubted" : "trusted";
}
/**
* Per-machine fix trust. Call beginTurn() at each turn, seen() whenever a
* captured error matches an entry, and injected() when a notice carried that
* entry's fix.
*/
var FixTrust = class {
	store;
	state;
	injectedThisTurn = /* @__PURE__ */ new Set();
	/** Settles once the store's asynchronous state, if any, has joined in. */
	ready;
	constructor(store = memoryTrustStore()) {
		this.store = store;
		this.state = store.load();
		this.ready = (store.loaded ?? Promise.resolve(void 0)).then((saved) => {
			for (const [id, record] of Object.entries(saved?.entries ?? {})) this.state.entries[id] ??= record;
		});
	}
	/** The record for an entry, or undefined when none applies to this fix. */
	record(id, fix) {
		const record = this.state.entries[id];
		return record?.fixSig === fixSig(fix) ? record : void 0;
	}
	/** The trust level of an entry's current fix. */
	level(id, fix) {
		return trustLevel(this.record(id, fix));
	}
	/**
	* A new turn in `scope`: recurrence is counted within one turn.
	*
	* @param scope - the session whose turn began; the default scope serves a
	*   caller with one session.
	*/
	beginTurn(scope = "") {
		const prefix = `${scope}\0`;
		for (const key of this.injectedThisTurn) if (key.startsWith(prefix)) this.injectedThisTurn.delete(key);
	}
	/**
	* A captured error matched `id`. If its fix was injected earlier in this
	* turn of the same scope, the fix did not hold: count a recurrence.
	*/
	seen(id, fix, scope = "") {
		const key = `${scope}\0${id}`;
		const record = this.record(id, fix);
		if (record === void 0 || !this.injectedThisTurn.has(key)) return;
		record.recurredAfterInject++;
		this.injectedThisTurn.delete(key);
		this.store.save(this.state);
	}
	/** A notice carried `id`'s fix, in `scope`'s current turn. */
	injected(id, fix, scope = "") {
		const record = this.record(id, fix) ?? {
			injected: 0,
			recurredAfterInject: 0,
			succeeded: 0,
			fixSig: fixSig(fix)
		};
		record.injected++;
		this.state.entries[id] = record;
		this.injectedThisTurn.add(`${scope}\0${id}`);
		this.store.save(this.state);
	}
	/** The fix for `id` was confirmed to work (resolution detection, T14). */
	succeeded(id, fix) {
		const record = this.record(id, fix);
		if (record === void 0) return;
		record.succeeded++;
		this.store.save(this.state);
	}
	/** A copy of the whole state, for persistence or `err_stats`. */
	snapshot() {
		return structuredClone(this.state);
	}
};
/**
* Decides, per session, which captured errors become notices: the `inject`
* setting, non-injectable entries, fix trust and the caps, in that order.
*/
var Injector = class {
	mode;
	caps;
	trust;
	scope;
	constructor(deps = {}) {
		this.mode = deps.mode ?? "hit-only";
		this.caps = deps.caps ?? new CapTracker();
		this.trust = deps.trust ?? new FixTrust();
		this.scope = deps.scope ?? "";
	}
	beginTurn() {
		this.caps.beginTurn();
		this.trust.beginTurn(this.scope);
	}
	beginStep() {
		this.caps.beginStep();
	}
	/**
	* Tell fix trust that a captured error matched `hit`, without offering a
	* notice yet. For a notice that rides a later step (T13: a dead turn's
	* error): the capture is observed when it happens, the notice offered when
	* the next step opens, with `observed` set.
	*/
	observe(hit) {
		this.trust.seen(hit.id, hit.entry.fix, this.scope);
	}
	/**
	* Offer a captured error.
	*
	* @param event - the hit or the miss.
	* @param observed - observe() already saw this hit; do not count it again.
	* @returns the notice to inject, or undefined to stay silent.
	*/
	offer(event, observed = false) {
		if (this.mode === "off") return void 0;
		if (event.kind === "miss") {
			if (this.mode !== "always" || !this.caps.tryEmit(event.id)) return void 0;
			return notice(event.id, noticeText(event));
		}
		const { id, entry, injectable } = event.hit;
		if (!observed) this.observe(event.hit);
		if (!injectable) return void 0;
		const carriesFix = oneLine(entry.fix) !== "";
		const level = carriesFix ? this.trust.level(id, entry.fix) : "trusted";
		if (level === "suppressed") return void 0;
		if (!this.caps.tryEmit(id, entry.status === "fixed")) return void 0;
		if (carriesFix) this.trust.injected(id, entry.fix, this.scope);
		return notice(id, noticeText(event, level));
	}
	/**
	* Offer the one-shot fix prompt for `id`, which looks resolved (T14). It
	* spends the step and turn budgets like any notice. Its per-ID budget is
	* its own, not the entry's: the prompt is a different message, asked once
	* by construction, and an entry whose "no fix recorded yet" notices used up
	* its two would otherwise never be asked.
	*
	* @returns the notice, or undefined when `inject` is off or a cap refuses.
	*/
	ask(id) {
		if (this.mode === "off" || !this.caps.tryEmit(`${id}\0ask`)) return void 0;
		return notice(id, {
			kind: "ask-fix",
			text: hardClip(askFixText(id))
		});
	}
};
function notice(id, { kind, text }) {
	return {
		id,
		kind,
		text,
		source: noticeSource(text)
	};
}
/** Values of the `sessionDigest` setting. */
const SESSION_DIGEST_MODES = [
	"off",
	"counts",
	"index"
];
/**
* The session-opening digest (§7), or undefined when there is nothing to say:
* the mode is `off`, or the knowledge base has no entries.
*
* `counts` is one line of about 20 tokens:
* `[errkb] 37 known errors; known fixes are shown when an error repeats.`
* `index` adds the most-hit injectable entries, at most
* {@link DIGEST_MAX_TITLES}, one per line, ties broken by document order.
*
* @param entries - the knowledge base's entries, in document order.
* @param mode - the `sessionDigest` setting.
*/
function sessionDigestText(entries, mode) {
	if (mode === "off" || entries.length === 0) return void 0;
	const n = entries.length;
	const head = `[errkb] ${n} known ${n === 1 ? "error" : "errors"}; known fixes are shown when an error repeats.`;
	if (mode === "counts") return head;
	const top = entries.map((entry, order) => ({
		entry,
		order
	})).filter(({ entry }) => entry.injectable).sort((a, b) => b.entry.hits - a.entry.hits || a.order - b.order).slice(0, 10).map(({ entry }) => `${entry.id} ${clip(oneLine(entry.title), 80)} (${hitsText(entry.hits)})`);
	return top.length === 0 ? head : [`${head} Most frequent:`, ...top].join("\n");
}
/** The system-prompt section's name and position (§7). */
const SYSTEM_PROMPT_SECTION = {
	name: "plugin:errkb",
	order: 10400
};
/**
* The standing guidance, about 50 tokens (§7). Its last sentence names
* `err_record` (T15), the only tool that writes a fix. No `{{variable}}`
* references: the text is rendered as is.
*/
const SYSTEM_PROMPT_HINT = "Errors are tracked by the errkb plugin. A context line starting with [errkb] names a known error and, when one is recorded, its fix: try that fix before re-diagnosing. When you resolve an error that has no recorded fix, record a working fix with err_record.";
//#endregion
//#region src/store.ts
/** Entry status values (§8). */
const ENTRY_STATUSES = [
	"open",
	"fixed",
	"wontfix"
];
/** The human-readable field labels, in both languages (§8, §17 Q2). */
const LABELS = {
	en: {
		fingerprint: "Fingerprint",
		category: "Category",
		firstSeen: "First seen",
		lastSeen: "Last seen",
		hits: "Hits",
		trigger: "Trigger",
		raw: "Raw message",
		fix: "Fix",
		status: "Status",
		notes: "Notes"
	},
	zh: {
		fingerprint: "指纹",
		category: "分类",
		firstSeen: "首次",
		lastSeen: "最近",
		hits: "命中",
		trigger: "触发",
		raw: "原始信息",
		fix: "解法",
		status: "状态",
		notes: "备注"
	}
};
const FIELD_BY_LABEL = /* @__PURE__ */ new Map();
for (const set of Object.values(LABELS)) for (const key of [
	"fingerprint",
	"category",
	"firstSeen",
	"trigger",
	"raw",
	"fix",
	"status",
	"notes"
]) FIELD_BY_LABEL.set(set[key], key);
/**
* Format an entry ID: prefix plus zero-padded number.
*
* @param n - the ID number, from 1.
* @param idPrefix - the `idPrefix` setting.
* @param idWidth - the `idWidth` setting.
* @returns e.g. `E-0007`.
*/
function formatId(n, idPrefix = "E-", idWidth = 4) {
	return `${idPrefix}${String(n).padStart(idWidth, "0")}`;
}
const DEFAULT_STORE_OPTIONS = {
	idPrefix: "E-",
	idWidth: 4,
	maxEntries: 200,
	labels: "en",
	share: "public",
	maxSampleChars: 500,
	lockStaleMs: 1e4,
	lockTimeoutMs: 1e4,
	lockRetryMs: 10
};
/** The lock could not be taken in time. */
var LockTimeoutError = class extends Error {
	path;
	constructor(path) {
		super(`timed out waiting for the lock ${path}`);
		this.path = path;
		this.name = "LockTimeoutError";
	}
};
/**
* Read `.lock`, treating a Windows handle that is being deleted as "no lock".
* Another process removing the file can make it refuse to open with EPERM or
* EBUSY until its handle closes; the lock loop already retries contention, so
* that race must not become a failure. Real permission problems still surface
* from the reads and writes that follow.
*/
async function readLock(fs, lock) {
	try {
		return await fs.readFile(lock);
	} catch (error) {
		const code = errorCode(error);
		if (process.platform === "win32" && (code === "EPERM" || code === "EBUSY")) return;
		throw error;
	}
}
/**
* Run `fn` holding the knowledge base's `.lock` (§13): created with `wx`, one
* older than `lockStaleMs` is taken over, and waiting gives up after
* `lockTimeoutMs` with a LockTimeoutError. The store and state.json take the
* same lock, so one process never writes either while another is mid-write.
*
* @param lock - the `.lock` path; its directory is created first.
*/
async function withFileLock(lock, o, fs, clock, fn) {
	async function removeIfUnchanged(held) {
		if (await readLock(fs, lock) === held) await fs.remove(lock);
	}
	await fs.mkdir(dirname(lock));
	const token = `${process.pid}-${randomBytes(6).toString("hex")}`;
	const deadline = clock.now().getTime() + o.lockTimeoutMs;
	for (;;) {
		if (await fs.createExclusive(lock, token)) break;
		const held = await readLock(fs, lock);
		const mtime = held === void 0 ? void 0 : await fs.mtimeMs(lock);
		const now = clock.now().getTime();
		if (mtime !== void 0 && now - mtime > o.lockStaleMs) {
			await removeIfUnchanged(held);
			continue;
		}
		if (now >= deadline) throw new LockTimeoutError(lock);
		await clock.sleep(o.lockRetryMs * (.5 + clock.random()));
	}
	try {
		return await fn();
	} finally {
		if (await readLock(fs, lock) === token) await fs.remove(lock);
	}
}
/** Write `data` to a temp file beside `path`, then rename it over `path`. */
async function writeFileAtomic(fs, path, data) {
	const temp = `${path}.${randomBytes(6).toString("hex")}.tmp`;
	await fs.writeFile(temp, data);
	try {
		await fs.rename(temp, path);
	} catch (error) {
		await fs.remove(temp);
		throw error;
	}
}
const errorCode = (error) => error.code;
/**
* The real filesystem. Every "missing" case (ENOENT) is a value, not an error;
* anything else - a directory where a file should be, a permission denial -
* propagates.
*/
function nodeStoreFs() {
	return {
		async readFile(path) {
			try {
				return await fsp.readFile(path, "utf8");
			} catch (error) {
				if (errorCode(error) === "ENOENT") return void 0;
				throw error;
			}
		},
		writeFile: (path, data) => fsp.writeFile(path, data, "utf8"),
		appendFile: (path, data) => fsp.appendFile(path, data, "utf8"),
		rename: (from, to) => fsp.rename(from, to),
		async createExclusive(path, data) {
			try {
				await fsp.writeFile(path, data, {
					encoding: "utf8",
					flag: "wx"
				});
				return true;
			} catch (error) {
				const code = errorCode(error);
				if (code === "EEXIST") return false;
				if (code === "EPERM" || code === "EBUSY") return false;
				throw error;
			}
		},
		async mtimeMs(path) {
			try {
				return (await fsp.stat(path)).mtimeMs;
			} catch (error) {
				if (errorCode(error) === "ENOENT") {
					const parent = await fsp.stat(dirname(path)).catch(() => void 0);
					if (parent !== void 0 && !parent.isDirectory()) throw error;
					return;
				}
				throw error;
			}
		},
		remove: (path) => fsp.rm(path, { force: true }),
		async list(dir) {
			try {
				return await fsp.readdir(dir);
			} catch (error) {
				if (errorCode(error) === "ENOENT") return [];
				throw error;
			}
		},
		mkdir: async (dir) => {
			await fsp.mkdir(dir, { recursive: true });
		}
	};
}
/** The real clock. */
function systemClock() {
	return {
		now: () => /* @__PURE__ */ new Date(),
		sleep: (ms) => new Promise((done) => setTimeout(done, ms)),
		random: Math.random
	};
}
/** Default caps: a few days of ordinary use fits comfortably. */
const DEFAULT_QUEUE_LIMITS = {
	maxEvents: 1e3,
	maxBytes: 32 << 20
};
/** Byte size used for the cap: the serialized request plus its bookkeeping. */
function queuedBytes(queued) {
	return Buffer.byteLength(JSON.stringify(queued), "utf8");
}
/** Read the document, migrating a version 1 (or unreadable) file to empty. */
function parseQueueDocument(text) {
	const empty = {
		version: 2,
		dropped: 0,
		queue: []
	};
	if (text === void 0) return empty;
	let parsed;
	try {
		parsed = JSON.parse(text);
	} catch {
		return empty;
	}
	if (typeof parsed !== "object" || parsed === null) return empty;
	const doc = parsed;
	if (doc.version !== 2) return empty;
	const queue = Array.isArray(doc.queue) ? doc.queue.filter((item) => typeof item === "object" && item !== null && typeof item.observationId === "string" && typeof item.request === "object" && item.request !== null) : [];
	return {
		version: 2,
		dropped: typeof doc.dropped === "number" && doc.dropped >= 0 ? doc.dropped : 0,
		queue
	};
}
/** Build the connector-local observation queue. */
function createObservationQueue(deps) {
	const { files } = deps;
	const limits = {
		...DEFAULT_QUEUE_LIMITS,
		...deps.options
	};
	const fs = deps.fs ?? nodeStoreFs();
	const clock = deps.clock ?? systemClock();
	const o = DEFAULT_STORE_OPTIONS;
	const dir = dirname(files.state);
	const statsOf = (doc) => ({
		pending: doc.queue.length,
		bytes: doc.queue.reduce((total, item) => total + queuedBytes(item), 0),
		dropped: doc.dropped
	});
	async function read() {
		return parseQueueDocument(await fs.readFile(files.state));
	}
	async function write(doc) {
		await writeFileAtomic(fs, files.state, `${JSON.stringify(doc, void 0, 2)}\n`);
	}
	/** Drop the oldest items until both caps hold; the newest is always kept. */
	function trim(doc) {
		while (doc.queue.length > 1 && (doc.queue.length > limits.maxEvents || statsOf(doc).bytes > limits.maxBytes)) {
			doc.queue.shift();
			doc.dropped += 1;
		}
		return doc;
	}
	return {
		enqueue(request, at = clock.now()) {
			if (request.observation_id === "") return Promise.reject(/* @__PURE__ */ new Error("observation_id: required for the offline queue"));
			return withFileLock(files.lock, o, fs, clock, async () => {
				const existing = await read();
				if (existing.queue.some((item) => item.observationId === request.observation_id)) return statsOf(existing);
				const doc = trim({
					...existing,
					queue: [...existing.queue, {
						observationId: request.observation_id,
						queuedAt: at.toISOString(),
						request
					}]
				});
				await write(doc);
				return statsOf(doc);
			});
		},
		list() {
			return withFileLock(files.lock, o, fs, clock, async () => {
				const doc = await read();
				await fs.mkdir(dir);
				return doc.queue;
			});
		},
		remove(observationId) {
			return withFileLock(files.lock, o, fs, clock, async () => {
				const doc = await read();
				const kept = doc.queue.filter((item) => item.observationId !== observationId);
				if (kept.length === doc.queue.length) return;
				await write({
					...doc,
					queue: kept
				});
			});
		},
		stats() {
			return withFileLock(files.lock, o, fs, clock, async () => statsOf(await read()));
		}
	};
}
//#endregion
//#region src/connector.ts
/** The connector SDK version on every request (spec/connector.cue). */
const CONNECTOR_SDK_VERSION = "v1";
/** The default local pipe name (EKB-029). */
const DEFAULT_PIPE = String.raw`\\.\pipe\errkb`;
/** How long one pipe call may wait for its answer. */
const DEFAULT_PIPE_TIMEOUT_MS = 1e4;
/** The hook/plugin capture channel (§3.1): a structured host event. */
const CONNECTOR_CHANNEL = "A";
/** A core failure. `unavailable` (or a retryable error) while the daemon is down. */
var CoreError = class extends Error {
	/** The closed error code: invalid_request/not_found/unavailable/conflict/internal. */
	code;
	/** Whether replaying the same request may succeed later. */
	retryable;
	/** @param code - the closed core error code. */
	constructor(code, message, retryable = false) {
		super(message);
		this.name = "CoreError";
		this.code = code;
		this.retryable = retryable;
	}
	/** True when the daemon is down (queue the observation instead of failing). */
	get unavailable() {
		return this.code === "unavailable" || this.retryable;
	}
};
/**
* A transport that dials the daemon for every call. The daemon may restart at
* any time, so the connector never holds a long-lived connection; the pipe is
* cheap on Windows and this keeps "daemon came back" handling trivial.
*/
function createPipeTransport(options = {}) {
	const pipe = options.pipe ?? DEFAULT_PIPE;
	const timeoutMs = options.timeoutMs ?? 1e4;
	const connect$1 = options.connect ?? connect;
	const nextId = options.requestId ?? (() => randomUUID());
	return { call(operation, payload) {
		const requestId = nextId();
		const line = `${JSON.stringify({
			schema_version: "v1",
			request_id: requestId,
			operation,
			payload
		})}\n`;
		return new Promise((resolve, reject) => {
			const socket = connect$1(pipe);
			let settled = false;
			let buffer = "";
			const finish = (action) => {
				if (settled) return;
				settled = true;
				clearTimeout(timer);
				socket.destroy();
				action();
			};
			const timer = setTimeout(() => finish(() => reject(new CoreError("unavailable", `the errkb daemon did not answer within ${timeoutMs} ms`, true))), timeoutMs);
			socket.setEncoding("utf8");
			socket.on("data", (chunk) => {
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
			socket.on("error", (error) => finish(() => reject(new CoreError("unavailable", `the errkb daemon is not reachable on ${pipe}: ${error.message}`, true))));
			socket.on("close", () => finish(() => reject(new CoreError("unavailable", "the errkb daemon closed the pipe before answering", true))));
			socket.write(line);
		});
	} };
}
/** Decode and validate one response line. */
function decodeResponse(text, requestId, operation) {
	let envelope;
	try {
		envelope = JSON.parse(text);
	} catch {
		throw new CoreError("internal", "the errkb daemon returned a response that is not JSON");
	}
	if (envelope.schema_version !== "v1") throw new CoreError("internal", `the errkb daemon answered with schema ${String(envelope.schema_version)}, want v1`);
	if (envelope.request_id !== requestId) throw new CoreError("internal", "the errkb daemon answered a different request");
	if (envelope.operation !== operation) throw new CoreError("internal", `the errkb daemon answered ${String(envelope.operation)} for ${operation}`);
	if (envelope.error !== void 0) throw new CoreError(String(envelope.error.code ?? "internal"), String(envelope.error.message ?? "the errkb daemon failed"), envelope.error.retryable === true);
	if (envelope.result === void 0) throw new CoreError("internal", "the errkb daemon answered without a result");
	return envelope.result;
}
/** A typed view over a {@link Transport}. */
function createCore(transport) {
	const call = (operation, payload) => transport.call(operation, payload);
	return {
		capture: (request) => call("capture", request),
		lookup: (payload) => call("lookup", payload),
		search: (payload) => call("search", payload),
		feedback: (payload) => call("feedback", payload),
		recordSolution: (payload) => call("record_solution", payload),
		setStatus: (payload) => call("set_status", payload),
		list: (payload) => call("list", payload),
		stats: (payload) => call("stats", payload)
	};
}
/** Build the thin connector adapter. */
function createConnector(options) {
	const core = options.core ?? createCore(createPipeTransport());
	const queue = options.queue;
	const connectorId = options.connectorId;
	const sessionId = options.sessionId;
	const projectKey = options.projectKey ?? "default";
	const channel = options.channel ?? "A";
	const newObservationId = options.newObservationId ?? (() => randomUUID());
	const now = options.now ?? (() => /* @__PURE__ */ new Date());
	const requestFor = (observation, observationId) => ({
		schema_version: "v1",
		connector_id: connectorId,
		session_id: sessionId,
		observation_id: observationId,
		channel,
		observation: {
			...observation,
			project_key: observation.project_key ?? projectKey
		}
	});
	async function send(request) {
		const answer = await core.capture(request);
		return {
			queued: false,
			created: answer.created,
			issueId: answer.issue_id,
			fingerprint: answer.fingerprint
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
					if (queue === void 0) throw new CoreError("unavailable", "the errkb daemon is down and no offline queue is configured", true);
					await queue.enqueue(request, now());
					return { queued: true };
				}
				throw error;
			}
		},
		async replay() {
			if (queue === void 0) return {
				replayed: 0,
				remaining: 0
			};
			const pending = await queue.list();
			let replayed = 0;
			for (const item of pending) {
				try {
					await send(item.request);
				} catch (error) {
					return {
						replayed,
						remaining: pending.length - replayed,
						error: error instanceof Error ? error.message : String(error)
					};
				}
				await queue.remove(item.observationId);
				replayed += 1;
			}
			return {
				replayed,
				remaining: 0
			};
		},
		pending: () => queue === void 0 ? Promise.resolve({
			pending: 0,
			bytes: 0,
			dropped: 0
		}) : queue.stats(),
		notice(candidates) {
			const best = candidates[0];
			if (best === void 0) return void 0;
			const fix = best.solution?.body ?? "";
			const approximate = best.approximate;
			if (fix.trim() === "") return {
				kind: "no-fix",
				text: oneLine(`[errkb] ${best.issue_id} seen before (${best.match_reason}), no fix recorded yet.`)
			};
			const closing = approximate ? WORDING.approximate : WORDING.hit;
			return {
				kind: approximate ? "near" : "hit",
				text: oneLine(`[errkb] ${best.issue_id} known | fix: ${fix}${approximate ? ` ${closing}` : ` ${closing}`}`)
			};
		}
	};
}
/** Extract one line of text from whatever an agent failure carries. */
function errorText$1(value) {
	if (typeof value === "string") return value;
	if (value instanceof Error) return value.message;
	if (typeof value === "object" && value !== null) {
		const message = value.message;
		if (typeof message === "string") return message;
	}
	return String(value);
}
/**
* Turn an agent failure into a raw observation. The core decides the category,
* severity, transient flag, fingerprint and the redacted texts.
*/
function observationFromAgentError(payload, options = {}) {
	return {
		observed_at: (options.now ?? (() => /* @__PURE__ */ new Date()))().toISOString(),
		source: "agent",
		message: errorText$1(payload.error),
		...payload.agent?.id === void 0 ? {} : { tool_name: payload.agent.id },
		...options.projectKey === void 0 ? {} : { project_key: options.projectKey },
		...options.runtimeVersions === void 0 ? {} : { runtime_versions: options.runtimeVersions },
		evidence: "dsh agent/error"
	};
}
/** The text of a tool result, whether it carries text or blocks. */
function toolResultText(result) {
	if (typeof result.text === "string") return result.text;
	if (typeof result.content === "string") return result.content;
	if (Array.isArray(result.content)) {
		const parts = [];
		for (const block of result.content) if (typeof block === "object" && block !== null) {
			const text = block.text;
			if (typeof text === "string") parts.push(text);
		}
		if (parts.length > 0) return parts.join("\n");
	}
	if (typeof result.output === "string") return result.output;
	return JSON.stringify(result.output ?? result.content ?? "");
}
/** The command a tool execution ran, when its input names one. */
function commandOf(exec) {
	const input = exec.input ?? {};
	for (const key of [
		"command",
		"cmd",
		"script"
	]) {
		const value = input[key];
		if (typeof value === "string" && value.trim() !== "") return value;
	}
}
/**
* Turn a failed tool result into a raw observation. A successful result
* returns undefined: the knowledge base records failures, not every call.
*/
function observationFromToolResult(exec, result, options = {}) {
	const text = toolResultText(result);
	const exit = /\[exit code: (\d+)\]/.exec(text);
	const exitCode = exit === null ? void 0 : Number(exit[1]);
	if (!(result.isError === true || exitCode !== void 0 && exitCode !== 0)) return void 0;
	const now = options.now ?? (() => /* @__PURE__ */ new Date());
	const command = commandOf(exec);
	const toolName = exec.tool?.name ?? exec.call?.name;
	const headline = extractHeadline(text);
	return {
		observed_at: now().toISOString(),
		source: "tool",
		message: text,
		...headline.code === void 0 ? {} : { error_code: headline.code },
		...command === void 0 ? {} : { command },
		...exitCode === void 0 ? {} : { exit_code: exitCode },
		...toolName === void 0 ? {} : { tool_name: toolName },
		...options.projectKey === void 0 ? {} : { project_key: options.projectKey },
		...options.runtimeVersions === void 0 ? {} : { runtime_versions: options.runtimeVersions },
		evidence: "dsh tools/result"
	};
}
/**
* Build the connector-local queue over `state.json`.
*
* @param files - `state` is the queue file, `lock` the knowledge base lock.
* @param options - the caps and injectable filesystem/clock (tests).
*/
function createConnectorQueue(files, options = {}) {
	return createObservationQueue({
		files,
		options
	});
}
const DEFAULT_RECORDER_OPTIONS = {
	projectKey: "default",
	connectorId: "dsh",
	sessionId: "dsh",
	capture: [
		"agent",
		"tool",
		"command",
		"llm"
	],
	captureExitCodes: true,
	providers: ["*"]
};
/** The text of a tool result: its text blocks, one per line. */
function resultText(content) {
	return content.filter((block) => {
		return block.type === "text" && typeof block.text === "string";
	}).map((block) => block.text).join("\n");
}
const COMMAND_KEYS = [
	"command",
	"cmd",
	"script"
];
/**
* The command a tool call ran, from its parsed arguments: a string argument
* as is, or the first of `command`, `cmd` and `script` that is a string or a
* list of strings (joined with spaces).
*
* @param args - `exec.arguments`, deep-frozen JSON.
* @returns the command, or `undefined` when the arguments name none.
*/
function commandFrom(args) {
	if (typeof args === "string") return args;
	if (typeof args !== "object" || args === null) return void 0;
	for (const key of COMMAND_KEYS) {
		const value = args[key];
		if (typeof value === "string") return value;
		if (Array.isArray(value) && value.every((v) => typeof v === "string")) return value.join(" ");
	}
}
/**
* The capture input for one tool outcome: a failure as a tool error, anything
* else as command output, whose exit code decides whether it is forwarded.
*/
function toolInput(exec, result) {
	if (result.isError) {
		const code = result.error.info?.code;
		return {
			kind: "tool",
			toolName: exec.name,
			isError: true,
			message: result.error.message,
			...code === void 0 ? {} : { code }
		};
	}
	const command = commandFrom(exec.arguments);
	return {
		kind: "command",
		toolName: exec.name,
		text: resultText(result.content),
		...command === void 0 ? {} : { command }
	};
}
/** A host agent's id, when the value exposes a readable one. */
function agentId(value) {
	try {
		const agent = value?.agent;
		return typeof agent?.id === "string" && agent.id !== "" ? agent.id : void 0;
	} catch {
		return;
	}
}
const exitCodePattern = /\[exit code: (\d+)\]/g;
/** The last `[exit code: N]` in `text`, when there is one. */
function exitCode(text) {
	let code;
	for (const match of text.matchAll(exitCodePattern)) code = Number.parseInt(match[1], 10);
	return code;
}
/** One observation for a capture input, or undefined when it is not a failure. */
function observationFor(input, captureExitCodes) {
	const at = (/* @__PURE__ */ new Date()).toISOString();
	switch (input.kind) {
		case "agent": {
			const text = safeErrorText(input.error);
			return {
				observed_at: at,
				source: "agent",
				message: text.message,
				...text.code === void 0 ? {} : { error_code: text.code }
			};
		}
		case "llm": return {
			observed_at: at,
			source: "llm",
			message: input.message,
			...input.code === void 0 ? {} : { error_code: input.code }
		};
		case "tool": return {
			observed_at: at,
			source: "tool",
			message: input.message,
			...input.toolName === void 0 ? {} : { tool_name: input.toolName },
			...input.code === void 0 ? {} : { error_code: input.code }
		};
		case "command": {
			const code = exitCode(input.text);
			if (!captureExitCodes || code === void 0 || code === 0) return void 0;
			return {
				observed_at: at,
				source: "command-exit",
				message: input.text,
				exit_code: code,
				...input.command === void 0 ? {} : { command: input.command },
				...input.toolName === void 0 ? {} : { tool_name: input.toolName }
			};
		}
	}
}
/** A synthetic entry for a candidate or a list row (the adapter's view). */
function entryView(id, title, fingerprint, hits, fix, status) {
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
		notes: ""
	};
}
function statusOf(raw) {
	return raw === "fixed" || raw === "wontfix" || raw === "misjudged" ? raw : "open";
}
/** Map a core candidate to the hit shape the injection layer understands. */
function hitFrom(candidate, projectKey) {
	const fix = candidate.solution?.body ?? "";
	const status = fix.trim() === "" ? "open" : "fixed";
	const entry = entryView(candidate.issue_id, candidate.title, candidate.fingerprint, candidate.hits ?? 0, fix, status);
	const version = candidate.solution?.solution_version_id;
	if (version !== void 0) Object.assign(entry, { solutionVersionId: version });
	const exact = !candidate.approximate && (candidate.match_reason === "exact_fingerprint" || candidate.match_reason === "issue_id");
	const via = candidate.match_reason === "error_code" ? "code" : exact ? "exact" : "fuzzy";
	const similarity = exact ? 1 : Math.max(0, Math.min(1, candidate.confidence ?? 0));
	return {
		matched: true,
		id: entry.id,
		entry,
		via,
		approximate: candidate.approximate,
		similarity,
		injectable: true
	};
}
/** Build the recorder the injection layer and the tools run on. */
function createRecorder(deps = {}) {
	const options = {
		...DEFAULT_RECORDER_OPTIONS,
		...deps.options
	};
	const projectKey = options.projectKey ?? "default";
	const connectorId = options.connectorId ?? "dsh";
	const defaultSession = options.sessionId ?? "dsh";
	const captureSources = new Set(options.capture ?? []);
	const captureExitCodes = options.captureExitCodes !== false;
	const outcomes = [];
	const stats = {
		appended: 0,
		hits: 0,
		counted: 0,
		timeouts: 0,
		failures: 0
	};
	const inFlight = /* @__PURE__ */ new Set();
	let lastLog = 0;
	const DEDUPE_WINDOW_MS = 1500;
	const recentFailures = /* @__PURE__ */ new Map();
	const providers = new Set((options.providers ?? ["*"]).map((provider) => provider.trim().toLowerCase()));
	/** Whether this provider's request failures are captured. */
	function providerAllowed(provider) {
		if (providers.size === 0 || providers.has("*")) return true;
		return providers.has(provider.trim().toLowerCase());
	}
	/** True when this observation was already forwarded a moment ago. */
	function duplicate(observation, session) {
		const firstLine = observation.message.split(/\r?\n/, 1)[0]?.trim() ?? "";
		const key = `${session ?? defaultSession}\u0000${firstLine}`;
		const now = Date.now();
		const seen = recentFailures.get(key);
		for (const [known, at] of recentFailures) if (now - at >= DEDUPE_WINDOW_MS) recentFailures.delete(known);
		if (seen !== void 0 && now - seen < DEDUPE_WINDOW_MS) return true;
		recentFailures.set(key, now);
		return false;
	}
	const connectors = /* @__PURE__ */ new Map();
	/** One connector per host session, sharing the core and the queue. */
	function connectorFor(session) {
		const key = session ?? defaultSession;
		const existing = connectors.get(key);
		if (existing !== void 0) return existing;
		const built = deps.connector?.(key) ?? (deps.createConnector !== void 0 ? deps.createConnector(key) : createConnector({
			connectorId,
			sessionId: key,
			projectKey,
			...deps.queue === void 0 ? {} : { queue: deps.queue }
		}));
		connectors.set(key, built);
		return built;
	}
	/** Count and append one outcome; never throws. */
	function record(outcome) {
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
	function fail(error) {
		stats.failures++;
		const now = Date.now();
		if (now - lastLog < 6e4) return;
		lastLog = now;
		deps.logger?.warn("errkb: %s", error instanceof Error ? error.message : String(error));
	}
	/** Forward one observation; never rejects. */
	function forward(observation, session) {
		if (observation === void 0) return Promise.resolve(record({ kind: "counted" }));
		if (duplicate(observation, session)) return Promise.resolve(record({ kind: "counted" }));
		const pending = (async () => {
			try {
				const outcome = await connectorFor(session).capture(observation);
				if (outcome.queued === true) return record({ kind: "counted" });
				if (outcome.issueId === void 0) return record({ kind: "counted" });
				if (outcome.created === true) return record({
					kind: "appended",
					id: outcome.issueId
				});
				return record({
					kind: "hit",
					id: outcome.issueId,
					hits: 1
				});
			} catch (error) {
				fail(error);
				outcomes.push({ kind: "failed" });
				return { kind: "failed" };
			}
		})();
		inFlight.add(pending);
		pending.finally(() => inFlight.delete(pending));
		return pending;
	}
	function capture(input, session) {
		if (!captureSources.has(input.kind)) return Promise.resolve(record({ kind: "timeout" }));
		return forward(observationFor(input, captureExitCodes), session);
	}
	/** Map one core response to a match; undefined when nothing matched. */
	function matchOf(response) {
		const best = response?.candidates?.[0];
		if (best === void 0) return { matched: false };
		return hitFrom(best, projectKey);
	}
	async function lookup(input) {
		try {
			const observation = observationFor(input, captureExitCodes);
			if (observation === void 0) return void 0;
			return matchOf(await connectorFor().core.lookup({
				schema_version: "v1",
				project_key: projectKey,
				normalized_message: observation.message,
				...observation.error_code === void 0 ? {} : { error_code: observation.error_code }
			}));
		} catch (error) {
			fail(error);
			return;
		}
	}
	async function lookupIssue(id) {
		try {
			const best = (await connectorFor().core.lookup({
				schema_version: "v1",
				project_key: projectKey,
				issue: id
			})).candidates?.[0];
			return best === void 0 ? void 0 : hitFrom(best, projectKey);
		} catch (error) {
			fail(error);
			return;
		}
	}
	async function lookupFingerprint(fingerprint) {
		try {
			const best = (await connectorFor().core.lookup({
				schema_version: "v1",
				project_key: projectKey,
				fingerprint
			})).candidates?.[0];
			return best === void 0 ? void 0 : hitFrom(best, projectKey);
		} catch (error) {
			fail(error);
			return;
		}
	}
	async function search(query, limit = 3) {
		try {
			return (await connectorFor().core.search({
				schema_version: "v1",
				query,
				project_key: projectKey,
				limit: Math.max(1, Math.min(limit, 3))
			})).candidates ?? [];
		} catch (error) {
			fail(error);
			return;
		}
	}
	async function entries() {
		try {
			return ((await connectorFor().core.list({
				schema_version: "v1",
				kind: "issue",
				limit: 100,
				project_key: projectKey
			})).items ?? []).map((item) => {
				const status = statusOf(item.status);
				const excludedStatus = item.status ?? "";
				const entry = entryView(item.id, item.title, "", item.hits ?? 0, item.fix ?? "", status);
				return {
					entry,
					sig: entry.fingerprint,
					category: entry.category,
					code: void 0,
					proj: projectKey,
					tokens: /* @__PURE__ */ new Set(),
					length: entry.title.length,
					excluded: excludedStatus === "wontfix" || excludedStatus === "misjudged" ? excludedStatus : void 0
				};
			});
		} catch (error) {
			fail(error);
			return;
		}
	}
	function fixed(id, fix) {
		return {
			kind: "fixed",
			entry: entryView(id, "", "", 0, fix, "fixed")
		};
	}
	async function recordFix(id, fix) {
		if (fix.trim() === "") return { kind: "timeout" };
		try {
			await connectorFor().core.recordSolution({
				schema_version: "v1",
				issue_id: id,
				body: fix,
				source: "dsh",
				actor: "dsh"
			});
			return fixed(id, fix);
		} catch (error) {
			fail(error);
			return { kind: "failed" };
		}
	}
	async function setStatus(id, status) {
		try {
			await connectorFor().core.setStatus({
				schema_version: "v1",
				issue_id: id,
				status
			});
			return {
				kind: "fixed",
				entry: entryView(id, "", "", 0, "", status)
			};
		} catch (error) {
			fail(error);
			return { kind: "failed" };
		}
	}
	async function replay() {
		try {
			await connectorFor().replay();
		} catch (error) {
			fail(error);
		}
	}
	return {
		capture,
		agentError(payload) {
			return forward({
				observed_at: (/* @__PURE__ */ new Date()).toISOString(),
				source: "agent",
				message: safeErrorText(payload.error).message
			}, agentId(payload));
		},
		requestError(payload) {
			const observation = observationFor({
				kind: "llm",
				code: payload.failure.code,
				message: payload.failure.message
			}, captureExitCodes);
			const session = agentId(payload);
			if (observation === void 0) return Promise.resolve(record({ kind: "timeout" }));
			if (!captureSources.has("llm")) return Promise.resolve(record({ kind: "timeout" }));
			if (!providerAllowed(payload.provider)) {
				duplicate(observation, session);
				return Promise.resolve(record({ kind: "timeout" }));
			}
			return forward(observation, session);
		},
		toolResult(exec, result) {
			const input = toolInput(exec, result);
			if (input.kind === "tool") return forward(observationFor(input, captureExitCodes), agentId(exec));
			const observation = observationFor(input, captureExitCodes);
			if (observation === void 0) return void 0;
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
				const version = (found?.entry)?.solutionVersionId;
				if (found === void 0 || version === void 0) return false;
				await connectorFor().core.feedback({
					schema_version: "v1",
					issue_id: issueId,
					solution_version_id: version,
					outcome
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
		projectKey
	};
}
const DEFAULT_INJECTION_OPTIONS = {
	inject: "hit-only",
	captureFix: "prompt-once",
	sessionDigest: "counts",
	systemPromptHint: true
};
const emptyCounts = () => ({
	notices: 0,
	fixNotices: 0,
	noticeTokens: 0
});
const FIX_NOTICES = [
	"hit",
	"near",
	"doubted"
];
/** A user message carrying `text`, attributed to the plugin. */
function pluginMessage(text, source) {
	return createUserMessage({
		content: [{
			type: "text",
			text
		}],
		source
	});
}
/**
* A hit whose hit count includes the occurrence being reported: the core is
* about to add it, and "known (1 hit)" for the second sighting would undersell.
*/
function counted(hit) {
	return {
		...hit,
		entry: {
			...hit.entry,
			hits: hit.entry.hits + 1
		}
	};
}
/**
* Bind the four injection points to a recorder.
*
* @param deps - the recorder, settings, trust store and caps.
* @returns listener bodies that never throw into a turn.
*/
function createInjection(deps) {
	const o = {
		...DEFAULT_INJECTION_OPTIONS,
		...deps.options
	};
	const { recorder } = deps;
	const sessions = /* @__PURE__ */ new Map();
	const awaitingId = /* @__PURE__ */ new WeakMap();
	const injecting = o.inject !== "off";
	const totals = emptyCounts();
	/** The message for a notice about to reach the model, counted. */
	function deliver(state, notice) {
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
	function stateFor(session, fresh = false) {
		const state = !fresh && sessions.get(session) || {
			injector: new Injector({
				mode: o.inject,
				caps: new CapTracker(deps.caps),
				scope: session
			}),
			resolver: new ResolutionTracker(),
			turn: void 0,
			pending: [],
			resolving: Promise.resolve(),
			counts: emptyCounts()
		};
		sessions.delete(session);
		sessions.set(session, state);
		if (sessions.size > 64) sessions.delete(sessions.keys().next().value);
		return state;
	}
	/**
	* `start()`, with a handler attached before anything else can run: a throw
	* or a rejection is counted and settles as undefined. Every promise the
	* hooks start and do not await at once goes through here, so none is ever
	* left to reject unhandled while `next()` runs (§13).
	*/
	function guard(start) {
		return start().catch((error) => {
			recorder.fail(error);
		});
	}
	/** Queue a notice for the next step; never rejects. */
	function track(state, item, write) {
		if (write !== void 0) guard(() => write).then((outcome) => {
			item.written = true;
			if (outcome?.kind === "appended") item.id = outcome.id;
		});
		else item.written = true;
		state.pending.push(item);
		if (state.pending.length > 4) state.pending.shift();
	}
	/**
	* Look a tool outcome up and offer it; never rejects. Reading the payload
	* is inside the try: a malformed result (`isError` with no `error`) throws
	* there, and this runs while `next()` is still pending.
	*/
	async function toolNotice(exec, result, state) {
		try {
			const found = await recorder.lookup(toolInput(exec, result));
			if (found === void 0) return void 0;
			if (found.matched) return state.injector.offer({
				kind: "hit",
				hit: counted(found)
			});
			if (o.inject === "always") awaitingId.set(exec, state);
			return;
		} catch (error) {
			recorder.fail(error);
			return;
		}
	}
	/** Queue a resolution step behind the session's earlier ones. */
	function resolving(state, step) {
		state.resolving = state.resolving.then(() => guard(step)).then(() => {});
	}
	/**
	* Feed one tool outcome to resolution detection (T14). A failure is watched
	* once its write names the entry; a success resolves what it matches. The
	* turn is taken now, when the call happened.
	*/
	function detect(state, exec, result, write) {
		const input = toolInput(exec, result);
		const outcome = callOutcome({
			toolName: exec.name,
			isError: input.kind === "tool",
			text: input.kind === "command" ? input.text : "",
			...input.kind === "command" && input.command !== void 0 ? { command: input.command } : {}
		});
		const turn = state.resolver.turn;
		if (!outcome.ok) {
			if (write === void 0) return;
			resolving(state, async () => {
				const written = await write;
				if (written.kind === "appended" || written.kind === "hit") state.resolver.occurred(written.id, outcome.key, turn);
			});
			return;
		}
		resolving(state, async () => {
			for (const id of state.resolver.succeeded(outcome.keys, turn)) await resolved(state, id, turn);
		});
	}
	/**
	* `id` looks resolved: its fix earned a success, and an entry without one
	* gets the one-shot prompt on the next step, under `captureFix:
	* 'prompt-once'`.
	*/
	async function resolved(state, id, turn) {
		const found = await recorder.lookupIssue(id);
		if (found === void 0) return;
		const { entry } = found;
		recorder.feedback(id, "success");
		if (!injecting || o.captureFix !== "prompt-once" || entry.fix.trim() !== "" || !state.resolver.ask(id)) return;
		track(state, {
			read: Promise.resolve(),
			miss: false,
			written: true,
			ask: id,
			turn
		}, void 0);
	}
	/** The first pending notice the caps allow; the rest are dropped. */
	async function drain(state) {
		const items = state.pending.slice();
		await Promise.all(items.map((item) => item.read));
		const kept = [];
		let notice;
		for (const item of items) if (item.hit !== void 0) notice ??= state.injector.offer({
			kind: "hit",
			hit: item.hit
		}, true);
		else if (item.id !== void 0) notice ??= state.injector.offer({
			kind: "miss",
			id: item.id
		});
		else if (item.ask !== void 0 && state.resolver.open(item.turn)) {
			const asked = notice === void 0 ? state.injector.ask(item.ask) : void 0;
			if (asked !== void 0) notice = asked;
			else kept.push(item);
		}
		state.pending = state.pending.filter((item) => !items.includes(item) || kept.includes(item));
		return notice;
	}
	return {
		options: o,
		agentError(payload) {
			try {
				const session = agentId(payload);
				if (!injecting || session === void 0) {
					recorder.agentError(payload);
					return;
				}
				const state = stateFor(session);
				const found = guard(() => recorder.lookup({
					kind: "agent",
					error: payload.error
				}));
				const write = recorder.agentError(payload);
				const item = {
					read: Promise.resolve(),
					miss: false,
					written: false
				};
				item.read = found.then((result) => {
					try {
						if (result === void 0) return;
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
				if (state !== void 0) {
					awaitingId.delete(exec);
					track(state, {
						read: Promise.resolve(),
						miss: true,
						written: false
					}, write);
				}
				const session = agentId(exec);
				if (session !== void 0) detect(stateFor(session), exec, result, write);
			} catch (error) {
				recorder.fail(error);
			}
		},
		async postExecute(exec, result, next) {
			let pending;
			let state;
			try {
				const session = agentId(exec);
				if (injecting && session !== void 0) {
					state = stateFor(session);
					const current = state;
					pending = guard(() => toolNotice(exec, result, current));
				}
			} catch (error) {
				recorder.fail(error);
			}
			const decision = await next();
			if (pending === void 0 || state === void 0) return decision;
			try {
				const notice = await pending;
				if (notice === void 0) return decision;
				return {
					...decision,
					additionalContexts: [...decision.additionalContexts ?? [], deliver(state, notice)]
				};
			} catch (error) {
				recorder.fail(error);
				return decision;
			}
		},
		async preStep(payload, next) {
			let state;
			try {
				const session = agentId(payload);
				if (session === void 0) return next();
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
			if (state === void 0 || decision.kind !== "enter" || state.pending.length === 0) return decision;
			try {
				const notice = await drain(state);
				if (notice === void 0) return decision;
				return {
					...decision,
					messages: [...decision.messages, deliver(state, notice)]
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
				if (index === void 0) return;
				const text = sessionDigestText(index.map(({ entry, excluded }) => ({
					id: entry.id,
					title: entry.title,
					hits: entry.hits,
					injectable: excluded === void 0
				})), o.sessionDigest);
				if (text === void 0) return;
				const head = text.split("\n", 1)[0];
				agent.inject(pluginMessage(text, noticeSource(head)));
			} catch (error) {
				recorder.fail(error);
			}
		},
		counts(session) {
			if (session === void 0) return { ...totals };
			const state = sessions.get(session);
			return state === void 0 ? emptyCounts() : { ...state.counts };
		},
		section() {
			return o.systemPromptHint ? {
				...SYSTEM_PROMPT_SECTION,
				text: SYSTEM_PROMPT_HINT
			} : void 0;
		}
	};
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
function registerInjection(ctx, injection, recorder) {
	ctx.on("agent/error", (payload) => {
		injection.agentError(payload);
	});
	ctx.on("tools/result", (exec, result) => {
		injection.toolResult(exec, result);
	});
	ctx.on("tools/post-execute", (exec, result, next) => injection.postExecute(exec, result, next));
	ctx.on("agent/pre-step", (payload, next) => injection.preStep(payload, next));
	ctx.on("agent/session-start", (payload) => injection.sessionStart(payload));
	ctx.on("agent/session-start", () => {
		recorder.replay();
	});
	const section = injection.section();
	if (section === void 0) return;
	try {
		ctx.systemPrompt.section(section);
	} catch (error) {
		recorder.fail(error);
	}
}
/** Category of an entry err_record creates from a message without one. */
const DEFAULT_RECORD_CATEGORY = "agent";
/** A query quoted back in a miss is clipped to this. */
const QUERY_MAX_CHARS = 80;
const SIGNATURE = /^[0-9a-f]{12}$/i;
const DEFAULT_TOOLS_OPTIONS = {
	idPrefix: "E-",
	idWidth: 4
};
/** The error branch every tool's output has. */
const ERROR = {
	type: "object",
	properties: { error: {
		type: "string",
		required: true
	} },
	additionalProperties: false
};
const isError = (value) => "error" in value;
const text = (body) => [{
	type: "text",
	text: body
}];
/** Render an error value as one line naming the tool. */
const errorText = (tool, value) => text(`${tool}: ${value.error}`);
const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
/** One line, clipped. */
const short = (value, max) => clip(oneLine(value), max);
/** Similarity rounded for display and for the JSON value. */
const round = (n) => Math.round(n * 100) / 100;
/**
* Bind the tools to a recorder.
*
* @param deps - the recorder, the injection layer, the KB label and settings.
* @returns the five definitions, ready for `ctx.tools.register()`.
*/
function createTools(deps) {
	const o = {
		...DEFAULT_TOOLS_OPTIONS,
		...deps.options
	};
	const { recorder } = deps;
	const idPattern = new RegExp(`^${escapeRegExp(o.idPrefix)}(\\d+)$`, "i");
	/** The ID number in an ID-shaped string, or undefined. */
	function idNumber(value) {
		const m = idPattern.exec(value.trim());
		return m === null ? void 0 : Number(m[1]);
	}
	/** The canonical spelling of an ID-shaped string, for messages. */
	function canonicalId(value) {
		const n = idNumber(value);
		return n === void 0 ? value.trim() : formatId(n, o.idPrefix, o.idWidth);
	}
	/** The indexed core entries, or an error value when the read failed. */
	async function readIndex() {
		return await recorder.entries() ?? { error: "could not reach the errkb core (is the daemon running?)" };
	}
	/** The entry an ID-shaped string names, from the core. */
	async function findIssue(id) {
		return (await recorder.lookupIssue(id))?.entry;
	}
	/**
	* Run a tool body; anything it throws comes back as an error value, counted
	* like a failed write.
	*/
	async function safely(body) {
		try {
			return await body();
		} catch (error) {
			recorder.fail(error);
			return { error: `internal error: ${safeErrorText(error).message}` };
		}
	}
	/** A trimmed string argument, or undefined when it is absent or blank. */
	const given = (value) => {
		const trimmed = value?.trim();
		return trimmed === void 0 || trimmed === "" ? void 0 : trimmed;
	};
	/** A write outcome's error, or its entry. */
	function fixedEntry(outcome) {
		if (outcome.kind === "fixed" && outcome.entry !== void 0) return outcome.entry;
		if (outcome.kind === "unknown") return { error: "no such entry" };
		return { error: "could not reach the errkb core (see the plugin log)" };
	}
	const ENTRY_VIEW = {
		type: "object",
		properties: {
			id: {
				type: "string",
				required: true
			},
			title: {
				type: "string",
				required: true
			},
			category: {
				type: "string",
				required: true
			},
			hits: {
				type: "integer",
				required: true
			},
			status: {
				type: "string",
				enum: ENTRY_STATUSES,
				required: true
			},
			fix: {
				type: "string",
				required: true
			},
			raw: { type: "string" }
		},
		additionalProperties: false
	};
	return [
		defineTool({
			name: "err_lookup",
			description: "Look up an error in the errkb knowledge base by entry ID, 12-hex fingerprint or the error text itself. Returns the entry with its recorded fix, or the closest entries when nothing matches.",
			parameters: {
				query: {
					type: "string",
					required: true,
					description: `An entry ID (${formatId(7, o.idPrefix, o.idWidth)}), a 12-hex fingerprint, or the error message.`
				},
				full: {
					type: "boolean",
					description: "Also return the redacted raw sample. Default false."
				}
			},
			output: {
				schema: { oneOf: [{
					type: "object",
					properties: {
						query: {
							type: "string",
							required: true
						},
						via: {
							type: "string",
							enum: [
								"id",
								"fingerprint",
								"exact",
								"fuzzy",
								"code",
								"none"
							],
							required: true
						},
						entry: {
							oneOf: [ENTRY_VIEW, { type: "null" }],
							required: true
						},
						closest: {
							type: "array",
							items: {
								type: "object",
								properties: {
									id: {
										type: "string",
										required: true
									},
									title: {
										type: "string",
										required: true
									},
									similarity: {
										type: "number",
										required: true
									}
								},
								additionalProperties: false
							},
							required: true
						}
					},
					additionalProperties: false
				}, ERROR] },
				render(_args, value) {
					if (isError(value)) return errorText("err_lookup", value);
					const { entry } = value;
					if (entry !== null) {
						const lines = [
							`${entry.id} · ${entry.title}`,
							`category: ${entry.category} · hits: ${entry.hits} · status: ${entry.status} · matched by ${value.via}`,
							entry.fix === "" ? "fix: (none recorded)" : `fix: ${entry.fix}`
						];
						if (entry.raw !== void 0) lines.push("raw:", entry.raw);
						return text(lines.join("\n"));
					}
					const head = `No entry matches "${short(value.query, QUERY_MAX_CHARS)}".`;
					if (value.closest.length === 0) return text(head);
					return text([`${head} Closest:`, ...value.closest.map((c) => `${c.id} ${c.title} (similarity ${c.similarity})`)].join("\n"));
				}
			},
			isConcurrencySafe: () => true,
			execute: ({ query, full }) => safely(async () => {
				const q = given(query);
				if (q === void 0) return { error: "query is empty" };
				const view = (entry) => ({
					id: entry.id,
					title: entry.title,
					category: entry.category,
					hits: entry.hits,
					status: entry.status,
					fix: entry.fix,
					...full === true ? { raw: entry.raw } : {}
				});
				const result = (via, entry) => ({
					query: q,
					via,
					entry: view(entry),
					closest: []
				});
				if (idNumber(q) !== void 0) return { error: `entry ids are core UUIDs now; look ${canonicalId(q)} up by its fingerprint or error text` };
				if (SIGNATURE.test(q)) {
					const hit = await recorder.lookupFingerprint(q.toLowerCase());
					if (hit !== void 0 && hit.matched) return result("fingerprint", hit.entry);
				}
				const input = {
					kind: "tool",
					isError: true,
					toolName: "err_lookup",
					message: q
				};
				const hit = await recorder.lookup(input);
				if (hit === void 0) return { error: "could not reach the errkb core (is the daemon running?)" };
				if (hit.matched) return result(hit.via === "code" ? "code" : hit.via === "exact" ? "exact" : "fuzzy", hit.entry);
				return {
					query: q,
					via: "none",
					entry: null,
					closest: (await recorder.search(q, 3) ?? []).map((candidate) => ({
						id: candidate.issue_id,
						title: short(candidate.title, 120),
						similarity: round(Math.max(0, Math.min(1, candidate.confidence ?? 0)))
					}))
				};
			})
		}),
		defineTool({
			name: "err_record",
			description: "Record what you learned about an error in the errkb knowledge base. Give exactly one of id (an existing entry) or message (the error text). A message updates an entry only when it matches exactly; on an approximate match nothing is written and the closest entry's ID is returned, so confirm it with id. A new entry is created when nothing matches. A fix marks the entry fixed. This is the only way a fix is written.",
			parameters: {
				id: {
					type: "string",
					description: `An existing entry, e.g. ${formatId(7, o.idPrefix, o.idWidth)}.`
				},
				message: {
					type: "string",
					description: "The error text, when you do not know the ID. Only an exact match updates an existing entry."
				},
				fix: {
					type: "string",
					description: "What fixed it, in one or two sentences."
				},
				status: {
					type: "string",
					enum: ENTRY_STATUSES,
					description: "fixed, wontfix (never inject it) or open. A fix alone sets fixed."
				},
				note: {
					type: "string",
					description: "A line added to the entry's notes."
				},
				category: {
					type: "string",
					description: `For message: the category to match and file under, e.g. tool or command-exit. Default: match any; file new entries under ${DEFAULT_RECORD_CATEGORY}.`
				}
			},
			output: {
				schema: { oneOf: [{
					type: "object",
					properties: {
						id: {
							type: "string",
							required: true
						},
						created: {
							type: "boolean",
							required: true
						},
						status: {
							type: "string",
							enum: ENTRY_STATUSES,
							required: true
						},
						hasFix: {
							type: "boolean",
							required: true
						}
					},
					additionalProperties: false
				}, ERROR] },
				render(_args, value) {
					if (isError(value)) return errorText("err_record", value);
					const what = value.created ? "Created" : "Updated";
					const fix = value.hasFix ? "" : ", no fix recorded";
					return text(`${what} ${value.id} (status ${value.status}${fix}).`);
				}
			},
			execute: (args) => safely(async () => {
				const id = given(args.id);
				const message = given(args.message);
				const fix = given(args.fix);
				given(args.note);
				given(args.category);
				const { status } = args;
				if (id === void 0 === (message === void 0)) return { error: "give exactly one of id and message" };
				if (args.fix !== void 0 && fix === void 0) return { error: "fix is empty" };
				let target;
				if (id !== void 0) {
					const found = await findIssue(id);
					if (found === void 0) return { error: `no entry ${canonicalId(id)}` };
					target = {
						id: found.id,
						created: false
					};
				} else {
					const input = {
						kind: "tool",
						isError: true,
						toolName: "err_record",
						message
					};
					const textHit = await recorder.lookup(input);
					if (textHit !== void 0 && textHit.matched && textHit.via !== "exact") return { error: `closest match is ${textHit.id} (approximate, by ${textHit.via}); nothing was written. Call err_record with id: "${textHit.id}" to confirm, or reword message` };
					if (textHit !== void 0 && textHit.matched) target = {
						id: textHit.id,
						created: false
					};
					else {
						const outcome = await recorder.capture(input);
						if (outcome.kind === "appended") target = {
							id: outcome.id,
							created: true
						};
						else if (outcome.kind === "hit") target = {
							id: outcome.id,
							created: false
						};
						else return { error: "could not reach the errkb core; the observation was queued and will be replayed" };
					}
				}
				let entry;
				if (fix !== void 0) {
					const fixed = fixedEntry(await recorder.recordFix(target.id, fix));
					if (isError(fixed)) return fixed;
					entry = fixed;
				}
				if (status !== void 0) {
					const updated = fixedEntry(await recorder.setStatus(target.id, status));
					if (isError(updated)) return updated;
					entry = updated;
				}
				if (entry === void 0) entry = await findIssue(target.id);
				if (entry === void 0) return { error: `no entry ${target.id}` };
				return {
					id: target.id,
					created: target.created,
					status: entry.status,
					hasFix: entry.fix.trim() !== ""
				};
			})
		}),
		defineTool({
			name: "err_list",
			description: "List entries of the errkb knowledge base: ID, title and hits only, no bodies.",
			parameters: {
				cat: {
					type: "string",
					description: "Only this category: tool, command-exit, llm, agent, or a display category such as tool / bash."
				},
				status: {
					type: "string",
					enum: ENTRY_STATUSES
				},
				limit: {
					type: "integer",
					description: `At most this many entries (default 20, at most 200).`
				}
			},
			output: {
				schema: { oneOf: [{
					type: "object",
					properties: {
						entries: {
							type: "array",
							items: {
								type: "object",
								properties: {
									id: {
										type: "string",
										required: true
									},
									title: {
										type: "string",
										required: true
									},
									hits: {
										type: "integer",
										required: true
									}
								},
								additionalProperties: false
							},
							required: true
						},
						total: {
							type: "integer",
							required: true
						}
					},
					additionalProperties: false
				}, ERROR] },
				render(_args, value) {
					if (isError(value)) return errorText("err_list", value);
					if (value.total === 0) return text("No entries.");
					return text([...value.entries.map((e) => `${e.id} (${e.hits}) ${e.title}`), `${value.entries.length} of ${value.total} shown.`].join("\n"));
				}
			},
			isConcurrencySafe: () => true,
			execute: (args) => safely(async () => {
				const limit = args.limit ?? 20;
				if (!Number.isInteger(limit) || limit < 1) return { error: "limit must be a positive integer" };
				const index = await readIndex();
				if (!Array.isArray(index)) return index;
				const cat = given(args.cat)?.toLowerCase();
				const matches = index.filter((i) => (cat === void 0 || i.category.toLowerCase() === cat || i.entry.category.toLowerCase() === cat) && (args.status === void 0 || i.entry.status === args.status));
				return {
					entries: matches.slice(0, Math.min(limit, 200)).map(({ entry }) => ({
						id: entry.id,
						title: short(entry.title, 120),
						hits: entry.hits
					})),
					total: matches.length
				};
			})
		}),
		defineTool({
			name: "err_forget",
			description: "Mark a misjudged entry wontfix so it is never injected again. Nothing is deleted; the status change is audited by the core.",
			parameters: {
				id: {
					type: "string",
					required: true
				},
				reason: {
					type: "string",
					description: "Why, for the audit trail."
				}
			},
			output: {
				schema: { oneOf: [{
					type: "object",
					properties: {
						id: {
							type: "string",
							required: true
						},
						archived: {
							type: "boolean",
							required: true
						}
					},
					additionalProperties: false
				}, ERROR] },
				render(_args, value) {
					if (isError(value)) return errorText("err_forget", value);
					return text(`Marked ${value.id} wontfix; it will not be injected.`);
				}
			},
			execute: (args) => safely(async () => {
				const id = given(args.id);
				if (id === void 0) return { error: "id is empty" };
				const found = await findIssue(id);
				if (found === void 0) return { error: `no entry ${canonicalId(id)}` };
				const target = found.id;
				if ((await recorder.setStatus(target, "wontfix")).kind !== "fixed") return { error: "could not reach the errkb core (see the plugin log)" };
				return {
					id: target,
					archived: true
				};
			})
		}),
		defineTool({
			name: "err_stats",
			description: "Show the errkb ledger: entries, hits, notices injected, an estimate of tokens saved, open entries without a fix and the knowledge base path. Costs no model call.",
			parameters: { scope: {
				type: "string",
				enum: ["session", "all"],
				description: "Notices of this session or of every session since the plugin started (default all). Entry figures are always the whole knowledge base."
			} },
			output: {
				schema: { oneOf: [{
					type: "object",
					properties: {
						scope: {
							type: "string",
							enum: ["session", "all"],
							required: true
						},
						kbDir: {
							type: "string",
							required: true
						},
						entries: {
							type: "integer",
							required: true
						},
						hits: {
							type: "integer",
							required: true
						},
						openWithoutFix: {
							type: "integer",
							required: true
						},
						notices: {
							type: "integer",
							required: true
						},
						fixNotices: {
							type: "integer",
							required: true
						},
						noticeTokens: {
							type: "integer",
							required: true
						},
						estimatedTokensSaved: {
							type: "integer",
							required: true
						}
					},
					additionalProperties: false
				}, ERROR] },
				render(_args, value) {
					if (isError(value)) return errorText("err_stats", value);
					const where = value.scope === "session" ? "this session" : "all sessions";
					const net = value.fixNotices * 800 - value.noticeTokens;
					const clamped = net < 0 ? ` = −${-net}, shown as 0` : "";
					return text([
						`Knowledge base: ${value.kbDir}`,
						`Entries: ${value.entries} · hits: ${value.hits} · open without a fix: ${value.openWithoutFix}`,
						`Notices (${where}): ${value.notices}, ${value.fixNotices} with a fix, ${value.noticeTokens} tokens`,
						`Estimated tokens saved: ${value.estimatedTokensSaved} (estimate: ${value.fixNotices} fix notices × 800 − ${value.noticeTokens} notice tokens${clamped})`
					].join("\n"));
				}
			},
			isConcurrencySafe: () => true,
			execute: (args, exec) => safely(async () => {
				const scope = args.scope ?? "all";
				const session = exec.agent?.id;
				if (scope === "session" && session === void 0) return { error: "scope session needs a call from an agent session" };
				const index = await readIndex();
				if (!Array.isArray(index)) return index;
				const counts = deps.injection.counts(scope === "session" ? session : void 0);
				return {
					scope,
					kbDir: deps.kbDir,
					entries: index.length,
					hits: index.reduce((sum, { entry }) => sum + entry.hits, 0),
					openWithoutFix: index.filter(({ entry }) => entry.status === "open" && entry.fix.trim() === "").length,
					...counts,
					estimatedTokensSaved: estimateSaved(counts.fixNotices, counts.noticeTokens)
				};
			})
		})
	];
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
function estimateSaved(fixNotices, noticeTokens) {
	return Math.max(0, fixNotices * 800 - noticeTokens);
}
/**
* Register the five tools (T15). A tool the registry refuses (a duplicate
* name) is counted and logged; the others are still registered.
*
* @param ctx - the plugin's context, with the `tools` service.
* @param deps - see {@link createTools}.
*/
function registerTools(ctx, deps) {
	for (const tool of createTools(deps)) try {
		ctx.tools.register(tool);
	} catch (error) {
		deps.recorder.fail(error);
	}
}
//#endregion
//#region src/index.ts
const name = "err-kb";
const inject = ["tools", "systemPrompt"];
const Config = z.object({
	pipe: z.string().default("\\\\.\\pipe\\errkb"),
	pipeTimeoutMs: z.number().default(1e4),
	offlineMaxEvents: z.number().default(1e3),
	offlineMaxBytes: z.number().default(32 * 1024 * 1024),
	connectorId: z.string().default("dsh"),
	projectKey: z.string().default("default"),
	capture: z.array(z.string()).default([
		"tool",
		"command",
		"llm",
		"agent"
	]),
	captureExitCodes: z.boolean().default(true),
	transientThreshold: z.number().default(5),
	fuzzyThreshold: z.number().default(.72),
	captureFix: z.string().default("prompt-once"),
	inject: z.string().default("hit-only"),
	sessionDigest: z.string().default("counts"),
	systemPromptHint: z.boolean().default(true),
	kbDir: z.string().default(""),
	idPrefix: z.string().default("E-"),
	idWidth: z.number().default(4),
	providers: z.array(z.string()).default(["*"]),
	share: z.string().default("public"),
	maxEntries: z.number().default(200),
	maxSampleChars: z.number().default(500),
	exportDir: z.string().default(""),
	labels: z.union(["en", "zh"]).default("en")
});
/**
* The `idPrefix` shape: letters, digits, `-` and `_`, not ending in a digit, so
* the trailing digits of an ID are its number alone and a prefix never spans a
* header's ` · `.
*/
const ID_PREFIX_PATTERN = /^[A-Za-z0-9_-]*[A-Za-z_-]$/;
/** The documented ranges, checked by normalizeSettings(). */
const SETTING_BOUNDS = {
	fuzzyThreshold: {
		min: .5,
		max: 1,
		integer: false
	},
	idWidth: {
		min: 1,
		max: 9,
		integer: true
	},
	maxEntries: {
		min: 1,
		max: Infinity,
		integer: true
	},
	maxSampleChars: {
		min: 0,
		max: Infinity,
		integer: true
	},
	pipeTimeoutMs: {
		min: 1,
		max: 6e5,
		integer: true
	},
	offlineMaxEvents: {
		min: 1,
		max: 1e5,
		integer: true
	},
	offlineMaxBytes: {
		min: 1,
		max: 1024 * 1024 * 1024,
		integer: true
	}
};
/** `value` inside `bounds`; a non-finite value takes `fallback`. */
function clampSetting(value, bounds, fallback) {
	if (Number.isNaN(value)) return fallback;
	const whole = bounds.integer && Number.isFinite(value) ? Math.floor(value) : value;
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
function normalizeSettings(config, warn = () => void 0) {
	const defaults = Config({});
	const out = { ...config };
	const show = (value) => typeof value === "string" ? JSON.stringify(value) : String(value);
	const report = (key, given, used) => warn(`err-kb: setting ${key}=${show(given)} is out of range; using ${show(used)}`);
	for (const key of Object.keys(SETTING_BOUNDS)) {
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
function recorderOptions(config) {
	return {
		capture: config.capture,
		captureExitCodes: config.captureExitCodes,
		connectorId: config.connectorId,
		projectKey: config.projectKey,
		providers: config.providers
	};
}
/** `value` when it is one of `allowed`, else `fallback`. */
function oneOf(value, allowed, fallback) {
	return allowed.includes(value) ? value : fallback;
}
/**
* The injection settings a configuration selects.
*
* @param config - the plugin's settings.
* @returns the settings for createInjection(); an unknown `inject`,
*   `captureFix` or `sessionDigest` value takes its default.
*/
function injectionOptions(config) {
	return {
		inject: oneOf(config.inject, INJECT_MODES, DEFAULT_INJECTION_OPTIONS.inject),
		captureFix: oneOf(config.captureFix, CAPTURE_FIX_MODES, DEFAULT_INJECTION_OPTIONS.captureFix),
		sessionDigest: oneOf(config.sessionDigest, SESSION_DIGEST_MODES, DEFAULT_INJECTION_OPTIONS.sessionDigest),
		systemPromptHint: config.systemPromptHint
	};
}
function apply(ctx, given) {
	const config = normalizeSettings(given, (message) => ctx.logger.warn(message));
	const kb = resolveKbDir(config.kbDir, defaultProbe());
	ctx.logger.info(formatKbLog(kb));
	ctx.logger.info("err-kb: knowledge base is the local core at %s (connector %s)", config.pipe, config.connectorId);
	const queue = createObservationQueue({
		files: {
			state: kb.files.state,
			lock: kb.files.lock
		},
		options: {
			maxEvents: config.offlineMaxEvents,
			maxBytes: config.offlineMaxBytes
		}
	});
	const recorder = createRecorder({
		logger: ctx.logger,
		queue,
		options: recorderOptions(config),
		createConnector: (sessionId) => createConnector({
			connectorId: config.connectorId,
			sessionId,
			projectKey: config.projectKey,
			queue,
			core: createCore(createPipeTransport({
				pipe: config.pipe,
				timeoutMs: config.pipeTimeoutMs
			}))
		})
	});
	const injection = createInjection({
		recorder,
		options: injectionOptions(config)
	});
	registerInjection(ctx, injection, recorder);
	registerTools(ctx, {
		recorder,
		injection,
		kbDir: config.pipe,
		options: {
			idPrefix: config.idPrefix,
			idWidth: config.idWidth
		}
	});
	ctx.on("agent/request-error", async (payload, next) => {
		try {
			return await next();
		} finally {
			try {
				await recorder.requestError(payload);
			} catch {}
		}
	});
}
//#endregion
export { CONNECTOR_CHANNEL, CONNECTOR_SDK_VERSION, Config, CoreError, DEFAULT_PIPE, DEFAULT_PIPE_TIMEOUT_MS, ID_PREFIX_PATTERN, SETTING_BOUNDS, apply, createConnector, createConnectorQueue, createCore, createPipeTransport, inject, injectionOptions, name, normalizeSettings, observationFromAgentError, observationFromToolResult, recorderOptions };

//# sourceMappingURL=index.js.map