// Package tooltimeguard melindungi dua parameter tool daripada salah guna
// oleh model — patch #21 (bash timeout) dan #22 (question options).
//
// Latar #21 (eksperimen testtesttest round 1–4): model Grok menjana tool
// call bash dengan `timeout` kecil (180–1800) kerana dia sangka unit itu
// DETIK; OpenCode membacanya sebagai MILISAAT dan membunuh npm install
// selepas 0.18s. Lima percubaan install mati sebelum sempat berjalan, dan
// model menyerahkan langkah manual kepada user.
//
// Latar #22 (round 4, 01:50:40): model memanggil tool `question` dengan
// soalan yang bagus — tapi `options` kosong []; cadangan disimpan dalam
// teks soalan sahaja, jadi user nampak popup tanpa pilihan untuk klik.
// Model tahu konsep "tanya soalan dengan cadangan" tapi letak cadangan
// dalam ayat, bukan dalam struktur — keluarga sama dengan masalah timeout.
//
// Dua lapisan:
//
// Lapisan A (request path, ApplySchemaHints): tulis semula description
// tool bash/shell (unit milisaat) dan tool question (options wajib
// berstruktur) supaya petunjuk sampai ke model sebagai sebahagian schema,
// bukan persona yang boleh dilupakan.
//
// Lapisan B (response path, EnlargeToolTimeout): bila model tetap menjana
// timeout kecil untuk command yang diketahui lambat (npm install, create-*,
// build), naikkan nilai itu ke minimum selamat sebelum sampai ke client.
package tooltimeguard

import (
	"encoding/json"
	"strings"
)

const (
	// Hint dilampirkan pada description tool bash/shell.
	Hint = " IMPORTANT: the timeout parameter is in MILLISECONDS. 180 means 0.18 seconds — enough time for nothing. Package installs (npm install, npx create-*) need timeout 300000 (5 minutes); builds (npm run build) need 120000 (2 minutes). Before sending a tool call, think 'how many MILLISECONDS does this need' and never pass a value under 10000 for install/build commands. Dev servers (npm run dev, next dev, vite, python -m http.server) NEVER EXIT — they run forever. NEVER run them as a foreground bash command (the tool will kill them at the timeout). Instead start them as a BACKGROUND process (on Windows: Start-Process -FilePath 'npm' -ArgumentList 'run','dev' -WorkingDirectory 'path' -PassThru), wait a few seconds, then verify with an HTTP request (Invoke-WebRequest -Uri 'http://localhost:3000' -UseBasicParsing -TimeoutSec 10). The HTTP 200 response IS the verification — not the process output. Shell is Windows PowerShell 5.1: NEVER use `&&` or `||` to chain commands — BOTH are invalid statement separators here and fail with an instant ParseError ('The token ... is not a valid statement separator'). Chain with `;` or `if ($?) { ... }`, or set the working directory with the tool's own path parameter instead of `cd X && cmd`. `ls -la`, `cat`, and Unix syntax also fail — use Get-ChildItem / Get-Content."

	// questionHint dilampirkan pada description tool question (patch #22 v2).
	questionHint = " IMPORTANT: the 'options' field is a STRUCTURED ARRAY, not optional decoration. For every question you must fill options with 2-4 concrete choices as objects ({\"label\": \"short text (1-5 words)\", \"description\": \"one line explaining the choice\"}) so the user can CLICK them. Writing the suggestions only inside the question text while leaving options [] is a failed call — the popup renders nothing clickable. The question TEXT should still explain your recommendations fully (what you recommend and why) — options are the clickable shortcut, not a replacement for a complete explanation. Open-ended questions with no meaningful choices are the ONLY exception."

	// editHint dilampirkan pada description tool edit (patch #28 K19).
	editHint = " IMPORTANT: ALWAYS read the file with the read tool IMMEDIATELY before editing — never edit from memory. Copy oldString from the actual file read, never from what you remember. If oldString equals newString, the edit is a no-op and will be blocked. NEVER edit a file you have not read in this turn."

	// fileHint dilampirkan pada description tool read/write (patch #28 K19).
	fileHint = " IMPORTANT: Before writing a file, read it first if it exists. Large files (10000+ lines) may need multiple reads. If write fails, check the directory exists. This tool does not need a timeout parameter — it is not a terminal tool."

	// buildTimeoutMs ialah nilai selamat untuk command build.
	buildTimeoutMs = 120000

	// installTimeoutMs digunakan untuk command install (muat turun pakej).
	installTimeoutMs = 300000

	// testTimeoutMs digunakan untuk command test (npm test, pytest, go test).
	testTimeoutMs = 300000

	// maxInspectionBytes hadkan pemeriksaan body supaya request gergasi
	// tidak membazir CPU parse.
	maxInspectionBytes = 4 << 20 // 4 MiB (sesi panjang dengan banyak tools)
)

