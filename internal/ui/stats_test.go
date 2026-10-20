package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/audit"
)

func TestStatsPageAlwaysShowsTheFalseMergeTarget(t *testing.T) {
	page := NewStatsPage(audit.Stats{})
	rows := page.MetricRows()
	if len(rows) == 0 {
		t.Fatal("the statistics view is empty")
	}
	first := rows[0]
	if !strings.Contains(first.Label, "False merges") || !strings.Contains(first.Label, "target: 0") {
		t.Fatalf("first metric = %q, want the false-merge target line", first.Label)
	}
	if first.Value != "0" {
		t.Fatalf("false merges value = %q, want 0", first.Value)
	}
}

func TestStatsPageSampleHints(t *testing.T) {
	cases := []struct {
		rounds int64
		want   string
	}{
		{0, "no retrievals recorded yet"},
		{3, "not meaningful yet"},
		{10, "measured, not cached"},
	}
	for _, testCase := range cases {
		page := NewStatsPage(audit.Stats{Retrieval: audit.RetrievalStats{Rounds: testCase.rounds}})
		if note := page.SampleNote(); !strings.Contains(note, testCase.want) {
			t.Fatalf("rounds %d note = %q, want %q", testCase.rounds, note, testCase.want)
		}
	}
}

func TestSavingRowsSeparateMeasuredFromEstimated(t *testing.T) {
	page := NewStatsPage(audit.Stats{
		Savings: audit.SavingsStats{
			InjectedCharacters:        800,
			InjectedTokens:            200,
			AvoidedDiagnoses:          3,
			AvoidedTokens:             1200,
			AssumedTokensPerDiagnosis: 400,
			Assumption:                "estimated: each avoided diagnosis is assumed to have cost about 400 tokens; this is an assumption, not a measurement",
		},
	})
	rows := page.SavingRows()
	if len(rows) != 4 {
		t.Fatalf("saving rows = %d, want 4", len(rows))
	}
	if !strings.Contains(rows[0].Label, "measured") {
		t.Fatalf("first saving row = %q, want measured", rows[0].Label)
	}
	for _, row := range rows[1:] {
		if !strings.Contains(row.Label, "estimated") {
			t.Fatalf("saving row %q is not marked estimated", row.Label)
		}
	}
	if !strings.Contains(page.Assumption(), "assumption") {
		t.Fatalf("assumption = %q", page.Assumption())
	}
	if rows[3].Value != "1200" {
		t.Fatalf("avoided tokens = %q, want 1200", rows[3].Value)
	}
}

func TestStatsRatesAreFormattedFromCounts(t *testing.T) {
	page := NewStatsPage(audit.Stats{
		Retrieval: audit.RetrievalStats{Rounds: 3, ExactTopOne: 2},
	})
	rows := page.MetricRows()
	found := false
	for _, row := range rows {
		if strings.Contains(row.Label, "Exact fingerprint top-1 rate") {
			found = true
			if row.Value != "66.7%" {
				t.Fatalf("exact rate = %q, want 66.7%%", row.Value)
			}
		}
	}
	if !found {
		t.Fatal("the exact fingerprint rate row is missing")
	}
}

func TestAuditRowsAndRangeLabel(t *testing.T) {
	from := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	page := NewStatsPage(audit.Stats{})
	page.SetAudit(audit.Page{
		Total: 21,
		Limit: 10,
		Records: []audit.Row{{
			ID:         1,
			OccurredAt: from.Add(30 * time.Minute),
			Action:     "solution.version.added",
			Entity:     "solution",
			EntityID:   "6f0e1a2b-1234-4abc-8def-0123456789ab",
			Operator:   "owner",
			Source:     "ui",
		}},
	}, audit.Filter{From: from, To: to, Limit: 10, Offset: 10})

	rows := page.AuditRows()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Label, "solution.version.added") {
		t.Fatalf("audit row label = %q", rows[0].Label)
	}
	if !strings.Contains(rows[0].Value, "solution") || !strings.Contains(rows[0].Value, "6f0e1a2b") {
		t.Fatalf("audit row target = %q", rows[0].Value)
	}
	if !strings.Contains(rows[0].Note, "owner") || !strings.Contains(rows[0].Note, "ui") {
		t.Fatalf("audit row note = %q", rows[0].Note)
	}
	label := page.AuditRangeLabel()
	if !strings.Contains(label, "page 2/3") {
		t.Fatalf("range label = %q, want page 2/3", label)
	}
}

func TestEmptyAuditPageSaysSo(t *testing.T) {
	page := NewStatsPage(audit.Stats{})
	page.SetAudit(audit.Page{}, audit.Filter{})
	rows := page.AuditRows()
	if len(rows) != 1 || !strings.Contains(rows[0].Label, "No audit rows") {
		t.Fatalf("empty audit rows = %+v", rows)
	}
}
