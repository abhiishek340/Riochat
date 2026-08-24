package mcp

import (
	"encoding/json"
	"testing"
)

func TestAggregateUsage(t *testing.T) {
	in := []UsageItem{
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 3, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: " a ", ToolName: "T", Count: 2, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 1, PriceNanoUSD: 200},
		{ServerID: 2, ServerName: "A", ToolName: "t", Count: 1, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: "A", ToolName: "zero", Count: 0, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: "A", ToolName: "neg", Count: -1, PriceNanoUSD: 100},
	}
	got := AggregateUsage(in)
	want := []UsageItem{
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 5, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 1, PriceNanoUSD: 200},
		{ServerID: 2, ServerName: "A", ToolName: "t", Count: 1, PriceNanoUSD: 100},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d: %+v want %+v", i, got[i], want[i])
		}
	}
	if AggregateUsage(nil) != nil || AggregateUsage([]UsageItem{{Count: 0}}) != nil {
		t.Fatal("empty result is none")
	}
}

func TestPlanBilling(t *testing.T) {
	usage := []UsageItem{
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 3, PriceNanoUSD: 100},
		{ServerID: 1, ServerName: "A", ToolName: "t", Count: 1, PriceNanoUSD: 200},
		{ServerID: 1, ServerName: "A", ToolName: "free", Count: 4, PriceNanoUSD: 0},
	}
	plan := PlanBilling(usage, "usage")
	if len(plan.Lines) != 2 || plan.TotalNanoUSD != 500 || len(plan.Snapshot) != 3 {
		t.Fatalf("plan: %+v", plan)
	}
	l := plan.Lines[0]
	if l.ServiceCode != "mcp_tool.A.t" || l.ServiceName != "A / t" || l.PricingMode != "call" || l.Calls != 3 ||
		l.UnitPriceNanoUSD != 100 || l.BilledNanoUSD != 300 || l.RateMultiplier != 1 {
		t.Fatalf("line: %+v", l)
	}
	if plan.Lines[1].BilledNanoUSD != 200 {
		t.Fatalf("second line: %+v", plan.Lines[1])
	}
	self := PlanBilling(usage, "self")
	if len(self.Lines) != 0 || self.TotalNanoUSD != 0 || len(self.Snapshot) != 3 {
		t.Fatalf("self mode charges nothing but keeps the snapshot: %+v", self)
	}
	var snap []map[string]any
	if err := json.Unmarshal([]byte(self.SnapshotJSON()), &snap); err != nil || len(snap) != 3 {
		t.Fatalf("snapshot json: %s", self.SnapshotJSON())
	}
	for _, f := range []string{"serverID", "serverName", "toolName", "callCount", "priceNanousd"} {
		if _, ok := snap[0][f]; !ok {
			t.Errorf("snapshot lacks %s", f)
		}
	}
	if PlanBilling(nil, "usage").SnapshotJSON() != "[]" {
		t.Fatal("no usage is an empty list")
	}
	// Blank names become "unknown".
	blank := PlanBilling([]UsageItem{{Count: 1, PriceNanoUSD: 9}, {ServerName: "S", Count: 1, PriceNanoUSD: 9}, {ToolName: "T", Count: 1, PriceNanoUSD: 9}}, "period")
	codes := []string{blank.Lines[0].ServiceCode, blank.Lines[1].ServiceCode, blank.Lines[2].ServiceCode}
	names := []string{blank.Lines[0].ServiceName, blank.Lines[1].ServiceName, blank.Lines[2].ServiceName}
	if codes[0] != "mcp_tool.unknown.unknown" || codes[1] != "mcp_tool.S.unknown" || codes[2] != "mcp_tool.unknown.T" ||
		names[0] != "unknown" || names[1] != "S" || names[2] != "T" {
		t.Fatalf("codes %v names %v", codes, names)
	}
	// Overflow saturates instead of wrapping.
	big := PlanBilling([]UsageItem{{ServerID: 1, ServerName: "S", ToolName: "T", Count: 1 << 40, PriceNanoUSD: 1 << 40}}, "usage")
	if big.TotalNanoUSD <= 0 {
		t.Fatalf("overflow: %d", big.TotalNanoUSD)
	}
}