// terminalToolNames ialah nama tool terminal/shell yang digunakan oleh
// berbagai IDE dan coding agent (patch #21 v5). Hint timeout + dev server +
// shell contract mesti sampai kepada SEMUA IDE — bukan sahaja OpenCode.
var terminalToolNames = map[string]bool{
	"bash":                 true, // OpenCode, Anthropic convention
	"Bash":                 true, // Claude Code (huruf besar)
	"shell":                true,
	"Shell":                true,
	"terminal":             true,
	"Terminal":             true,
	"run_terminal_cmd":     true, // Cursor
	"execute_command":      true, // Cline
	"run_command":          true,
	"run":                  true, // Aider
	"command":              true,
	"exec":                 true,
	"execute_bash":         true,
	"run_shell_command":    true, // Gemini CLI
	"codebase_run_terminal": true,
}

// isTerminalToolName melaporkan sama ada nama tool ini adalah terminal/shell
// tool menurut konvensyen mana-mana IDE yang dikenali. Perbandingan adalah
// case-sensitive secara sengaja — nama seperti "Bash" (Claude Code) dan
// "bash" (OpenCode) kedua-duanya disenaraikan.
func isTerminalToolName(name string) bool {
	return terminalToolNames[name]
}

// ApplySchemaHints melaksanakan Lapisan A (kini merangkumi patch #21 dan
// #22): tulis semula description tool "bash"/"shell" (unit timeout) dan
// tool "question" (options wajib berstruktur) dalam request body supaya
// petunjuk sampai ke model sebagai sebahagian schema setiap request.
// Cover kedua-dua format: OpenAI Chat ({"type":"function","function":{...}})
// dan Anthropic Messages ({"name":"bash","description":"...","input_schema":...}).
// Body dikembalikan tanpa perubahan jika tiada tools, body terlalu besar,
// atau parse gagal — kegagalan di sini tidak boleh menenggelamkan request.
func ApplySchemaHints(body []byte) []byte {
	if len(body) == 0 || len(body) > maxInspectionBytes {
		return body
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	rawTools, ok := payload["tools"]
	if !ok || strings.TrimSpace(string(rawTools)) == "" || string(rawTools) == "null" {
		return body
	}
	var tools []map[string]any
	if json.Unmarshal(rawTools, &tools) != nil {
		return body
	}
	changed := false
	for _, tool := range tools {
		// Chat format: schema berada dalam sub-objek "function".
		// Anthropic Messages format: flat pada objek tool sendiri.
		target := tool
		if function, ok := tool["function"].(map[string]any); ok {
			target = function
		}
		name, _ := target["name"].(string)
		description, _ := target["description"].(string)
		var hint, marker string
		switch {
		case isTerminalToolName(name):
			hint = Hint
			marker = "MILLISECONDS"
		case name == "question":
			hint = questionHint
			marker = "STRUCTURED ARRAY"
		case isEditToolName(name):
			hint = editHint
			marker = "READ BEFORE EDIT"
		case name == "write", name == "read":
			hint = fileHint
			marker = "READ BEFORE WRITE"
		default:
			continue
		}
		if strings.Contains(description, marker) {
			continue // hint sudah ada (request ulangan / idempotensi)
		}
		if description == "" {
			target["description"] = strings.TrimSpace(hint)
		} else {
			target["description"] = description + hint
		}
		changed = true
	}
	if !changed {
		return body
	}
	// payload menyimpan "tools" sebagai RawMessage asal — tulis semula versi
	// yang telah diubah sebelum marshal, kalau tidak perubahan hilang.
	rawUpdated, err := json.Marshal(tools)
	if err != nil {
		return body
	}
	payload["tools"] = json.RawMessage(rawUpdated)
	updated, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updated
}

// ApplyTimeoutHint ialah alias untuk ApplySchemaHints — dikekalkan supaya
// rujukan sedia ada (service.go, UPDATE.md) tidak pecah; namanya kini
// merangkumi kedua-dua hint (timeout + question options).
func ApplyTimeoutHint(body []byte) []byte {
	return ApplySchemaHints(body)
}

// slowToolCommandKind mengelaskan command yang diketahui memakan masa.
// Nilai pulangan: "install", "build", atau "" (biasa).
func slowToolCommandKind(command string) string {
	fields := strings.Fields(strings.ToLower(command))
	if len(fields) == 0 {
		return ""
	}
	bin := strings.TrimSuffix(fields[0], ".exe")
	bin = strings.TrimSuffix(bin, ".cmd")
	rest := strings.Join(fields[1:], " ")
	switch bin {
	case "npm", "pnpm", "yarn", "bun":
		switch {
		case strings.Contains(rest, "install"), rest == "i", strings.HasPrefix(rest, "i "), strings.Contains(rest, "add "), strings.HasPrefix(rest, "ci"):
			return "install"
		case strings.Contains(rest, "run build"), strings.HasPrefix(rest, "build"):
			return "build"
		case strings.Contains(rest, "test"), strings.Contains(rest, "run test"), strings.HasPrefix(rest, "test"):
			return "test"
		}
	case "npx", "pnpx", "bunx":
		return "install" // npx hampir selalu memuat turun sesuatu
	case "uv", "pip", "pip3", "composer", "cargo", "mvn", "gradle":
		return "install"
	case "pytest", "python", "python3", "py":
		if strings.Contains(rest, "test") || strings.Contains(rest, "pytest") {
			return "test"
		}
		return "install"
	case "go":
		if strings.Contains(rest, "test") {
			return "test"
		}
		return "install"
	case "tsc", "jest", "vitest", "playwright", "cypress":
		return "test"
	}
	return ""
}

// isDevServerCommand kesan command dev server yang berjalan selamanya.
// Command ini tidak boleh dijalankan sebagai foreground bash — tool akan
// membunuhnya pada timeout walaupun timeout besar, dan user nampak halaman
// kosong dalam browser. Ia mesti dimulakan sebagai proses background.
// Patch #21 v6: juga kesan wrapper bypass seperti "cmd.exe /c npm run dev"
// atau "bash -c 'npm run dev'" — model boleh cuba wrap command untuk
// mengelak deteksi langsung.
func isDevServerCommand(command string) bool {
	lc := strings.ToLower(command)
	// Strip common wrappers: cmd.exe /c, powershell -c, bash -c, sh -c
	for _, prefix := range []string{"cmd.exe /c ", "cmd /c ", "powershell -c ", "pwsh -c ", "bash -c ", "sh -c "} {
		if strings.HasPrefix(lc, prefix) {
			lc = strings.Trim(lc[len(prefix):], "'\" ")
			break
		}
	}
	// Strip ; separator prefix: 'Set-Location x; npm run dev' → 'npm run dev'
	// Model boleh prepend cd/Set-Location sebelum dev server command.
	for strings.Contains(lc, ";") {
		idx := strings.Index(lc, ";")
		prefix := lc[:idx]
		// Hanya strip jika prefix kelihatan seperti Set-Location/cd/echo
		if strings.HasPrefix(prefix, "set-location ") || strings.HasPrefix(prefix, "cd ") ||
			strings.HasPrefix(prefix, "echo ") || strings.HasPrefix(prefix, "chdir ") {
			lc = strings.TrimSpace(lc[idx+1:])
		} else {
			break
		}
	}
	fields := strings.Fields(lc)
	if len(fields) == 0 {
		return false
	}
	bin := strings.TrimSuffix(fields[0], ".exe")
	bin = strings.TrimSuffix(bin, ".cmd")
	rest := strings.Join(fields[1:], " ")
	switch bin {
	case "npm", "pnpm", "yarn", "bun":
		return strings.Contains(rest, "run dev") || strings.HasPrefix(rest, "dev")
	case "next", "vite", "astro", "nuxt", "remix":
		if strings.Contains(rest, "build") {
			return false
		}
		return strings.Contains(rest, "dev") || bin == "vite"
	case "python", "python3", "py":
		return strings.Contains(rest, "-m http.server") || strings.Contains(rest, "manage.py runserver")
	case "flask":
		return strings.Contains(rest, "run")
	case "php":
		return strings.Contains(rest, "artisan serve") || strings.Contains(rest, "-s ")
	case "go":
		return strings.Contains(rest, "run .") && strings.Contains(rest, "air")
	}
	return false
}

// rewriteDevServerBackground menulis semula command foreground dev server
// menjadi Start-Process background (Windows PowerShell 5.1). Kembalikan
// command baru dan bool sama ada perubahan dibuat. Contoh:
//
//	npm run dev  →  Start-Process -FilePath 'npm' -ArgumentList 'run','dev' -WorkingDirectory '<cwd>' -PassThru | Select-Object -ExpandProperty Id
//
// WorkingDirectory diambil daripada args["workdir"] atau args["cd"] jika
// ada; jika tiada, ia dibiarkan kosong dan model perlu set sendiri — lebih
// baik daripada start di lokasi salah.
func rewriteDevServerBackground(args map[string]any) bool {
	command, _ := args["command"].(string)
	if !isDevServerCommand(command) {
		return false
	}
	// Jangan rewrite kalau sudah Start-Process / Start-Job.
	if strings.Contains(strings.ToLower(command), "start-process") || strings.Contains(strings.ToLower(command), "start-job") {
		return false
	}
	workdir, _ := args["workdir"].(string)
	if workdir == "" {
		workdir, _ = args["cd"].(string)
	}
	exeAndArgs := splitCommandForStartProcess(command)
	if len(exeAndArgs) == 0 {
		return false
	}
	exe := exeAndArgs[0]
	rest := exeAndArgs[1:]
	argList := make([]string, 0, len(rest))
	for _, a := range rest {
		argList = append(argList, "'"+strings.ReplaceAll(a, "'", "''")+"'")
	}
	var sb strings.Builder
	sb.WriteString("Start-Process -FilePath '")
	sb.WriteString(exe)
	sb.WriteString("' -ArgumentList ")
	if len(argList) > 0 {
		sb.WriteString("@(")
		sb.WriteString(strings.Join(argList, ","))
		sb.WriteString(")")
	} else {
		sb.WriteString("@()")
	}
	if workdir != "" {
		sb.WriteString(" -WorkingDirectory '")
		sb.WriteString(strings.ReplaceAll(workdir, "'", "''"))
		sb.WriteString("'")
	}
	sb.WriteString(" -PassThru | Select-Object -ExpandProperty Id")
	args["command"] = sb.String()
	args["timeout"] = float64(30000)
	delete(args, "workdir")
	delete(args, "cd")
	return true
}

// splitCommandForStartProcess pecahkan command string kepada binary dan
// argumennya dengan penghormatan asas terhadap petikan berganda.
func splitCommandForStartProcess(command string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	for _, r := range command {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	if len(parts) == 0 {
		return nil
	}
	// Buat path-like prefix pada executable (cth. C:\...\npm.cmd) kekal satu token.
	parts[0] = strings.TrimSuffix(parts[0], ".cmd")
	parts[0] = strings.TrimSuffix(parts[0], ".exe")
	return parts
}

// EnlargeToolTimeout melaksanakan Lapisan B: baca arguments JSON sebuah
// function call; jika tool bash/shell membawa timeout < 10000ms untuk
// command yang diketahui lambat, naikkan ke nilai selamat. Jika command
// adalah dev server foreground, tulis semula menjadi Start-Process
// background. Pulangkan arguments (string JSON) yang mungkin telah
// dibetulkan dan bool sama ada perubahan dibuat. Idempoten dan
// behavior-neutral untuk arguments yang bukan JSON sah.
func EnlargeToolTimeout(toolName, arguments string) (string, bool) {
	if !isTerminalToolName(toolName) {
		return arguments, false
	}
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return arguments, false
	}
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return arguments, false
	}
	// Patch #21 v4: dev server foreground → rewrite Start-Process background.
	if rewriteDevServerBackground(args) {
		updated, err := json.Marshal(args)
		if err != nil {
			return arguments, false
		}
		return string(updated), true
	}
	command, _ := args["command"].(string)
	kind := slowToolCommandKind(command)
	if kind == "" {
		return arguments, false
	}
	timeoutValue, exists := args["timeout"]
	if !exists {
		return arguments, false
	}
	timeout, ok := timeoutValue.(float64)
	if !ok {
		return arguments, false
	}
	if timeout >= 10000 {
		return arguments, false
	}
	if kind == "install" {
		args["timeout"] = float64(installTimeoutMs)
	} else if kind == "test" {
		args["timeout"] = float64(testTimeoutMs)
	} else {
		args["timeout"] = float64(buildTimeoutMs)
	}
	updated, err := json.Marshal(args)
	if err != nil {
		return arguments, false
	}
	return string(updated), true
}

