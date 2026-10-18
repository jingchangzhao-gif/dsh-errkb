-- Migration 0006: backup, restore, retention and scoped deletion (EKB-026).
--
-- Three retention classes are independent, as required by section 6:
--   * the redacted sample may expire first and is erased in place;
--   * the normalized text (the event row itself) expires next and the whole
--     observation is removed, which cascades to its occurrences;
--   * audit rows live longest and are only removed by the retention job.
--
-- Deletion scopes are explicit: by project, by connector and by time. The time
-- boundary is exclusive on events.observed_at: an observation whose timestamp is
-- exactly the cutoff is kept. Deletion never removes audit rows, and it writes
-- one audit row itself (action delete.scope) with the scope and the affected
-- counts. A preview function reports the same counts without deleting anything.
--
-- audit_log has no foreign keys (0001), so a business cascade cannot reach it.
-- The trigger below additionally rejects an accidental manual DELETE or
-- TRUNCATE; only the retention job, which sets errkb.audit_retention, may
-- remove expired audit rows.

CREATE INDEX audit_log_occurred_at_idx ON audit_log (occurred_at);
CREATE INDEX audit_log_retain_until_idx ON audit_log (retain_until) WHERE retain_until IS NOT NULL;
CREATE INDEX events_connector_observed_idx ON events (connector_id, observed_at);
CREATE INDEX occurrences_occurred_at_idx ON occurrences (occurred_at);
CREATE INDEX outcomes_observed_at_idx ON outcomes (observed_at);
CREATE INDEX retrievals_created_at_idx ON retrievals (created_at);
CREATE INDEX notices_created_at_idx ON notices (created_at);

CREATE FUNCTION errkb_audit_guard() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('errkb.audit_retention', true) IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION 'audit_log is append-only; only the retention job may remove audit rows'
            USING ERRCODE = '55000';
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NULL END;
END $$;

CREATE TRIGGER audit_log_protect
    BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION errkb_audit_guard();

CREATE FUNCTION errkb_audit_no_truncate() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_log cannot be truncated; use the retention job'
        USING ERRCODE = '55000';
END $$;

CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION errkb_audit_no_truncate();

COMMENT ON TRIGGER audit_log_no_truncate ON audit_log IS
    'Audit rows are removed one by one by errkb_purge_retention() so the removal itself is counted and logged.';

-- Retention defaults match the policy defaults documented in backup.go; the
-- connector configuration (spec/config.cue retention) decides the actual days.
CREATE FUNCTION errkb_purge_retention(
    p_sample_days     integer DEFAULT 7,
    p_normalized_days integer DEFAULT 90,
    p_audit_days      integer DEFAULT 365,
    p_now             timestamptz DEFAULT now()
) RETURNS TABLE (kind text, removed bigint)
LANGUAGE plpgsql
AS $$
DECLARE
    v_sample_cutoff     timestamptz;
    v_normalized_cutoff timestamptz;
    v_audit_cutoff      timestamptz;
    v_samples           bigint := 0;
    v_events            bigint := 0;
    v_audit             bigint := 0;
BEGIN
    IF p_sample_days < 0 OR p_normalized_days < 0 OR p_audit_days < 0 THEN
        RAISE EXCEPTION 'retention days must be zero or positive'
            USING ERRCODE = '22023';
    END IF;

    v_sample_cutoff     := p_now - make_interval(days => p_sample_days);
    v_normalized_cutoff := p_now - make_interval(days => p_normalized_days);
    v_audit_cutoff      := p_now - make_interval(days => p_audit_days);

    -- Samples expire first, in place, but only on rows whose normalized text
    -- still lives; the later rows are deleted whole below.
    UPDATE events
       SET redacted_sample = ''
     WHERE observed_at < v_sample_cutoff
       AND observed_at >= v_normalized_cutoff
       AND redacted_sample <> '';
    GET DIAGNOSTICS v_samples = ROW_COUNT;

    DELETE FROM events WHERE observed_at < v_normalized_cutoff;
    GET DIAGNOSTICS v_events = ROW_COUNT;

    PERFORM set_config('errkb.audit_retention', 'on', true);
    DELETE FROM audit_log WHERE occurred_at < v_audit_cutoff;
    GET DIAGNOSTICS v_audit = ROW_COUNT;
    PERFORM set_config('errkb.audit_retention', 'off', true);

    INSERT INTO audit_log (action, entity, entity_id, operator, source, detail)
    VALUES ('retention.purge', 'retention', '', 'retention', 'retention',
            jsonb_build_object(
                'now', p_now,
                'sample_days', p_sample_days,
                'normalized_days', p_normalized_days,
                'audit_days', p_audit_days,
                'samples_cleared', v_samples,
                'events_removed', v_events,
                'audit_removed', v_audit));

    RETURN QUERY VALUES
        ('sample', v_samples),
        ('normalized', v_events),
        ('audit', v_audit);
