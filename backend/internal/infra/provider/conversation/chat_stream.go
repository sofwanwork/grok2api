package conversation

import (
	"fmt"
	"io"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/pkg/tooltimeguard"
)

func (c *streamConverter) startChat() error {
	return c.writeData(map[string]any{
		"id": strings.Replace(c.id, "resp_", "chatcmpl_", 1), "object": "chat.completion.chunk",
		"created": c.created, "model": c.model, "system_fingerprint": systemFingerprint(c.model),
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	})
}

func (c *streamConverter) textDeltaChat(delta string) error {
	emit, matched := c.stopFilter.Push(delta)
	if matched != "" {
		c.stopSequence = matched
	}
	if emit == "" {
		return nil
	}
	return c.chatDelta(map[string]any{"content": emit})
}

func (c *streamConverter) chatDelta(delta map[string]any) error {
	if err := c.start(); err != nil {
		return err
	}
	return c.writeData(map[string]any{
		"id": strings.Replace(c.id, "resp_", "chatcmpl_", 1), "object": "chat.completion.chunk", "created": c.created, "model": c.model, "system_fingerprint": systemFingerprint(c.model),
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}},
	})
}

// markChatReasoningStart emits an SSE comment so the gateway can include
// encrypted or buffered thinking in its generation window. SSE clients ignore
// comments, so the public Chat Completions JSON contract remains unchanged.
func (c *streamConverter) markChatReasoningStart() error {
	if c.chatReasoningMark {
		return nil
	}
	if err := c.start(); err != nil {
		return err
	}
	if _, err := io.WriteString(c.writer, ": grok2api-reasoning-start\n\n"); err != nil {
		return err
	}
	c.chatReasoningMark = true
	return nil
}

func (c *streamConverter) toolStartChat(item responseItem, _ int) error {
	if err := c.start(); err != nil {
		return err
	}
	if _, exists := c.tools[item.ID]; exists {
		return nil
	}
	tool := streamTool{Index: len(c.tools), ID: item.CallID, Name: item.Name, Arguments: item.Arguments}
	c.tools[item.ID] = tool
	return c.chatDelta(map[string]any{"tool_calls": []any{map[string]any{
		"index": tool.Index, "id": tool.ID, "type": "function", "function": map[string]any{"name": tool.Name, "arguments": ""},
	}}})
}

func (c *streamConverter) toolDeltaChat(itemID, delta string) error {
	tool, ok := c.tools[itemID]
	if !ok {
		return nil
	}
	// Patch #21 v2: buffer arguments instead of forwarding them live.
	// Progressive argument deltas would deliver a tiny broken `timeout`
	// straight to the client before the guard could rewrite it at .done —
	// and HTTP tool-call contract does not need progressive args (clients
	// act on finish_reason=tool_calls with the complete arguments string).
	tool.SentArgs = false
	tool.Arguments += delta
	c.tools[itemID] = tool
	return nil
}

func (c *streamConverter) toolArgumentsDoneChat(itemID, arguments string) error {
	tool, ok := c.tools[itemID]
	if !ok || tool.Closed {
		return nil
	}
	if !tool.SentArgs {
		// Gabungkan delta buffered dengan arguments done yang mungkin kosong.
		arguments = strings.TrimSpace(arguments)
		if arguments == "" {
			arguments = strings.TrimSpace(tool.Arguments)
		} else if strings.TrimSpace(tool.Arguments) != "" {
			// Upstream may have buffered partial deltas AND a done payload —
			// the done payload is authoritative (it carries the final value).
		}
		if arguments != "" {
			// Patch #21 lapisan B: bila model jana timeout terlalu kecil untuk
			// command lambat (npm install/build dll) — naikkan nilai itu ke
			// minimum selamat sebelum delta sampai ke client. Idempoten dan
			// hanya berkesan pada arguments yang sah JSON.
			if corrected, changed := tooltimeguard.EnlargeToolTimeout(tool.Name, arguments); changed {
				arguments = corrected
			}
			// Patch #26: no-op edit interceptor — oldString == newString
			// ditulis semula dengan marker supaya model sedar dan fix.
			if corrected, changed := tooltimeguard.InterceptNoOpEdit(tool.Name, arguments); changed {
				arguments = corrected
			}
			if err := c.chatDelta(map[string]any{"tool_calls": []any{map[string]any{"index": tool.Index, "function": map[string]any{"arguments": arguments}}}}); err != nil {
				return err
			}
			tool.SentArgs = true
		}
	}
	c.tools[itemID] = tool
	return nil
}

func (c *streamConverter) doneChat(status string) error {
	if c.stopSequence == "" {
		if pending := c.stopFilter.Flush(); pending != "" {
			if err := c.chatDelta(map[string]any{"content": pending}); err != nil {
				return err
			}
		}
	}
	// Upstream withheld the CoT (anti-distillation) but usage proves the model
	// thought: give clients a visible marker instead of a silent empty trace.
	if !c.reasoningEmitted && c.usage.OutputTokensDetails.ReasoningTokens > 0 {
		placeholder := fmt.Sprintf("[thinking: %d tokens — trace withheld by upstream]", c.usage.OutputTokensDetails.ReasoningTokens)
		if err := c.chatDelta(map[string]any{"reasoning_content": placeholder}); err != nil {
			return err
		}
	}
	finishReason := "stop"
	if len(c.tools) > 0 {
		finishReason = "tool_calls"
	} else if c.refused {
		finishReason = "content_filter"
	} else if status == "incomplete" {
		finishReason = "length"
	}
	if err := c.writeData(map[string]any{
		"id": strings.Replace(c.id, "resp_", "chatcmpl_", 1), "object": "chat.completion.chunk", "created": c.created, "model": c.model, "system_fingerprint": systemFingerprint(c.model),
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason}}, "usage": chatUsage(c.usage),
	}); err != nil {
		return err
	}
	c.finished = true
	_, err := io.WriteString(c.writer, "data: [DONE]\n\n")
	return err
}

func (c *streamConverter) streamErrorChat(data []byte) error {
	if err := c.writeData(map[string]any{"error": normalizeOpenAIStreamError(streamErrorValue(data))}); err != nil {
		return err
	}
	_, err := io.WriteString(c.writer, "data: [DONE]\n\n")
	return err
}

func normalizeOpenAIStreamError(value any) map[string]any {
	result := map[string]any{"message": "Upstream request failed", "type": "api_error"}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"message", "type", "code", "param"} {
			if field, exists := object[key]; exists && field != nil {
				result[key] = field
			}
		}
	} else if message, ok := value.(string); ok && strings.TrimSpace(message) != "" {
		result["message"] = message
	}
	return result
}