// StreamActivityGuard (patch #27) menjejaki aktiviti dalam satu stream
// converter — adakah model pernah (a) run build, (b) start dev server,
// (c) verify dengan HTTP. Digunakan dalam doneChat untuk inject reminder
// jika model claim siap tanpa dev server yang aktif.
type StreamActivityGuard struct {
	HasBuild    bool     // model run npm run build / next build / vite build
	HasDevStart bool     // model run Start-Process npm run dev / Start-Process ...
	HasHTTPVerify bool    // model run Invoke-WebRequest / curl ke localhost
	UsedTools   bool     // model gunakan mana-mana tool
}

// NewStreamActivityGuard mencipta guard untuk satu stream converter.
func NewStreamActivityGuard() *StreamActivityGuard {
	return &StreamActivityGuard{}
}

// NoteToolCall merekodkan aktiviti tool call — command yang dijalankan.
func (g *StreamActivityGuard) NoteToolCall(toolName, command string) {
	if g == nil {
		return
	}
	if isTerminalToolName(toolName) {
		g.UsedTools = true
		lc := strings.ToLower(command)
		if strings.Contains(lc, "run build") || strings.Contains(lc, " build") ||
			strings.Contains(lc, "next build") || strings.Contains(lc, "tsc ") {
			g.HasBuild = true
		}
		if strings.Contains(lc, "start-process") || strings.Contains(lc, "start-job") {
			g.HasDevStart = true
		}
		if strings.Contains(lc, "invoke-webrequest") || strings.Contains(lc, "curl") ||
			strings.Contains(lc, "invoke-restmethod") || strings.Contains(lc, "wget") {
			if strings.Contains(lc, "localhost") || strings.Contains(lc, "127.0.0.1") ||
				strings.Contains(lc, "http://") {
				g.HasHTTPVerify = true
			}
		}
	}
	if isEditToolName(toolName) || toolName == "write" || toolName == "read" {
		g.UsedTools = true
	}
}

