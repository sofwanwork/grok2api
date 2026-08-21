package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const testPersona = "You are AKIF."

// chatMessages decodes the messages array of a Chat Completions body.
func chatMessages(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("injected body is not valid JSON: %v\n%s", err, body)
	}
	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("messages missing or not an array: %s", body)
	}
	messages := make([]map[string]any, 0, len(rawMessages))
	for _, entry := range rawMessages {
		message, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("message is not an object: %s", body)
		}
		messages = append(messages, message)
	}
	return messages
}

func systemContents(messages []map[string]any) []string {
	var found []string
	for _, message := range messages {
		role, _ := message["role"].(string)
		if strings.EqualFold(role, "system") || strings.EqualFold(role, "developer") {
			content, _ := message["content"].(string)
			found = append(found, content)
		}
	}
	return found
}

// The persona is a fallback voice, not an override. An IDE that ships its own
// system prompt (opencode AGENTS.md, Cursor rules) must reach upstream intact.
func TestInjectPersonaIntoChatRequestNeverOverridesClientSystemPrompt(t *testing.T) {
	adapter := NewAdapter(Config{PersonaSystemPrompt: testPersona}, nil)
	for _, role := range []string{"system", "developer", "SYSTEM", "Developer"} {
		t.Run(role, func(t *testing.T) {
			body := []byte(`{"model":"grok-4.6","messages":[` +
				`{"role":"` + role + `","content":"client rules"},` +
				`{"role":"user","content":"hi"}]}`)
			injected, err := adapter.injectPersonaIntoChatRequest(body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(injected), testPersona) {
				t.Fatalf("persona overrode the client system prompt: %s", injected)
			}
			contents := systemContents(chatMessages(t, injected))
			if len(contents) != 1 || contents[0] != "client rules" {
				t.Fatalf("client instructions were altered: %#v", contents)
			}
		})
	}
}

// With no client system prompt the persona supplies the voice, and it must be
// first so upstream reads it as the leading instruction.
func TestInjectPersonaIntoChatRequestFillsMissingSystemPrompt(t *testing.T) {
	adapter := NewAdapter(Config{PersonaSystemPrompt: testPersona}, nil)
	body := []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hi"}]}`)
	injected, err := adapter.injectPersonaIntoChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	messages := chatMessages(t, injected)
	if len(messages) != 2 {
		t.Fatalf("expected persona plus user message: %#v", messages)
	}
	if role, _ := messages[0]["role"].(string); role != "system" {
		t.Fatalf("persona is not the leading system message: %#v", messages[0])
	}
	if content, _ := messages[0]["content"].(string); content != testPersona {
		t.Fatalf("persona content = %#v", messages[0]["content"])
	}
	if role, _ := messages[1]["role"].(string); role != "user" {
		t.Fatalf("user message was displaced: %#v", messages[1])
	}
}

// Opt-in append mode keeps client instructions and adds the persona after the
// last system/developer message, never before it.
func TestInjectPersonaIntoChatRequestAppendsAfterClientSystem(t *testing.T) {
	adapter := NewAdapter(Config{
		PersonaSystemPrompt:           testPersona,
		PersonaAppendWithClientSystem: true,
	}, nil)
	body := []byte(`{"model":"grok-4.6","messages":[` +
		`{"role":"system","content":"first"},` +
		`{"role":"developer","content":"second"},` +
		`{"role":"user","content":"hi"}]}`)
	injected, err := adapter.injectPersonaIntoChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	contents := systemContents(chatMessages(t, injected))
	want := []string{"first", "second", testPersona}
	if len(contents) != len(want) {
		t.Fatalf("system messages = %#v", contents)
	}
	for i := range want {
		if contents[i] != want[i] {
			t.Fatalf("system order = %#v, want %#v", contents, want)
		}
	}
}

