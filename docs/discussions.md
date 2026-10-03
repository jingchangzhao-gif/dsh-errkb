# Open design discussions

Six questions that the design document (`docs/设计说明书.md`) either leaves open or does not raise. Each one states the problem, the options and a recommendation, and ends with questions for the owner. Items are numbered so they can be answered line by line in review; the answers belong in §17 / §19 of the design document once settled.

Several items are now decided and implemented; each says so at its top, with a pointer to the code: 1.2, 2(a), 2(c), 4 and 5.1–5.2, plus §17 Q2 and Q4 (topic 6). Everything else is still an open question for the owner, and nothing here decides it.

---

## 1. Coexistence with other notice-injecting plugins

**1.2 decided (T12):** yes. A hit ends with "Known fix: try this first, before re-diagnosing or researching." instead of "Apply this; do not re-diagnose." It orders the work rather than forbidding any, so it does not contradict a research nudge on the same step. Implemented in `src/inject.ts` (`WORDING`). 1.1 and 1.3 stay open.

**Problem.** Our caps (≤ 1 notice per step, ≤ 3 per turn, ≤ 2 per ID per session, §7) only count our own notices. Other `dsh` plugins inject on the same hooks:

- `dsh-research-nudge` prepends a "go and research" notice through `tools/post-execute` → `additionalContexts` — our injection point.
- `dsh-error-audit` notifies the model that "an error occurred" from `agent/error` / `tools/result` — our capture hooks.

On one failing step the model can therefore read "apply E-0007, do not re-diagnose" next to "stop and research this" and "an error occurred, inspect the logs". The messages contradict each other, and the token budget in the cost model assumes ours is the only notice.

**Options.**

- **A — Document only.** A "Compatibility" note in both READMEs naming the overlap, plus `inject: 'off'` as the escape hatch.
- **B — Detect and defer.** At `apply`, look for known co-installed plugins and suppress or reword our notice when one of them fires on the same step. Fragile: it depends on other plugins' internals and names.
- **C — Shared convention upstream.** Propose a small convention in `dsh` (for example a per-step notice budget, or a `priority` on `additionalContexts`) so plugins can see each other's notices.

**Recommendation.** A now, C later. Add a T13 integration test that installs `dsh-research-nudge` alongside and asserts our notice is still present, still ≤ 120 tokens, and still the first plugin notice on that step (or records what actually happens).

**Questions.**

1.1. Is A enough for the first release?
1.2. Should our notice wording acknowledge other advice ("known fix; try this before researching") rather than "do not re-diagnose"?
1.3. Is a proposal to `dsh` upstream (option C) in scope for this project at all?

---

## 2. Fingerprint gaps (T06)

**(a) Multi-line output.** **Decided (T10):** the proposal below, implemented in `src/capture.ts` (`extractHeadline`), with the traceback rule checked first. `tsc` with forty errors, a `pnpm install` log, or a Python traceback (where the *last* line matters) all arrive as one block. Fingerprinting the whole block means one extra error gives a new ID. Proposal: a headline extractor that runs before normalisation —

1. the first line matching `ERR_[A-Z_]+|E[A-Z]{2,}|[A-Z]\w*Error|error TS\d+`;
2. for a traceback (`Traceback (most recent call last):`), the last non-empty line;
3. otherwise the last non-empty line.

The matched code (`ERR_PNPM_…`, `EPERM`, `TS2307`, `ModuleNotFoundError`) is stored as a strong key next to `sig`.

**(b) Localised OS text.** **Partly implemented, rule still open (2.2):** `src/match.ts` has a code fallback that gives a near hit when category and code agree and both messages are under 40 characters after normalisation (`SHORT_MESSAGE_CHARS`). It does not compare filenames, and a longer localised message with a different wording still misses. On a Chinese Windows system, EPERM can surface as `拒绝访问` rather than `operation not permitted`. Same error, two signatures, two IDs. Proposal: when category + code + filename agree, treat it as a near hit (reuse the ID, mark "approximate match, verify first") even if the text differs.

**(c) Jaccard on CJK.** **Decided and implemented (T09):** Latin word tokens plus CJK character bigrams, in `src/match.ts` (2.3). §5.2 computes Jaccard over a "normalised token set" without saying how tokens are split. If they are split on whitespace, a Chinese message has no spaces, so it becomes one token and Jaccard is 0 or 1 — the 0.72 threshold means nothing. Proposal: Latin word tokens plus CJK character bigrams (`拒绝访问` → `拒绝`, `绝访`, `访问`).

**Questions.**

2.1. Accept the headline extractor and its regex order as the T06 starting point?
2.2. Is "category + code + filename" the right near-hit rule for (b), or should it be category + code only?
2.3. Accept CJK bigrams for (c)?

---

## 3. A labelled corpus before trusting 0.72

**Not implemented; still open.** There is no `tests/fixtures/corpus/` and no threshold sweep. The T09 tests pin behaviour at 0.71 / 0.72 / 0.73 only, so 0.72 remains a placeholder.

**Problem.** T09 checks that 0.71 / 0.72 / 0.73 behave as coded. It does not check that 0.72 is the right number. A threshold that is too low merges different errors under one ID — the worst failure, because the wrong fix is then injected with "do not re-diagnose".

**Proposal.**

- `tests/fixtures/corpus/`, one folder per real error, each holding several samples of that error (different paths, line numbers, PIDs, locales).
- A `distinct-lookalikes/` folder of pairs that must *not* merge, e.g. `ENOENT … package.json` vs `ENOENT … tsconfig.json`, `TS2307` for two different modules.
- A test that sweeps the threshold from 0.50 to 0.95 and reports false merges and misses at each step; pick the value with **zero false merges and the fewest misses**.
- CI fails on any new false merge. Every sample passes through `redact.ts` before it is committed.