// ShouldRemindDevServer melaporkan sama ada reminder perlu di-inject
// sebelum stream tamat: model dah gunakan tools (kerja projek web) dan
// run build, tapi tak pernah start dev server atau verify HTTP.
func (g *StreamActivityGuard) ShouldRemindDevServer() bool {
	if g == nil {
		return false
	}
	return g.UsedTools && g.HasBuild && !g.HasDevStart && !g.HasHTTPVerify
}

// noopEditCount ialah jumlah no-op edit berturut-turut untuk sesi stream
// semasa (per-converter, patch #26 v2). Reset apabila edit sah berjaya.
// 3 no-op = circuit breaker trips — model perlu STOP dan tukar strategi.
const (
	noOpEditSoftLimit   = 1
	noOpEditHardLimit   = 2
)

// DevServerReminderText ialah text yang di-inject sebagai reasoning_content
// delta sebelum stream tamat — model akan nampak ini dalam konteks dan
// dapat reminder yang jelas.
const DevServerReminderText = "\n\n[⚠️ GATEWAY REMINDER (patch #27): You ran a build tool but never started the dev server as a background process or verified with an HTTP request. \"Build passes\" does NOT mean the website works — the user sees an empty browser. Start the dev server NOW with Start-Process (background), wait 5-8 seconds, then verify with Invoke-WebRequest to http://localhost:3000. Only claim \"siap/done\" after you see HTTP 200 with your content.]"

