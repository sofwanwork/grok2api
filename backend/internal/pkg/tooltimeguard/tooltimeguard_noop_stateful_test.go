package tooltimeguard

import (
	"encoding/json"
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
	// v3: no HTML comment, no _gatewayMarker — just truncated newString
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment marker — that breaks JSX/TSX")
	}
	if strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("must NOT have _gatewayMarker field — corrupts tool call arguments")
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
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
	if strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("must NOT have _gatewayMarker field")
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
	// v3: no HTML comment, no _gatewayMarker — truncated newString sahaja
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
	if strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("must NOT have _gatewayMarker field")
	}
	// newString mesti truncated (bukan sama dengan oldString penuh)
	newStr := ""
	json.Unmarshal([]byte(updated), &struct{}{}) // parse untuk semak
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) == nil {
		newStr, _ = result["newString"].(string)
	}
	if len(newStr) == 0 {
		t.Fatal("newString must not be empty")
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
	// No-op baru → truncated newString (bukan HTML comment)
	args := `{"oldString":"a","newString":"a"}`
	updated, _ := InterceptNoOpEditStateful("edit", args, state)
	if strings.Contains(updated, "<!--") {
		t.Fatal("after reset, must NOT contain HTML comment")
	}
	if strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("must NOT have _gatewayMarker field")
	}
	if state[0] != 1 {
		t.Fatalf("after reset + 1 no-op, state must be 1, got %d", state[0])
	}
}

func TestNoOpEditFourthAttemptStillCircuitBreaker(t *testing.T) {
	state := []int{3}
	args := `{"oldString":"a","newString":"a"}`
	updated, changed := InterceptNoOpEditStateful("edit", args, state)
	if !changed {
		t.Fatal("4th attempt must still be intercepted")
	}
	// v3: no HTML comment, no _gatewayMarker — truncated newString sahaja
	if strings.Contains(updated, "<!--") {
		t.Fatal("4th attempt must NOT contain HTML comment")
	}
	if strings.Contains(updated, "_gatewayMarker") {
		t.Fatal("4th attempt must NOT have _gatewayMarker field")
	}
}
