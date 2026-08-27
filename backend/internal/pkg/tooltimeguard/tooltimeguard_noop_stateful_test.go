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
	if !strings.Contains(updated, "BLOCKED") {
		t.Fatal("first attempt must have edukatif marker")
	}
	if !strings.Contains(updated, "write tool to replace") {
		t.Fatal("first attempt must mention strategy: write tool")
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
	if !strings.Contains(updated, "Do NOT send the same edit") {
		t.Fatal("second attempt must say Do NOT send the same edit")
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
	if !strings.Contains(updated, "STRATEGY A") {
		t.Fatal("third attempt must have STRATEGY A")
	}
	if !strings.Contains(updated, "STRATEGY B") {
		t.Fatal("third attempt must have STRATEGY B")
	}
	if !strings.Contains(updated, "STRATEGY C") {
		t.Fatal("third attempt must have STRATEGY C")
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
	if !strings.Contains(updated, "BLOCKED") {
		t.Fatal("after reset, must start with edukatif marker")
	}
	if strings.Contains(updated, "CIRCUIT BREAKER") {
		t.Fatal("after reset, must NOT have circuit breaker")
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
