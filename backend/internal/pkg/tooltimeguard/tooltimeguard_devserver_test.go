package tooltimeguard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsDevServerCommandNpmRunDev(t *testing.T) {
	if !isDevServerCommand("npm run dev") {
		t.Fatal("npm run dev must be detected")
	}
}

func TestIsDevServerCommandNextDev(t *testing.T) {
	if !isDevServerCommand("next dev") {
		t.Fatal("next dev must be detected")
	}
}

func TestIsDevServerCommandVite(t *testing.T) {
	if !isDevServerCommand("vite --port 3000") {
		t.Fatal("vite must be detected")
	}
}

func TestIsDevServerCommandPythonHttpServer(t *testing.T) {
	if !isDevServerCommand("python -m http.server 8080") {
		t.Fatal("python -m http.server must be detected")
	}
}

func TestIsDevServerCommandIgnoresBuild(t *testing.T) {
	if isDevServerCommand("npm run build") {
		t.Fatal("npm run build must NOT be detected as dev server")
	}
}

func TestIsDevServerCommandIgnoresInstall(t *testing.T) {
	if isDevServerCommand("npm install") {
		t.Fatal("npm install must NOT be detected as dev server")
	}
}

func TestRewriteDevServerBackgroundNpmRunDev(t *testing.T) {
	args := map[string]any{
		"command": "npm run dev",
		"timeout": float64(15000),
	}
	if !rewriteDevServerBackground(args) {
		t.Fatal("rewrite must trigger for npm run dev")
	}
	cmd, _ := args["command"].(string)
	if !strings.Contains(cmd, "Start-Process") {
		t.Fatalf("command must be rewritten to Start-Process, got: %s", cmd)
	}
	if !strings.Contains(cmd, "'npm'") {
		t.Fatalf("must reference npm, got: %s", cmd)
	}
	if !strings.Contains(cmd, "'run'") || !strings.Contains(cmd, "'dev'") {
		t.Fatalf("must pass run and dev as args, got: %s", cmd)
	}
	to, _ := args["timeout"].(float64)
	if to != 30000 {
		t.Fatalf("timeout must be 30000, got %v", to)
	}
}

func TestRewriteDevServerBackgroundWithWorkdir(t *testing.T) {
	args := map[string]any{
		"command": "npm run dev",
		"timeout": float64(15000),
		"workdir": "C:\\Users\\Sofwan\\Desktop\\testtesttest",
	}
	if !rewriteDevServerBackground(args) {
		t.Fatal("rewrite must trigger")
	}
	cmd, _ := args["command"].(string)
	if !strings.Contains(cmd, "C:\\Users\\Sofwan\\Desktop\\testtesttest") {
		t.Fatalf("must include workdir, got: %s", cmd)
	}
	_, hasWorkdir := args["workdir"]
	if hasWorkdir {
		t.Fatal("workdir key must be deleted after rewrite")
	}
}

func TestRewriteDevServerBackgroundIgnoresStartProcess(t *testing.T) {
	args := map[string]any{
		"command": "Start-Process -FilePath 'npm' -ArgumentList 'run','dev'",
		"timeout": float64(15000),
	}
	if rewriteDevServerBackground(args) {
		t.Fatal("must NOT rewrite an already-background command")
	}
}

func TestEnlargeToolTimeoutRewritesDevServer(t *testing.T) {
	// Lapisan B v4: npm run dev dengan timeout kecil → Start-Process
	args := `{"command":"npm run dev","timeout":15000}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("must rewrite dev server foreground to background")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("updated args must be valid JSON")
	}
	cmd, _ := result["command"].(string)
	if !strings.Contains(cmd, "Start-Process") {
		t.Fatalf("command must be Start-Process, got: %s", cmd)
	}
}

func TestEnlargeToolTimeoutRewritesDevServerWithCd(t *testing.T) {
	// Model kadang guna cd + npm run dev
	args := `{"command":"npm run dev","timeout":5000,"cd":"C:\\proj"}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("must rewrite")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("must be valid JSON")
	}
	cmd, _ := result["command"].(string)
	if !strings.Contains(cmd, "C:\\proj") {
		t.Fatalf("must include cd as WorkingDirectory, got: %s", cmd)
	}
}

func TestEnlargeToolTimeoutDoesNotRewriteNonDevServer(t *testing.T) {
	args := `{"command":"npm run build","timeout":5000}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("build still needs timeout raise")
	}
	var result map[string]any
	if json.Unmarshal([]byte(updated), &result) != nil {
		t.Fatal("must be valid JSON")
	}
	cmd, _ := result["command"].(string)
	if strings.Contains(strings.ToLower(cmd), "start-process") {
		t.Fatal("build must NOT be rewritten to Start-Process")
	}
}
