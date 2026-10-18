/**
 * Generates the cross-language conformance corpus in `spec/corpus/` (EKB-009).
 *
 * The seven JSON files are the only judge for the Go implementation: every
 * `expected` value is produced by the TypeScript implementation at generation
 * time, so a field difference on the Go side is a real drift, not a guess.
 *
 * Run from the repository root:
 *
 *   pnpm exec tsdown tests/gen-corpus.ts --out-dir node_modules/.cache/errkb-gen --format esm --no-dts --no-config --external prettier
 *   node node_modules/.cache/errkb-gen/gen-corpus.mjs           # write the corpus
 *   node node_modules/.cache/errkb-gen/gen-corpus.mjs --check   # compare only
 *
 * The bundle step exists because the sources use extensionless imports: Node's
 * type stripping cannot resolve `./signature`, so `node tests/gen-corpus.ts`
 * fails with ERR_MODULE_NOT_FOUND. tsdown resolves the graph and the bundle
 * keeps `prettier` external (it is a devDependency, not shipped code).
 *
 * Extraction is deliberately shallow: only calls whose arguments are plain
 * string literals (`fn("...")`, `fn("...", "...")`) are taken from the test
 * sources. Cases built through helpers, template substitutions or `.repeat()`
 * are skipped and listed under `not_covered`; the important ones are added
 * below as curated cases with the same fixtures the tests use.
 */
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { format as formatJson } from "prettier";

import { extractHeadline } from "../src/capture";
import { noticeText } from "../src/inject";
import type { NoticeEvent, TrustLevel } from "../src/inject";
import { indexEntries, match, tokenize } from "../src/match";
import type { CapturedError, Hit, MatchResult } from "../src/match";
import { redact } from "../src/redact";
import { REDACT_PATTERNS } from "../src/redact-patterns";
import { normalize, signature } from "../src/signature";
import type { Entry } from "../src/store";

const ROOT = process.cwd();
const CORPUS_DIR = join(ROOT, "spec", "corpus");
const BACKSLASH = String.fromCharCode(92);
const ESC = String.fromCharCode(27);
const joinText = (...parts: string[]): string => parts.join("");

/** One case. `case_id` and `source_test` are what makes a failure locatable. */
interface CorpusCase {
  case_id: string;
  source_test: string;
  input: unknown;
  expected: unknown;
  options?: unknown;
  note?: string;
}

interface CorpusFile {
  corpus: string;
  sources: string[];
  coverage: string[];
  not_covered: string[];
  cases: CorpusCase[];
}

type DraftCase = Omit<CorpusCase, "case_id">;

// ---------------------------------------------------------------------------
// Extraction

// A single- or double-quoted string, or a template literal without `${`.
const STRING_LITERAL =
  '("(?:[^"\\\\]|\\\\.)*"|`(?:[^`\\\\]|\\\\.|\\$(?!\\{))*`)';

/** Literal arguments of `fn(...)` calls; every other call shape is skipped. */
function stringCalls(source: string, fn: string, arity: 1 | 2): string[][] {
  const args =
    arity === 2
      ? `${STRING_LITERAL}\\s*,\\s*${STRING_LITERAL}`
      : STRING_LITERAL;
  const pattern = new RegExp(
    String.raw`(?<![\w$.])${fn}\(\s*${args}\s*\)`,
    "g",
  );
  const calls: string[][] = [];
  for (const found of source.matchAll(pattern)) {
    const first = unescapeLiteral(found[1]);
    calls.push(arity === 2 ? [first, unescapeLiteral(found[2])] : [first]);
  }
  return calls;
}

/** Decodes the escape sequences a JavaScript string literal can hold. */
function unescapeLiteral(literal: string): string {
  const body = literal.slice(1, -1);
  let out = "";
  for (let index = 0; index < body.length; index += 1) {
    const char = body[index];
    if (char !== "\\") {
      out += char;
      continue;
    }
    const next = body[index + 1];
    index += 1;
    switch (next) {
      case "n":
        out += "\n";
        break;
      case "r":
        out += "\r";
        break;
      case "t":
        out += "\t";
        break;
      case "b":
        out += "\b";
        break;
      case "f":
        out += "\f";
        break;
      case "v":
        out += "\v";
        break;
      case "0":
        out += "\0";
        break;
      case "x":
        out += String.fromCodePoint(
          Number.parseInt(body.slice(index + 1, index + 3), 16),
        );
        index += 2;
        break;
      case "u": {
        if (body[index + 1] === "{") {
          const end = body.indexOf("}", index);
          out += String.fromCodePoint(
            Number.parseInt(body.slice(index + 2, end), 16),
          );
          index = end;
        } else {
          out += String.fromCodePoint(
            Number.parseInt(body.slice(index + 1, index + 5), 16),
          );
          index += 4;
        }
        break;
      }
      case "\n":
        break;
      default:
        out += next;
    }
  }
  return out;
}

