package gateway

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Tool-call degradation is an upstream behaviour, not a gateway defect: the
// model narrates a tool call in prose ("run tool write_file with path is ...")
// instead of emitting a structured tool_calls delta. Measured locally on a
// Grok Build account it happens on roughly half of the requests whose tool
// arguments are large, and it is stochastic: the same prompt alternates
// between a clean tool call and prose.
//
// The verdict is kept separate from QualityWithhold on purpose. Missing
// thinking cools the serving account for hours because it indicates a
// downgraded account. Degradation here is not the account's fault, so
// penalising accounts would burn the pool for something it cannot fix.
const (
	// maxToolDegradationInspectionBytes bounds request-body inspection. The
	// diagnostic is advisory, so oversized bodies are skipped instead of
	// spending time scanning them.
	maxToolDegradationInspectionBytes = 1 << 20
	// toolNarrationWindow is how far into the visible text a narration marker
	// must appear. Observed degradations start narrating at offset 0, so a
	// tight window keeps legitimate prose that merely discusses tools (for
	// example a user asking how tool calling works) from matching.
	toolNarrationWindow = 160
	// minToolNarrationRunes avoids classifying a barely-started stream, where
	// the real tool_calls delta may still be in flight.
	minToolNarrationRunes = 40
)

// toolNarrationMarkers are the prose forms observed when the model describes a
// call instead of making one. Live capture on a Grok Build account produced
// plain prose ("run tool X with path is ..."), a bare verb form without the
// word "tool" ("call X with path is ..."), and fenced pseudo-XML blocks
// (```xml <invoke tool="X">, ```xml <tool_call name="X">), so all three shapes
// are matched here.
var toolNarrationMarkers = []string{
	"tool call",
	"tool_call",
	"invoke tool",
	"invoking tool",
	"run tool",
	"running tool",
	"call tool",
	"calling tool",
	"use tool",
	"using tool",
	"execute tool",
	"executing tool",
	// Fenced pseudo-XML the model emits instead of a structured call.
	"<invoke tool",
	"<tool_call",
	"<function call",
	"<function_call",
	"<tool name",
	"<invoke name",
	// A fenced parameter block. It only appears inside a degraded XML
	// narration and always sits beside the tool name and dictation, so the
	// loose window is safe here.
	"<parameter name",
	"<parameter ",
}

// bareToolNarrationMarkers omit the word "tool" entirely, so they also match
// ordinary sentences such as "You can run bash yourself if you prefer". They
// are held to the stricter argument test below.
var bareToolNarrationMarkers = []string{
	"invoke ",
	"call ",
	"run ",
	"execute ",
}

// toolArgumentMarkers are the argument-dictation forms that separate a real
// degradation from prose that merely explains tool calling. Every degraded
// sample observed upstream dictates arguments inline ("with path is ...",
// "content is ..."), whereas an explanation of how tools work does not.
var toolArgumentMarkers = []string{
	" with ",
	" is ",
	"=",
	":",
}

// strictToolArgumentMarkers apply to bare verb matches. " is " is deliberately
// excluded: "run bash yourself if you prefer; the command is short" would
// otherwise be misread as argument dictation.
var strictToolArgumentMarkers = []string{
	" with ",
	"=",
	":",
	"(",
	"\"",
}

const (
	// looseArgumentWindow bounds the argument search after an explicit
	// "...tool..." marker, which is already a strong degradation signal.
	looseArgumentWindow = 160
	// strictArgumentWindow keeps a bare verb match anchored: the dictation must
	// follow the tool name almost immediately, as in "call write_file with
	// path is ...".
	strictArgumentWindow = 32
)

