<!-- ERRORS-history.md — a Markdown document produced by the exporter, with two
     solution versions for the same issue.

     Generated 2026-10-21 with internal/database.ExportMarkdown (the exporter was
     added by EKB-025 and has not changed since) against a real PostgreSQL 16.15
     database whose schema was migrated to 0008. The body of the newest version is
     rendered inline and the older version follows the history comment that
     EKB-025 introduced, so the file exercises the multi-version upgrade path of
     the importer. -->
# ERRORS

## E-0001 · pnpm build: ERR_PNPM_NO_SCRIPT Missing script: "build"
<!-- errkb: sig=4ed4b05db309 cat=command-exit code=ERR_PNPM_NO_SCRIPT first=2026-10-19T08:00:00Z proj=legacy-project -->

- Fingerprint: `4ed4b05db309`
- Category: `command-exit / ERR_PNPM_NO_SCRIPT`
- First seen: 2026-10-19 08:00 · Last seen: 2026-10-19 08:00 · Hits: 1
- Trigger:
- Raw message:
  ```text
  pnpm build: ERR_PNPM_NO_SCRIPT Missing script: "build"
  ```
- Fix:
  Run pnpm run build after adding the script.

  <!-- errkb-history: v1 -->
  Add a build script to package.json.
- Status: `fixed`
- Notes:
  the script may need extra arguments