END $$;

COMMENT ON FUNCTION errkb_purge_retention(integer, integer, integer, timestamptz) IS
    'Three independent retention classes. Samples are erased in place, expired events are deleted (occurrences cascade), and audit rows are removed only behind the errkb.audit_retention guard.';

CREATE FUNCTION errkb_delete_preview(
    p_project_key text DEFAULT NULL,
    p_connector_id uuid DEFAULT NULL,
    p_before timestamptz DEFAULT NULL
) RETURNS TABLE (kind text, rows bigint)
LANGUAGE plpgsql
STABLE
AS $$
BEGIN
    IF p_project_key IS NULL AND p_connector_id IS NULL AND p_before IS NULL THEN
        RAISE EXCEPTION 'at least one deletion filter (project, connector or before) is required'
            USING ERRCODE = '22023';
    END IF;

    RETURN QUERY
    WITH target_issues AS (
        SELECT i.id FROM issues i
        WHERE p_project_key IS NOT NULL AND i.project_key = p_project_key
    ), target_events AS (
        SELECT e.event_id FROM events e
        WHERE (p_project_key IS NULL OR e.project_key = p_project_key)
          AND (p_connector_id IS NULL OR e.connector_id = p_connector_id)
          AND (p_before IS NULL OR e.observed_at < p_before)
    ), target_occurrences AS (
        SELECT o.id FROM occurrences o
        WHERE o.issue_id IN (SELECT id FROM target_issues)
           OR o.event_id IN (SELECT event_id FROM target_events)
           OR (p_connector_id IS NOT NULL AND o.connector_id = p_connector_id
               AND (p_before IS NULL OR o.occurred_at < p_before))
           OR (p_before IS NOT NULL AND p_project_key IS NULL AND p_connector_id IS NULL
               AND o.occurred_at < p_before)
    ), target_solutions AS (
        SELECT s.id FROM solutions s WHERE s.issue_id IN (SELECT id FROM target_issues)
    )
    SELECT 'issues', count(*) FROM target_issues
    UNION ALL
    SELECT 'solutions', count(*) FROM target_solutions
    UNION ALL
    SELECT 'solution_versions', count(*) FROM solution_versions v
        WHERE v.solution_id IN (SELECT id FROM target_solutions)
    UNION ALL
    SELECT 'outcomes', count(*) FROM outcomes u
        WHERE u.issue_id IN (SELECT id FROM target_issues)
           OR u.occurrence_id IN (SELECT id FROM target_occurrences)
           OR (p_before IS NOT NULL AND u.observed_at < p_before)
    UNION ALL
    SELECT 'retrievals', count(*) FROM retrievals r
        WHERE r.candidate_issue_id IN (SELECT id FROM target_issues)
           OR (p_before IS NOT NULL AND r.created_at < p_before)
    UNION ALL
    SELECT 'notices', count(*) FROM notices n
        WHERE n.issue_id IN (SELECT id FROM target_issues)
           OR (p_before IS NOT NULL AND n.created_at < p_before)
    UNION ALL
    SELECT 'occurrences', count(*) FROM target_occurrences
    UNION ALL
    SELECT 'events', count(*) FROM target_events
    UNION ALL
    SELECT 'audit_log', 0::bigint;
END $$;

COMMENT ON FUNCTION errkb_delete_preview(text, uuid, timestamptz) IS
    'Reports exactly the rows errkb_delete_scope() would remove. Audit rows are never deleted by a scope; the audit_log row counts 0.';

CREATE FUNCTION errkb_delete_scope(
    p_project_key text DEFAULT NULL,
    p_connector_id uuid DEFAULT NULL,
    p_before timestamptz DEFAULT NULL,
    p_actor text DEFAULT 'owner'
) RETURNS TABLE (kind text, removed bigint)
LANGUAGE plpgsql
AS $$
DECLARE
    v_issue_ids         uuid[] := '{}';
    v_event_ids         uuid[] := '{}';
    v_occurrence_ids    uuid[] := '{}';
    v_solution_ids      uuid[] := '{}';
    v_issues            bigint := 0;
    v_solutions         bigint := 0;
    v_solution_versions bigint := 0;
    v_outcomes          bigint := 0;
    v_retrievals        bigint := 0;
    v_notices           bigint := 0;
    v_occurrences       bigint := 0;
    v_events            bigint := 0;
