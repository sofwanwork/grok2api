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

	// buildTimeoutMs ialah nilai selamat untuk command build.
	buildTimeoutMs = 120000

	// installTimeoutMs digunakan untuk command install (muat turun pakej).
	installTimeoutMs = 300000

	// maxInspectionBytes hadkan pemeriksaan body supaya request gergasi
	// tidak membazir CPU parse.
	maxInspectionBytes = 1 << 20 // 1 MiB
)

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
		switch name {
		case "bash", "shell":
			hint = Hint
			marker = "MILLISECONDS"
		case "question":
			hint = questionHint
			marker = "STRUCTURED ARRAY"
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
		}
	case "npx", "pnpx", "bunx":
		return "install" // npx hampir selalu memuat turun sesuatu
	case "uv", "pip", "pip3", "composer", "cargo", "mvn", "gradle":
		return "install"
	}
	return ""
}

// isDevServerCommand kesan command dev server yang berjalan selamanya.
// Command ini tidak boleh dijalankan sebagai foreground bash — tool akan
// membunuhnya pada timeout walaupun timeout besar, dan user nampak halaman
// kosong dalam browser. Ia mesti dimulakan sebagai proses background.
func isDevServerCommand(command string) bool {
	fields := strings.Fields(strings.ToLower(command))
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
		// next dev / vite [args] — tapi bukan next build.
		// "vite --port 3000" → rest = "--port 3000", tiada "dev" →
		// treat sebagai dev server juga (vite tanpa subcommand = serve).
		if strings.Contains(rest, "build") {
			return false
		}
		return strings.Contains(rest, "dev") || bin == "vite"
	case "python", "python3", "py":
		return strings.Contains(rest, "-m http.server") || strings.Contains(rest, "manage.py runserver")
	case "flask":
		return strings.Contains(rest, "run") // flask run --debug
	case "php":
		return strings.Contains(rest, "artisan serve") || strings.Contains(rest, "-s ")
	case "go":
		return strings.Contains(rest, "run .") && strings.Contains(rest, "air") // air live reload (jarang)
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
	if toolName != "bash" && toolName != "shell" {
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
	} else {
		args["timeout"] = float64(buildTimeoutMs)
	}
	updated, err := json.Marshal(args)
	if err != nil {
		return arguments, false
	}
	return string(updated), true
}
