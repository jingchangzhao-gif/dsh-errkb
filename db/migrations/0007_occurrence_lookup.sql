-- 0007_occurrence_lookup.sql
--
-- EKB-027 follow-up: the retrieval reranker (internal/database/rerank.go) reads
-- the latest occurrence of a candidate issue with
--
--   select ... from occurrences o
--    where o.issue_id = $1
--    order by o.occurred_at desc, o.id desc
--    limit 1
--
-- EKB-020 dropped the (issue_id, event_id) unique constraint so a replayed event
-- could append a second occurrence, and with that constraint the only index that
-- covered this access path disappeared. Every candidate then scanned the whole
-- occurrences table: on the EKB-027 100k-issue dataset (221,803 occurrences) one
-- lateral cost 206.8-236.9 ms and the retrieval class missed its p95 gate.
--
-- This index restores the covering lookup (measured 0.037-0.28 ms) and changes
-- no other behaviour: it is a pure read path for the newest occurrence, and the
-- append-only occurrence writes stay untouched.

create index occurrences_issue_latest_idx
    on occurrences (issue_id, occurred_at desc, id desc);

comment on index occurrences_issue_latest_idx is
    'Covers the latest-occurrence lookup of the retrieval reranker (EKB-027).';