**Questions.**

3.1. Is the corpus part of T09, or a new task?
3.2. Who supplies the first real samples — the owner's own sessions, or synthetic ones?
3.3. Should the threshold be per category (tool vs LLM errors) if the sweep shows they differ?

---

## 4. Fix trust: a negative signal

**Decided (T12):** the proposal below, implemented in `src/inject.ts` (`FixTrust`, `trustLevel`). Answers: 4.1 — the 1 / 2 thresholds are accepted (`DOUBT_AFTER`, `SUPPRESS_AFTER`). 4.2 — "recurred" means the same entry is captured again later in the same turn that injected its fix. 4.3 — yes: the counts are kept against a hash of the fix text, so editing the fix starts them again. A recorded success (`succeeded`, fed by resolution detection in T14) lifts the suppression. The state is a plain serializable object behind an injectable `TrustStore`, persisted by `src/state.ts` in `state.json`, which is machine-local and never committed; it is never written into `ERRORS.md`. Listing suppressed IDs in `err_stats` belongs to T15.

**Problem.** Nothing in the design notices that a fix did not work. If E-0007 is injected and the same error recurs on the next step, the next notice still says "apply this, do not re-diagnose". Only a human marking `wontfix` or a misjudgment stops it.

**Proposal.** Per ID, in `state.json` (machine-local, never in git): `injected` and `recurredAfterInject`.

- After **1** recurrence: the notice changes to "this fix failed here last time; verify before applying".
- After **2** recurrences with no success: stop auto-injecting that ID on this machine, and list it in `err_stats` as "suppressed — fix did not work here".
- **Never** edit `ERRORS.md` automatically. The human decides whether the fix is wrong.

Prior art: `compounded` promotes lessons after clean uses and demotes them when they steer a task wrong.

**Questions.**

4.1. Accept the 1 / 2 thresholds?
4.2. What counts as "recurred": same ID within the same turn, or within N steps?
4.3. Should a suppressed ID reset after the entry's fix text is edited?

---

## 5. Redaction cannot be complete → revisit §17 Q4

**5.1 decided (T07):** one shared pattern list in `src/redact-patterns.ts`, used by `src/redact.ts`; `tests/redact-patterns.test.ts` checks it against the regular expression in `.github/workflows/privacy-guard.yml`, which as a shell step keeps its own copy. **5.2 decided (§17 Q4):** real entries go in a private KB repository; this repository ignores `errors/` and publishes only curated, redacted entries in `seeds/`. **5.3 stays open:** nothing records `device` or `proj` yet, so whether public mode hashes or drops them has not had to be answered.

**Problem.** §4.4 replaces `sk-`, `Bearer`, `api_key=`, `token=`, long base64/hex, e-mail and request IDs. It would still let through:

- internal hostnames and IP addresses;
- `user@host` in SSH and git errors (not an e-mail with a public domain);
- short tokens below the length thresholds;
- provider keys with other prefixes (`xai-`, `AIza…`);
- the sample entry in §8 itself: `device=DESKTOP-A proj=报错的回收再利用` publishes a machine name and a project name.

`privacy-guard.yml` already knows `ghp_`, `github_pat_` and `AKIA`, which §4.4 does not list — the two lists have drifted before any code exists. `dsh-error-lens` states the same limit in its README: text-rule redaction cannot cover every format.

**Proposals.**

- One shared pattern list, used by both `redact.ts` and the CI privacy guard.
- CI runs `redact()` over `errors/*.md` and fails if the output differs from the input (i.e. something unredacted was committed).
- `device` and `proj` are hashed or omitted when `share: 'public'`.
- For Q4: prefer a **private** KB repository for real entries, and keep only curated public seed entries in this repository.

**Questions.**

5.1. Accept one shared pattern list?
5.2. Change the Q4 default to "private KB repository; public seeds only"?
5.3. Should `device` / `proj` be hashed or dropped in public mode?

---

## 6. §17 decisions, with recommendations

**Q2 and Q4 are decided**, as recorded in both READMEs: English labels by default with `labels: 'zh'`, both always parsed (Q2); a private KB repository with public seeds only (Q4). The other six rows are still open; the recommendations below are not decisions.

| # | Question | Recommendation |
| - | -------- | -------------- |
| Q1 | Names | Keep `dsh-errkb` / `err-kb` / `err_` / `E-`. Both npm names were unclaimed on 2026-10-01; consider reserving `dsh-errkb` with a `0.0.0` placeholder |
| Q2 | Field-name language | English labels, with a `labels: 'zh'` option. Parsing reads only the `<!-- errkb: … -->` comment and the fix field, so labels are presentation |
| Q3 | Counters out of git | Accept |
| Q4 | Commit `errors/` | See topic 5: private KB repository, public seeds only |
| Q5 | Scope of reuse | Accept one shared KB; on equal scores, prefer entries from the same project |
| Q6 | `captureExitCodes` default | On — provided the headline extractor (topic 2a) lands in T06; otherwise noisy |
| Q7 | P7 extras | Obsidian export only. Decide whether `err_export` (JSON backup, §15 P7) stays or is cut |
| Q8 | Package root | Accept |

Two items outside §17:

- **OS support statement.** Only Windows is verified. State that, and decide whether Linux/macOS are "expected to work" or "unsupported".
- **T19 — publish to npm.** Name reservation, `files` check, provenance, a release workflow. Nothing in T01–T18 covers it.

**Questions.**

6.1. Which rows can be accepted as written?
6.2. Add T19?
