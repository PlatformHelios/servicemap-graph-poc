package cmdb

import (
	"strings"
	"testing"
)

func TestSummarizeRequests(t *testing.T) {
	stats := summarizeRequests([]CatalogRequestSummary{
		{ID: "REQ-1", State: RequestFulfilled, Version: "1.0.0", SubmittedAt: "2026-10-01T09:00:00Z", ApprovedAt: "2026-10-01T11:00:00Z", FinishedAt: "2026-10-01T11:30:00Z"},
		{ID: "REQ-2", State: RequestFailed, Version: "1.0.0", SubmittedAt: "2026-10-02T09:00:00Z", ApprovedAt: "2026-10-02T13:00:00Z", FinishedAt: "2026-10-02T13:01:00Z", FailureReason: "no capacity"},
		{ID: "REQ-3", State: RequestInReview, Version: "1.0.0", SubmittedAt: "2026-10-03T09:00:00Z"},
		{ID: "REQ-4", State: RequestInProgress, Version: "1.1.0", SubmittedAt: "2026-10-04T09:00:00Z", ApprovedAt: "2026-10-04T09:30:00Z"},
	})
	if stats.Total != 4 || stats.ByState["fulfilled"] != 1 || stats.ByState["failed"] != 1 {
		t.Fatalf("counts = %+v", stats)
	}
	if stats.ByVersion["1.0.0"] != 1 || stats.ByVersion["1.1.0"] != 1 {
		t.Errorf("open requests by version = %v", stats.ByVersion)
	}
	if stats.AverageApprovalHours == nil || *stats.AverageApprovalHours < 2.16 || *stats.AverageApprovalHours > 2.17 {
		t.Errorf("average approval hours = %v, want (2 + 4 + 0.5) / 3", stats.AverageApprovalHours)
	}
	if stats.AverageFulfilmentHours == nil || *stats.AverageFulfilmentHours != 0.5 {
		t.Errorf("average fulfilment hours = %v, want 0.5 (fulfilled only)", stats.AverageFulfilmentHours)
	}
	if strings.Join(stats.RecentFailures, ",") != "no capacity" {
		t.Errorf("recent failures = %v", stats.RecentFailures)
	}
	if empty := summarizeRequests(nil); empty.AverageApprovalHours != nil || empty.Total != 0 {
		t.Errorf("no requests = %+v", empty)
	}
}

func TestFieldOrderFollowsXOrder(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{"b": map[string]any{}, "a": map[string]any{}, "c": map[string]any{}},
		"x-order":    []any{"c", "a", "missing"},
	}
	if got := strings.Join(fieldOrder(schema), ","); got != "c,a,b" {
		t.Errorf("fieldOrder = %s, want c,a,b", got)
	}
}
