package controller

import (
	"testing"
	"time"
)

func TestContactUsageUsesMostLimitedCurrentWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	used20, used80 := 20.0, 80.0
	usage := summarizeContactUsage(profileUsageSnapshot{Windows: []profileUsageWindow{{UsedPercent: &used20}, {UsedPercent: &used80}}}, now.Add(-5*time.Minute), now)
	if usage.Status != "available" || usage.RemainingPercent == nil || *usage.RemainingPercent != 20 {
		t.Fatalf("usage=%+v", usage)
	}
	stale := summarizeContactUsage(profileUsageSnapshot{Windows: []profileUsageWindow{{UsedPercent: &used80}}}, now.Add(-2*time.Hour), now)
	if stale.Status != "stale" || stale.RemainingPercent != nil {
		t.Fatalf("stale usage=%+v", stale)
	}
}

func TestContactUsageSupportsSpendBalance(t *testing.T) {
	now := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	limit, used := 10.0, 7.5
	spend := summarizeContactUsage(profileUsageSnapshot{Spend: &profileUsageSpend{Currency: "USD", Limit: &limit, Used: &used}}, now, now)
	if spend.Status != "available" || spend.RemainingAmount == nil || *spend.RemainingAmount != 2.5 || spend.Unit != "USD" {
		t.Fatalf("spend=%+v", spend)
	}
	balance := summarizeContactUsage(profileUsageSnapshot{Balances: []profileUsageBalance{{Unit: "credits", Amount: 3}}}, now, now)
	if balance.Status != "available" || balance.RemainingAmount == nil || *balance.RemainingAmount != 3 || balance.Unit != "credits" {
		t.Fatalf("balance=%+v", balance)
	}
}
