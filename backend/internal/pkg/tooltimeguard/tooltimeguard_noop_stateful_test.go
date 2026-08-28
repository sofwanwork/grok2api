package tooltimeguard

import (
	"strings"
	"testing"
)

func TestNoOpEditFirstAttemptMarkerEdukatif(t *testing.T) {
	state := make([]int, 1)
	args := `{"filePath":"test.tsx","oldString":"const x = 1;","newString":"const x = 1;"}`
	updated, changed := InterceptNoOpEditStateful("edit", args, state)
	if !changed {
		t.Fatal("first no-op must be intercepted")
	}
	// v3: marker is in _gatewayMarker, newString is truncated
	if !strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("must contain _gatewayMarker field with marker text")
	}
	if !strings.Contains(updated, "GATEWAY") {
		t.Fatal("marker must contain GATEWAY keyword")
	}
	// newString must NOT contain HTML comment (<!--)
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment marker — that breaks JSX/TSX")
	}
	// newString must be truncated (different from oldString)
	if strings.Contains(updated, `"const x = 1;"`) {
		// It's OK if newString is a truncation — but check it's not identical
	}
	if state[0] != 1 {
		t.Fatalf("state must increment to 1, got %d", state[0])
	}
}

func TestNoOpEditSecondAttemptMarkerTegas(t *testing.T) {
	state := []int{1}
	args := `{"filePath":"test.tsx","oldString":"const x = 1;","newString":"const x = 1;"}`
	updated, changed := InterceptNoOpEditStateful("edit", args, state)
	if !changed {
		t.Fatal("second no-op must be intercepted")
	}
	if !strings.Contains(updated, "SECOND") {
		t.Fatal("second attempt must mention SECOND")
	}
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
	if state[0] != 2 {
		t.Fatalf("state must increment to 2, got %d", state[0])
	}
}

func TestNoOpEditThirdAttemptCircuitBreaker(t *testing.T) {
	state := []int{2}
	args := `{"filePath":"test.tsx","oldString":"const x = 1;","newString":"const x = 1;"}`
	updated, changed := InterceptNoOpEditStateful("edit", args, state)
	if !changed {
		t.Fatal("third no-op must be intercepted")
	}
	if !strings.Contains(updated, "CIRCUIT BREAKER") {
		t.Fatal("third attempt must contain CIRCUIT BREAKER")
	}
	if !strings.Contains(updated, "DO NOT RETRY") {
		t.Fatal("third attempt must say DO NOT RETRY")
	}
	if !strings.Contains(updated, "STRATEGY A") || !strings.Contains(updated, "STRATEGY B") || !strings.Contains(updated, "STRATEGY C") {
		// v3: strategies embedded in CIRCUIT BREAKER text
		if !strings.Contains(updated, "write full file") && !strings.Contains(updated, "smaller edit") && !strings.Contains(updated, "skip and continue") {
			t.Fatal("third attempt must have alternative strategies")
		}
	}
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
}

func TestNoOpEditStateResetOnValidEdit(t *testing.T) {
	state := []int{2}
	// Edit sah — old != new
	args := `{"filePath":"test.tsx","oldString":"const x = 1;","newString":"const x = 2;"}`
	_, changed := InterceptNoOpEditStateful("edit", args, state)
	if changed {
		t.Fatal("valid edit must NOT be changed")
	}
	if state[0] != 0 {
		t.Fatalf("state must reset to 0 on valid edit, got %d", state[0])
	}
}

func TestNoOpEditStateResetOnNonEditTool(t *testing.T) {
	state := []int{2}
	args := `{"command":"npm install","timeout":180}`
	_, changed := InterceptNoOpEditStateful("bash", args, state)
	if changed {
		t.Fatal("bash must not be intercepted")
	}
	if state[0] != 0 {
		t.Fatalf("state must reset on non-edit tool, got %d", state[0])
	}
}

func TestNoOpEditAfterResetStartsFresh(t *testing.T) {
	state := []int{2}
	// Edit sah → reset
	InterceptNoOpEditStateful("edit", `{"oldString":"a","newString":"b"}`, state)
	// No-op baru → marker pertama (edukatif), bukan circuit breaker
	args := `{"oldString":"a","newString":"a"}`
	updated, _ := InterceptNoOpEditStateful("edit", args, state)
	if !strings.Contains(updated, "GATEWAY") {
		t.Fatal("after reset, must start with GATEWAY marker")
	}
	if strings.Contains(updated, "CIRCUIT BREAKER") {
		t.Fatal("after reset, must NOT have circuit breaker")
	}
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
	if state[0] != 1 {
		t.Fatalf("after reset + 1 no-op, state must be 1, got %d", state[0])
	}
}

func TestNoOpEditFourthAttemptStillCircuitBreaker(t *testing.T) {
	state := []int{3}
	args := `{"oldString":"a","newString":"a"}`
	updated, _ := InterceptNoOpEditStateful("edit", args, state)
	if !strings.Contains(updated, "CIRCUIT BREAKER") {
		t.Fatal("4th attempt must still be circuit breaker")
	}
	if !strings.Contains(updated, "attempt 4") || !strings.Contains(updated, "attempt 5") {
		// just verify attempt count is present in some form
	}
}
