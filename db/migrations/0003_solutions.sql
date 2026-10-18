-- Migration 0003: solution versions, outcomes and the authoritative trust
-- calculation (EKB-021).
--
-- The three tables stay separate, as section 4 requires:
--   * solutions          - candidate fixes for one issue; carries only state
--   * solution_versions  - the text, preconditions, risk and applicable
--                          environment of each revision (section 6 requires a
--                          copied fix to show source, environment and risk)
--   * outcomes           - worked / failed / unknown per environment
--
-- Trust is computed here and nowhere else. No column stores it, so no connector
-- or client can write a trust value; errkb_trust() derives it from outcomes.
-- Section 8.2 downgrades src/resolve-detect.ts to "emit outcomes only".
--
-- Status machine (closed; anything else is rejected by the trigger):
--   candidate -> verified | deprecated | blocked
--   verified  -> deprecated | blocked
--   blocked   -> deprecated
--   deprecated -> terminal
-- deprecated is "a better solution replaced this one"; blocked is "this must
-- not be applied (risk or inapplicable environment)".
--
-- The two legacy states of 0001 are folded in before the new constraint is
-- added: accepted -> verified, rejected/superseded -> deprecated.

update solutions
set status = case status
        when 'accepted' then 'verified'
        when 'rejected' then 'deprecated'
        when 'superseded' then 'deprecated'
        else status
    end
where status in ('accepted', 'rejected', 'superseded');

alter table solutions drop constraint if exists solutions_status_check;
alter table solutions add constraint solutions_status_check
    check (status in ('candidate', 'verified', 'deprecated', 'blocked'));

create or replace function errkb_solution_status_transition()
returns trigger
language plpgsql
as $$
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

drop trigger if exists solutions_status_transition on solutions;
create trigger solutions_status_transition
    before update of status on solutions
    for each row execute function errkb_solution_status_transition();

-- Version history additions: when this revision became effective, and the
-- environment it applies to (empty object means "every environment").
alter table solution_versions
    add column if not exists effective_at timestamptz not null default now();
alter table solution_versions
    add column if not exists applies_to jsonb not null default '{}'::jsonb;

create index if not exists solution_versions_solution_idx
    on solution_versions (solution_id, version);

-- Environment-specific lookups: trust filters by environment JSON.
create index if not exists outcomes_environment_idx
    on outcomes using gin (environment jsonb_path_ops);

-- Deterministic environment matching. The wanted environment constrains only
-- the keys it carries: os/shell/connector must be equal, every runtime_versions
-- key it names must be present with the same value, and extra candidate keys
-- are allowed. An empty wanted environment matches every outcome.
create or replace function errkb_env_matches(candidate jsonb, wanted jsonb)
returns boolean
language sql
immutable
as $$
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

-- Authoritative trust for one solution in one environment.
--
-- Formula: Laplace smoothed success ratio with a 0.5 prior over four virtual
-- attempts, so a solution with no evidence scores 0.5 and needs several
-- outcomes to move far away from it:
--
--     trust = (successes + 2) / (successes + failures + 4)
--
--   * unknown outcomes are kept and reported, but they are not evidence for or
--     against the fix, so they do not enter the denominator (dropping them
--     would hide them; counting them as failures would understate a fix nobody
--     could verify);
--   * the result depends only on the stored rows: no clock, no randomness;
--   * samples is the total number of matching outcomes;
--   * outcomes from every version of the solution count, because those are the
--     actual uses the database observed.
create or replace function errkb_trust(
    p_solution_id uuid,
    p_environment jsonb default '{}'::jsonb
)
returns table (
    trust_score double precision,
    samples integer,
    successes integer,
    failures integer,
    unknowns integer
)
language plpgsql
stable
as $$
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
