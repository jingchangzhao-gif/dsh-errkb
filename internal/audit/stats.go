// Statistics for the EKB-049 view. Every number here is aggregated by PostgreSQL
// from the same tables the rest of the system writes; nothing is cached, and no
// number is computed in Go from a sample the UI happened to load. The only Go
// arithmetic is turning counts into rates.
package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssumedTokensPerDiagnosis is the only assumption behind the savings estimate:
// it assumes that a repeated failure which already has a verified solution would
// otherwise have cost the host roughly this many tokens of model context to
// diagnose again. It is an assumption, not a measurement, and the view must show
// it next to the estimate.
const AssumedTokensPerDiagnosis = 400

// CorrectionActions are the audit actions that mean "a human changed the
// knowledge base" — they are the manual correction load metric.
var CorrectionActions = []string{
	"solution.version.added",
	"solution.status.changed",
	"issue.status.changed",
	"solution.deleted",
}

// Stats is one collected snapshot of the EKB-049 metrics.
type Stats struct {
	GeneratedAt time.Time
	Retrieval   RetrievalStats
	Solutions   SolutionStats
	Injections  InjectionStats
	Repeats     RepeatStats
	Latency     LatencyStats
	Corrections CorrectionStats
	Faults      FaultStats
	Savings     SavingsStats
}

// RetrievalStats covers the retrieval path: how often an exact fingerprint hit,
// how accurate approximate candidates were, whether a false merge ever happened,
// and the measured latency (from EKB-049's duration_ms column, added in 0008).
type RetrievalStats struct {
	Rounds                  int64
	ExactTopOne             int64
	ApproximateTopOne       int64
	FalseMerges             int64
	ApproximateTopOneHits   int64
	ApproximateTopOneRuns   int64
	ApproximateTopThreeHits int64
	ApproximateTopThreeRuns int64
	P50Milliseconds         float64
	P95Milliseconds         float64
	LatencySamples          int64
}

// SolutionStats counts recorded outcomes and the resulting success rate.
type SolutionStats struct {
	Success     int64
	Failure     int64
	Unknown     int64
	SuccessRate float64
}

// InjectionStats measures how much text was handed back to the host.
type InjectionStats struct {
	Notices         int64
	Characters      int64
	EstimatedTokens int64
}

// RepeatStats counts issues seen more than once.
type RepeatStats struct {
	RepeatedIssues int64
	RepeatedHits   int64
}

// LatencyStats is the capture-to-notice latency, when a notice followed a capture.
type LatencyStats struct {
	P50Milliseconds float64
	Samples         int64
}

// CorrectionStats is the manual correction load, by action.
type CorrectionStats struct {
	Total    int64
	ByAction []ActionCount
}

// FaultStats is the count of persisted faults (the daemon writes "fault.*" audit
// rows; queue and connector faults have no table yet, and the card requires the
// view to say so).
type FaultStats struct {
	Total    int64
	ByAction []ActionCount
}

// ActionCount is one audit action and how often it happened.
type ActionCount struct {
	Action string
	Count  int64
}

// SavingsStats separates what was measured from what was estimated. Injected
// characters and estimated tokens came out of real notices; the avoided-diagnosis
// count is inferred from issues that were hit more than once and already have a
// verified solution, and the token number multiplies that by
// AssumedTokensPerDiagnosis.
type SavingsStats struct {
	InjectedCharacters        int64
	InjectedTokens            int64
	AvoidedDiagnoses          int64
	AvoidedTokens             int64
	AssumedTokensPerDiagnosis int64
	// Assumption is shown verbatim next to the estimate.
	Assumption string
}

// Service collects the metrics from one pool.
type Service struct {
	pool *pgxpool.Pool
}

