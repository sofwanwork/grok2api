package inference

import "testing"

// grok-4.5/4.6 have verified 64k output capacity. The earlier 16k ceiling
// starved xhigh effort because reasoning tokens count against the same budget.
func TestModelMaxOutputTokensAdvertisesFullBudgetForReasoningModels(t *testing.T) {
	for _, id := range []string{"grok-4.5", "grok-4.6", "grok-4.3"} {
		if got := modelMaxOutputTokens(id, 256_000); got != 65536 {
			t.Fatalf("modelMaxOutputTokens(%q) = %d, want 65536", id, got)
		}
	}
}

// Effort-suffixed aliases are the same upstream model, so they must inherit the
// base budget. An alias falling back to the 10%-of-context heuristic would
// silently hand xhigh a smaller budget than plain grok-4.6.
//
// Only real aliases inherit: an alias exists when the base model advertises
// more than one controllable effort level (see reasoningEffortSuffixes and the
// capability table in modeldomain). xhigh belongs to grok-4.6 only; grok-4.5
// stops at high, so "grok-4.5-xhigh" is not an alias and correctly falls back
// to the heuristic.
func TestModelMaxOutputTokensAliasesInheritBaseBudget(t *testing.T) {
	for _, id := range []string{
		"grok-4.6-low", "grok-4.6-medium", "grok-4.6-high", "grok-4.6-xhigh",
		"grok-4.5-low", "grok-4.5-medium", "grok-4.5-high",
	} {
		if got := modelMaxOutputTokens(id, 256_000); got != 65536 {
			t.Fatalf("modelMaxOutputTokens(%q) = %d, want 65536 inherited from base", id, got)
		}
	}
}

// An effort suffix the base model does not support is not an alias, so it takes
// the heuristic path. Pinned so the distinction stays visible: if grok-4.5 ever
// gains xhigh, this test flips and the inherit test above should cover it.
func TestModelMaxOutputTokensUnsupportedEffortSuffixIsNotAnAlias(t *testing.T) {
	if got := modelMaxOutputTokens("grok-4.5-xhigh", 256_000); got == 65536 {
		t.Fatal("grok-4.5 now advertises xhigh: move this case into the inherit test")
	}
}

// Small models keep their smaller pinned budgets: the 64k bump is targeted at
// reasoning models, not a blanket raise.
func TestModelMaxOutputTokensKeepsSmallModelBudgets(t *testing.T) {
	for _, id := range []string{"grok-3-mini", "grok-3-mini-fast", "grok-chat-fast"} {
		if got := modelMaxOutputTokens(id, 256_000); got != 8192 {
			t.Fatalf("modelMaxOutputTokens(%q) = %d, want 8192", id, got)
		}
	}
}

// Unpinned models fall back to 10% of the context window, clamped to a 4k floor
// and a 64k ceiling.
func TestModelMaxOutputTokensHeuristicIsClamped(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		contextWindow int
		want          int
	}{
		{"tiny context hits floor", 8_000, 4096},
		{"zero context hits floor", 0, 4096},
		{"negative context hits floor", -1, 4096},
		{"mid context uses ten percent", 200_000, 20_000},
		{"huge context hits ceiling", 4_000_000, 65536},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := modelMaxOutputTokens("unknown-model", testCase.contextWindow); got != testCase.want {
				t.Fatalf("modelMaxOutputTokens(unknown, %d) = %d, want %d",
					testCase.contextWindow, got, testCase.want)
			}
		})
	}
}

// The ceiling here must match the default the CLI adapter injects
// (chatDefaultMaxOutputTokens in internal/infra/provider/cli/adapter.go).
// If they drift, /v1/models advertises a budget the gateway will not grant.
func TestAdvertisedCeilingMatchesAdapterDefault(t *testing.T) {
	const adapterDefault = 65536
	if got := modelMaxOutputTokens("grok-4.6", 256_000); got != adapterDefault {
		t.Fatalf("advertised ceiling %d != adapter default %d; "+
			"update chatDefaultMaxOutputTokens in internal/infra/provider/cli/adapter.go too",
			got, adapterDefault)
	}
}
