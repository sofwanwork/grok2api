package tooltimeguard

import "testing"

func TestStreamActivityGuardBuildWithoutDevServer(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("bash", `{"command":"npm run build","timeout":120000}`)
	g.NoteToolCall("write", `{"filePath":"page.tsx"}`)
	if !g.HasBuild {
		t.Fatal("build must be detected")
	}
	if g.HasDevStart {
		t.Fatal("dev start should NOT be detected")
	}
	if !g.ShouldRemindDevServer() {
		t.Fatal("should remind — build ran, no dev server, no HTTP verify")
	}
}

func TestStreamActivityGuardBuildWithDevServer(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("bash", `{"command":"npm run build","timeout":120000}`)
	g.NoteToolCall("bash", `{"command":"Start-Process -FilePath 'npm' -ArgumentList 'run','dev'","timeout":30000}`)
	if !g.HasDevStart {
		t.Fatal("dev start must be detected")
	}
	if g.ShouldRemindDevServer() {
		t.Fatal("should NOT remind — dev server was started")
	}
}

func TestStreamActivityGuardBuildWithHTTPVerify(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("bash", `{"command":"npm run build","timeout":120000}`)
	g.NoteToolCall("bash", `{"command":"Invoke-WebRequest -Uri 'http://localhost:3000'","timeout":15000}`)
	if !g.HasHTTPVerify {
		t.Fatal("HTTP verify must be detected")
	}
	if g.ShouldRemindDevServer() {
		t.Fatal("should NOT remind — HTTP verify was done")
	}
}

func TestStreamActivityGuardNoBuildNoRemind(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("bash", `{"command":"Get-ChildItem"}`)
	g.NoteToolCall("edit", `{"oldString":"a","newString":"b"}`)
	if g.ShouldRemindDevServer() {
		t.Fatal("should NOT remind — no build was run (not a web project)")
	}
}

func TestStreamActivityGuardNoToolsNoRemind(t *testing.T) {
	g := NewStreamActivityGuard()
	if g.ShouldRemindDevServer() {
		t.Fatal("should NOT remind — no tools used at all")
	}
}

func TestStreamActivityGuardCurlVerify(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("bash", `{"command":"npm run build","timeout":120000}`)
	g.NoteToolCall("bash", `{"command":"curl http://localhost:3000","timeout":15000}`)
	if !g.HasHTTPVerify {
		t.Fatal("curl to localhost must count as HTTP verify")
	}
	if g.ShouldRemindDevServer() {
		t.Fatal("should NOT remind after curl verify")
	}
}

func TestStreamActivityGuardNonTerminalToolDoesNotTrigger(t *testing.T) {
	g := NewStreamActivityGuard()
	g.NoteToolCall("write", `{"filePath":"page.tsx","content":"hello"}`)
	if g.ShouldRemindDevServer() {
		t.Fatal("write alone should not trigger reminder")
	}
}

func TestStreamActivityGuardNilGuardSafe(t *testing.T) {
	var g *StreamActivityGuard
	g.NoteToolCall("bash", `{"command":"npm run build"}`) // must not panic
	if g.ShouldRemindDevServer() {
		t.Fatal("nil guard must never remind")
	}
}
