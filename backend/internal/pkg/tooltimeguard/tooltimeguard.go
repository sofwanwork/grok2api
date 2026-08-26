// Package tooltimeguard melindungi parameter `timeout` tool bash daripada
// salah erti unit oleh model — patch #21.
//
// Latar (eksperimen testtesttest round 1–4): model Grok menjana tool call
// bash dengan `timeout` kecil (180–1800) kerana dia sangka unit itu DETIK;
// OpenCode membacanya sebagai MILISAAT dan membunuh npm install selepas
// 0.18s. Lima percubaan install mati sebelum sempat berjalan, dan model
// menyerahkan langkah manual kepada user.
//
// Dua lapisan:
//
// Lapisan A (request path, Hint): tulis semula description tool bash/shell
// supaya unit milisaat dinyatakan jelas pada setiap request — ilmu itu
// sampai ke model sebagai sebahagian schema, bukan persona yang boleh
// dilupakan.
//
// Lapisan B (response path, Enlarge): bila model tetap menjana timeout
// kecil untuk command yang diketahui lambat (npm install, create-*,
// build), naikkan nilai itu ke minimum selamat sebelum sampai ke client.
package tooltimeguard

import (
	"encoding/json"
	"strings"
)

const (
	// Hint dilampirkan pada description tool bash/shell.
	Hint = " IMPORTANT: the timeout parameter is in MILLISECONDS. 180 means 0.18 seconds — enough time for nothing. Package installs (npm install, npx create-*) need timeout 300000 (5 minutes); builds (npm run build) need 120000 (2 minutes). Before sending a tool call, think 'how many MILLISECONDS does this need' and never pass a value under 10000 for install/build commands."

	// buildTimeoutMs ialah nilai selamat untuk command build.
	buildTimeoutMs = 120000

	// installTimeoutMs digunakan untuk command install (muat turun pakej).
	installTimeoutMs = 300000

	// maxInspectionBytes hadkan pemeriksaan body supaya request gergasi
	// tidak membazir CPU parse.
	maxInspectionBytes = 1 << 20 // 1 MiB
)

// ApplyTimeoutHint melaksanakan Lapisan A: tulis semula description tool
// bernama "bash" atau "shell" dalam request body supaya unit timeout
// dinyatakan dengan jelas. Cover kedua-dua format: OpenAI Chat
// ({"type":"function","function":{...}}) dan Anthropic Messages
// ({"name":"bash","description":"...","input_schema":{...}}). Body
// dikembalikan tanpa perubahan jika tiada tools, body terlalu besar, atau
// parse gagal — kegagalan di sini tidak boleh menenggelamkan request.
func ApplyTimeoutHint(body []byte) []byte {
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
		if name != "bash" && name != "shell" {
			continue
		}
		description, _ := target["description"].(string)
		if strings.Contains(description, "MILLISECONDS") {
			continue // hint sudah ada (request ulangan / idempotensi)
		}
		if description == "" {
			target["description"] = strings.TrimSpace(Hint)
		} else {
			target["description"] = description + Hint
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
