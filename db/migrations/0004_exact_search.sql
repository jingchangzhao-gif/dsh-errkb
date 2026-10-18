-- 0004_exact_search.sql
--
-- The exact layer of errkb_search(): the first two steps of section 5's fixed
-- retrieval order.
--
--   1. issue id, exact fingerprint, error code
--   2. deterministic filters over normalized message, command and tool name
--
-- Every branch below is a plain equality predicate on an indexed column. The
-- function intentionally does not support combining dimensions: the caller
-- passes exactly one selector (plus an optional project_key), which keeps every
-- plan a single index lookup and makes the result set easy to reason about.
--
-- Matching semantics (fixed here and pinned by search_test.go):
--
--   * id, fingerprint, error code, normalized message, command and tool name
--     are exact, case-sensitive, whole-value equality; there is no prefix,
--     LIKE, similarity() or trigram matching in this layer (EKB-023 owns the
--     fuzzy candidates).
--   * normalized_message is expected to be normalized by the capture layer
--     (EKB-013) before it reaches the database; this layer never rewrites it.
--   * an unknown selector returns an empty set, while a malformed argument
--     raises SQLSTATE 22023 so callers can tell "no candidates" from "bad
--     request" (EKB-024 depends on that distinction).
--
-- Result shape (shared with EKB-023 and EKB-024):
--
--   issue_id, fingerprint, title, project_key, category, status, hits,
--   last_seen, match_reason, approximate, score
--
-- Exact rows always report approximate = false and score = 1.0. match_reason
-- is one of issue_id, exact_fingerprint, error_code, deterministic_filter.
--
-- Deterministic order: hits desc, last_seen desc, id asc. The id tiebreaker
-- makes the row order total, so equal inputs always return the same sequence.
--
-- Index map:
--
--   issues_pkey / issues_fingerprint_project_key_key  id and fingerprint
--   events_normalized_message_idx                    normalized message filter
--   events_error_code_idx                            error code filter
--   events_command_idx                               command filter
--   events_tool_name_idx                             tool name filter
--   occurrences_issue_id_event_id_key                issue <-> occurrence join
--   occurrences_event_idx                            event <-> occurrence join
--
-- The event filters use EXISTS, so the planner can start from one event index
-- lookup and walk to the issue through the occurrence keys.

CREATE INDEX IF NOT EXISTS events_normalized_message_idx ON events (normalized_message);
CREATE INDEX IF NOT EXISTS events_error_code_idx ON events (error_code) WHERE error_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS events_command_idx ON events (command) WHERE command IS NOT NULL;
CREATE INDEX IF NOT EXISTS events_tool_name_idx ON events (tool_name) WHERE tool_name IS NOT NULL;

CREATE OR REPLACE FUNCTION errkb_search(
    p_issue_id         text DEFAULT NULL,
    p_fingerprint      text DEFAULT NULL,
    p_error_code       text DEFAULT NULL,
    p_normalized_message text DEFAULT NULL,
    p_command          text DEFAULT NULL,
    p_tool_name        text DEFAULT NULL,
    p_project_key      text DEFAULT NULL,
    p_limit            integer DEFAULT 10
) RETURNS TABLE (
    issue_id     uuid,
    fingerprint  text,
    title        text,
    project_key  text,
    category     text,
    status       text,
    hits         integer,
    last_seen    timestamptz,
    match_reason text,
    approximate  boolean,
    score        double precision
)
LANGUAGE plpgsql STABLE
AS $$
DECLARE
    v_selectors integer;
BEGIN
    v_selectors := (p_issue_id IS NOT NULL)::integer
                 + (p_fingerprint IS NOT NULL)::integer
                 + (p_error_code IS NOT NULL)::integer
                 + (p_normalized_message IS NOT NULL)::integer
                 + (p_command IS NOT NULL)::integer
                 + (p_tool_name IS NOT NULL)::integer;

    IF v_selectors = 0 THEN
        RAISE EXCEPTION 'errkb_search: one lookup dimension is required'
            USING ERRCODE = '22023';
    END IF;
    IF v_selectors > 1 THEN
        RAISE EXCEPTION 'errkb_search: pass exactly one lookup dimension'
            USING ERRCODE = '22023';
    END IF;

    IF p_issue_id IS NOT NULL
       AND p_issue_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
        RAISE EXCEPTION 'errkb_search: p_issue_id is not a UUID'
            USING ERRCODE = '22023';
    END IF;

    IF p_fingerprint IS NOT NULL AND p_fingerprint !~ '^[0-9a-f]{12}$' THEN
        RAISE EXCEPTION 'errkb_search: p_fingerprint must be 12 lowercase hex characters'
            USING ERRCODE = '22023';
    END IF;

    IF p_limit IS NULL OR p_limit < 1 OR p_limit > 100 THEN
        RAISE EXCEPTION 'errkb_search: p_limit must be between 1 and 100'
            USING ERRCODE = '22023';
    END IF;

    IF p_issue_id IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'issue_id'::text, false, 1.0::double precision
        FROM issues i
        WHERE i.id = p_issue_id::uuid
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;

    IF p_fingerprint IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'exact_fingerprint'::text, false, 1.0::double precision
        FROM issues i
        WHERE i.fingerprint = p_fingerprint
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;

    IF p_error_code IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'error_code'::text, false, 1.0::double precision
        FROM issues i
        WHERE EXISTS (
            SELECT 1
            FROM events e
            JOIN occurrences o ON o.event_id = e.event_id
            WHERE o.issue_id = i.id
              AND e.error_code = p_error_code
        )
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;

    IF p_normalized_message IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'deterministic_filter'::text, false, 1.0::double precision
        FROM issues i
        WHERE EXISTS (
            SELECT 1
            FROM events e
            JOIN occurrences o ON o.event_id = e.event_id
            WHERE o.issue_id = i.id
              AND e.normalized_message = p_normalized_message
        )
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;

    IF p_command IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'deterministic_filter'::text, false, 1.0::double precision
        FROM issues i
        WHERE EXISTS (
            SELECT 1
            FROM events e
            JOIN occurrences o ON o.event_id = e.event_id
            WHERE o.issue_id = i.id
              AND e.command = p_command
        )
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;

    IF p_tool_name IS NOT NULL THEN
        RETURN QUERY
        SELECT i.id, i.fingerprint, i.title, i.project_key, i.category, i.status,
               i.hits, i.last_seen, 'deterministic_filter'::text, false, 1.0::double precision
        FROM issues i
        WHERE EXISTS (
            SELECT 1
            FROM events e
            JOIN occurrences o ON o.event_id = e.event_id
            WHERE o.issue_id = i.id
              AND e.tool_name = p_tool_name
        )
          AND (p_project_key IS NULL OR i.project_key = p_project_key)
        ORDER BY i.hits DESC, i.last_seen DESC, i.id
        LIMIT p_limit;
        RETURN;
    END IF;
END;
$$;

COMMENT ON FUNCTION errkb_search(text, text, text, text, text, text, text, integer) IS
    'Exact retrieval layer (section 5 steps 1-2): one indexed selector plus an optional project filter; no fuzzy matching.';
