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
	Hint = " IMPORTANT: the timeout parameter is in MILLISECONDS. 180 means 0.18 seconds — enough time for nothing. Package installs (npm install, npx create-*) need timeout 300000 (5 minutes); builds (npm run build) need 120000 (2 minutes). Before sending a tool call, think 'how many MILLISECONDS does this need' and never pass a value under 10000 for install/build commands."

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

// EnlargeToolTimeout melaksanakan Lapisan B: baca arguments JSON sebuah
// function call; jika tool bash/shell membawa timeout < 10000ms untuk
// command yang diketahui lambat, naikkan ke nilai selamat. Pulangkan
// arguments (string JSON) yang mungkin telah dibetulkan dan bool sama ada
// perubahan dibuat. Idempoten dan behavior-neutral untuk arguments yang
// bukan JSON sah.
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
