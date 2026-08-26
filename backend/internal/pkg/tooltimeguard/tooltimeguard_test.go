package tooltimeguard

import (
	"strings"
	"testing"
)

// --- Lapisan A: ApplyTimeoutHint ---

func TestApplyTimeoutHintRewritesChatFormat(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [{"role":"user","content":"hi"}],
		"tools": [
			{"type":"function","function":{"name":"bash","description":"Execute a shell command","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}}},
			{"type":"function","function":{"name":"read","description":"Read a file","parameters":{}}}
		]
	}`)
	updated := ApplyTimeoutHint(body)
	text := string(updated)
	if !strings.Contains(text, "MILLISECONDS") {
		t.Fatal("hint must be appended to bash tool description")
	}
	if !strings.Contains(text, `"name":"read"`) || !strings.Contains(text, `"description":"Read a file"`) {
		t.Fatal("read tool must be preserved")
	}
	// Hint muncul sekali dalam description bash (hint tu sendiri sebut
	// "MILLISECONDS" dua kali, jadi kira kemunculan HINT bukan frasa).
	if !strings.Contains(text, `"description":"Execute a shell command IMPORTANT: the timeout parameter is in MILLISECONDS.`) {
		t.Fatal("hint must be appended to bash description")
	}
	if !strings.Contains(text, `"command":{"type":"string"}`) && !strings.Contains(text, `"command": {"type":"string"}`) {
		t.Fatal("parameters schema must be preserved")
	}
	// Idempoten: panggil sekali lagi, tiada perubahan.
	again := ApplyTimeoutHint(updated)
	if strings.Contains(string(again), "Execute a shell command IMPORTANT: the timeout parameter is in MILLISECONDS. 180 means 0.18 seconds — enough time for nothing. Package installs (npm install, npx create-*) need timeout 300000 (5 minutes); builds (npm run build) need 120000 (2 minutes). Before sending a tool call, think 'how many MILLISECONDS does this need' and never pass a value under 10000 for install/build commands. IMPORTANT") {
		t.Fatal("hint must be idempotent — second pass must not duplicate")
	}
}

func TestApplyTimeoutHintRewritesAnthropicFormat(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"max_tokens": 65536,
		"messages": [{"role":"user","content":"hi"}],
		"tools": [
			{"name":"bash","description":"Run shell commands","input_schema":{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}},
			{"name":"edit","description":"Edit a file","input_schema":{}}
		]
	}`)
	updated := ApplyTimeoutHint(body)
	text := string(updated)
	if !strings.Contains(text, `"description":"Run shell commands IMPORTANT: the timeout parameter is in MILLISECONDS.`) {
		t.Fatal("hint must be appended to bash tool description in Anthropic format")
	}
	if !strings.Contains(text, `"description":"Edit a file"`) || strings.Contains(text, `"description":"Edit a file IMPORTANT`) {
		t.Fatal("edit tool must be untouched")
	}
}

func TestApplyTimeoutHintLeavesBodyUnchangedWithoutBash(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[{"type":"function","function":{"name":"read","description":"Read a file","parameters":{}}}]}`)
	if string(ApplyTimeoutHint(body)) != string(body) {
		t.Fatal("body without bash tool must be returned unchanged")
	}
}

func TestApplyTimeoutHintLeavesBodyUnchangedWithoutTools(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"hi"}]}`)
	if string(ApplyTimeoutHint(body)) != string(body) {
		t.Fatal("body without tools must be returned unchanged")
	}
}

func TestApplyTimeoutHintLeavesInvalidJSONUnchanged(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools": [broken`)
	if string(ApplyTimeoutHint(body)) != string(body) {
		t.Fatal("invalid JSON must be returned unchanged")
	}
}

// --- Lapisan B: EnlargeToolTimeout ---

func TestEnlargeToolTimeoutRaisesTinyInstallTimeout(t *testing.T) {
	args := `{"command":"npm install","timeout":180}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("tiny install timeout must be raised")
	}
	if !strings.Contains(updated, `"timeout":300000`) {
		t.Fatalf("install timeout must become 300000, got %s", updated)
	}
	if !strings.Contains(updated, `"command":"npm install"`) {
		t.Fatal("command must be preserved")
	}
}

func TestEnlargeToolTimeoutRaisesTinyBuildTimeout(t *testing.T) {
	args := `{"command":"npm run build","timeout":900}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("tiny build timeout must be raised")
	}
	if !strings.Contains(updated, `"timeout":120000`) {
		t.Fatalf("build timeout must become 120000, got %s", updated)
	}
}

func TestEnlargeToolTimeoutLeavesLargeTimeout(t *testing.T) {
	args := `{"command":"npm install","timeout":300000}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if changed || updated != args {
		t.Fatal("large timeout must not be touched")
	}
}

func TestEnlargeToolTimeoutLeavesNonSlowCommand(t *testing.T) {
	args := `{"command":"Get-ChildItem","timeout":180}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if changed || updated != args {
		t.Fatal("fast command with small timeout must not be touched")
	}
}

func TestEnlargeToolTimeoutLeavesNonBashTool(t *testing.T) {
	args := `{"command":"npm install","timeout":180}`
	updated, changed := EnlargeToolTimeout("read", args)
	if changed || updated != args {
		t.Fatal("non-bash tool must not be touched")
	}
}

func TestEnlargeToolTimeoutHandlesNpx(t *testing.T) {
	args := `{"command":"npx create-next-app@latest app","timeout":600}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed || !strings.Contains(updated, `"timeout":300000`) {
		t.Fatalf("npx install timeout must be raised to 300000, got %s", updated)
	}
}

func TestEnlargeToolTimeoutHandlesMissingTimeout(t *testing.T) {
	args := `{"command":"npm install"}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if changed || updated != args {
		t.Fatal("missing timeout must not be invented")
	}
}

func TestEnlargeToolTimeoutHandlesInvalidJSON(t *testing.T) {
	args := `{"command":"npm install","timeout":`
	updated, changed := EnlargeToolTimeout("bash", args)
	if changed || updated != args {
		t.Fatal("invalid JSON must be returned unchanged")
	}
}

func TestEnlargeToolTimeoutHandlesTimeoutAsString(t *testing.T) {
	args := `{"command":"npm install","timeout":"180"}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if changed || updated != args {
		t.Fatal("non-numeric timeout must not be touched")
	}
}
