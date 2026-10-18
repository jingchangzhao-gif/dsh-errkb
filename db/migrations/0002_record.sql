-- Migration 0002: idempotent recording of an ErrorEvent (EKB-020).
--
-- The function below is the only write path for events. The caller supplies one
-- ErrorEvent as JSON; the function records the fact, aggregates it by the exact
-- fingerprint and returns the identifiers.
--
-- Chosen concurrency mechanism: unique constraints plus ON CONFLICT inside one
-- transaction. A Go-side "SELECT then INSERT" would have a check-then-use race
-- (the same shape as the EKB-003 .lock failure), so the database owns the
-- decision. An advisory lock is not needed: every write is an upsert on a
-- unique key, so two callers never have to agree beforehand.
--
-- Choices that this migration fixes:
--   * events are append-only. A replay of the same event_id hits the primary
--     key and `ON CONFLICT (event_id) DO NOTHING` leaves the stored row
--     untouched; the function then verifies the replay's fingerprint still
--     matches, otherwise it raises instead of silently merging unrelated data.
--   * occurrences are never deduplicated. Every call is one appearance, even a
--     replay of the same event id, so the EKB-019 unique (issue_id, event_id)
--     would collapse replays and is dropped here.
--   * the aggregation key is the exact fingerprint (section 5). There is no
--     similarity path in this function.
--   * trust is not computed here; EKB-021 owns errkb_trust(). This function only
--     records facts and counts.
--   * the function runs in the caller's transaction. It does not open one, so
--     the caller can record and roll back as one unit.

alter table occurrences drop constraint if exists occurrences_issue_id_event_id_key;

create or replace function errkb_record_event(
    event jsonb,
    p_source text default 'connector',
    p_actor text default 'connector'
) returns table (
    event_id uuid,
    occurrence_id uuid,
    issue_id uuid,
    fingerprint text,
    created boolean
)
language plpgsql
as $$
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
