package cli

import (
	"encoding/json"
	"testing"
)

func decodeMaxTokens(t *testing.T, body []byte) (json.RawMessage, bool) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v\n%s", err, body)
	}
	value, ok := payload["max_tokens"]
	return value, ok
}

// Reasoning models count thinking tokens against the completion budget, so a
// request with no cap inherits a small upstream default and starves xhigh
// effort. The gateway fills in a 64k budget instead.
func TestEnsureChatMaxOutputTokensFillsMissingBudget(t *testing.T) {
	for _, testCase := range []struct{ name, body string }{
		{"absent", `{"model":"grok-4.6","messages":[]}`},
		{"null", `{"model":"grok-4.6","max_tokens":null,"messages":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value, ok := decodeMaxTokens(t, ensureChatMaxOutputTokens([]byte(testCase.body)))
			if !ok {
				t.Fatal("max_tokens was not injected")
			}
			var budget int
			if err := json.Unmarshal(value, &budget); err != nil {
				t.Fatalf("max_tokens is not a number: %s", value)
			}
			if budget != chatDefaultMaxOutputTokens {
				t.Fatalf("max_tokens = %d, want %d", budget, chatDefaultMaxOutputTokens)
			}
		})
	}
}

// An explicit caller budget always wins, including a deliberately small one.
// Silently raising it would break agents that cap output on purpose.
func TestEnsureChatMaxOutputTokensNeverOverridesExplicitBudget(t *testing.T) {
	for _, testCase := range []struct{ name, body string }{
		{"max_tokens", `{"model":"grok-4.6","max_tokens":128,"messages":[]}`},
		{"max_completion_tokens", `{"model":"grok-4.6","max_completion_tokens":256,"messages":[]}`},
		{"both", `{"model":"grok-4.6","max_tokens":128,"max_completion_tokens":256,"messages":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := string(ensureChatMaxOutputTokens([]byte(testCase.body))); got != testCase.body {
				t.Fatalf("explicit budget was rewritten: %s", got)
			}
		})
	}
}

// A malformed body must pass through byte-for-byte so the downstream validator
// owns the error rather than this helper masking it.
func TestEnsureChatMaxOutputTokensPassesThroughMalformedBody(t *testing.T) {
	for _, body := range []string{`{"messages":`, `not json`, ``} {
		if got := string(ensureChatMaxOutputTokens([]byte(body))); got != body {
			t.Fatalf("malformed body was rewritten: %q -> %q", body, got)
		}
	}
}

// The injected default must match the ceiling advertised by /v1/models
// (inference.modelMaxOutputTokens). If the two drift, the gateway either
// advertises a budget it will not grant, or injects one it will reject.
//
// Cross-package constants cannot be imported here without a dependency cycle,
// so this pins the value with an explicit reminder. Keep in sync with
// grokMaxOutputTokens in internal/transport/http/inference/handler.go.
func TestChatDefaultMaxOutputTokensMatchesAdvertisedCeiling(t *testing.T) {
	const advertisedCeiling = 65536
	if chatDefaultMaxOutputTokens != advertisedCeiling {
		t.Fatalf("chatDefaultMaxOutputTokens = %d but /v1/models advertises %d; "+
			"update grokMaxOutputTokens in internal/transport/http/inference/handler.go too",
			chatDefaultMaxOutputTokens, advertisedCeiling)
	}
}
