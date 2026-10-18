# Labelled retrieval corpus

Ground truth for error identity: each case is a pair of redacted error messages
that a human has labelled as **the same error** or as **lookalikes that must not
be merged**. This directory is the sole basis for the zero-wrong-merge
acceptance in `EKB-024`; the unlabelled conformance corpora next to it
(`spec/corpus/*.json`) constrain *how* messages are normalized, not whether two
messages mean the same thing.

## Files

| File | Set | Answers |
| --- | --- | --- |
| `same-error.json` | `same-error` | The same technical error expressed differently (paths, line numbers, PIDs, timestamps, durations, digests, casing). |
| `distinct-lookalike.json` | `distinct-lookalike` | Errors that look alike but have different fixes; merging them is a failure. |
| `cross-language.json` | `cross-language` | The same error in Chinese, in English, and at CJK/Latin token boundaries. |

Every case has a `pair`. Side `a` is the stored entry (the previous
occurrence); side `b` is the new capture being matched against it. The
direction matters: a retrieval result is wrong when `b` is merged into `a`.
Sides carry `category`, an optional `code`, and the `message`. Some cases add
`command` or `tool_name` when the command or the tool is what separates the
two; `cross-language.json` adds `language`.

## Labels

| Label | Meaning |
| --- | --- |
| `same-error` | Same underlying technical problem. One fix resolves both; reusing the stored fix is correct. |
| `distinct-lookalike` | Similar text or the same error code, but a different problem. The stored fix does not apply and must not be injected. |
| `uncertain` | The annotator could not decide. Kept for completeness but **excluded from every metric**. |

`distinct-lookalike` cases also carry `lookalike_kind`:

| `lookalike_kind` | The trap |
| --- | --- |
| `same-code-different-cause` | The same error code covers unrelated root causes. |
| `similar-message-different-command` | The message is identical or nearly so; only the triggering command or tool tells them apart. |
| `same-cause-different-tool` | The same root cause surfaced through different tools; the fixes are independent (for example two different volumes filling up). |

## Field reference

```json
{
  "case_id": "same-error-001",
  "pair": {
    "a": { "category": "tool", "code": "ENOENT", "message": "..." },
    "b": { "category": "tool", "code": "ENOENT", "message": "..." }
  },
  "label": "same-error",
  "why": "One sentence a human can check without running anything.",
  "lookalike_kind": "only on distinct-lookalike cases",
  "measured": {
    "normalized_equal": true,
    "fingerprint_equal": true,
    "token_jaccard": 1.0,
    "baseline": "exact"
  },
  "confirmed_by": "jingchangzhao-gif",
  "confirmed_at": "2026-10-19"
}
```

- `case_id` — `<set>-NNN`, stable, never reused.
- `why` — the human-readable basis; it must stand alone.
- `measured` — informational snapshot of the TypeScript implementation at
  annotation time: `normalize` equality, `signature` (fingerprint) equality,
  Jaccard over `tokenize(normalize(message))`, and how the baseline `match()`
  scored side `b` against side `a` (`exact`, `fuzzy`, `code`, or `miss`). It is
  not part of the label; it shows which traps are live.
- `confirmed_by` / `confirmed_at` — who accepted the label and when (UTC
  date). A case without both does not count, and annotations made by the
  system under test do not count either.

## Annotation process

1. Candidate pairs come from captured errors, from the existing test fixtures,
   or from a constructed scenario; AI may propose pairs and suggested labels.
2. The text is redacted first (see below).
3. AI records why the pair is the same or different and measures it with the
   current implementation. Suggested labels stay suggestions.
4. The project owner (`jingchangzhao-gif`) reviews the case list and confirms
   or corrects each label; only confirmed labels are written as final, with
   `confirmed_by` and `confirmed_at`.
5. A correction is added as a new commit that changes the label, the `why`
   text, and the confirmation tuple together; labels are never edited silently.

## Redaction

The corpus is public, so every message is written as if it were already
redacted: no API keys, tokens or bearer headers; no per-user home directories;
no drive-letter paths; no real request IDs or customer hostnames. Machine
locations use the repository's usual placeholders (`/opt/...`, `/srv/...`,
`%LOCALAPPDATA%`, `<repo-root>`) and synthetic fixtures are clearly synthetic
(`left-pad`, `example.net`). Two scans must stay clean:

- the repository `privacy-guard` workflow, and
- the acceptance scan for `sk-`, `Bearer`, `api_key`, and `D:\`.

## Known uncovered scope

- Only Chinese (`zh-CN`) and English, and only one encoding-mismatch case
  (`cross-language-007`, labelled `uncertain`). Japanese/Korean and other
  locales are absent.
- Only technical errors; business errors are out of scope for v1.
- Two sides per case: no multi-error logs, no issue-level grouping, no
  aggregate (`issue_id`, `hit_count`) material.
- No timing, flakiness, or ordering cases; no performance data.
- Categories are `tool` and `command-exit`; `llm` and `agent` payloads are
  missing.
- The set is small (29 cases) and curated, not sampled from real users; it
  proves the known traps, not the full distribution.
- Embeddings are not represented because v1 does not use them.

## How EKB-024 uses this

For each `distinct-lookalike` case, matching side `b` against side `a` must not
produce an auto-merged, injectable hit through any path (exact fingerprint,
fuzzy similarity, or code fallback); at most it may surface side `a` as one of
the at most three explained candidates requiring user confirmation. **Zero
wrong merges** means zero such auto-merges across the whole set. Recall on
`same-error` cases (including the confirmed `cross-language` ones) is reported
secondarily; `uncertain` cases are excluded from both numbers. The corpus is an
oracle, so it is applied unchanged by the Go implementation and by the
TypeScript baseline alike.
