-- Migration 0001: schema v1, the entities of section 4 of the implementation plan.
--
-- This file was a comment-only shell until EKB-019. It is filled in place
-- because it has never been executed against a persistent database: the local
-- PostgreSQL 16 instance had zero user tables before this migration, and the
-- CI database job runs against a fresh, disposable postgres:16 container. The
-- evidence is recorded in db/migrations/README.md.
--
-- Rules for later migrations:
--   * one file per change, named NNNN_name.sql, applied in filename order;
--   * never edit a file that has been applied anywhere — add a new migration;
--     the runner stores a checksum per applied version and rejects edited files;
--   * the runner wraps every file in its own transaction and records a row in
--     schema_migrations only after the whole file succeeded.
--
-- Domain choices that this schema fixes:
--   * events are the redacted raw observations. They never carry an issue_id:
--     a single appearance is not automatically a new issue (section 4).
--   * constraints live in issues (fingerprint + project_key) and occurrences
--     link events to issues with the environment of each appearance.
--   * retention is per artifact, not one column: events carry separate deadlines
--     for the redacted sample and the normalized text, audit_log carries its own.
--   * audit_log has no foreign keys, so deleting a business row can never
--     cascade into the audit trail.
--   * append-only is a convention for application code (EKB-020/026); retention
--     and user-requested deletion may still remove rows, so no trigger blocks
--     UPDATE/DELETE here.

CREATE TABLE connectors (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL UNIQUE,
    version       text NOT NULL DEFAULT '',
    channel       text NOT NULL CHECK (channel IN ('A', 'B', 'C', 'D', 'E')),
    capabilities  jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    source        text NOT NULL,
    actor         text NOT NULL
);

CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connector_id  uuid NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    external_id   text NOT NULL,
    project_key   text NOT NULL DEFAULT '',
    started_at    timestamptz NOT NULL,
    ended_at      timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    source        text NOT NULL,
    actor         text NOT NULL,
    UNIQUE (connector_id, external_id)
);

CREATE TABLE events (
    event_id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connector_id        uuid NOT NULL REFERENCES connectors (id) ON DELETE CASCADE,
    session_id          uuid REFERENCES sessions (id) ON DELETE SET NULL,
    observed_at         timestamptz NOT NULL,
    channel             text NOT NULL CHECK (channel IN ('A', 'B', 'C', 'D', 'E')),
    category            text NOT NULL,
    severity            text NOT NULL,
    transient           boolean NOT NULL DEFAULT false,
    headline            text NOT NULL,
    normalized_message  text NOT NULL,
    redacted_sample     text NOT NULL,
    error_code          text,
    command             text,
    exit_code           integer,
    tool_name           text,
    os                  text NOT NULL,
    shell               text NOT NULL DEFAULT '',
    runtime_versions    jsonb NOT NULL DEFAULT '{}'::jsonb,
    project_key         text NOT NULL DEFAULT '',
    fingerprint         text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{12}$'),
    schema_version      text NOT NULL DEFAULT 'v1' CHECK (schema_version = 'v1'),
    evidence            text NOT NULL,
    confidence          double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    source              text NOT NULL,
    actor               text NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    sample_retain_until     timestamptz,
    normalized_retain_until timestamptz
);

CREATE INDEX events_fingerprint_idx ON events (fingerprint);
CREATE INDEX events_observed_at_idx ON events (observed_at);
CREATE INDEX events_project_idx ON events (project_key, observed_at);

CREATE TABLE issues (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{12}$'),
    project_key text NOT NULL DEFAULT '',
    title       text NOT NULL,
    category    text NOT NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'wontfix', 'misjudged')),
    trigger     text NOT NULL DEFAULT '',
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    hits        integer NOT NULL DEFAULT 0 CHECK (hits >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    source      text NOT NULL,
    actor       text NOT NULL,
    UNIQUE (fingerprint, project_key)
);

CREATE TABLE occurrences (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id     uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    event_id     uuid NOT NULL REFERENCES events (event_id) ON DELETE CASCADE,
    connector_id uuid REFERENCES connectors (id) ON DELETE SET NULL,
    project_key  text NOT NULL DEFAULT '',
    environment  jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at  timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    source       text NOT NULL,
    actor        text NOT NULL,
    UNIQUE (issue_id, event_id)
);

CREATE INDEX occurrences_event_idx ON occurrences (event_id);

CREATE TABLE solutions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    status     text NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate', 'accepted', 'rejected', 'superseded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    source     text NOT NULL,
    actor      text NOT NULL
);

CREATE INDEX solutions_issue_idx ON solutions (issue_id);

CREATE TABLE solution_versions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    solution_id   uuid NOT NULL REFERENCES solutions (id) ON DELETE CASCADE,
    version       integer NOT NULL CHECK (version >= 1),
    body          text NOT NULL,
    preconditions text NOT NULL DEFAULT '',
    risks         text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    source        text NOT NULL,
    actor         text NOT NULL,
    UNIQUE (solution_id, version)
);

CREATE TABLE outcomes (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id            uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    solution_version_id uuid NOT NULL REFERENCES solution_versions (id) ON DELETE CASCADE,
    occurrence_id       uuid REFERENCES occurrences (id) ON DELETE SET NULL,
    outcome             text NOT NULL CHECK (outcome IN ('success', 'failure', 'unknown')),
    note                text NOT NULL DEFAULT '',
    environment         jsonb NOT NULL DEFAULT '{}'::jsonb,
    observed_at         timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    source              text NOT NULL,
    actor               text NOT NULL
);

CREATE INDEX outcomes_solution_idx ON outcomes (solution_version_id);

CREATE TABLE retrievals (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id         uuid REFERENCES sessions (id) ON DELETE SET NULL,
    query              text NOT NULL,
    candidate_issue_id uuid NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    candidate_rank     integer NOT NULL CHECK (candidate_rank BETWEEN 1 AND 3),
    score              double precision NOT NULL CHECK (score >= 0 AND score <= 1),
    match_fields       text[] NOT NULL DEFAULT '{}',
    approximate        boolean NOT NULL,
    environment_diff   jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX retrievals_session_idx ON retrievals (session_id);

CREATE TABLE notices (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       uuid REFERENCES sessions (id) ON DELETE SET NULL,
    issue_id         uuid REFERENCES issues (id) ON DELETE SET NULL,
    kind             text NOT NULL,
    body             text NOT NULL,
    estimated_tokens integer NOT NULL CHECK (estimated_tokens >= 0),
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notices_session_idx ON notices (session_id);

-- The audit trail has no foreign keys on purpose: deleting or cascading a
-- business row must never remove an audit record (section 4 and card EKB-019).
CREATE TABLE audit_log (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    action       text NOT NULL,
    entity       text NOT NULL,
    entity_id    text NOT NULL DEFAULT '',
    operator     text NOT NULL,
    source       text NOT NULL,
    detail       jsonb NOT NULL DEFAULT '{}'::jsonb,
    retain_until timestamptz
);
