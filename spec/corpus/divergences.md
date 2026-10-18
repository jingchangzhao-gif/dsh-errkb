# Cross-language divergences

The Go implementation is authoritative. The plan's section 8.2 status column decides what a
difference means: `src/signature.ts` and `src/redact.ts` are **Keep** (the Go side must reproduce
them exactly, so a difference there is a Go bug or a corpus bug), while `src/match.ts` and
`src/store.ts` are **downgraded** (no authority; differences are recorded here instead of being
chased), and `src/inject.ts` is a DSH connector with no Go counterpart.

The differential suite (`internal/core/diff_test.go`) compares the Go output against each corpus
case's expected value, field by field. The corpus is generated from the TypeScript implementation
by `tests/gen-corpus.ts` (EKB-009), so the expected values are the TypeScript oracle; the
generator's `--check` mode keeps that oracle current.

The rule is "no undocumented divergence", not "zero divergence". Every registered row is locked by
the test named in its last column: if the shape of the gap changes, that test fails and the
registry has to be updated in the same change. Rows may not be removed by silently deleting the
lock, and no difference may be handled with `t.Skip`.

## Recorded divergences

| Corpus   | Fields                                | Go behaviour                                                                                                                                                                   | TypeScript behaviour                                                                                                                                                                                                     | Class               | Locking test                         |
| -------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------- | ------------------------------------ |
| `match`  | `matched`, `id`, `via`, `approximate` | No Go counterpart: the Go side owns tokenization and similarity; ranking is `Rerank` (EKB-017), which returns scored candidates with match reasons instead of a match verdict. | `match()` returns a full verdict, but `src/match.ts` is downgraded, so it has no authority.                                                                                                                              | downgraded-ts       | `TestDiffMatchSemanticsAreNotPorted` |
| `match`  | `similarity`                          | Token overlap (`Jaccard`) only; there is no match route on the Go side.                                                                                                        | Route-defined: `1` for an exact fingerprint hit, `0` for the code fallback, token overlap for a fuzzy hit. For an exact hit the two values therefore differ (the corpus case reports `1` while the Go overlap is lower). | downgraded-ts       | `TestDiffMatchSimilarity`            |
| `notice` | `kind`, `text`                        | No Go counterpart: notice composition belongs to the DSH connector (`src/inject.ts`, a Keep implementation with no Go side).                                                   | `noticeText()` composes the notice; `notice.json` stays TypeScript-owned.                                                                                                                                                | intentional-absence | `TestDiffNoticeIsTypeScriptOnly`     |

Current recorded divergences: 3. No other divergence is known; the remaining corpus fields
(`headline.line`, `headline.code`, `normalize.normalized`, `fingerprint.fingerprint`,
`tokenize.tokens`, fuzzy `match.similarity`, `redact.redacted`) agree exactly in both
implementations.

## P2 closure evidence

- Zero-I/O import scan: `go test ./internal/core/ -run DiffNoIOImports` parses every non-test file
  in `internal/core` and `internal/redact` and rejects `os`, `net`, `net/http`, `os/exec`,
  `io/ioutil`, `math/rand` and `crypto/rand`. It passes.
- Full differential run: `go test ./internal/core/ -run Diff` passes.
- Labelled smoke: `TestDiffLabelledSmoke` re-runs the distinct-lookalike corpus through `Rerank`;
  all 12 pairs stay approximate.
