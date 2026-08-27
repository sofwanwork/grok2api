package account

import (
	"testing"
	"time"
)

func TestBuildBillingQuotaWindowUsesRemaining(t *testing.T) {
	credential := Credential{ID: 42}
	billing := Billing{
		MonthlyLimit:       500000,
		Used:               100000,
		CreditUsagePercent: 20.0,
		SyncedAt:           time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC),
		UsagePeriodEnd:     "2026-08-29T00:00:00+00:00",
	}
	window := BuildBillingQuotaWindow(credential, billing)
	if window == nil {
		t.Fatal("window must not be nil")
	}
	if window.Remaining != 400000 {
		t.Fatalf("remaining = %d, want 400000", window.Remaining)
	}
	if window.Total != 500000 {
		t.Fatalf("total = %d, want 500000", window.Total)
	}
	if window.UsagePercent != 20.0 {
		t.Fatalf("usage_percent = %f, want 20.0", window.UsagePercent)
	}
	if window.Source != QuotaSourceUpstream {
		t.Fatalf("source = %s, want upstream", window.Source)
	}
	if window.ResetAt == nil || !window.ResetAt.Equal(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("reset_at = %v, want 2026-08-29", window.ResetAt)
	}
}

func TestBuildBillingQuotaWindowComputesUsagePercentWhenMissing(t *testing.T) {
	credential := Credential{ID: 42}
	billing := Billing{MonthlyLimit: 500000, Used: 250000, CreditUsagePercent: 0}
	window := BuildBillingQuotaWindow(credential, billing)
	if window.UsagePercent != 50.0 {
		t.Fatalf("computed usage_percent = %f, want 50.0", window.UsagePercent)
	}
}

func TestBuildBillingQuotaWindowZeroRemainingWhenOver(t *testing.T) {
	credential := Credential{ID: 42}
	billing := Billing{MonthlyLimit: 500000, Used: 824728, CreditUsagePercent: 164.9}
	window := BuildBillingQuotaWindow(credential, billing)
	if window.Remaining != 0 {
		t.Fatalf("over-quota remaining = %d, want 0", window.Remaining)
	}
}

func TestBuildBillingQuotaWindowMissingResetAt(t *testing.T) {
	credential := Credential{ID: 42}
	billing := Billing{MonthlyLimit: 500000, Used: 0, UsagePeriodEnd: ""}
	window := BuildBillingQuotaWindow(credential, billing)
	if window.ResetAt != nil {
		t.Fatalf("missing usage_period_end must produce nil reset_at, got %v", window.ResetAt)
	}
}

func TestBuildBillingQuotaWindowReturnsNilForZeroMonthlyLimit(t *testing.T) {
	credential := Credential{ID: 42}
	// MonthlyLimit 0 = "tiada data limit", BUKAN "tiada quota" — window
	// mesti nil supaya akaun kekal fallback (priority/failureCount).
	billing := Billing{MonthlyLimit: 0, Used: 0, PlanName: "Free"}
	window := BuildBillingQuotaWindow(credential, billing)
	if window != nil {
		t.Fatalf("MonthlyLimit 0 must produce nil window, got %+v", window)
	}
}

func TestBuildBillingQuotaWindowReturnsNilForNegativeMonthlyLimit(t *testing.T) {
	credential := Credential{ID: 42}
	billing := Billing{MonthlyLimit: -1, Used: 0}
	window := BuildBillingQuotaWindow(credential, billing)
	if window != nil {
		t.Fatalf("MonthlyLimit < 0 must produce nil window, got %+v", window)
	}
}
