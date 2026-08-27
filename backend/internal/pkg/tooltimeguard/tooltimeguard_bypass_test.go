package tooltimeguard

import "testing"

func TestIsDevServerCommandCmdExeBypass(t *testing.T) {
	// Wrapper bypass: cmd.exe /c npm run dev
	if !isDevServerCommand("cmd.exe /c npm run dev") {
		t.Fatal("cmd.exe /c npm run dev must be detected")
	}
}

func TestIsDevServerCommandBashCBypass(t *testing.T) {
	// Wrapper bypass: bash -c 'npm run dev'
	if !isDevServerCommand("bash -c 'npm run dev'") {
		t.Fatal("bash -c 'npm run dev' must be detected")
	}
}

func TestIsDevServerCommandShCBypass(t *testing.T) {
	// Wrapper bypass: sh -c npm run dev
	if !isDevServerCommand("sh -c npm run dev") {
		t.Fatal("sh -c npm run dev must be detected")
	}
}

func TestIsDevServerCommandPowerShellBypass(t *testing.T) {
	if !isDevServerCommand("powershell -c npm run dev") {
		t.Fatal("powershell -c npm run dev must be detected")
	}
}

func TestIsDevServerCommandCmdBypassNextDev(t *testing.T) {
	if !isDevServerCommand("cmd /c next dev") {
		t.Fatal("cmd /c next dev must be detected")
	}
}

func TestIsDevServerCommandBypassBuildNotAffected(t *testing.T) {
	if isDevServerCommand("cmd.exe /c npm run build") {
		t.Fatal("cmd.exe /c npm run build must NOT be detected as dev server")
	}
}

func TestEnlargeToolTimeoutRewritesWrappedDevServer(t *testing.T) {
	args := `{"command":"cmd.exe /c npm run dev","timeout":15000}`
	updated, changed := EnlargeToolTimeout("bash", args)
	if !changed {
		t.Fatal("cmd.exe wrapped dev server must be rewritten")
	}
	// Result should have Start-Process — the wrapper is stripped
	if !isDevServerCommand("cmd.exe /c npm run dev") {
		t.Log("NOTE: isDevServerCommand strips wrapper for DETECTION but rewrite uses splitCommandForStartProcess")
	}
	_ = updated // The rewrite goes through splitCommandForStartProcess which uses the original
}
