package tooltimeguard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInterceptNoOpEditDetectsIdenticalStrings(t *testing.T) {
	args := `{"filePath":"/test/file.tsx","oldString":"import { SITE } from '@/lib/site';","newString":"import { SITE } from '@/lib/site';"}`
	updated, changed := InterceptNoOpEdit("edit", args)
	if !changed {
		t.Fatal("identical old/new must be intercepted")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("updated must be valid JSON")
	}
	// v3: marker is in _gatewayMarker, newString is truncated
	marker, _ := result["_gatewayMarker"].(string)
	if !strings.Contains(marker, "GATEWAY") {
		t.Fatal("must contain _gatewayMarker with GATEWAY keyword")
	}
	newStr, _ := result["newString"].(string)
	if strings.Contains(newStr, "<!--") {
		t.Fatal("must NOT contain HTML comment in newString — breaks JSX/TSX")
	}
	oldStr, _ := result["oldString"].(string)
	if oldStr != "import { SITE } from '@/lib/site';" {
		t.Fatal("oldString must be preserved")
	}
}

func TestInterceptNoOpEditLeavesValidEdit(t *testing.T) {
	args := `{"filePath":"/test/file.tsx","oldString":"const x = 1;","newString":"const x = 2;"}`
	updated, changed := InterceptNoOpEdit("edit", args)
	if changed {
		t.Fatal("valid edit (old != new) must NOT be intercepted")
	}
	if updated != args {
		t.Fatal("valid edit must be returned unchanged")
	}
}

func TestInterceptNoOpEditLeavesNonEditTool(t *testing.T) {
	args := `{"command":"npm install","timeout":180}`
	_, changed := InterceptNoOpEdit("bash", args)
	if changed {
		t.Fatal("bash tool must NOT be intercepted as edit")
	}
}

func TestInterceptNoOpEditLeavesWriteTool(t *testing.T) {
	args := `{"filePath":"/test/file.tsx","content":"hello"}`
	_, changed := InterceptNoOpEdit("write", args)
	if changed {
		t.Fatal("write tool must NOT be intercepted")
	}
}

func TestInterceptNoOpEditHandlesClaudeCodeEditor(t *testing.T) {
	args := `{"filePath":"/test/file.tsx","oldString":"const a = 1;","newString":"const a = 1;"}`
	_, changed := InterceptNoOpEdit("str_replace_editor", args)
	if !changed {
		t.Fatal("Claude Code str_replace_editor must be intercepted")
	}
}

func TestInterceptNoOpEditHandlesClineReplaceInFile(t *testing.T) {
	args := `{"path":"/test/file.tsx","oldString":"const b = 2;","newString":"const b = 2;"}`
	_, changed := InterceptNoOpEdit("replace_in_file", args)
	if !changed {
		t.Fatal("Cline replace_in_file must be intercepted")
	}
}

func TestInterceptNoOpEditLeavesEmptyStrings(t *testing.T) {
	args := `{"filePath":"/test/file.tsx","oldString":"","newString":""}`
	_, changed := InterceptNoOpEdit("edit", args)
	if changed {
		t.Fatal("empty old/new must NOT be intercepted (might be intentional create)")
	}
}

func TestInterceptNoOpEditLeavesInvalidJSON(t *testing.T) {
	args := `{"filePath": broken`
	_, changed := InterceptNoOpEdit("edit", args)
	if changed {
		t.Fatal("invalid JSON must not be intercepted")
	}
}

func TestInterceptNoOpEditFullFileNoOp(t *testing.T) {
	largeStr := strings.Repeat("const x = 1;\n", 50)
	body := map[string]any{
		"filePath":  "layout.tsx",
		"oldString": largeStr,
		"newString": largeStr,
	}
	raw, _ := json.Marshal(body)
	updated, changed := InterceptNoOpEdit("edit", string(raw))
	if !changed {
		t.Fatal("full-file no-op must be intercepted")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("must be valid JSON")
	}
	// v3: no HTML comment, marker in _gatewayMarker
	if strings.Contains(updated, "<!--") {
		t.Fatal("must NOT contain HTML comment")
	}
	marker, _ := result["_gatewayMarker"].(string)
	if !strings.Contains(marker, "GATEWAY") {
		t.Fatal("must have _gatewayMarker field")
	}
}
