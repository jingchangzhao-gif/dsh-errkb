# Workflow control center

[简体中文](README.zh-CN.md)

This directory is the single resume point for project repair and cleanup work. It contains the active queue, the evidence behind cleanup decisions and the exact file order for upcoming repairs.

## Read in this order

1. [Current workflow and open issues](current-workflow.md) — the unresolved queue. Start with the only `NEXT` row.
2. [File repair sequence](file-repair-sequence.md) — files, edits, tests and completion gates for each workflow ID.
3. [Unused-code audit](unused-code-audit.md) — what is disposable, stale, dormant, compatibility-only or active.
4. [Project status](../project-status.md) — current delivery truth and release boundary.
5. [Implementation plan](../implementation-plan.md) — product decisions and historical EKB task order.

## Ownership rules

- `current-workflow.md` is the authority for **what remains open and what comes next**.
- `file-repair-sequence.md` is the authority for **which files are changed and in what order**.
- `unused-code-audit.md` is the authority for **what may be deleted and under which condition**.
- `project-status.md` remains the authority for **what is currently usable or releasable**.
- `implementation-plan.md` preserves product decisions, completed-card evidence and acceptance criteria.

Do not create another active todo list elsewhere. When an issue is solved, record durable evidence in the appropriate status, plan, acceptance or CHANGELOG file and remove the row from both current-workflow language versions.

## Directory contents

| English | 中文 | Purpose |
| --- | --- | --- |
| `README.md` | `README.zh-CN.md` | Folder index and document ownership |
| `current-workflow.md` | `current-workflow.zh-CN.md` | Ordered unresolved issues only |
| `file-repair-sequence.md` | `file-repair-sequence.zh-CN.md` | Per-issue file order and verification |
| `unused-code-audit.md` | `unused-code-audit.zh-CN.md` | Cleanup classification and deletion gate |