// NewService wraps a pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Collect runs every metric query read-only on one connection and returns the
// snapshot. Any failing query fails the collection; nothing is partially shown.
func (s *Service) Collect(ctx context.Context) (Stats, error) {
	if s.pool == nil {
		return Stats{}, errors.New("audit: statistics need a database pool")
	}
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("audit: acquire a connection: %w", err)
	}
	defer pooled.Release()
	conn := pooled.Conn()

	stats := Stats{GeneratedAt: time.Now().UTC()}
	if stats.Retrieval, err = collectRetrieval(ctx, conn); err != nil {
		return Stats{}, err
	}
	if stats.Solutions, err = collectSolutions(ctx, conn); err != nil {
		return Stats{}, err
	}
	if stats.Injections, err = collectInjections(ctx, conn); err != nil {
		return Stats{}, err
	}
	if stats.Repeats, err = collectRepeats(ctx, conn); err != nil {
		return Stats{}, err
	}
	if stats.Latency, err = collectLatency(ctx, conn); err != nil {
		return Stats{}, err
	}
	totals, err := collectActionCounts(ctx, conn, `action = any($1::text[])`, CorrectionActions)
	if err != nil {
		return Stats{}, err
	}
	stats.Corrections = CorrectionStats{Total: sumActions(totals), ByAction: totals}
	faults, err := collectActionCounts(ctx, conn, `action like 'fault.%'`)
	if err != nil {
		return Stats{}, err
	}
	stats.Faults = FaultStats{Total: sumActions(faults), ByAction: faults}
	avoided, err := collectAvoidedDiagnoses(ctx, conn)
	if err != nil {
		return Stats{}, err
	}
	stats.Savings = SavingsStats{
		InjectedCharacters:        stats.Injections.Characters,
		InjectedTokens:            stats.Injections.EstimatedTokens,
		AvoidedDiagnoses:          avoided,
		AvoidedTokens:             avoided * AssumedTokensPerDiagnosis,
		AssumedTokensPerDiagnosis: AssumedTokensPerDiagnosis,
		Assumption: fmt.Sprintf("estimated: each avoided diagnosis is assumed to have cost about %d tokens; this is an assumption, not a measurement",
			AssumedTokensPerDiagnosis),
	}
	return stats, nil
}

func collectRetrieval(ctx context.Context, conn *pgx.Conn) (RetrievalStats, error) {
	var stats RetrievalStats

	// A round is one logical query: the rows persisted by a single Retrieve call.
	// Rows from one Retrieve share session, query and (at second resolution) time;
	// grouping on those three is stable because ranks are 1..3 per call.
	if err := conn.QueryRow(ctx, `
		with rounds as (
			select coalesce(session_id::text, '') as session,
			       query,
			       date_trunc('second', created_at) as at,
			       bool_or(candidate_rank = 1 and not approximate and match_fields @> array['fingerprint']) as exact_top_one,
			       bool_or(candidate_rank = 1 and approximate) as approximate_top_one
			from retrievals
			group by 1, 2, 3
		)
		select count(*),
		       count(*) filter (where exact_top_one),
		       count(*) filter (where approximate_top_one)
		from rounds
	`).Scan(&stats.Rounds, &stats.ExactTopOne, &stats.ApproximateTopOne); err != nil {
		return RetrievalStats{}, fmt.Errorf("audit: retrieval rounds: %w", err)
	}

	if err := conn.QueryRow(ctx, `
		select count(*) from retrievals
		where not approximate and not (match_fields @> array['fingerprint'])
	`).Scan(&stats.FalseMerges); err != nil {
		return RetrievalStats{}, fmt.Errorf("audit: false merges: %w", err)
	}

	if err := conn.QueryRow(ctx, `
		select count(*) filter (where candidate_rank = 1),
		       count(*) filter (where candidate_rank <= 3),
		       count(*) filter (where candidate_rank = 1 and exists (
		           select 1 from outcomes o
		           where o.issue_id = r.candidate_issue_id and o.outcome = 'success')),
		       count(*) filter (where candidate_rank <= 3 and exists (
		           select 1 from outcomes o
		           where o.issue_id = r.candidate_issue_id and o.outcome = 'success'))
		from retrievals r
		where r.approximate
	`).Scan(&stats.ApproximateTopOneRuns, &stats.ApproximateTopThreeRuns,
		&stats.ApproximateTopOneHits, &stats.ApproximateTopThreeHits); err != nil {
		return RetrievalStats{}, fmt.Errorf("audit: approximate accuracy: %w", err)
	}

	if err := conn.QueryRow(ctx, `
		select coalesce(percentile_cont(0.5) within group (order by duration_ms), 0),
		       coalesce(percentile_cont(0.95) within group (order by duration_ms), 0),
		       count(duration_ms)
		from retrievals
	`).Scan(&stats.P50Milliseconds, &stats.P95Milliseconds, &stats.LatencySamples); err != nil {
		return RetrievalStats{}, fmt.Errorf("audit: retrieval latency: %w", err)
	}
	return stats, nil
}

