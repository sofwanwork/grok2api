package inference

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/application/gateway"
)

// hostedToolWarningTrailer reports server-side tool declarations that the
// upstream accepted but never executed. It is an HTTP trailer because the
// verdict is only known after usage arrives at the end of the response.
const hostedToolWarningTrailer = "X-Grok2API-Warning"

// hostedToolNotExecutedWarning is the trailer value and log code emitted when a
// request declared hosted tools but upstream reported zero server-side tool use.
const hostedToolNotExecutedWarning = "hosted_tool_not_executed"

// hostedToolsContextKey carries the declared hosted tool types from the request
// handler to the response writer without widening five writer signatures.
const hostedToolsContextKey = "grok2api_declared_hosted_tools"

// maxHostedToolInspectionBytes bounds tool parsing. Requests larger than this
// are dominated by conversation history, not tool declarations, and the
// diagnostic is advisory only.
const maxHostedToolInspectionBytes = 1 << 20

// serverExecutedToolTypes lists tools that Grok must run on its own
// infrastructure. Only these can be proven unexecuted through
// usage.num_server_side_tools_used.
//
// Client-executed tools (function, custom, shell, local_shell, mcp, and the
// Anthropic bash/text_editor/computer families) are deliberately excluded: the
// gateway returns their calls to the caller for execution, so a zero
// server-side count is the correct and expected outcome for them.
var serverExecutedToolTypes = map[string]struct{}{
	"web_search":         {},
	"x_search":           {},
	"code_interpreter":   {},
	"code_execution":     {},
	"image_generation":   {},
	"collections_search": {},
	"file_search":        {},
}

// normalizeHostedToolType reduces dated and preview tool aliases to the base
// family name. Anthropic pins versions ("web_search_20250305") and OpenAI uses
// preview suffixes ("web_search_preview_2025_03_11"); both denote one family.
func normalizeHostedToolType(value string) string {
	kind := strings.ToLower(strings.TrimSpace(value))
	if kind == "" {
		return ""
	}
	for family := range serverExecutedToolTypes {
		if kind == family || strings.HasPrefix(kind, family+"_") {
			return family
		}
	}
	return kind
}

// declaredHostedTools returns the sorted, de-duplicated set of server-executed
// tool families declared in a request body. An empty result means the request
// cannot produce a hosted-tool warning.
func declaredHostedTools(body []byte) []string {
	if len(body) == 0 || len(body) > maxHostedToolInspectionBytes {
		return nil
	}
	var envelope struct {
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(envelope.Tools))
	for _, tool := range envelope.Tools {
		kind := normalizeHostedToolType(tool.Type)
		if kind == "" {
			continue
		}
		if _, hosted := serverExecutedToolTypes[kind]; !hosted {
			continue
		}
		seen[kind] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for kind := range seen {
		result = append(result, kind)
	}
	sort.Strings(result)
	return result
}

// hostedToolsUnexecuted decides whether to warn that declared server-side tools
// never ran. It answers false whenever the evidence is incomplete, so the
// trailer stays a reliable signal instead of routine noise.
//
// A warning requires all of:
//   - the request declared at least one server-executed tool;
//   - upstream actually reported usage (usage.Reported), so a truncated,
//     failed, or interrupted response never produces a verdict;
//   - both num_server_side_tools_used and num_sources_used are zero, since
//     either being positive proves the upstream ran something;
//   - the response produced output tokens, so an empty or withheld body is
//     attributed to the failure rather than to the tools.
func hostedToolsUnexecuted(declared []string, usage gateway.Usage) bool {
	if len(declared) == 0 || !usage.Reported {
		return false
	}
	if usage.NumServerSideToolsUsed > 0 || usage.NumSourcesUsed > 0 {
		return false
	}
	return usage.OutputTokens > 0
}
