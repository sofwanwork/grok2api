package conversation

import (
	"encoding/json"
	"testing"
)

// requestInput converts a Chat Completions body and returns the Responses
// input array.
func requestInput(t *testing.T, body string) []map[string]any {
	t.Helper()
	converted, err := ConvertRequest([]byte(body), "grok-4.6", OperationChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(converted, &payload); err != nil {
		t.Fatalf("converted body is not valid JSON: %v\n%s", err, converted)
	}
	rawInput, ok := payload["input"].([]any)
	if !ok {
		t.Fatalf("input missing or not an array: %s", converted)
	}
	items := make([]map[string]any, 0, len(rawInput))
	for _, entry := range rawInput {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("input item is not an object: %s", converted)
		}
		items = append(items, item)
	}
	return items
}

// The outbound half of reasoning_opaque is covered elsewhere. This is the
// inbound half: a blob the client echoes back must be replayed as a reasoning
// input item, which is what preserves thinking continuity across turns once the
// server-side replay cache misses (TTL expiry or account rotation).
func TestConvertChatRequestReplaysAssistantReasoningOpaque(t *testing.T) {
	input := requestInput(t, `{
		"model":"grok-4.6","messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"answer","reasoning_opaque":"gAAAAABopaque"},
			{"role":"user","content":"second"}
		]}`)
	if len(input) != 4 {
		t.Fatalf("expected reasoning item to be added: %#v", input)
	}
	// The reasoning item must precede its own message so upstream reads the
	// thinking before the answer it produced.
	reasoning := input[1]
	if reasoning["type"] != "reasoning" {
		t.Fatalf("item 1 is not the replayed reasoning: %#v", reasoning)
	}
	if reasoning["encrypted_content"] != "gAAAAABopaque" {
		t.Fatalf("encrypted_content = %#v", reasoning["encrypted_content"])
	}
	// An empty summary array is required by the upstream history contract.
	summary, ok := reasoning["summary"].([]any)
	if !ok || len(summary) != 0 {
		t.Fatalf("summary = %#v, want empty array", reasoning["summary"])
	}
	if input[2]["type"] != "message" || input[2]["role"] != "assistant" {
		t.Fatalf("assistant message did not follow its reasoning: %#v", input[2])
	}
}

// Only assistant turns carry reasoning, and only non-blank blobs are replayed.
// A stray field on another role must not inject a phantom reasoning item.
func TestConvertChatRequestIgnoresIrrelevantReasoningOpaque(t *testing.T) {
	for _, testCase := range []struct{ name, body string }{
		{"user role", `{"model":"grok-4.6","messages":[
			{"role":"user","content":"hi","reasoning_opaque":"gAAAAABopaque"}]}`},
		{"system role", `{"model":"grok-4.6","messages":[
			{"role":"system","content":"rules","reasoning_opaque":"gAAAAABopaque"},
			{"role":"user","content":"hi"}]}`},
		{"blank blob", `{"model":"grok-4.6","messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"answer","reasoning_opaque":"   "}]}`},
		{"absent field", `{"model":"grok-4.6","messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"answer"}]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, item := range requestInput(t, testCase.body) {
				if item["type"] == "reasoning" {
					t.Fatalf("unexpected reasoning item: %#v", item)
				}
			}
		})
	}
}

// A reasoning-only assistant turn still has to replay its blob: an assistant
// message whose visible content was empty must not drop the thinking with it.
func TestConvertChatRequestReplaysReasoningWithoutAssistantContent(t *testing.T) {
	input := requestInput(t, `{
		"model":"grok-4.6","messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"","reasoning_opaque":"gAAAAABopaque"},
			{"role":"user","content":"second"}
		]}`)
	var reasoningCount int
	for _, item := range input {
		if item["type"] == "reasoning" {
			reasoningCount++
			if item["encrypted_content"] != "gAAAAABopaque" {
				t.Fatalf("encrypted_content = %#v", item["encrypted_content"])
			}
		}
	}
	if reasoningCount != 1 {
		t.Fatalf("expected exactly one replayed reasoning item: %#v", input)
	}
}

// Multi-turn continuity: every assistant turn replays its own blob, in order,
// so a long conversation keeps its full chain of thought.
func TestConvertChatRequestReplaysReasoningAcrossMultipleTurns(t *testing.T) {
	input := requestInput(t, `{
		"model":"grok-4.6","messages":[
			{"role":"user","content":"q1"},
			{"role":"assistant","content":"a1","reasoning_opaque":"blob1"},
			{"role":"user","content":"q2"},
			{"role":"assistant","content":"a2","reasoning_opaque":"blob2"},
			{"role":"user","content":"q3"}
		]}`)
	var blobs []string
	for _, item := range input {
		if item["type"] == "reasoning" {
			blob, _ := item["encrypted_content"].(string)
			blobs = append(blobs, blob)
		}
	}
	if len(blobs) != 2 || blobs[0] != "blob1" || blobs[1] != "blob2" {
		t.Fatalf("replayed blobs = %#v, want [blob1 blob2]", blobs)
	}
}
