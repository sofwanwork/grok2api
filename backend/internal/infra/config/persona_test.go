package config

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A disabled persona must produce no prompt regardless of configured text, so
// toggling `enabled` is a reliable kill switch.
func TestPersonaEffectiveSystemPromptRespectsEnabledAndTrims(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		persona PersonaConfig
		want    string
	}{
		{"disabled with text", PersonaConfig{Enabled: false, SystemPrompt: "You are AKIF."}, ""},
		{"enabled trims surrounding space", PersonaConfig{Enabled: true, SystemPrompt: "  You are AKIF.\n\t"}, "You are AKIF."},
		{"enabled but blank", PersonaConfig{Enabled: true, SystemPrompt: " \n\t "}, ""},
		{"enabled with text", PersonaConfig{Enabled: true, SystemPrompt: "You are AKIF."}, "You are AKIF."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.persona.EffectiveSystemPrompt(); got != testCase.want {
				t.Fatalf("EffectiveSystemPrompt() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// An oversized persona is truncated rather than rejected: a misconfigured value
// must never take down inference.
func TestPersonaSizeLimitedSystemPromptTruncatesInsteadOfFailing(t *testing.T) {
	persona := PersonaConfig{
		Enabled:              true,
		SystemPrompt:         strings.Repeat("a", 100),
		MaxSystemPromptBytes: 10,
	}
	got := persona.SizeLimitedSystemPrompt()
	if len(got) != 10 {
		t.Fatalf("truncated length = %d, want 10", len(got))
	}
	if got != strings.Repeat("a", 10) {
		t.Fatalf("truncated prompt = %q", got)
	}
}

// Truncation must land on a rune boundary. Cutting mid-sequence would emit
// invalid UTF-8 into the upstream payload; the persona is written in Malay and
// may contain emoji, so multi-byte runes are the normal case, not an edge case.
func TestPersonaSizeLimitedSystemPromptNeverSplitsRunes(t *testing.T) {
	for _, testCase := range []struct{ name, text string }{
		{"3-byte runes", strings.Repeat("あ", 40)},
		{"4-byte emoji", strings.Repeat("🙂", 40)},
		{"2-byte accents", strings.Repeat("é", 40)},
		{"mixed widths", strings.Repeat("aé🙂あ", 20)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Sweep limits so at least some land mid-rune for every width.
			for limit := 1; limit <= 24; limit++ {
				persona := PersonaConfig{
					Enabled:              true,
					SystemPrompt:         testCase.text,
					MaxSystemPromptBytes: limit,
				}
				got := persona.SizeLimitedSystemPrompt()
				if len(got) > limit {
					t.Fatalf("limit %d produced %d bytes", limit, len(got))
				}
				if !utf8.ValidString(got) {
					t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
				}
				if !strings.HasPrefix(testCase.text, got) {
					t.Fatalf("limit %d altered the leading text: %q", limit, got)
				}
			}
		})
	}
}

// An unset or non-positive cap falls back to the documented default instead of
// truncating everything to nothing.
func TestPersonaSizeLimitedSystemPromptFallsBackToDefaultCap(t *testing.T) {
	for _, limit := range []int{0, -1} {
		persona := PersonaConfig{
			Enabled:              true,
			SystemPrompt:         strings.Repeat("a", 64),
			MaxSystemPromptBytes: limit,
		}
		if got := persona.SizeLimitedSystemPrompt(); len(got) != 64 {
			t.Fatalf("cap %d truncated a small persona to %d bytes", limit, len(got))
		}
	}

	oversized := PersonaConfig{
		Enabled:      true,
		SystemPrompt: strings.Repeat("a", DefaultPersonaMaxSystemPromptBytes+512),
	}
	if got := oversized.SizeLimitedSystemPrompt(); len(got) != DefaultPersonaMaxSystemPromptBytes {
		t.Fatalf("default cap produced %d bytes, want %d", len(got), DefaultPersonaMaxSystemPromptBytes)
	}
}

// A disabled persona stays empty even when it would otherwise be truncated,
// so the size cap cannot resurrect a switched-off persona.
func TestPersonaSizeLimitedSystemPromptStaysEmptyWhenDisabled(t *testing.T) {
	persona := PersonaConfig{
		Enabled:              false,
		SystemPrompt:         strings.Repeat("a", 100),
		MaxSystemPromptBytes: 10,
	}
	if got := persona.SizeLimitedSystemPrompt(); got != "" {
		t.Fatalf("disabled persona returned %q", got)
	}
}