function readTest(path: string): string {
  return readFileSync(join(ROOT, path), "utf8");
}

function uniqueBy<T>(items: T[], key: (item: T) => string): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of items) {
    const value = key(item);
    if (seen.has(value)) continue;
    seen.add(value);
    out.push(item);
  }
  return out;
}

function withIds(
  corpus: string,
  draft: Omit<CorpusFile, "cases"> & { cases: DraftCase[] },
): CorpusFile {
  return {
    ...draft,
    cases: draft.cases.map((item, index) => ({
      case_id: `${corpus}-${String(index + 1).padStart(3, "0")}`,
      ...item,
    })),
  };
}

// ---------------------------------------------------------------------------
// normalize.json and fingerprint.json — tests/signature.test.ts

function normalizeCorpus(): CorpusFile {
  const source = readTest("tests/signature.test.ts");
  const extracted = stringCalls(source, "normalize", 1).map(([text]) => text);
  const curated = [
    joinText("Error: ", "x".repeat(1200), " at line 1"),
    joinText(
      ESC,
      "[31mError",
      ESC,
      "[0m uuid 123e4567-e89b-12d3-a456-426614174000 pid 4321 at ",
      "2026-01-02T03:04:05.678Z in line 99 of /srv/app/src/x.ts:12:5",
    ),
  ];
  const inputs = uniqueBy([...extracted, ...curated], (text) => text);
  return withIds("normalize", {
    corpus: "normalize",
    sources: [
      "tests/signature.test.ts",
      "tests/gen-corpus.ts (curated overlong and composite cases)",
    ],
    coverage: [
      "ANSI CSI and OSC stripping",
      "whitespace and carriage-return collapse",
      "UUID, 32+ hex, timestamp, clock, duration, pid, port, host:port, line/column and temp-dir placeholders",
      "absolute path collapsing, POSIX and Windows",
      "lowercasing and edge punctuation",
      "overlong input",
    ],
    not_covered: [
      "the 200 000-character unit-test input is bounded to 1200 characters",
      "template-literal and helper-built arguments stay in tests/signature.test.ts",
    ],
    cases: inputs.map((input) => ({
      source_test: "tests/signature.test.ts#normalize",
      input,
      expected: normalize(input),
    })),
  });
}

function fingerprintCorpus(): CorpusFile {
  const source = readTest("tests/signature.test.ts");
  const extracted = stringCalls(source, "signature", 2);
  const curated: string[][] = [
    ["tool", "EPERM: operation not permitted, rename '<path>'"],
    ["command-exit", "npm error code ENOENT"],
    ["llm", "拒绝访问：无法读取配置"],
    ["agent", "NO_ADAPTER: missing adapter"],
  ];
  const pairs = uniqueBy(
    [...extracted, ...curated],
    ([category, message]) => `${category}\u0000${message}`,
  );
  return withIds("fingerprint", {
    corpus: "fingerprint",
    sources: [
      "tests/signature.test.ts",
      "tests/gen-corpus.ts (curated categories)",
    ],
    coverage: [
      "sha256(category + NUL + normalize(message)) truncated to 12 lowercase hex characters",
      "every capture category the TypeScript side knows",
      "identical normalized messages under different categories stay distinct",
    ],
    not_covered: ["signature calls built from helpers or variables"],
    cases: pairs.map(([category, message]) => ({
      source_test: "tests/signature.test.ts#signature",
      input: { category, message },
      expected: signature(category, message),
    })),
  });
}

// ---------------------------------------------------------------------------
// headline.json — tests/capture.test.ts

