package tooltimeguard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsTerminalToolNameOpenCodeBash(t *testing.T) {
	if !isTerminalToolName("bash") {
		t.Fatal("bash must match")
	}
}

func TestIsTerminalToolNameClaudeCodeBash(t *testing.T) {
	if !isTerminalToolName("Bash") {
		t.Fatal("Bash (Claude Code) must match")
	}
}

func TestIsTerminalToolNameCursor(t *testing.T) {
	if !isTerminalToolName("run_terminal_cmd") {
		t.Fatal("run_terminal_cmd (Cursor) must match")
	}
}

func TestIsTerminalToolNameCline(t *testing.T) {
	if !isTerminalToolName("execute_command") {
		t.Fatal("execute_command (Cline) must match")
	}
}

func TestIsTerminalToolNameAider(t *testing.T) {
	if !isTerminalToolName("run") {
		t.Fatal("run (Aider) must match")
	}
}

func TestIsTerminalToolNameNotWrite(t *testing.T) {
	if isTerminalToolName("write") {
		t.Fatal("write must NOT match")
	}
}

func TestIsTerminalToolNameNotRead(t *testing.T) {
	if isTerminalToolName("read") {
		t.Fatal("read must NOT match")
	}
}

func TestApplySchemaHintsRewritesCursorTerminalTool(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[{"type":"function","function":{"name":"run_terminal_cmd","description":"Run a terminal command","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}}}]}`)
	updated := ApplySchemaHints(body)
	text := string(updated)
	if !strings.Contains(text, "MILLISECONDS") {
		t.Fatal("Cursor terminal tool must receive timeout hint")
	}
}

func TestApplySchemaHintsRewritesClaudeCodeBash(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[{"type":"function","function":{"name":"Bash","description":"Execute bash commands","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}}}]}`)
	updated := ApplySchemaHints(body)
	text := string(updated)
	if !strings.Contains(text, "MILLISECONDS") {
		t.Fatal("Claude Code Bash tool must receive timeout hint")
	}
}

func TestApplySchemaHintsRewritesClineExecuteCommand(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[{"type":"function","function":{"name":"execute_command","description":"Execute a CLI command","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}}}]}`)
	updated := ApplySchemaHints(body)
	text := string(updated)
	if !strings.Contains(text, "MILLISECONDS") {
		t.Fatal("Cline execute_command tool must receive timeout hint")
	}
}

func TestApplySchemaHintsLeavesNonTerminalTools(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[{"type":"function","function":{"name":"write_to_file","description":"Write file","parameters":{}}}]}`)
	updated := ApplySchemaHints(body)
	if string(updated) != string(body) {
		t.Fatal("write_to_file must be returned unchanged")
	}
}

func TestEnlargeToolTimeoutWorksForCursorTerminalTool(t *testing.T) {
	args := `{"command":"npm install","timeout":180}`
	updated, changed := EnlargeToolTimeout("run_terminal_cmd", args)
	if !changed {
		t.Fatal("Cursor run_terminal_cmd must trigger timeout raise")
	}
	if !strings.Contains(updated, `"timeout":300000`) {
		t.Fatalf("Cursor timeout must be raised to 300000, got: %s", updated)
	}
}

func TestEnlargeToolTimeoutWorksForClaudeCodeBash(t *testing.T) {
	args := `{"command":"npm run dev","timeout":15000}`
	updated, changed := EnlargeToolTimeout("Bash", args)
	if !changed {
		t.Fatal("Claude Code Bash must trigger dev server rewrite")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("must be valid JSON")
	}
	cmd, _ := result["command"].(string)
	if !strings.Contains(strings.ToLower(cmd), "start-process") {
		t.Fatalf("Claude Code Bash dev server must be rewritten to Start-Process, got: %s", cmd)
	}
}

func TestEnlargeToolTimeoutWorksForClineExecuteCommand(t *testing.T) {
	args := `{"command":"npm run build","timeout":5000}`
	updated, changed := EnlargeToolTimeout("execute_command", args)
	if !changed {
		t.Fatal("Cline execute_command must trigger timeout raise")
	}
	if !strings.Contains(updated, `"timeout":120000`) {
		t.Fatalf("Cline build timeout must be raised to 120000, got: %s", updated)
	}
}
