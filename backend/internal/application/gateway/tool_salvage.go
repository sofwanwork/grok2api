package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// Tool-call salvage converts a degraded stream — the model narrating a call in
// a fenced pseudo-XML block instead of emitting a structured tool_calls delta —
// back into a real tool call. The narration carries the complete arguments, so
// reconstructing the call is both faster and cheaper than retrying: no second
// generation is billed and no 50% degradation lottery is re-entered.
//
// Salvage is deliberately conservative. It only accepts fenced blocks
// (```xml ... ```), because the fence gives an unambiguous end boundary for an
// unterminated final parameter; prose narration ("run tool X with path is ...")
// has no reliable boundaries and keeps the retry path. The tool name must come
// from a wrapper tag or from a single declared tool, and must be one the
// request actually declared.

const (
	// maxSalvageStreamBytes bounds the degraded-stream read. Narrations
	// observed upstream ran up to ~48k characters; 8 MiB leaves ample headroom
	// while still protecting the request from unbounded reads.
	maxSalvageStreamBytes = 8 << 20
	// maxSalvageArgs rejects absurd parameter counts from malformed blocks.
	maxSalvageArgs = 16
)

// salvageWrapperTags open a call or parameter wrapper rather than an argument.
var salvageWrapperTags = map[string]bool{
	"parameter":     true,
	"tool":          true,
	"tool_call":     true,
	"tool_use":      true,
	"invoke":        true,
	"function":      true,
	"function_call": true,
}

// salvageToolCallStream reads a degraded upstream stream and, when the visible
// text is a salvageable XML narration, returns a synthetic Chat SSE stream
// carrying the reconstructed tool call. It always returns a usable reader: on
// failure the original bytes are replayed so the caller's deliver-last path
// still forwards the prose answer.
func salvageToolCallStream(body io.Reader, declared []string, model string, usage Usage) (io.ReadCloser, bool) {
	if body == nil || len(declared) == 0 {
		if body == nil {
			return io.NopCloser(bytes.NewReader(nil)), false
		}
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxSalvageStreamBytes+1))
	if err != nil || len(raw) > maxSalvageStreamBytes {
		return io.NopCloser(bytes.NewReader(raw)), false
	}
	text := extractChatStreamText(raw)
	toolName, args, ok := extractXMLToolNarration(text, declared)
	if !ok {
		return io.NopCloser(bytes.NewReader(raw)), false
	}
	stream := buildSalvagedChatStream(model, toolName, args, usage)
	if len(stream) == 0 {
		return io.NopCloser(bytes.NewReader(raw)), false
	}
	return io.NopCloser(bytes.NewReader(stream)), true
}

// extractChatStreamText concatenates the visible content deltas of a Chat SSE
// stream. Reasoning, tool_calls, and control frames are ignored: salvage only
// cares about what the model narrated visibly.
func extractChatStreamText(raw []byte) string {
	var builder strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		for _, choice := range event.Choices {
			builder.WriteString(choice.Delta.Content)
		}
	}
	return builder.String()
}

// extractXMLToolNarration parses a fenced pseudo-XML tool narration into the
// tool name and its arguments. Rules, driven by shapes captured live:
//
//   - the block must open with ```xml and close with ``` so an unterminated
//     final parameter has an unambiguous end;
//   - the tool name comes from a wrapper tag attribute (name=, tool=) or, when
//     absent, from a single declared tool — a hallucinated name is rejected;
//   - arguments come from <parameter name="X">V</parameter> pairs (the last
//     one may run to the fence) and from terminated bare tags (<path>V</path>).
func extractXMLToolNarration(text string, declared []string) (string, map[string]string, bool) {
	fenceOpen := strings.Index(text, "```xml")
	if fenceOpen < 0 {
		return "", nil, false
	}
	region := text[fenceOpen+len("```xml"):]
	// Locate the block end. Well-formed blocks close with ```; degraded ones
	// often never do, ending instead with a run of wrapper close tags or the
	// stream end. Use the LAST ``` if present, otherwise trim any trailing
	// wrapper closes and accept the remainder — the parameter scan below is
	// what actually bounds each argument value.
	fenceClose := strings.LastIndex(region, "```")
	if fenceClose >= 0 {
		region = region[:fenceClose]
	} else {
		region = strings.TrimRight(region, " \t\r\n")
		for {
			trimmed := false
			for _, closer := range []string{"</parameter>", "</tool>", "</tool_call>", "</tool_use>", "</invoke>", "</function>", "</function_call>"} {
				if strings.HasSuffix(region, closer) {
					region = strings.TrimRight(region[:len(region)-len(closer)], " \t\r\n")
					trimmed = true
					break
				}
			}
			if !trimmed {
				break
			}
		}
	}
	if !strings.Contains(region, "<") {
		return "", nil, false
	}

	toolName := salvageWrapperToolName(region)
	if toolName == "" {
		if len(declared) == 1 {
			toolName = declared[0]
		} else {
			return "", nil, false
		}
	}
	if !salvageDeclaredContains(declared, toolName) {
		return "", nil, false
	}

	args := salvageRegionArguments(region)
	if len(args) == 0 {
		return "", nil, false
	}
	return toolName, args, true
}