BEGIN
    IF p_project_key IS NULL AND p_connector_id IS NULL AND p_before IS NULL THEN
        RAISE EXCEPTION 'at least one deletion filter (project, connector or before) is required'
            USING ERRCODE = '22023';
    END IF;
    IF p_actor IS NULL OR trim(p_actor) = '' THEN
        RAISE EXCEPTION 'an actor is required for a deletion' USING ERRCODE = '22023';
    END IF;

    -- The function body is one statement, so every delete below commits or rolls
    -- back together with the audit row that records the scope.
    SELECT coalesce(array_agg(i.id), '{}') INTO v_issue_ids
      FROM issues i
     WHERE p_project_key IS NOT NULL AND i.project_key = p_project_key;

    SELECT coalesce(array_agg(e.event_id), '{}') INTO v_event_ids
      FROM events e
     WHERE (p_project_key IS NULL OR e.project_key = p_project_key)
       AND (p_connector_id IS NULL OR e.connector_id = p_connector_id)
       AND (p_before IS NULL OR e.observed_at < p_before);

    SELECT coalesce(array_agg(o.id), '{}') INTO v_occurrence_ids
      FROM occurrences o
     WHERE o.issue_id = any(v_issue_ids)
        OR o.event_id = any(v_event_ids)
        OR (p_connector_id IS NOT NULL AND o.connector_id = p_connector_id
            AND (p_before IS NULL OR o.occurred_at < p_before))
        OR (p_before IS NOT NULL AND p_project_key IS NULL AND p_connector_id IS NULL
            AND o.occurred_at < p_before);

    SELECT coalesce(array_agg(s.id), '{}') INTO v_solution_ids
      FROM solutions s WHERE s.issue_id = any(v_issue_ids);

    DELETE FROM outcomes u
     WHERE u.issue_id = any(v_issue_ids)
        OR u.occurrence_id = any(v_occurrence_ids)
        OR (p_before IS NOT NULL AND u.observed_at < p_before);
    GET DIAGNOSTICS v_outcomes = ROW_COUNT;

    DELETE FROM retrievals r
     WHERE r.candidate_issue_id = any(v_issue_ids)
        OR (p_before IS NOT NULL AND r.created_at < p_before);
    GET DIAGNOSTICS v_retrievals = ROW_COUNT;

    DELETE FROM notices n
     WHERE n.issue_id = any(v_issue_ids)
        OR (p_before IS NOT NULL AND n.created_at < p_before);
    GET DIAGNOSTICS v_notices = ROW_COUNT;

    DELETE FROM occurrences o WHERE o.id = any(v_occurrence_ids);
    GET DIAGNOSTICS v_occurrences = ROW_COUNT;

    DELETE FROM solution_versions v WHERE v.solution_id = any(v_solution_ids);
    GET DIAGNOSTICS v_solution_versions = ROW_COUNT;

    DELETE FROM solutions s WHERE s.id = any(v_solution_ids);
    GET DIAGNOSTICS v_solutions = ROW_COUNT;

    DELETE FROM events e WHERE e.event_id = any(v_event_ids);
    GET DIAGNOSTICS v_events = ROW_COUNT;

    DELETE FROM issues i WHERE i.id = any(v_issue_ids);
    GET DIAGNOSTICS v_issues = ROW_COUNT;

    INSERT INTO audit_log (action, entity, entity_id, operator, source, detail)
    VALUES ('delete.scope', 'scope',
            coalesce(p_project_key, '') || '|' || coalesce(p_connector_id::text, ''),
            p_actor, 'user',
            jsonb_build_object(
                'project_key', p_project_key,
                'connector_id', p_connector_id,
                'before', p_before,
                'counts', jsonb_build_object(
                    'issues', v_issues,
                    'solutions', v_solutions,
                    'solution_versions', v_solution_versions,
                    'outcomes', v_outcomes,
                    'retrievals', v_retrievals,
                    'notices', v_notices,
                    'occurrences', v_occurrences,
                    'events', v_events)));

    RETURN QUERY VALUES
        ('issues', v_issues),
        ('solutions', v_solutions),
        ('solution_versions', v_solution_versions),
        ('outcomes', v_outcomes),
        ('retrievals', v_retrievals),
        ('notices', v_notices),
        ('occurrences', v_occurrences),
        ('events', v_events);
END $$;

COMMENT ON FUNCTION errkb_delete_scope(text, uuid, timestamptz, text) IS
    'Deletes the same rows errkb_delete_preview() reports, in one transaction, and records one delete.scope row in audit_log. Audit rows are never part of the scope.';
