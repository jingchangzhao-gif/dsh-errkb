package ui

import (
	"fmt"
	"strings"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/audit"
)

// StatsPage is the read-only statistics and audit view model (EKB-049). It holds
// one collected snapshot; every number in it came from a database aggregate, and
// the model only formats. The audit list is a separate, paged read.
type StatsPage struct {
	stats  audit.Stats
	page   audit.Page
	filter audit.Filter
	status string
	loaded bool
}

// minMeaningfulRounds is the sample size below which the card requires the view
// to say the rates are not yet meaningful.
const minMeaningfulRounds = 5

// StatRow is one labeled line in the statistics view.
type StatRow struct {
	Label string
	Value string
	Note  string
}

// NewStatsPage wraps a collected snapshot.
func NewStatsPage(stats audit.Stats) *StatsPage {
	return &StatsPage{stats: stats, loaded: true}
}

// SetStatus records a load error so the view can show it instead of stale data.
func (p *StatsPage) SetStatus(message string) {
	p.status = message
}

// Status is non-empty when the last collection or audit read failed.
func (p *StatsPage) Status() string { return p.status }

// Loaded reports whether a snapshot is present.
func (p *StatsPage) Loaded() bool { return p.loaded }

// Stats returns the snapshot (read-only use).
func (p *StatsPage) Stats() audit.Stats { return p.stats }

// DatabaseError returns the error text when no snapshot could be collected.
func (p *StatsPage) DatabaseError() string {
	if p.loaded {
		return ""
	}
	return p.status
}

// SampleNote explains whether the measured rates are meaningful yet.
func (p *StatsPage) SampleNote() string {
	if !p.loaded {
		return "no statistics loaded"
	}
	rounds := p.stats.Retrieval.Rounds
	if rounds == 0 {
		return "no retrievals recorded yet — rates appear after the first lookup"
	}
	if rounds < minMeaningfulRounds {
		return fmt.Sprintf("only %d retrieval(s) recorded — rates are not meaningful yet", rounds)
	}
	return fmt.Sprintf("%d retrieval rounds · measured, not cached", rounds)
}

// Assumption is the savings assumption shown next to the estimate.
func (p *StatsPage) Assumption() string { return p.stats.Savings.Assumption }

// MetricRows are the measured retrieval and outcome lines. The false-merge line is
// always present — the card requires the target zero to stay visible even when it
// is met — and latency is the measured p50/p95, not a cached number.
func (p *StatsPage) MetricRows() []StatRow {
	stats := p.stats
	rows := []StatRow{
		{Label: "False merges (target: 0)", Value: intText(stats.Retrieval.FalseMerges),
			Note: "a non-fingerprint match was ever stored as exact — must stay 0"},
		{Label: "Exact fingerprint top-1 rate", Value: percentText(stats.Retrieval.ExactHitRate()),
			Note: fmt.Sprintf("%d of %d retrieval rounds", stats.Retrieval.ExactTopOne, stats.Retrieval.Rounds)},
		{Label: "Approximate top-1 accuracy", Value: percentText(stats.Retrieval.ApproximateTopOneAccuracy()),
			Note: fmt.Sprintf("%d of %d approximate top-1 candidates confirmed by a success outcome",
				stats.Retrieval.ApproximateTopOneHits, stats.Retrieval.ApproximateTopOneRuns)},
		{Label: "Approximate top-3 accuracy", Value: percentText(stats.Retrieval.ApproximateTopThreeAccuracy()),
			Note: fmt.Sprintf("%d of %d approximate candidates in the shown three confirmed",
				stats.Retrieval.ApproximateTopThreeHits, stats.Retrieval.ApproximateTopThreeRuns)},
		{Label: "Retrieval latency p50", Value: millisecondsText(stats.Retrieval.P50Milliseconds),
			Note: fmt.Sprintf("measured over %d retrieval(s)", stats.Retrieval.LatencySamples)},
		{Label: "Retrieval latency p95", Value: millisecondsText(stats.Retrieval.P95Milliseconds),
			Note: "measured, not cached"},
		{Label: "Verified solution success rate", Value: percentText(stats.Solutions.SolutionSuccessRate()),
			Note: fmt.Sprintf("%d success, %d failure, %d unknown",
				stats.Solutions.Success, stats.Solutions.Failure, stats.Solutions.Unknown)},
		{Label: "Repeated failures", Value: intText(stats.Repeats.RepeatedHits),
			Note: fmt.Sprintf("%d issue(s) seen more than once", stats.Repeats.RepeatedIssues)},
		{Label: "Capture-to-notice latency p50", Value: millisecondsText(stats.Latency.P50Milliseconds),
			Note: fmt.Sprintf("%d notice(s) followed a capture", stats.Latency.Samples)},
		{Label: "Manual corrections", Value: intText(stats.Corrections.Total),
			Note: describeActionCounts(stats.Corrections.ByAction)},
		{Label: "Persisted faults", Value: intText(stats.Faults.Total),
			Note: "fault.* audit rows; queue and connector faults have no table yet, so they are not counted here"},
	}
	return rows
}