// A disabled or blank persona must be a no-op, and a malformed body must be
// passed through untouched so the downstream validator owns the error.
func TestInjectPersonaIntoChatRequestPassesThroughUnaffectedBodies(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		persona string
		body    string
	}{
		{"empty persona", "", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"whitespace persona", "   \n\t ", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"malformed json", testPersona, `{"messages":`},
		{"no messages field", testPersona, `{"model":"grok-4.6"}`},
		{"messages not an array", testPersona, `{"messages":"nope"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := NewAdapter(Config{PersonaSystemPrompt: testCase.persona}, nil)
			injected, err := adapter.injectPersonaIntoChatRequest([]byte(testCase.body))
			if err != nil {
				t.Fatal(err)
			}
			if string(injected) != testCase.body {
				t.Fatalf("body was rewritten: %s", injected)
			}
		})
	}
}

// Anthropic Messages carries instructions in a top-level `system` field that is
// either a string or a block array. Both shapes must survive.
func TestInjectPersonaIntoMessagesRequestRespectsClientSystemField(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{"string system", `{"model":"grok-4.6","system":"client rules","messages":[]}`},
		{"block system", `{"model":"grok-4.6","system":[{"type":"text","text":"client rules"}],"messages":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := NewAdapter(Config{PersonaSystemPrompt: testPersona}, nil)
			injected, err := adapter.injectPersonaIntoMessagesRequest([]byte(testCase.body))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(injected), testPersona) {
				t.Fatalf("persona overrode the client system field: %s", injected)
			}
			if !strings.Contains(string(injected), "client rules") {
				t.Fatalf("client instructions lost: %s", injected)
			}
		})
	}
}

func TestInjectPersonaIntoMessagesRequestFillsAbsentOrEmptySystem(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{"absent", `{"model":"grok-4.6","messages":[]}`},
		{"empty string", `{"model":"grok-4.6","system":"","messages":[]}`},
		{"null", `{"model":"grok-4.6","system":null,"messages":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := NewAdapter(Config{PersonaSystemPrompt: testPersona}, nil)
			injected, err := adapter.injectPersonaIntoMessagesRequest([]byte(testCase.body))
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(injected, &payload); err != nil {
				t.Fatalf("injected body is not valid JSON: %v\n%s", err, injected)
			}
			if payload["system"] != testPersona {
				t.Fatalf("system = %#v", payload["system"])
			}
		})
	}
}

// KNOWN GAP, documented rather than asserted as desired behaviour.
//
// isEmptyJSON (normalize.go) treats only ``, `null` and `""` as empty, so an
// explicit empty block array `"system": []` counts as a client-supplied system
// field. The persona is therefore skipped and upstream receives no instructions
// at all. Anthropic SDKs that always emit the key and let callers append blocks
// hit this: the request is silently persona-less.
//
// This test pins the current behaviour so the gap is visible and a future fix
// is a deliberate, reviewed change. To fix, extend isEmptyJSON to recognise
// `[]` and `{}` — but that helper is shared with other normalisation paths, so
// the blast radius needs checking first.
func TestInjectPersonaIntoMessagesRequestSkipsPersonaForEmptyBlockArray(t *testing.T) {
	adapter := NewAdapter(Config{PersonaSystemPrompt: testPersona}, nil)
	injected, err := adapter.injectPersonaIntoMessagesRequest(
		[]byte(`{"model":"grok-4.6","system":[],"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(injected), testPersona) {
		t.Fatal("persona now fills an empty block array: update this test and remove the KNOWN GAP note")
	}
}

// Append mode must preserve the client's system shape: a string stays a string,
// a block array gains a text block rather than being flattened.
func TestAppendPersonaToAnthropicSystemPreservesShape(t *testing.T) {
	t.Run("string keeps client text first", func(t *testing.T) {
		combined, err := appendPersonaToAnthropicSystem(json.RawMessage(`"client rules"`), testPersona)
		if err != nil {
			t.Fatal(err)
		}
		var value string
		if err := json.Unmarshal(combined, &value); err != nil {
			t.Fatalf("string system became %s", combined)
		}
		if !strings.HasPrefix(value, "client rules") || !strings.HasSuffix(value, testPersona) {
			t.Fatalf("combined system = %q", value)
		}
	})

	t.Run("blocks gain a trailing text block", func(t *testing.T) {
		combined, err := appendPersonaToAnthropicSystem(
			json.RawMessage(`[{"type":"text","text":"client rules"}]`), testPersona)
		if err != nil {
			t.Fatal(err)
		}
		var blocks []map[string]any
		if err := json.Unmarshal(combined, &blocks); err != nil {
			t.Fatalf("block system became %s", combined)
		}
		if len(blocks) != 2 {
			t.Fatalf("blocks = %#v", blocks)
		}
		if blocks[0]["text"] != "client rules" {
			t.Fatalf("client block altered: %#v", blocks[0])
		}
		if blocks[1]["type"] != "text" || blocks[1]["text"] != testPersona {
			t.Fatalf("persona block = %#v", blocks[1])
		}
	})

	t.Run("unsupported shape reports an error", func(t *testing.T) {
		if _, err := appendPersonaToAnthropicSystem(json.RawMessage(`{"type":"text"}`), testPersona); err == nil {
			t.Fatal("an object system field must not be silently accepted")
		}
	})
}
