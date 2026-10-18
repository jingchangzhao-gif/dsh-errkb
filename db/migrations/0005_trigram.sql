-- 0005_trigram.sql
--
-- Fuzzy candidate generation: section 5 step 3 ("PostgreSQL pg_trgm
-- candidate generation"). Candidates feed the Go reranker (EKB-024); this
-- layer never decides that two issues are the same, so its result carries a
-- similarity and the matched fields, never a merge verdict.
--
-- pg_trgm is an optional extension. The migration tries to create it and
-- keeps going when that is impossible: in that case the GIN trigram indexes
-- are not created, `errkb_trigram_available()` reports false, and
-- `errkb_candidates()` takes the bounded fallback below instead of failing.
-- Falling back is explicit (the `degraded` column and the Go wrapper's
-- capability flag), never a silent empty set.
--
-- Bounded by construction:
--
--   * the trigram operator only produces rows above the pg_trgm similarity
--     threshold, which the function sets (transaction-locally) to
--     `p_threshold`. A threshold of 0 is rejected because it would turn the
--     index lookup into a full scan; 0 < p_threshold <= 1 keeps every
--     index match a genuine candidate.
--   * the candidate rows pulled out of the index are capped at the constant
--     candidate pool (500) before the per-issue dedupe.
--   * the result is additionally capped at `p_limit` (1..100) issues.
--
-- Weighted fields: normalized_message, command and tool_name each carry an
-- explicit weight parameter. A field counts as matched when its similarity is
-- at or above `p_threshold`; the score is the weighted sum over the matched
-- fields. Setting a field's weight to zero disables its contribution, which
-- also removes candidates that only matched through that field.
--
-- The fallback is the exact layer only: whole-value equality on the three
-- text fields, bounded by the same `p_limit`, with NULL similarity/score and
-- `degraded = true`.

DO $$
BEGIN
    BEGIN
        -- The extension lives in `public` on purpose: it is a database-wide
        -- object, and pinning its schema keeps the GIN operator class stable
        -- when migrations run from a restricted search_path (the test harness
        -- creates one schema per test).
        EXECUTE 'create extension if not exists pg_trgm schema public';
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'errkb: pg_trgm is unavailable (%), fuzzy candidates will degrade to the exact layer', SQLERRM;
    END;
END;
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        EXECUTE 'create index if not exists events_normalized_message_trgm_idx on events using gin (normalized_message gin_trgm_ops)';
        EXECUTE 'create index if not exists events_command_trgm_idx on events using gin (command gin_trgm_ops)';
        EXECUTE 'create index if not exists events_tool_name_trgm_idx on events using gin (tool_name gin_trgm_ops)';
    ELSE
        RAISE NOTICE 'errkb: pg_trgm is unavailable, GIN trigram indexes were not created';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION errkb_trigram_available()
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
    SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')
$$;

COMMENT ON FUNCTION errkb_trigram_available() IS
    'True when pg_trgm is installed, i.e. when errkb_candidates() can generate fuzzy candidates.';

CREATE OR REPLACE FUNCTION errkb_candidates(
    p_query text,
    p_project_key text DEFAULT NULL,
    p_threshold double precision DEFAULT 0.30,
    p_limit integer DEFAULT 20,
    p_message_weight double precision DEFAULT 1.00,
    p_command_weight double precision DEFAULT 0.40,
    p_tool_name_weight double precision DEFAULT 0.20
)
RETURNS TABLE (
    issue_id uuid,
    fingerprint text,
    title text,
    project_key text,
    match_fields text[],
    similarity double precision,
    score double precision,
    approximate boolean,
    degraded boolean
)
LANGUAGE plpgsql
-- Candidate lookup must be index-driven: with the trigram OR predicate the
-- planner estimates a sequential scan as cheaper at this selectivity and
-- measured 3-4x slower (221 ms vs 53 ms on 23k rows). The hint is scoped to
-- this function only; revisit it together with the 100k-row gate (EKB-027).
SET enable_seqscan = off
AS $$
BEGIN
    IF p_query IS NULL OR btrim(p_query) = '' THEN
        RAISE EXCEPTION 'errkb_candidates: a non-empty query is required' USING ERRCODE = '22023';
    END IF;
    IF p_threshold IS NULL OR p_threshold <= 0 OR p_threshold > 1 THEN
        RAISE EXCEPTION 'errkb_candidates: threshold must be greater than 0 and at most 1' USING ERRCODE = '22023';
    END IF;
    IF p_limit IS NULL OR p_limit < 1 OR p_limit > 100 THEN
        RAISE EXCEPTION 'errkb_candidates: limit must be between 1 and 100' USING ERRCODE = '22023';
    END IF;
    IF coalesce(p_message_weight, -1) < 0
       OR coalesce(p_command_weight, -1) < 0
       OR coalesce(p_tool_name_weight, -1) < 0 THEN
        RAISE EXCEPTION 'errkb_candidates: weights must be non-negative' USING ERRCODE = '22023';
    END IF;
    IF coalesce(p_message_weight, 0) + coalesce(p_command_weight, 0) + coalesce(p_tool_name_weight, 0) <= 0 THEN
        RAISE EXCEPTION 'errkb_candidates: at least one weight must be positive' USING ERRCODE = '22023';
    END IF;

    IF NOT errkb_trigram_available() THEN
        -- Bounded fallback: the exact layer only, explicitly marked degraded.
        RETURN QUERY
        SELECT DISTINCT ON (i.id)
               i.id, i.fingerprint, i.title, i.project_key,
               array_remove(ARRAY[
                   CASE WHEN e.normalized_message = p_query THEN 'normalized_message'::text END,
                   CASE WHEN e.command = p_query THEN 'command'::text END,
                   CASE WHEN e.tool_name = p_query THEN 'tool_name'::text END
               ], NULL) AS match_fields,
               NULL::double precision,
               NULL::double precision,
               false,
               true
        FROM events e
        JOIN occurrences o ON o.event_id = e.event_id
        JOIN issues i ON i.id = o.issue_id
        WHERE (p_project_key IS NULL OR i.project_key = p_project_key)
          AND (e.normalized_message = p_query
               OR e.command = p_query
               OR e.tool_name = p_query)
        ORDER BY i.id, i.hits DESC
        LIMIT p_limit;
        RETURN;
    END IF;

    -- The trigram operator reads this GUC; matching it to p_threshold keeps
    -- the index-backed candidate set exactly as wide as the caller asked.
    PERFORM set_config('pg_trgm.similarity_threshold', p_threshold::text, true);

    RETURN QUERY EXECUTE $q$
        SELECT c.issue_id, c.fingerprint, c.title, c.project_key,
               c.match_fields, c.similarity, c.score, true, false
        FROM (
            SELECT DISTINCT ON (o.issue_id)
                   i.id AS issue_id, i.fingerprint, i.title, i.project_key,
                   s.match_fields, s.similarity, s.score
            FROM (
                -- The candidate pool (hard cap 500, the row limit below) is
                -- applied before the joins, so a broad match never multiplies
                -- candidates by occurrences.
                SELECT s1.event_id,
                       array_remove(ARRAY[
                           CASE WHEN s1.sim_message >= $2::double precision THEN 'normalized_message'::text END,
                           CASE WHEN s1.sim_command >= $2::double precision THEN 'command'::text END,
                           CASE WHEN s1.sim_tool >= $2::double precision THEN 'tool_name'::text END
                       ], NULL) AS match_fields,
                       greatest(s1.sim_message, s1.sim_command, s1.sim_tool)::double precision AS similarity,
                       (CASE WHEN s1.sim_message >= $2::double precision THEN $3::double precision * s1.sim_message ELSE 0 END
                      + CASE WHEN s1.sim_command >= $2::double precision THEN $4::double precision * s1.sim_command ELSE 0 END
                      + CASE WHEN s1.sim_tool >= $2::double precision THEN $5::double precision * s1.sim_tool ELSE 0 END)::double precision AS score
                FROM (
                    -- OFFSET 0 keeps this subquery from being pulled up, so each
                    -- similarity() is evaluated once per candidate event and the
                    -- unweighted fields are not scored again.
                    SELECT e.event_id,
                           CASE WHEN e.normalized_message % $1::text
                                THEN similarity(e.normalized_message, $1::text) ELSE 0 END AS sim_message,
                           CASE WHEN e.command % $1::text
                                THEN similarity(e.command, $1::text) ELSE 0 END AS sim_command,
                           CASE WHEN e.tool_name % $1::text
                                THEN similarity(e.tool_name, $1::text) ELSE 0 END AS sim_tool
                    FROM events e
                    WHERE ($7::text IS NULL OR coalesce(e.project_key, '') = $7::text)
                      AND (e.normalized_message % $1::text
                           OR e.command % $1::text
                           OR e.tool_name % $1::text)
                    OFFSET 0
                ) s1
                WHERE greatest(s1.sim_message, s1.sim_command, s1.sim_tool) >= $2::double precision
                  -- A field whose weight is zero must not create a candidate on
                  -- its own: the score filter keeps the external weights
                  -- effective for both result size and ordering.
                  AND (CASE WHEN s1.sim_message >= $2::double precision THEN $3::double precision * s1.sim_message ELSE 0 END
                     + CASE WHEN s1.sim_command >= $2::double precision THEN $4::double precision * s1.sim_command ELSE 0 END
                     + CASE WHEN s1.sim_tool >= $2::double precision THEN $5::double precision * s1.sim_tool ELSE 0 END) > 0
                ORDER BY greatest(s1.sim_message, s1.sim_command, s1.sim_tool) DESC, s1.event_id
                LIMIT 500
            ) s
            JOIN occurrences o ON o.event_id = s.event_id
            JOIN issues i ON i.id = o.issue_id
            ORDER BY o.issue_id, s.score DESC
        ) c
        ORDER BY c.score DESC, c.issue_id
        LIMIT $6::integer
    $q$
    USING p_query, p_threshold, p_message_weight, p_command_weight, p_tool_name_weight, p_limit, p_project_key;
END;
$$;

COMMENT ON FUNCTION errkb_candidates(text, text, double precision, integer, double precision, double precision, double precision) IS
    'Fuzzy candidate generation (section 5 step 3): weighted pg_trgm similarity over indexed fields, bounded by threshold, a 500-row candidate pool and p_limit; returns candidates only, never a merge verdict. Degrades to the exact layer when pg_trgm is unavailable.';