// SavingRows are the savings lines, each marked measured or estimated. The card
// requires the split and the assumption to be visible.
func (p *StatsPage) SavingRows() []StatRow {
	stats := p.stats.Savings
	return []StatRow{
		{Label: "Characters handed back (measured)", Value: intText(stats.InjectedCharacters),
			Note: fmt.Sprintf("%d notice(s) written", p.stats.Injections.Notices)},
		{Label: "Tokens handed back (estimated from characters)", Value: intText(stats.InjectedTokens),
			Note: "estimate: about one token per four characters, not an exact count"},
		{Label: "Avoided diagnoses (estimated)", Value: intText(stats.AvoidedDiagnoses),
			Note: "repeated hits of issues that already have a verified solution"},
		{Label: "Avoided tokens (estimated)", Value: intText(stats.AvoidedTokens),
			Note: fmt.Sprintf("assumes %d tokens per avoided diagnosis — an assumption, not a measurement",
				stats.AssumedTokensPerDiagnosis)},
	}
}

// SetAudit replaces the audit page and the filter it was read with.
func (p *StatsPage) SetAudit(page audit.Page, filter audit.Filter) {
	p.page = page
	p.filter = filter
	p.loaded = true
}

// Filter is the audit filter this page last asked for.
func (p *StatsPage) Filter() audit.Filter { return p.filter }

// AuditTotal is the unpaged number of matching audit rows.
func (p *StatsPage) AuditTotal() int { return p.page.Total }

// AuditRows formats the current audit page: time, action, object, operator.
func (p *StatsPage) AuditRows() []StatRow {
	rows := make([]StatRow, 0, len(p.page.Records))
	for _, record := range p.page.Records {
		target := record.Entity
		if record.EntityID != "" {
			target = record.Entity + " " + shorten(record.EntityID, 12)
		}
		rows = append(rows, StatRow{
			Label: record.OccurredAt.UTC().Format("2006-01-02 15:04:05") + " · " + record.Action,
			Value: target,
			Note:  fmt.Sprintf("%s via %s", record.Operator, record.Source),
		})
	}
	if len(rows) == 0 {
		rows = append(rows, StatRow{Label: "No audit rows match this filter", Value: "—"})
	}
	return rows
}

// AuditRangeLabel describes the active audit time range for the view header.
func (p *StatsPage) AuditRangeLabel() string {
	from := "from beginning"
	to := "to now"
	if !p.filter.From.IsZero() {
		from = "from " + p.filter.From.UTC().Format("2006-01-02 15:04")
	}
	if !p.filter.To.IsZero() {
		to = "to " + p.filter.To.UTC().Format("2006-01-02 15:04")
	}
	filter := p.filter.Normalized()
	return fmt.Sprintf("%s %s · page %d/%d", from, to, filter.Offset/filter.Limit+1,
		maxInt(1, (p.page.Total+filter.Limit-1)/filter.Limit))
}

func describeActionCounts(counts []audit.ActionCount) string {
	if len(counts) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", count.Action, count.Count))
	}
	return strings.Join(parts, ", ")
}

func percentText(rate float64) string {
	return fmt.Sprintf("%.1f%%", rate*100)
}

func millisecondsText(value float64) string {
	return fmt.Sprintf("%.1f ms", value)
}

func intText(value int64) string {
	return fmt.Sprintf("%d", value)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