// declaredClientToolNames returns the function names the request declared for
// client-side execution. Hosted tools are excluded: retrying those can repeat
// an upstream search, sandbox run, or image job, so they must keep the existing
// no-replay boundary enforced by qualityRequestHasReplayUnsafeHostedTools.
func declaredClientToolNames(body []byte) []string {
	if len(body) == 0 || len(body) > maxToolDegradationInspectionBytes {
		return nil
	}
	var payload struct {
		Tools []struct {
			Type     string `json:"type"`
			Name     string `json:"name"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(payload.Tools))
	names := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		kind := strings.ToLower(strings.TrimSpace(tool.Type))
		// An absent type still means a Chat Completions function tool.
		if kind != "" && kind != "function" && kind != "custom" {
			continue
		}
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			name = strings.TrimSpace(tool.Name)
		}
		if name == "" {
			continue
		}
		lowered := strings.ToLower(name)
		if _, exists := seen[lowered]; exists {
			continue
		}
		seen[lowered] = struct{}{}
		names = append(names, lowered)
	}
	return names
}

// toolCallDegraded reports whether visible text narrates a call to one of the
// declared tools while the stream produced no structured call.
//
// All four conditions must hold, so a normal answer that happens to mention a
// tool name is not retried:
//   - the request declared client-executed tools,
//   - the stream emitted no tool_calls/function_call delta,
//   - a narration marker appears at the very start of the visible text, and
//   - a declared tool name plus inline argument dictation follow that marker.
//
// Bare verb markers ("call X", "run X") also match ordinary sentences, so they
// require the stricter, tightly anchored argument test.
func toolCallDegraded(declared []string, visibleText string, sawToolCall bool) (string, bool) {
	if len(declared) == 0 || sawToolCall {
		return "", false
	}
	trimmed := strings.TrimSpace(visibleText)
	if len([]rune(trimmed)) < minToolNarrationRunes {
		return "", false
	}
	lowered := strings.ToLower(trimmed)
	window := lowered
	if len(window) > toolNarrationWindow {
		window = window[:toolNarrationWindow]
	}
	if name, ok := matchNarration(lowered, window, declared, toolNarrationMarkers, toolArgumentMarkers, looseArgumentWindow); ok {
		return name, true
	}
	return matchNarration(lowered, window, declared, bareToolNarrationMarkers, strictToolArgumentMarkers, strictArgumentWindow)
}

// matchNarration finds the earliest marker inside window, then requires a
// standalone declared tool name followed by argument dictation.
func matchNarration(lowered, window string, declared, markers, argMarkers []string, argWindow int) (string, bool) {
	marker := -1
	for _, candidate := range markers {
		if at := strings.Index(window, candidate); at >= 0 && (marker < 0 || at < marker) {
			marker = at
		}
	}
	if marker < 0 {
		return "", false
	}
	// Scan from the marker over the whole text, not just the window, so a long
	// tool name straddling the window boundary still matches.
	tail := lowered[marker:]
	for _, name := range declared {
		at := strings.Index(tail, name)
		if at < 0 || !toolNameStandsAlone(tail, at, name) {
			continue
		}
		if !dictatesArgumentsAfter(tail[at+len(name):], argMarkers, argWindow) {
			continue
		}
		return name, true
	}
	return "", false
}

// dictatesArgumentsAfter reports whether text immediately following the tool
// name reads as inline argument dictation rather than ordinary prose.
func dictatesArgumentsAfter(text string, markers []string, window int) bool {
	if text == "" {
		return false
	}
	// Bound the search so a marker far later in a long answer does not count.
	if window > 0 && len(text) > window {
		text = text[:window]
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// toolNameStandsAlone rejects substring hits inside a longer identifier so a
// declared "read" does not match "already".
func toolNameStandsAlone(text string, at int, name string) bool {
	if at > 0 {
		if prev := rune(text[at-1]); isToolNameRune(prev) {
			return false
		}
	}
	end := at + len(name)
	if end < len(text) {
		if next := rune(text[end]); isToolNameRune(next) {
			return false
		}
	}
	return true
}

func isToolNameRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_' || value == '-'
}