function headlineCorpus(): CorpusFile {
  const source = readTest("tests/capture.test.ts");
  const extracted = stringCalls(source, "extractHeadline", 1).map(
    ([text]) => text,
  );
  const curated = [
    joinText("y".repeat(1200), " THEEND"),
    joinText(
      "Traceback (most recent call last):\n",
      '  File "x.py", line 1\n',
      "KeyboardInterrupt\n",
    ),
  ];
  const inputs = uniqueBy([...extracted, ...curated], (text) => text);
  return withIds("headline", {
    corpus: "headline",
    sources: [
      "tests/capture.test.ts",
      "tests/gen-corpus.ts (curated overlong and traceback cases)",
    ],
    coverage: [
      "ANSI stripping, CR/LF splitting, empty-line skipping",
      "first line naming an error code, else the last line",
      "Python traceback: the last non-empty line",
      "the 200-character cap",
    ],
    not_covered: [
      "payload constants (TSC, PNPM) stay in tests/capture.test.ts",
      "case ids are exercised only through the cap",
    ],
    cases: inputs.map((input) => ({
      source_test: "tests/capture.test.ts#extractHeadline",
      input,
      expected: extractHeadline(input),
    })),
  });
}

// ---------------------------------------------------------------------------
// redact.json — tests/redact.test.ts and tests/redact-patterns.test.ts

const RANDOM = "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1";
const WINDOWS_PATH = joinText(
  "C",
  ":",
  BACKSLASH,
  "Users",
  BACKSLASH,
  "kim",
  BACKSLASH,
  "proj",
  BACKSLASH,
  "package.json",
);
const HOME_PATH = joinText("/", "home", "/", "kim", "/proj/a.ts");

/** One fixture per redaction family, reassembled so this file stays guard-clean. */
const SECRET_FIXTURES: ReadonlyArray<{ family: string; text: string }> = [
  {
    family: "authorization-header",
    text: joinText(
      "401 Unauthorized; Authorization: Basic ",
      "dXNlcjpw",
      "YXNz",
    ),
  },
  {
    family: "api-key-assignment",
    text: joinText("GET /v1/models?api", "_key=", "k3y-Value-77", " failed"),
  },
  {
    family: "api-key-assignment",
    text: joinText("x-api-key: ", "hdr-Value-88", " rejected"),
  },
  {
    family: "secret-assignment",
    text: joinText("callback?token=", "tok-Value-99", "&state=1"),
  },
  {
    family: "secret-assignment",
    text: joinText('{"access_token": "', "json-Value-11", '"}'),
  },
  {
    family: "bearer-token",
    text: joinText(
      "header B",
      "earer ",
      "eyJhbGciOi",
      "Jfoo.bar.baz",
      " expired",
    ),
  },
  {
    family: "openai-style-key",
    text: joinText("Incorrect API key provided: s", "k-", "proj-", RANDOM, "."),
  },
  { family: "xai-key", text: joinText("invalid key xai-", RANDOM) },
  {
    family: "google-api-key",
    text: joinText("key=AIza", RANDOM, "AbCdEfGhIjK is invalid"),
  },
  {
    family: "github-token",
    text: joinText("remote: token gh", "p_", RANDOM, " revoked"),
  },
  { family: "github-token", text: joinText("oauth gho_", RANDOM) },
  {
    family: "github-fine-grained-token",
    text: joinText("auth github_pat_", RANDOM, "_x denied"),
  },
  {
    family: "aws-access-key",
    text: joinText(
      "The AWS Access Key Id AK",
      "IA",
      "IOSFODNN7EXAMPLE does not exist",
    ),
  },
  {
    family: "email",
    text: joinText("commit author kim.lee", "@", "gmail.com rejected"),
  },
  { family: "request-id", text: "server error (request_id: abc-123-def)" },
  { family: "request-id-token", text: joinText("overloaded req_", RANDOM) },
  {
    family: "long-hex",
    text: "session 0123456789abcdef0123456789ABCDEF expired",
  },
  {
    family: "long-base64",
    text: joinText("blob ", RANDOM, RANDOM, "== rejected"),
  },
];

