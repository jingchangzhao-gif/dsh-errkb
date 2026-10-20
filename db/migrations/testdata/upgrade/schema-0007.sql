-- schema-0007.sql — a real PostgreSQL 16.15 dump of the database at migration
-- 0007, used by the EKB-053 upgrade tests.
--
-- Generated 2026-10-21 from the repository at commit 84f8895:
--   1. created an empty database and applied db/migrations/0001..0007 with psql;
--   2. created the migrator ledger (internal/database/migrate.go ledgerSetup) and
--      recorded the seven rows with the SHA-256 checksums of those files;
--   3. pg_dump --schema-only --no-owner --no-privileges;
--   4. removed the pg_dump \restrict/\unrestrict lines and the statement that
--      resets search_path, and dropped the public. qualifier so the file can be
--      loaded into any test schema.
-- The ledger rows below are not part of a schema dump; they are added back so
-- Migrate treats 0001..0007 as already applied.
--
-- PostgreSQL database dump
--


-- Dumped from database version 16.15
-- Dumped by pg_dump version 16.15

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: pg_trgm; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;


--
-- Name: EXTENSION pg_trgm; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pg_trgm IS 'text similarity measurement and index searching based on trigrams';


--
-- Name: errkb_audit_guard(); Type: FUNCTION; Schema: public; Owner: -
--

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