// InterceptNoOpEdit (patch #26 v2) mengesan edit tool calls di mana
// oldString == newString. Berbanding v1 yang hanya tampal marker, v2:
//
//  1. No-op pertama: inject marker dengan 4 strategi konkrit untuk keluar
//     dari masalah (edit betul / write overwrite / pecah kecil / skip)
//  2. No-op kedua dan ketiga: marker lebih kuat — "DO NOT RETRY THIS EDIT"
//  3. Selepas 3 kali: injector berhenti — arahan sudah sampai, kalau model
//     masih loop itu isu model-level di luar kawalan gateway
func InterceptNoOpEdit(toolName, arguments string) (string, bool) {
	return interceptNoOpEditWithState(toolName, arguments, nil)
}

// InterceptNoOpEditStateful ialah versi stateful untuk session converter —
// menjejaki bilangan no-op berturut-turut supaya marker makin kuat setiap
// retry. state slice [int] dikongsi antara pemanggil.
func InterceptNoOpEditStateful(toolName, arguments string, state []int) (string, bool) {
	if len(state) == 0 {
		state = make([]int, 1)
	}
	result, changed := interceptNoOpEditWithState(toolName, arguments, state)
	if changed && len(state) > 0 {
		state[0]++
	}
	// Reset counter bila edit sah berlaku (old != new)
	if !changed && isEditToolName(toolName) {
		state[0] = 0
	}
	return result, changed
}