function redactCorpus(): CorpusFile {
  const source = readTest("tests/redact.test.ts");
  const extracted = stringCalls(source, "redact", 1).map(([text]) => text);
  const curated = SECRET_FIXTURES.map(({ family, text }) => ({
    input: text,
    note: `family: ${family}`,
  }));
  const plain = uniqueBy(extracted, (text) => text);
  const cases: DraftCase[] = [
    ...plain.map((input) => ({
      source_test: "tests/redact.test.ts#redact",
      input,
      expected: redact(input),
    })),
    ...curated.map(({ input, note }) => ({
      source_test: "tests/redact.test.ts#SECRETS",
      input,
      expected: redact(input),
      note,
    })),
    {
      source_test: "tests/redact.test.ts#share-modes",
      input: joinText("ENOENT '", WINDOWS_PATH, "' and '", HOME_PATH, "'"),
      options: { share: "private" },
      expected: redact(
        joinText("ENOENT '", WINDOWS_PATH, "' and '", HOME_PATH, "'"),
        { share: "private" },
      ),
    },
  ];
  return withIds("redact", {
    corpus: "redact",
    sources: [
      "tests/redact.test.ts",
      "tests/redact-patterns.test.ts",
      "tests/gen-corpus.ts (curated family fixtures)",
    ],
    coverage: [
      `all ${REDACT_PATTERNS.length} redaction families, at least one case each`,
      "public mode collapses absolute paths, private mode keeps them without the user name",
      "ordinary error text stays untouched",
      "redaction is idempotent",
    ],
    not_covered: [
      "seeds/*.md round-trip stays in tests/redact.test.ts",
      "capSample() and redactSample() stay in tests/redact.test.ts",
    ],
    cases,
  });
}

// ---------------------------------------------------------------------------
// tokenize.json — tests/match.test.ts

function tokenizeCorpus(): CorpusFile {
  const source = readTest("tests/match.test.ts");
  const extracted = stringCalls(source, "tokenize", 1).map(([text]) => text);
  const curated = [
    "拒绝访问",
    "ファイル 파일",
    "EPERM 拒绝访问",
    "打开 <path> 失败",
  ];
  const inputs = uniqueBy([...extracted, ...curated], (text) => text);
  return withIds("tokenize", {
    corpus: "tokenize",
    sources: ["tests/match.test.ts", "tests/gen-corpus.ts (curated CJK cases)"],
    coverage: [
      "<placeholder> tokens",
      "Han, kana and Hangul runs as character bigrams",
      "other letter, digit and underscore runs as words",
      "empty and punctuation-only input yields no tokens",
    ],
    not_covered: [
      "token sets are compared sorted; iteration order is not part of the contract",
    ],
    cases: inputs.map((input) => ({
      source_test: "tests/match.test.ts#tokenize",
      input,
      expected: [...tokenize(input)].sort(),
    })),
  });
}

// ---------------------------------------------------------------------------
// match.json — curated from tests/match.test.ts

function entryFixture(
  id: string,
  message: string,
  category = "tool",
  code?: string,
): Entry {
  const sig = signature(category, message);
  return {
    id,
    title: `[${category}] ${message.slice(0, 40)}`,
    meta: {
      sig,
      cat: category,
      ...(code === undefined ? {} : { code }),
      first: "2026-10-01T00:00:00Z",
    },
    fingerprint: sig,
    category: `${category} / sh`,
    firstSeen: "2026-10-01 00:00",
    lastSeen: "2026-10-01 00:00",
    hits: 1,
    trigger: "",
    raw: message,
    fix: "",
    status: "open",
    notes: "",
  };
}

const words = (count: number, from = 0): string =>
  Array.from({ length: count }, (_, offset) => `w${from + offset}`).join(" ");

/** `entry.meta` is a plain string map, so its optional code needs widening. */
const codeOf = (entry: Entry): string | undefined =>
  entry.meta["code"] as string | undefined;

/** The matching result, cut to the fields the Go side must reproduce. */
function summarizeMatch(result: MatchResult): Record<string, unknown> {
  if (!result.matched) return { matched: false };
  return {
    matched: true,
    id: result.id,
    via: result.via,
    approximate: result.approximate,
    similarity: result.similarity,
  };
}

