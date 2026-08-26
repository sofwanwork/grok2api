package gateway

import (
	"testing"
)

func TestDegradeCircuitOpenTripsAtThreshold(t *testing.T) {
	cfg := QualityRetryRuntime{DegradeCircuitThreshold: 2}
	if !degradeCircuitOpen(2, cfg) {
		t.Fatal("circuit must trip at threshold")
	}
	if !degradeCircuitOpen(3, cfg) {
		t.Fatal("circuit must trip beyond threshold")
	}
}

func TestDegradeCircuitOpenStaysClosedBelowThreshold(t *testing.T) {
	cfg := QualityRetryRuntime{DegradeCircuitThreshold: 2}
	if degradeCircuitOpen(0, cfg) {
		t.Fatal("circuit must stay closed at 0 withholds")
	}
	if degradeCircuitOpen(1, cfg) {
		t.Fatal("circuit must stay closed below threshold")
	}
}

func TestDegradeCircuitOpenDisabledAtZero(t *testing.T) {
	cfg := QualityRetryRuntime{DegradeCircuitThreshold: 0}
	if degradeCircuitOpen(99, cfg) {
		t.Fatal("circuit 0 must never trip")
	}
}

func TestDegradeCircuitOpenDisabledAtNegative(t *testing.T) {
	cfg := QualityRetryRuntime{DegradeCircuitThreshold: -1}
	if degradeCircuitOpen(99, cfg) {
		t.Fatal("negative threshold must never trip")
	}
}

func TestNormalizeQualityRetryClampsDegradeCircuit(t *testing.T) {
	cfg := normalizeQualityRetry(QualityRetryRuntime{DegradeCircuitThreshold: -5})
	if cfg.DegradeCircuitThreshold != 0 {
		t.Fatalf("negative threshold must be clamped to 0, got %d", cfg.DegradeCircuitThreshold)
	}
	cfg = normalizeQualityRetry(QualityRetryRuntime{DegradeCircuitThreshold: 3})
	if cfg.DegradeCircuitThreshold != 3 {
		t.Fatalf("positive threshold must be preserved, got %d", cfg.DegradeCircuitThreshold)
	}
}
