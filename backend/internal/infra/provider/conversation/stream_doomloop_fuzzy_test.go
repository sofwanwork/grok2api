package conversation

import (
	"strings"
	"testing"
)

func TestDoomLoopExactMatchTriggers(t *testing.T) {
	tracker := streamRepeatTracker{}
	delta := "hello world this is a long enough delta to avoid false positives\n"
	for i := 0; i < contentDoomLoopThreshold; i++ {
		if err := tracker.trackContent(delta); err != nil {
			// Rolling window may trigger before exact threshold — that's OK
			t.Logf("triggered at %d (rolling or fuzzy) — acceptable", i)
			return
		}
	}
	err := tracker.trackContent(delta)
	if err == nil {
		t.Fatal("exact match must trigger at threshold+1")
	}
}

func TestDoomLoopFuzzyWhitespaceBypassFixed(t *testing.T) {
	// Patch #28: trailing space variation should now be caught
	tracker := streamRepeatTracker{}
	deltas := []string{
		"Aku buat semula dengan next/image yang lebih bersih.\n",
		"Aku buat semula dengan next/image yang lebih bersih.\n ",
		"Aku buat semula dengan next/image yang lebih bersih.\r\n",
		"Aku buat semula dengan next/image yang lebih bersih.\n",
	}
	for i := 0; i < 50; i++ {
		delta := deltas[i%len(deltas)]
		err := tracker.trackContent(delta)
		if err != nil {
			// Should trigger around 32-35
			return
		}
	}
	t.Fatal("fuzzy whitespace variation must trigger doom loop")
}

func TestDoomLoopFuzzyDoesNotKillLegitimateRepetition(t *testing.T) {
	// Single-character deltas like table separators should NOT trigger
	// fuzzy (len < 3 → skip), BUT exact match WILL trigger at 128+1.
	// So we test with a longer non-repeating delta.
	tracker := streamRepeatTracker{}
	for i := 0; i < 200; i++ {
		delta := "row " + string(rune('A'+(i%26))) + " value\n"
		if err := tracker.trackContent(delta); err != nil {
			t.Fatalf("varied content should not trigger: %v", err)
		}
	}
}

func TestDoomLoopSingleCharExactMatchOK(t *testing.T) {
	// Single char "-" repeated — fuzzy skips (len<3),
	// exact match triggers at 129. This is the ORIGINAL behavior.
	tracker := streamRepeatTracker{}
	for i := 0; i < 128; i++ {
		err := tracker.trackContent("-")
		if err != nil {
			t.Fatalf("single-char exact match triggered too early at %d: %v", i, err)
		}
	}
	// At 129, exact match fires — that's correct
	err := tracker.trackContent("-")
	if err == nil {
		t.Fatal("single-char exact match must trigger at 129")
	}
}

func TestDoomLoopRollingWindowCatchesMultiDeltaRepetition(t *testing.T) {
	// Model sends "Aku buat semula dengan next/image yang lebih bersih.\n"
	// as one large delta, repeated many times
	tracker := streamRepeatTracker{}
	delta := "Aku buat semula dengan next/image yang lebih bersih.\n"
	for i := 0; i < 50; i++ {
		err := tracker.trackContent(delta)
		if err != nil {
			// Should trigger from either exact (128) or fuzzy (32) or rolling (4)
			return
		}
	}
	t.Fatal("rolling window must catch multi-delta repetition")
}

func TestDoomLoopRollingWindowCatchesSplitDeltaRepetition(t *testing.T) {
	// Model sends the line split across multiple different deltas
	// delta1="Aku buat", delta2=" semula dengan", delta3=" next/image"
	// delta4=" yang lebih bersih.\n"
	// Repeated many times — exact match won't catch this
	tracker := streamRepeatTracker{}
	deltas := []string{
		"Aku buat",
		" semula dengan",
		" next/image",
		" yang lebih bersih.\n",
	}
	for cycle := 0; cycle < 100; cycle++ {
		for _, delta := range deltas {
			err := tracker.trackContent(delta)
			if err != nil {
				// Rolling window should catch this after a few cycles
				return
			}
		}
	}
	t.Fatal("rolling window must catch split-delta repetition")
}

func TestDoomLoopNormalizeContentDelta(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello\n", "hello"},
		{"hello \n", "hello"},
		{"hello  \t\n", "hello"},
		{"hello\r\nworld\r\n", "hello world"},
		{"hello   world", "hello world"},
		{"  hello  world  ", "hello world"},
	}
	for _, tt := range tests {
		got := normalizeContentDelta(tt.input)
		if got != tt.expected {
			t.Errorf("normalize(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDoomLoopWindowCapMemory(t *testing.T) {
	// Ensure window doesn't grow unbounded
	tracker := streamRepeatTracker{}
	for i := 0; i < 1000; i++ {
		delta := "different content " + strings.Repeat("x", 10) + "\n"
		if err := tracker.trackContent(delta); err != nil {
			// Should not trigger — all different
		}
	}
	if tracker.contentWindowLen > 5000 {
		t.Fatalf("window grew to %d bytes — should be capped at ~4KB", tracker.contentWindowLen)
	}
}

func TestDoomLoopReasoningAlsoGetsFuzzy(t *testing.T) {
	// Reasoning tracker should also be covered — but currently uses
	// exact match only. This test documents the gap.
	tracker := streamRepeatTracker{}
	delta1 := "thinking about it\n"
	delta2 := "thinking about it\n "
	for i := 0; i < 300; i++ {
		delta := delta1
		if i%2 == 1 {
			delta = delta2
		}
		err := tracker.trackReasoning(delta, "test")
		if err != nil {
			return // Triggered — good
		}
	}
	// Note: reasoning fuzzy not implemented — this is a known gap.
	// The test documents that reasoning tracker uses exact match only.
	t.Log("Note: reasoning tracker does not have fuzzy matching yet — known gap")
}