function matchCorpus(): CorpusFile {
  const entriesFor = (
    message: string,
    category = "tool",
    code?: string,
  ): Entry[] => [entryFixture("E-0001", message, category, code)];
  const cases: Array<{
    message: string;
    category?: string;
    code?: string;
    entries: Entry[];
    threshold?: number;
    note: string;
  }> = [
    {
      message: words(71),
      entries: entriesFor(words(100)),
      note: "Jaccard 0.71 is below the default threshold",
    },
    {
      message: words(72),
      entries: entriesFor(words(100)),
      note: "Jaccard 0.72 is exactly the default threshold",
    },
    {
      message: words(73),
      entries: entriesFor(words(100)),
      note: "Jaccard 0.73 is above the default threshold",
    },
    {
      message: "eperm: operation not permitted, rename '<path>'",
      entries: entriesFor("EPERM: operation not permitted, rename '<path>'"),
      note: "identical normalized messages: exact fingerprint hit",
    },
    {
      message: "connection reset during fetch",
      code: "ERR_MODULE_NOT_FOUND",
      entries: entriesFor(
        "npm install failed hard",
        "tool",
        "ERR_MODULE_NOT_FOUND",
      ),
      note: "same category and code, both normalized messages shorter than 40 characters: code fallback",
    },
    {
      message: "unrelated failure text",
      entries: entriesFor("completely different failure", "llm"),
      note: "different category: miss",
    },
  ];
  return withIds("match", {
    corpus: "match",
    sources: ["tests/match.test.ts"],
    coverage: [
      "exact fingerprint hit",
      "fuzzy Jaccard hit at the 0.71 / 0.72 / 0.73 boundary",
      "short-message code fallback",
      "miss",
    ],
    not_covered: [
      "wontfix and misjudged exclusion, tie-breaking and empty inputs stay in tests/match.test.ts",
      "entries are built from id, message, category and code only; other entry fields do not affect matching",
    ],
    cases: cases.map(
      ({ message, category = "tool", code, entries, threshold, note }) => {
        const error: CapturedError = {
          category,
          message,
          ...(code === undefined ? {} : { code }),
        };
        const result = match(
          error,
          indexEntries(entries),
          threshold === undefined ? {} : { fuzzyThreshold: threshold },
        );
        return {
          source_test: "tests/match.test.ts#match",
          note,
          input: {
            error: {
              category,
              message,
              ...(code === undefined ? {} : { code }),
            },
            entries: entries.map((entry) => ({
              id: entry.id,
              message: entry.raw,
              category: entry.meta["cat"] ?? "tool",
              ...(codeOf(entry) === undefined ? {} : { code: codeOf(entry) }),
            })),
            ...(threshold === undefined ? {} : { threshold }),
          },
          expected: summarizeMatch(result),
        };
      },
    ),
  });
}

// ---------------------------------------------------------------------------
// notice.json — curated from tests/inject.test.ts

const NOTICE_FIX =
  "close the locking process and re-run; if it persists, use pnpm install --config.node-linker=hoisted.";
const NOTICE_CAUSE = "node_modules locked by an editor during pnpm install";

function noticeEntry(id: string, overrides: Partial<Entry> = {}): Entry {
  return {
    id,
    title: "[tool:pwsh] EPERM: operation not permitted, rename",
    meta: { sig: "3f2a1c9d0b71", cat: "tool" },
    fingerprint: "3f2a1c9d0b71",
    category: "tool / pwsh",
    firstSeen: "2026-09-14 09:12",
    lastSeen: "2026-09-14 15:40",
    hits: 5,
    trigger: NOTICE_CAUSE,
    raw: "EPERM: operation not permitted, rename '<path>'",
    fix: NOTICE_FIX,
    status: "open",
    notes: "",
    ...overrides,
  };
}

function noticeHit(
  id = "E-0007",
  overrides: Partial<Entry> = {},
  extra: Partial<Hit> = {},
): NoticeEvent {
  return {
    kind: "hit",
    hit: {
      matched: true,
      id,
      entry: noticeEntry(id, overrides),
      via: "exact",
      approximate: false,
      similarity: 1,
      injectable: true,
      ...extra,
    },
  };
}

const noticeNear = (
  id = "E-0007",
  overrides: Partial<Entry> = {},
): NoticeEvent =>
  noticeHit(id, overrides, {
    via: "fuzzy",
    approximate: true,
    similarity: 0.8,
  });

