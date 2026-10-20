-- 0008_retrieval_latency.sql
--
-- EKB-049 (metrics page) must show the retrieval p50/p95 from measured data.
-- The `retrievals` rows recorded the query and the candidates but never how long
-- the retrieval took, so the statistics view could only quote benchmark numbers
-- (docs/benchmarks.md) — which the card explicitly forbids ("p50/p95 from
-- measured data, not a cached snapshot or an estimate").
--
-- internal/database/rerank.go now times Retrieve with the monotonic clock and
-- stamps every row of the round with the same duration. The column is nullable
-- so rows written before this migration stay valid and are simply excluded from
-- the percentile, rather than being reported as zero.
alter table retrievals
    add column duration_ms integer
    check (duration_ms is null or duration_ms >= 0);

comment on column retrievals.duration_ms is
    'Measured retrieval duration in milliseconds (null on rows written before EKB-049).';