--
-- Name: errkb_audit_no_truncate(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_audit_no_truncate() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'audit_log cannot be truncated; use the retention job'
        USING ERRCODE = '55000';
END $$;


--
-- Name: errkb_candidates(text, text, double precision, integer, double precision, double precision, double precision); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_candidates(p_query text, p_project_key text DEFAULT NULL::text, p_threshold double precision DEFAULT 0.30, p_limit integer DEFAULT 20, p_message_weight double precision DEFAULT 1.00, p_command_weight double precision DEFAULT 0.40, p_tool_name_weight double precision DEFAULT 0.20) RETURNS TABLE(issue_id uuid, fingerprint text, title text, project_key text, match_fields text[], similarity double precision, score double precision, approximate boolean, degraded boolean)
    LANGUAGE plpgsql
    SET enable_seqscan TO 'off'
    AS $_$
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
$_$;


--
-- Name: FUNCTION errkb_candidates(p_query text, p_project_key text, p_threshold double precision, p_limit integer, p_message_weight double precision, p_command_weight double precision, p_tool_name_weight double precision); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_candidates(p_query text, p_project_key text, p_threshold double precision, p_limit integer, p_message_weight double precision, p_command_weight double precision, p_tool_name_weight double precision) IS 'Fuzzy candidate generation (section 5 step 3): weighted pg_trgm similarity over indexed fields, bounded by threshold, a 500-row candidate pool and p_limit; returns candidates only, never a merge verdict. Degrades to the exact layer when pg_trgm is unavailable.';


--
-- Name: errkb_delete_preview(text, uuid, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_delete_preview(p_project_key text DEFAULT NULL::text, p_connector_id uuid DEFAULT NULL::uuid, p_before timestamp with time zone DEFAULT NULL::timestamp with time zone) RETURNS TABLE(kind text, rows bigint)
    LANGUAGE plpgsql STABLE
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


--
-- Name: FUNCTION errkb_delete_preview(p_project_key text, p_connector_id uuid, p_before timestamp with time zone); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_delete_preview(p_project_key text, p_connector_id uuid, p_before timestamp with time zone) IS 'Reports exactly the rows errkb_delete_scope() would remove. Audit rows are never deleted by a scope; the audit_log row counts 0.';


--
-- Name: errkb_delete_scope(text, uuid, timestamp with time zone, text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_delete_scope(p_project_key text DEFAULT NULL::text, p_connector_id uuid DEFAULT NULL::uuid, p_before timestamp with time zone DEFAULT NULL::timestamp with time zone, p_actor text DEFAULT 'owner'::text) RETURNS TABLE(kind text, removed bigint)
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


--
-- Name: FUNCTION errkb_delete_scope(p_project_key text, p_connector_id uuid, p_before timestamp with time zone, p_actor text); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_delete_scope(p_project_key text, p_connector_id uuid, p_before timestamp with time zone, p_actor text) IS 'Deletes the same rows errkb_delete_preview() reports, in one transaction, and records one delete.scope row in audit_log. Audit rows are never part of the scope.';


--
-- Name: errkb_env_matches(jsonb, jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_env_matches(candidate jsonb, wanted jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    AS $$
    select
        (not (coalesce(wanted, '{}'::jsonb) ? 'os')
            or candidate ->> 'os' = wanted ->> 'os')
        and (not (coalesce(wanted, '{}'::jsonb) ? 'shell')
            or candidate ->> 'shell' = wanted ->> 'shell')
        and (not (coalesce(wanted, '{}'::jsonb) ? 'connector')
            or candidate ->> 'connector' = wanted ->> 'connector')
        and coalesce((
            select bool_and(candidate -> 'runtime_versions' ->> kv.key = kv.value)
            from jsonb_each_text(
                case
                    when jsonb_typeof(coalesce(wanted, '{}'::jsonb) -> 'runtime_versions') = 'object'
                        then wanted -> 'runtime_versions'
                    else '{}'::jsonb
                end
            ) as kv
        ), true);
$$;


--
-- Name: errkb_purge_retention(integer, integer, integer, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_purge_retention(p_sample_days integer DEFAULT 7, p_normalized_days integer DEFAULT 90, p_audit_days integer DEFAULT 365, p_now timestamp with time zone DEFAULT now()) RETURNS TABLE(kind text, removed bigint)
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


--
-- Name: FUNCTION errkb_purge_retention(p_sample_days integer, p_normalized_days integer, p_audit_days integer, p_now timestamp with time zone); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_purge_retention(p_sample_days integer, p_normalized_days integer, p_audit_days integer, p_now timestamp with time zone) IS 'Three independent retention classes. Samples are erased in place, expired events are deleted (occurrences cascade), and audit rows are removed only behind the errkb.audit_retention guard.';


--
-- Name: errkb_record_event(jsonb, text, text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_record_event(event jsonb, p_source text DEFAULT 'connector'::text, p_actor text DEFAULT 'connector'::text) RETURNS TABLE(event_id uuid, occurrence_id uuid, issue_id uuid, fingerprint text, created boolean)
    LANGUAGE plpgsql
    AS $$
#variable_conflict use_column
declare
    required_fields constant text[] := array[
        'event_id', 'connector_id', 'session_id', 'observed_at', 'channel',
        'category', 'severity', 'headline', 'normalized_message',
        'redacted_sample', 'os', 'project_key', 'fingerprint',
        'schema_version', 'evidence', 'confidence'
    ];
    field text;
    v_event_id uuid;
    v_observed_at timestamptz;
    v_fingerprint text;
    v_issue_id uuid;
    v_occurrence_id uuid;
    v_inserted integer;
    v_created boolean;
    v_stored_fingerprint text;
begin
    if event is null or jsonb_typeof(event) <> 'object' then
        raise exception 'errkb_record_event: an ErrorEvent JSON object is required'
            using errcode = '22023';
    end if;

    foreach field in array required_fields loop
        if coalesce(event ->> field, '') = '' then
            raise exception 'errkb_record_event: % is required', field
                using errcode = '22023';
        end if;
    end loop;

    if not (event ? 'transient') then
        raise exception 'errkb_record_event: transient is required'
            using errcode = '22023';
    end if;
    if event ->> 'schema_version' <> 'v1' then
        raise exception 'errkb_record_event: schema_version % is not supported', event ->> 'schema_version'
            using errcode = '22023';
    end if;

    v_event_id := (event ->> 'event_id')::uuid;
    v_observed_at := (event ->> 'observed_at')::timestamptz;
    v_fingerprint := event ->> 'fingerprint';

    -- Append-only: a replay is not an update, and a replay that claims a
    -- different fingerprint for the same event id is an error, not a merge.
    insert into events (
        event_id, connector_id, session_id, observed_at, channel, category,
        severity, transient, headline, normalized_message, redacted_sample,
        error_code, command, exit_code, tool_name, os, shell, runtime_versions,
        project_key, fingerprint, schema_version, evidence, confidence,
        source, actor
    ) values (
        v_event_id,
        (event ->> 'connector_id')::uuid,
        nullif(event ->> 'session_id', '')::uuid,
        v_observed_at,
        event ->> 'channel',
        event ->> 'category',
        event ->> 'severity',
        (event ->> 'transient')::boolean,
        event ->> 'headline',
        event ->> 'normalized_message',
        event ->> 'redacted_sample',
        nullif(event ->> 'error_code', ''),
        nullif(event ->> 'command', ''),
        (event ->> 'exit_code')::integer,
        nullif(event ->> 'tool_name', ''),
        event ->> 'os',
        coalesce(event ->> 'shell', ''),
        coalesce(event -> 'runtime_versions', '{}'::jsonb),
        event ->> 'project_key',
        v_fingerprint,
        event ->> 'schema_version',
        event ->> 'evidence',
        (event ->> 'confidence')::double precision,
        p_source,
        p_actor
    )
    on conflict (event_id) do nothing;

    get diagnostics v_inserted = row_count;
    v_created := v_inserted > 0;

    if not v_created then
        select events.fingerprint into v_stored_fingerprint
        from events
        where events.event_id = v_event_id;

        if v_stored_fingerprint <> v_fingerprint then
            raise exception 'errkb_record_event: event % was already recorded with fingerprint %, not %',
                v_event_id, v_stored_fingerprint, v_fingerprint
                using errcode = '23505';
        end if;
    end if;

    -- Exact fingerprint is the only aggregation key, and every call counts as
    -- one appearance. ON CONFLICT makes the upsert safe under concurrency.
    insert into issues (
        fingerprint, project_key, title, category, first_seen, last_seen,
        hits, source, actor
    ) values (
        v_fingerprint,
        event ->> 'project_key',
        event ->> 'headline',
        event ->> 'category',
        v_observed_at,
        v_observed_at,
        1,
        p_source,
        p_actor
    )
    on conflict (fingerprint, project_key) do update
        set hits = issues.hits + 1,
            first_seen = least(issues.first_seen, excluded.first_seen),
            last_seen = greatest(issues.last_seen, excluded.last_seen),
            updated_at = now()
    returning issues.id into v_issue_id;

    insert into occurrences (
        issue_id, event_id, connector_id, project_key, environment,
        occurred_at, source, actor
    ) values (
        v_issue_id,
        v_event_id,
        (event ->> 'connector_id')::uuid,
        event ->> 'project_key',
        jsonb_build_object(
            'os', event ->> 'os',
            'shell', coalesce(event ->> 'shell', ''),
            'runtime_versions', coalesce(event -> 'runtime_versions', '{}'::jsonb)
        ),
        v_observed_at,
        p_source,
        p_actor
    )
    returning occurrences.id into v_occurrence_id;

    return query select v_event_id, v_occurrence_id, v_issue_id, v_fingerprint, v_created;
end;
$$;


--
-- Name: errkb_search(text, text, text, text, text, text, text, integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_search(p_issue_id text DEFAULT NULL::text, p_fingerprint text DEFAULT NULL::text, p_error_code text DEFAULT NULL::text, p_normalized_message text DEFAULT NULL::text, p_command text DEFAULT NULL::text, p_tool_name text DEFAULT NULL::text, p_project_key text DEFAULT NULL::text, p_limit integer DEFAULT 10) RETURNS TABLE(issue_id uuid, fingerprint text, title text, project_key text, category text, status text, hits integer, last_seen timestamp with time zone, match_reason text, approximate boolean, score double precision)
    LANGUAGE plpgsql STABLE
    AS $_$
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
$_$;


--
-- Name: FUNCTION errkb_search(p_issue_id text, p_fingerprint text, p_error_code text, p_normalized_message text, p_command text, p_tool_name text, p_project_key text, p_limit integer); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_search(p_issue_id text, p_fingerprint text, p_error_code text, p_normalized_message text, p_command text, p_tool_name text, p_project_key text, p_limit integer) IS 'Exact retrieval layer (section 5 steps 1-2): one indexed selector plus an optional project filter; no fuzzy matching.';


--
-- Name: errkb_solution_status_transition(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_solution_status_transition() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
begin
    if old.status = new.status then
        return new;
    end if;

    if (old.status = 'candidate' and new.status in ('verified', 'deprecated', 'blocked'))
        or (old.status = 'verified' and new.status in ('deprecated', 'blocked'))
        or (old.status = 'blocked' and new.status = 'deprecated') then
        new.updated_at := now();
        return new;
    end if;

    raise exception 'errkb: illegal solution status transition % -> %', old.status, new.status
        using errcode = '23514';
end;
$$;


--
-- Name: errkb_trigram_available(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_trigram_available() RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')
$$;


--
-- Name: FUNCTION errkb_trigram_available(); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION errkb_trigram_available() IS 'True when pg_trgm is installed, i.e. when errkb_candidates() can generate fuzzy candidates.';


--
-- Name: errkb_trust(uuid, jsonb); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION errkb_trust(p_solution_id uuid, p_environment jsonb DEFAULT '{}'::jsonb) RETURNS TABLE(trust_score double precision, samples integer, successes integer, failures integer, unknowns integer)
    LANGUAGE plpgsql STABLE
    AS $$
#variable_conflict use_column
declare
    v_successes integer;
    v_failures integer;
    v_unknowns integer;
    v_score double precision;
begin
    select
        count(*) filter (where o.outcome = 'success'),
        count(*) filter (where o.outcome = 'failure'),
        count(*) filter (where o.outcome = 'unknown')
    into v_successes, v_failures, v_unknowns
    from outcomes o
    join solution_versions sv on sv.id = o.solution_version_id
    where sv.solution_id = p_solution_id
        and errkb_env_matches(o.environment, coalesce(p_environment, '{}'::jsonb));

    v_score := (v_successes + 2)::double precision
        / (v_successes + v_failures + 4)::double precision;

    return query
        select v_score,
               v_successes + v_failures + v_unknowns,
               v_successes,
               v_failures,
               v_unknowns;
end;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE audit_log (
    id bigint NOT NULL,
    occurred_at timestamp with time zone DEFAULT now() NOT NULL,
    action text NOT NULL,
    entity text NOT NULL,
    entity_id text DEFAULT ''::text NOT NULL,
    operator text NOT NULL,
    source text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    retain_until timestamp with time zone
);


--
-- Name: audit_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE audit_log ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME audit_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: connectors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE connectors (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    channel text NOT NULL,
    capabilities jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    CONSTRAINT connectors_channel_check CHECK ((channel = ANY (ARRAY['A'::text, 'B'::text, 'C'::text, 'D'::text, 'E'::text])))
);


--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE events (
    event_id uuid DEFAULT gen_random_uuid() NOT NULL,
    connector_id uuid NOT NULL,
    session_id uuid,
    observed_at timestamp with time zone NOT NULL,
    channel text NOT NULL,
    category text NOT NULL,
    severity text NOT NULL,
    transient boolean DEFAULT false NOT NULL,
    headline text NOT NULL,
    normalized_message text NOT NULL,
    redacted_sample text NOT NULL,
    error_code text,
    command text,
    exit_code integer,
    tool_name text,
    os text NOT NULL,
    shell text DEFAULT ''::text NOT NULL,
    runtime_versions jsonb DEFAULT '{}'::jsonb NOT NULL,
    project_key text DEFAULT ''::text NOT NULL,
    fingerprint text NOT NULL,
    schema_version text DEFAULT 'v1'::text NOT NULL,
    evidence text NOT NULL,
    confidence double precision NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    sample_retain_until timestamp with time zone,
    normalized_retain_until timestamp with time zone,
    CONSTRAINT events_channel_check CHECK ((channel = ANY (ARRAY['A'::text, 'B'::text, 'C'::text, 'D'::text, 'E'::text]))),
    CONSTRAINT events_confidence_check CHECK (((confidence >= (0)::double precision) AND (confidence <= (1)::double precision))),
    CONSTRAINT events_fingerprint_check CHECK ((fingerprint ~ '^[0-9a-f]{12}$'::text)),
    CONSTRAINT events_schema_version_check CHECK ((schema_version = 'v1'::text))
);


--
-- Name: issues; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE issues (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    fingerprint text NOT NULL,
    project_key text DEFAULT ''::text NOT NULL,
    title text NOT NULL,
    category text NOT NULL,
    status text DEFAULT 'open'::text NOT NULL,
    trigger text DEFAULT ''::text NOT NULL,
    first_seen timestamp with time zone DEFAULT now() NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL,
    hits integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    CONSTRAINT issues_fingerprint_check CHECK ((fingerprint ~ '^[0-9a-f]{12}$'::text)),
    CONSTRAINT issues_hits_check CHECK ((hits >= 0)),
    CONSTRAINT issues_status_check CHECK ((status = ANY (ARRAY['open'::text, 'resolved'::text, 'wontfix'::text, 'misjudged'::text])))
);


--
-- Name: notices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE notices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    session_id uuid,
    issue_id uuid,
    kind text NOT NULL,
    body text NOT NULL,
    estimated_tokens integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT notices_estimated_tokens_check CHECK ((estimated_tokens >= 0))
);


--
-- Name: occurrences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE occurrences (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    issue_id uuid NOT NULL,
    event_id uuid NOT NULL,
    connector_id uuid,
    project_key text DEFAULT ''::text NOT NULL,
    environment jsonb DEFAULT '{}'::jsonb NOT NULL,
    occurred_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL
);


--
-- Name: outcomes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE outcomes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    issue_id uuid NOT NULL,
    solution_version_id uuid NOT NULL,
    occurrence_id uuid,
    outcome text NOT NULL,
    note text DEFAULT ''::text NOT NULL,
    environment jsonb DEFAULT '{}'::jsonb NOT NULL,
    observed_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    CONSTRAINT outcomes_outcome_check CHECK ((outcome = ANY (ARRAY['success'::text, 'failure'::text, 'unknown'::text])))
);


--
-- Name: retrievals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE retrievals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    session_id uuid,
    query text NOT NULL,
    candidate_issue_id uuid NOT NULL,
    candidate_rank integer NOT NULL,
    score double precision NOT NULL,
    match_fields text[] DEFAULT '{}'::text[] NOT NULL,
    approximate boolean NOT NULL,
    environment_diff jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT retrievals_candidate_rank_check CHECK (((candidate_rank >= 1) AND (candidate_rank <= 3))),
    CONSTRAINT retrievals_score_check CHECK (((score >= (0)::double precision) AND (score <= (1)::double precision)))
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE schema_migrations (
    version text NOT NULL,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connector_id uuid NOT NULL,
    external_id text NOT NULL,
    project_key text DEFAULT ''::text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    ended_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL
);


--
-- Name: solution_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE solution_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    solution_id uuid NOT NULL,
    version integer NOT NULL,
    body text NOT NULL,
    preconditions text DEFAULT ''::text NOT NULL,
    risks text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    effective_at timestamp with time zone DEFAULT now() NOT NULL,
    applies_to jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT solution_versions_version_check CHECK ((version >= 1))
);


--
-- Name: solutions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE solutions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    issue_id uuid NOT NULL,
    status text DEFAULT 'candidate'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source text NOT NULL,
    actor text NOT NULL,
    CONSTRAINT solutions_status_check CHECK ((status = ANY (ARRAY['candidate'::text, 'verified'::text, 'deprecated'::text, 'blocked'::text])))
);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: connectors connectors_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY connectors
    ADD CONSTRAINT connectors_name_key UNIQUE (name);


--
-- Name: connectors connectors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY connectors
    ADD CONSTRAINT connectors_pkey PRIMARY KEY (id);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY events
    ADD CONSTRAINT events_pkey PRIMARY KEY (event_id);


--
-- Name: issues issues_fingerprint_project_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY issues
    ADD CONSTRAINT issues_fingerprint_project_key_key UNIQUE (fingerprint, project_key);


--
-- Name: issues issues_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY issues
    ADD CONSTRAINT issues_pkey PRIMARY KEY (id);


--
-- Name: notices notices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY notices
    ADD CONSTRAINT notices_pkey PRIMARY KEY (id);


--
-- Name: occurrences occurrences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY occurrences
    ADD CONSTRAINT occurrences_pkey PRIMARY KEY (id);


--
-- Name: outcomes outcomes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY outcomes
    ADD CONSTRAINT outcomes_pkey PRIMARY KEY (id);


--
-- Name: retrievals retrievals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY retrievals
    ADD CONSTRAINT retrievals_pkey PRIMARY KEY (id);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: sessions sessions_connector_id_external_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_connector_id_external_id_key UNIQUE (connector_id, external_id);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


--
-- Name: solution_versions solution_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY solution_versions
    ADD CONSTRAINT solution_versions_pkey PRIMARY KEY (id);


--
-- Name: solution_versions solution_versions_solution_id_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY solution_versions
    ADD CONSTRAINT solution_versions_solution_id_version_key UNIQUE (solution_id, version);


--
-- Name: solutions solutions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY solutions
    ADD CONSTRAINT solutions_pkey PRIMARY KEY (id);


--
-- Name: audit_log_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_occurred_at_idx ON audit_log USING btree (occurred_at);


--
-- Name: audit_log_retain_until_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_retain_until_idx ON audit_log USING btree (retain_until) WHERE (retain_until IS NOT NULL);


--
-- Name: events_command_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_command_idx ON events USING btree (command) WHERE (command IS NOT NULL);


--
-- Name: events_command_trgm_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_command_trgm_idx ON events USING gin (command gin_trgm_ops);


--
-- Name: events_connector_observed_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_connector_observed_idx ON events USING btree (connector_id, observed_at);


--
-- Name: events_error_code_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_error_code_idx ON events USING btree (error_code) WHERE (error_code IS NOT NULL);


--
-- Name: events_fingerprint_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_fingerprint_idx ON events USING btree (fingerprint);


--
-- Name: events_normalized_message_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_normalized_message_idx ON events USING btree (normalized_message);


--
-- Name: events_normalized_message_trgm_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_normalized_message_trgm_idx ON events USING gin (normalized_message gin_trgm_ops);


--
-- Name: events_observed_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_observed_at_idx ON events USING btree (observed_at);


--
-- Name: events_project_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_project_idx ON events USING btree (project_key, observed_at);


--
-- Name: events_tool_name_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_tool_name_idx ON events USING btree (tool_name) WHERE (tool_name IS NOT NULL);


--
-- Name: events_tool_name_trgm_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_tool_name_trgm_idx ON events USING gin (tool_name gin_trgm_ops);


--
-- Name: notices_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX notices_created_at_idx ON notices USING btree (created_at);


--
-- Name: notices_session_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX notices_session_idx ON notices USING btree (session_id);


--
-- Name: occurrences_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX occurrences_event_idx ON occurrences USING btree (event_id);


--
-- Name: occurrences_issue_latest_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX occurrences_issue_latest_idx ON occurrences USING btree (issue_id, occurred_at DESC, id DESC);


--
-- Name: INDEX occurrences_issue_latest_idx; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON INDEX occurrences_issue_latest_idx IS 'Covers the latest-occurrence lookup of the retrieval reranker (EKB-027).';


--
-- Name: occurrences_occurred_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX occurrences_occurred_at_idx ON occurrences USING btree (occurred_at);


--
-- Name: outcomes_environment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX outcomes_environment_idx ON outcomes USING gin (environment jsonb_path_ops);


--
-- Name: outcomes_observed_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX outcomes_observed_at_idx ON outcomes USING btree (observed_at);


--
-- Name: outcomes_solution_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX outcomes_solution_idx ON outcomes USING btree (solution_version_id);


--
-- Name: retrievals_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX retrievals_created_at_idx ON retrievals USING btree (created_at);


--
-- Name: retrievals_session_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX retrievals_session_idx ON retrievals USING btree (session_id);


--
-- Name: solution_versions_solution_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX solution_versions_solution_idx ON solution_versions USING btree (solution_id, version);


--
-- Name: solutions_issue_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX solutions_issue_idx ON solutions USING btree (issue_id);


--
-- Name: audit_log audit_log_no_truncate; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log FOR EACH STATEMENT EXECUTE FUNCTION errkb_audit_no_truncate();


--
-- Name: TRIGGER audit_log_no_truncate ON audit_log; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TRIGGER audit_log_no_truncate ON audit_log IS 'Audit rows are removed one by one by errkb_purge_retention() so the removal itself is counted and logged.';


--
-- Name: audit_log audit_log_protect; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER audit_log_protect BEFORE DELETE ON audit_log FOR EACH ROW EXECUTE FUNCTION errkb_audit_guard();


--
-- Name: solutions solutions_status_transition; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER solutions_status_transition BEFORE UPDATE OF status ON solutions FOR EACH ROW EXECUTE FUNCTION errkb_solution_status_transition();


--
-- Name: events events_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY events
    ADD CONSTRAINT events_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE CASCADE;


--
-- Name: events events_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY events
    ADD CONSTRAINT events_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL;


--
-- Name: notices notices_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY notices
    ADD CONSTRAINT notices_issue_id_fkey FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE SET NULL;


--
-- Name: notices notices_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY notices
    ADD CONSTRAINT notices_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL;


--
-- Name: occurrences occurrences_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY occurrences
    ADD CONSTRAINT occurrences_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE SET NULL;


--
-- Name: occurrences occurrences_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY occurrences
    ADD CONSTRAINT occurrences_event_id_fkey FOREIGN KEY (event_id) REFERENCES events(event_id) ON DELETE CASCADE;


--
-- Name: occurrences occurrences_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY occurrences
    ADD CONSTRAINT occurrences_issue_id_fkey FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;


--
-- Name: outcomes outcomes_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY outcomes
    ADD CONSTRAINT outcomes_issue_id_fkey FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;


--
-- Name: outcomes outcomes_occurrence_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY outcomes
    ADD CONSTRAINT outcomes_occurrence_id_fkey FOREIGN KEY (occurrence_id) REFERENCES occurrences(id) ON DELETE SET NULL;


--
-- Name: outcomes outcomes_solution_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY outcomes
    ADD CONSTRAINT outcomes_solution_version_id_fkey FOREIGN KEY (solution_version_id) REFERENCES solution_versions(id) ON DELETE CASCADE;


--
-- Name: retrievals retrievals_candidate_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY retrievals
    ADD CONSTRAINT retrievals_candidate_issue_id_fkey FOREIGN KEY (candidate_issue_id) REFERENCES issues(id) ON DELETE CASCADE;


--
-- Name: retrievals retrievals_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY retrievals
    ADD CONSTRAINT retrievals_session_id_fkey FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL;


--
-- Name: sessions sessions_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES connectors(id) ON DELETE CASCADE;


--
-- Name: solution_versions solution_versions_solution_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY solution_versions
    ADD CONSTRAINT solution_versions_solution_id_fkey FOREIGN KEY (solution_id) REFERENCES solutions(id) ON DELETE CASCADE;


--
-- Name: solutions solutions_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY solutions
    ADD CONSTRAINT solutions_issue_id_fkey FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--



INSERT INTO schema_migrations (version, name, checksum) VALUES ('0001','init','49a6f310018aecbd03dbcfd710e7945dbcb3140075488d3e3d3d4f8788753cac');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0002','record','82bc76750040afcbd5595b406b185910b22e2d995ff7149deccccff81d082563');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0003','solutions','b02d13bb5f64104e15ce7d2885cca1450fba08203f2de85a10358a906020943c');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0004','exact_search','b1ce262d03724e4a8d8a50a2f4d98a836462c93cb66f63c64f6b76fd3d0093f7');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0005','trigram','8728194d472eebb6f49f5b987fdb5f2748b1886725d249cb72dc57af618172d1');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0006','retention','19582daf3fe29512358125f70ef8e66d466014bdaa2b7bddd74e7ba81a6a0ef7');
INSERT INTO schema_migrations (version, name, checksum) VALUES ('0007','occurrence_lookup','a58db636044dd70d0c9b4132fc5ed76453e4e5a3ef918f9b8fa2c2d63ac77835');