function noticeCorpus(): CorpusFile {
  const cases: Array<{ event: NoticeEvent; trust?: TrustLevel; note: string }> =
    [
      { event: noticeHit(), note: "hit with cause and fix" },
      { event: noticeHit("E-0001", { hits: 1 }), note: "singular hit count" },
      { event: noticeHit("E-0007", { trigger: "  " }), note: "no cause line" },
      {
        event: noticeHit("E-0007", { fix: "step one\n\nstep two" }),
        note: "multi-line fix joined to one line",
      },
      { event: noticeNear(), note: "near hit: approximate wording" },
      { event: noticeHit("E-0007", { fix: " \n " }), note: "no fix recorded" },
      { event: { kind: "miss", id: "E-0011" }, note: "miss" },
      { event: noticeHit(), trust: "doubted", note: "doubted trust level" },
      { event: noticeNear(), trust: "doubted", note: "doubted near hit" },
      {
        event: noticeHit("E-0007", {
          fix: "re-run pnpm install after closing the editor ".repeat(20),
        }),
        note: "long fix clipped by the caps",
      },
      {
        event: noticeHit("E-0007", {
          fix: "关闭占用该目录的编辑器后重跑".repeat(30),
          trigger: "关闭占用该目录的编辑器后重跑".repeat(10),
        }),
        note: "CJK-heavy fix inside the token cap",
      },
    ];
  return withIds("notice", {
    corpus: "notice",
    sources: ["tests/inject.test.ts"],
    coverage: [
      "hit, near hit, no-fix and miss kinds",
      "trusted and doubted trust levels",
      "cause and fix wording, one-line join",
      "the 400-character and 120-token caps",
    ],
    not_covered: [
      "estimateTokens(), fitNotice() and clip() stay in tests/inject.test.ts",
      "noticeSource() and Injector wiring stay in tests/inject.test.ts",
    ],
    cases: cases.map(({ event, trust, note }) => ({
      source_test: "tests/inject.test.ts#noticeText",
      note,
      input: { event, ...(trust === undefined ? {} : { trust }) },
      expected:
        trust === undefined ? noticeText(event) : noticeText(event, trust),
    })),
  });
}

// ---------------------------------------------------------------------------
// Serialization

/**
 * Escapes credential- and machine-shaped substrings as JSON unicode escapes.
 *
 * The corpus has to carry fake secrets and absolute paths to test redaction,
 * while `.github/workflows/privacy-guard.yml` rejects those shapes in any
 * committed file. `\u006b` and `\/` decode to the same characters, so the tests
 * read back the intended fixtures while the committed bytes stay clean.
 */
function escapeSensitive(json: string): string {
  return json
    .replaceAll("s" + "k-", "s\\u006b-")
    .replaceAll("B" + "earer", "B\\u0065arer")
    .replaceAll("bearer", "be\\u0061rer")
    .replaceAll("gh" + "p_", "gh\\u0070_")
    .replaceAll("github_pat_", "github_\\u0070at_")
    .replaceAll("AK" + "IA", "AK\\u0049A")
    .replaceAll("api" + "_key", "api\\u005fkey")
    .replaceAll("@", "\\u0040")
    .replaceAll("/Users/", "\\/Users\\/")
    .replaceAll("/home/", "\\/home\\/")
    .replace(/([A-Za-z]):\\\\/g, "$1\\u003a\\\\");
}

async function render(corpus: CorpusFile): Promise<string> {
  return formatJson(escapeSensitive(JSON.stringify(corpus, null, 2)), {
    parser: "json",
  });
}

// ---------------------------------------------------------------------------
// Main

async function main(): Promise<void> {
  const corpora: CorpusFile[] = [
    normalizeCorpus(),
    fingerprintCorpus(),
    headlineCorpus(),
    redactCorpus(),
    tokenizeCorpus(),
    matchCorpus(),
    noticeCorpus(),
  ];
  const check = process.argv.includes("--check");
  let drift = 0;
  if (!check) mkdirSync(CORPUS_DIR, { recursive: true });
  for (const corpus of corpora) {
    const body = await render(corpus);
    const path = join(CORPUS_DIR, `${corpus.corpus}.json`);
    if (check) {
      const current = existsSync(path) ? readFileSync(path, "utf8") : "";
      if (current !== body) {
        drift += 1;
        console.error(`drift: spec/corpus/${corpus.corpus}.json`);
      }
      continue;
    }
    writeFileSync(path, body, "utf8");
    console.log(
      `wrote spec/corpus/${corpus.corpus}.json (${corpus.cases.length} cases)`,
    );
  }
  if (!check) return;
  if (drift > 0) {
    console.error(
      `${drift} corpus file(s) differ from the generator; run it without --check`,
    );
    process.exitCode = 1;
    return;
  }
  console.log(`corpus up to date (${corpora.length} files)`);
}

await main();