func interceptNoOpEditWithState(toolName, arguments string, state []int) (string, bool) {
	if !isEditToolName(toolName) {
		// Reset counter untuk non-edit tool calls — bincang berakhir.
		if len(state) > 0 {
			state[0] = 0
		}
		return arguments, false
	}
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return arguments, false
	}
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return arguments, false
	}
	oldStr, _ := args["oldString"].(string)
	newStr, _ := args["newString"].(string)
	if oldStr == "" || newStr == "" {
		return arguments, false
	}
	if oldStr != newStr {
		// Edit sah — reset counter.
		if len(state) > 0 {
			state[0] = 0
		}
		return arguments, false
	}

	attempt := 0
	if len(state) > 0 {
		attempt = state[0]
	}

	var marker string
	switch {
	case attempt >= noOpEditHardLimit:
		// Circuit breaker: arahan keras STOP + alternatif jelas
		marker = "\n<!-- ⛔ GATEWAY CIRCUIT BREAKER (patch #26 v2) — DO NOT RETRY THIS EDIT. This is attempt " + fmt_Sprint(attempt+1) + " of an identical no-op. The problem will NOT fix itself with another retry.\nChoose ONE different strategy NOW:\n  STRATEGY A: Use the write tool to replace the ENTIRE file with the final content you want (write overwrites completely).\n  STRATEGY B: Break the change into SMALLER edits — target a unique short snippet, not the whole file.\n  STRATEGY C: Skip this specific change entirely and continue with other work — note the skipped item in your final summary so the user can do it manually.\nRetrying this exact edit again wastes tokens and time for everyone. -->"
	case attempt >= noOpEditSoftLimit:
		// Retry #2: arahan lebih tegas, senaraikan strategi
		marker = "\n<!-- 🔴 GATEWAY (patch #26 v2): SECOND identical no-op detected. Do NOT send the same edit again — it will fail identically. Switch strategy:\n  A: use write tool to replace the entire file\n  B: make a smaller, more targeted edit with unique context\n  C: skip this change and continue with other work -->"
	default:
		// No-op pertama: edukatif dengan 4 strategi konkrit
		marker = "\n<!-- BLOCKED by gateway (patch #26 v2): This edit is a no-op — oldString equals newString. You copied the file content but forgot to make the actual change. Four ways forward:\n  1. Re-read the file, identify EXACTLY what should change, and write the CHANGED version into newString.\n  2. If diffing is hard, use the write tool to replace the ENTIRE file with your intended final content.\n  3. Make a SMALLER edit targeting a unique short snippet instead of the whole block.\n  4. If this section cannot be fixed, SKIP it and continue with other work. -->"
	}

	args["newString"] = oldStr + marker
	updated, err := json.Marshal(args)
	if err != nil {
		return arguments, false
	}
	return string(updated), true
}

// fmt_Sprint pengganti ringkas strconv.Itoa untuk elak import tambahan.
func fmt_Sprint(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// editToolNames ialah nama edit tool dari pelbagai IDE (patch #26).
var editToolNames = map[string]bool{
	"edit":               true, // OpenCode
	"str_replace_editor": true, // Claude Code
	"replace_in_file":    true, // Cline
	"write":              false, // write bukan edit (create new file)
	"apply_patch":        true, // Generic patch
	"edit_file":          true, // Generic
}

func isEditToolName(name string) bool {
	return editToolNames[name]
}