// salvageWrapperToolName finds the called tool on a wrapper tag: <tool
// name="X">, <tool_call name="X">, <invoke tool="X">, <function name="X">, ...
// Longer tag names are matched first so "<tool" cannot shadow "<tool_call".
func salvageWrapperToolName(region string) string {
	for _, prefix := range []string{"<tool_call", "<tool_use", "<function_call", "<tool", "<invoke", "<function"} {
		at := strings.Index(region, prefix)
		if at < 0 {
			continue
		}
		end := strings.IndexByte(region[at:], '>')
		if end < 0 {
			continue
		}
		header := region[at : at+end]
		for _, attr := range []string{"name", "tool"} {
			if value := salvageXMLAttr(header, attr); value != "" {
				return value
			}
		}
	}
	return ""
}

// salvageRegionArguments extracts argument name/value pairs from the fenced
// region. Unterminated parameters take the rest of the region (trimmed of the
// fence newline); unterminated bare tags are skipped, because their end cannot
// be located safely.
func salvageRegionArguments(region string) map[string]string {
	args := make(map[string]string)
	for i := 0; i < len(region); {
		lt := strings.IndexByte(region[i:], '<')
		if lt < 0 {
			break
		}
		pos := i + lt
		end := strings.IndexByte(region[pos:], '>')
		if end < 0 {
			break
		}
		inner := region[pos+1 : pos+end]
		tagName := inner
		if sp := strings.IndexAny(inner, " \t\r\n"); sp >= 0 {
			tagName = inner[:sp]
		}
		if strings.HasPrefix(tagName, "/") || strings.HasPrefix(tagName, "!") {
			i = pos + end + 1
			continue
		}
		if tagName == "parameter" {
			argName := salvageXMLAttr(inner, "name")
			if argName == "" {
				i = pos + end + 1
				continue
			}
			valueStart := pos + end + 1
			closeIdx := strings.Index(region[valueStart:], "</parameter>")
			value := ""
			if closeIdx < 0 {
				// Unterminated final parameter: the value runs to the fence.
				value = strings.TrimRight(region[valueStart:], "\r\n")
				if strings.TrimSpace(value) == "" {
					break
				}
				if _, exists := args[argName]; !exists {
					args[argName] = value
				}
				break
			}
			value = region[valueStart : valueStart+closeIdx]
			if _, exists := args[argName]; !exists && strings.TrimSpace(value) != "" {
				args[argName] = value
			}
			i = valueStart + closeIdx + len("</parameter>")
			continue
		}
		if salvageWrapperTags[tagName] {
			i = pos + end + 1
			continue
		}
		// Bare argument tag such as <path>/tmp/a.md</path>. Only a terminated
		// pair is accepted; an unterminated one cannot be bounded safely.
		valueStart := pos + end + 1
		closeTag := "</" + tagName + ">"
		closeIdx := strings.Index(region[valueStart:], closeTag)
		if closeIdx < 0 {
			i = pos + end + 1
			continue
		}
		value := region[valueStart : valueStart+closeIdx]
		if _, exists := args[tagName]; !exists && strings.TrimSpace(value) != "" {
			args[tagName] = value
		}
		i = valueStart + closeIdx + len(closeTag)
	}
	if len(args) > maxSalvageArgs {
		return nil
	}
	return args
}

// salvageXMLAttr reads attr="value" or attr='value' from a tag header.
func salvageXMLAttr(header string, attr string) string {
	for _, quote := range []byte{'"', '\''} {
		pattern := attr + "=" + string(quote)
		at := strings.Index(header, pattern)
		if at < 0 {
			continue
		}
		rest := header[at+len(pattern):]
		if end := strings.IndexByte(rest, quote); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

func salvageDeclaredContains(declared []string, name string) bool {
	for _, candidate := range declared {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

// buildSalvagedChatStream synthesizes a Chat SSE stream for the reconstructed
// call: one delta carrying the full tool call, one finish frame, [DONE].
func buildSalvagedChatStream(model string, toolName string, args map[string]string, usage Usage) []byte {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	now := time.Now().Unix()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	chunkID := "chatcmpl-salvage-" + suffix
	callID := "call-salvage-" + suffix

	toolCall := map[string]any{
		"index": 0, "id": callID, "type": "function",
		"function": map[string]any{"name": toolName, "arguments": string(argsJSON)},
	}
	first := map[string]any{
		"id": chunkID, "object": "chat.completion.chunk", "created": now, "model": model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"role": "assistant", "tool_calls": []any{toolCall}},
			"finish_reason": nil,
		}},
	}
	finish := map[string]any{
		"id": chunkID, "object": "chat.completion.chunk", "created": now, "model": model,
		"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls",
		}},
	}
	if usage.Reported {
		finish["usage"] = map[string]any{
			"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens,
			"total_tokens":              usage.TotalTokens,
			"completion_tokens_details": map[string]any{"reasoning_tokens": usage.ReasoningTokens},
		}
	}
	var stream bytes.Buffer
	for _, frame := range []map[string]any{first, finish} {
		data, err := json.Marshal(frame)
		if err != nil {
			return nil
		}
		stream.WriteString("data: ")
		stream.Write(data)
		stream.WriteString("\n\n")
	}
	stream.WriteString("data: [DONE]\n\n")
	return stream.Bytes()
}