func collectSolutions(ctx context.Context, conn *pgx.Conn) (SolutionStats, error) {
	var stats SolutionStats
	if err := conn.QueryRow(ctx, `
		select count(*) filter (where outcome = 'success'),
		       count(*) filter (where outcome = 'failure'),
		       count(*) filter (where outcome = 'unknown')
		from outcomes
	`).Scan(&stats.Success, &stats.Failure, &stats.Unknown); err != nil {
		return SolutionStats{}, fmt.Errorf("audit: solution outcomes: %w", err)
	}
	stats.SuccessRate = ratio(stats.Success, stats.Success+stats.Failure)
	return stats, nil
}

func collectInjections(ctx context.Context, conn *pgx.Conn) (InjectionStats, error) {
	var stats InjectionStats
	if err := conn.QueryRow(ctx, `
		select count(*), coalesce(sum(char_length(body)), 0), coalesce(sum(estimated_tokens), 0)
		from notices
	`).Scan(&stats.Notices, &stats.Characters, &stats.EstimatedTokens); err != nil {
		return InjectionStats{}, fmt.Errorf("audit: notices: %w", err)
	}
	return stats, nil
}

func collectRepeats(ctx context.Context, conn *pgx.Conn) (RepeatStats, error) {
	var stats RepeatStats
	if err := conn.QueryRow(ctx, `
		select count(*) filter (where hits > 1),
		       coalesce(sum(hits - 1) filter (where hits > 1), 0)
		from issues
	`).Scan(&stats.RepeatedIssues, &stats.RepeatedHits); err != nil {
		return RepeatStats{}, fmt.Errorf("audit: repeats: %w", err)
	}
	return stats, nil
}

func collectLatency(ctx context.Context, conn *pgx.Conn) (LatencyStats, error) {
	var stats LatencyStats
	if err := conn.QueryRow(ctx, `
		with pairs as (
			select n.created_at - (
			    select max(o.occurred_at) from occurrences o
			    where o.issue_id = n.issue_id and o.occurred_at <= n.created_at
			) as lag
			from notices n
			where n.issue_id is not null
		)
		select coalesce(percentile_cont(0.5) within group (
		           order by extract(epoch from lag) * 1000), 0),
		       count(lag)
		from pairs
		where lag is not null
	`).Scan(&stats.P50Milliseconds, &stats.Samples); err != nil {
		return LatencyStats{}, fmt.Errorf("audit: capture-to-notice latency: %w", err)
	}
	return stats, nil
}

func collectActionCounts(ctx context.Context, conn *pgx.Conn, condition string, arguments ...any) ([]ActionCount, error) {
	rows, err := conn.Query(ctx, `
		select action, count(*) from audit_log
		where `+condition+`
		group by action
		order by action
	`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("audit: actions: %w", err)
	}
	defer rows.Close()

	counts := make([]ActionCount, 0, 8)
	for rows.Next() {
		var item ActionCount
		if err := rows.Scan(&item.Action, &item.Count); err != nil {
			return nil, fmt.Errorf("audit: scan an action count: %w", err)
		}
		counts = append(counts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: actions: %w", err)
	}
	return counts, nil
}

func sumActions(counts []ActionCount) int64 {
	var total int64
	for _, count := range counts {
		total += count.Count
	}
	return total
}

func collectAvoidedDiagnoses(ctx context.Context, conn *pgx.Conn) (int64, error) {
	var avoided int64
	if err := conn.QueryRow(ctx, `
		select coalesce(sum(i.hits - 1), 0)
		from issues i
		where i.hits > 1 and exists (
		    select 1 from solutions s
		    where s.issue_id = i.id and s.status = 'verified'
		)
	`).Scan(&avoided); err != nil {
		return 0, fmt.Errorf("audit: avoided diagnoses: %w", err)
	}
	return avoided, nil
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// ExactHitRate is the share of rounds whose top candidate was an exact
// fingerprint match.
func (s RetrievalStats) ExactHitRate() float64 {
	return ratio(s.ExactTopOne, s.Rounds)
}

// ApproximateTopOneAccuracy is the share of approximate top-1 candidates that were
// later confirmed by a success outcome.
func (s RetrievalStats) ApproximateTopOneAccuracy() float64 {
	return ratio(s.ApproximateTopOneHits, s.ApproximateTopOneRuns)
}

// ApproximateTopThreeAccuracy is the same over the three shown candidates.
func (s RetrievalStats) ApproximateTopThreeAccuracy() float64 {
	return ratio(s.ApproximateTopThreeHits, s.ApproximateTopThreeRuns)
}

// SolutionSuccessRate is successes over decided (success+failure) outcomes.
func (s SolutionStats) SolutionSuccessRate() float64 { return s.SuccessRate }
