# ERRORS (seed entries)

Curated, generic entries that are worth having before the first error of your
own. They contain no machine names, project names, paths or credentials, and
tests/redact.test.ts fails if redaction would change a single character here.

The file is written by `renderEntry` in src/store.ts with English labels, and
tests/seeds.test.ts checks that it still is. Nothing copies these entries into a
knowledge base yet; that belongs to a later task.

## E-0001 · [llm] CONTEXT_OVERFLOW: the request does not fit the context window
<!-- errkb: sig=88fe8fb1a189 cat=llm code=CONTEXT_OVERFLOW first=2026-10-01T00:00:00Z -->

- Fingerprint: `88fe8fb1a189`
- Category: `llm / CONTEXT_OVERFLOW`
- First seen: 2026-10-01 00:00 · Last seen: 2026-10-01 00:00 · Hits: 0
- Trigger: A long session, or a tool result that pasted a large file into the conversation
- Raw message:
  ```text
  CONTEXT_OVERFLOW: the request exceeds the model's context window
  ```
- Fix:
  Shrink what is sent instead of retrying it unchanged:
  - compact the conversation, or start a new session from a short summary;
  - read large files by line range instead of whole;
  - only if the model really supports more, raise its `contextWindow` in the provider configuration.
- Status: `fixed`
- Notes:
  Seed entry. The message text is illustrative; providers word this error differently.

## E-0002 · [llm] NO_ADAPTER: no adapter for the configured provider
<!-- errkb: sig=bcf6cca60510 cat=llm code=NO_ADAPTER first=2026-10-01T00:00:00Z -->

- Fingerprint: `bcf6cca60510`
- Category: `llm / NO_ADAPTER`
- First seen: 2026-10-01 00:00 · Last seen: 2026-10-01 00:00 · Hits: 0
- Trigger: Starting a session right after editing the provider in a profile
- Raw message:
  ```text
  NO_ADAPTER: no adapter is registered for the configured provider
  ```
- Fix:
  The provider name in the profile matches no installed adapter, almost always because of a typo.
  Compare it with the names of the adapters that are actually installed, correct the spelling, and restart.
- Status: `fixed`
- Notes:
  Seed entry. The message text is illustrative.

## E-0003 · [tool:pwsh] EPERM: operation not permitted, rename
<!-- errkb: sig=3a12e3627689 cat=tool code=EPERM first=2026-10-01T00:00:00Z -->

- Fingerprint: `3a12e3627689`
- Category: `tool / pwsh`
- First seen: 2026-10-01 00:00 · Last seen: 2026-10-01 00:00 · Hits: 0
- Trigger: `pnpm install` on Windows while another process holds a file under `node_modules`
- Raw message:
  ```text
  EPERM: operation not permitted, rename '<path>\node_modules\.pnpm\<hash>'
  ```
- Fix:
  Close whatever holds the directory (an editor, a running dev server, real-time antivirus scanning), then re-run `pnpm install`.
  If it keeps failing, use `pnpm install --config.node-linker=hoisted`.
- Status: `fixed`
- Notes:
  Seed entry. Seen most often under paths with non-ASCII characters.
