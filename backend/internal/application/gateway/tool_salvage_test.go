package gateway

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Real narration shapes captured live from a degraded Grok Build stream. Each
// must round-trip into the same call the model tried to make.
func TestExtractXMLToolNarrationLiveShapes(t *testing.T) {
	cases := map[string]struct {
		text     string
		declared []string
		wantTool string
		wantPath string
		hasKey   string
	}{
		"function_call_wrapper": {
			text:     "```xml\n<function call>\n<tool name=\"write_file\">\n<parameter name=\"path\">/tmp/rt.md</parameter>\n<parameter name=\"content\"># Guide\n\nBody</parameter>\n```",
			declared: []string{"write_file"}, wantTool: "write_file", wantPath: "/tmp/rt.md", hasKey: "content",
		},
		"invoke_tool_attr": {
			text:     "```xml\n<invoke tool=\"write_file\">\n<parameter name=\"path\">/tmp/cap.md</parameter>\n<parameter name=\"content\">Shell body here\n```",
			declared: []string{"write_file"}, wantTool: "write_file", wantPath: "/tmp/cap.md", hasKey: "content",
		},
		"bare_tags": {
			text:     "```xml\n<tool_call name=\"write_file\">\n<path>/tmp/cap.md</path>\n<content>Some body</content>\n```",
			declared: []string{"write_file"}, wantTool: "write_file", wantPath: "/tmp/cap.md", hasKey: "content",
		},
		"parameter_only_single_declared": {
			text:     "```xml\n<parameter name=\"path\">/tmp/sv.md</parameter>\n<parameter name=\"content\"># Shell Scripting\n\nMore lines here\n```",
			declared: []string{"write_file"}, wantTool: "write_file", wantPath: "/tmp/sv.md", hasKey: "content",
		},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			name, args, ok := extractXMLToolNarration(tc.text, tc.declared)
			if !ok {
				t.Fatalf("extractXMLToolNarration(%s) = not ok", label)
			}
			if name != tc.wantTool {
				t.Fatalf("tool = %q, want %q", name, tc.wantTool)
			}
			if args["path"] != tc.wantPath {
				t.Fatalf("path = %q, want %q", args["path"], tc.wantPath)
			}
			if args[tc.hasKey] == "" {
				t.Fatalf("args[%q] empty: %v", tc.hasKey, args)
			}
		})
	}
}

// Unterminated final parameter: value must run to the fence, fence newline trimmed.
func TestExtractXMLToolNarrationUnterminatedFinalParameter(t *testing.T) {
	text := "```xml\n<parameter name=\"path\">/tmp/sv.md</parameter>\n<parameter name=\"content\"># Shell Scripting Basics\n\n## Structure\n```"
	_, args, ok := extractXMLToolNarration(text, []string{"write_file"})
	if !ok {
		t.Fatal("not ok")
	}
	if args["content"] != "# Shell Scripting Basics\n\n## Structure" {
		t.Fatalf("content = %q", args["content"])
	}
}

// Real degraded block observed upstream: the fence never closes and the model
// emits a run of duplicate </parameter> tags instead. Salvage must trim the
// trailing wrappers and keep the argument values.
func TestExtractXMLToolNarrationDuplicateClosers(t *testing.T) {
	text := "```xml\n<parameter name=\"path\">/tmp/ct.md</parameter>\n<parameter name=\"content\"># Guide body\n\nMore\n</parameter>\n</parameter>\n</parameter>"
	name, args, ok := extractXMLToolNarration(text, []string{"write_file"})
	if !ok {
		t.Fatal("not ok")
	}
	if name != "write_file" {
		t.Fatalf("name = %q", name)
	}
	if args["path"] != "/tmp/ct.md" {
		t.Fatalf("path = %q", args["path"])
	}
	if !strings.Contains(args["content"], "# Guide body") {
		t.Fatalf("content = %q", args["content"])
	}
	if strings.Contains(args["content"], "</parameter>") {
		t.Fatalf("trailing wrappers leaked into content: %q", args["content"])
	}
}

// Safety rejections. Wrong answers here can destroy data: partial args for a
// write_file call (missing content) must never be salvaged.
func TestExtractXMLToolNarrationRejects(t *testing.T) {
	// No fence at all means prose narration — retry path, not salvage.
	cases := map[string]struct {
		text     string
		declared []string
	}{
		"no fence":            {"run tool write_file with path is /tmp/x.md content is body", []string{"write_file"}},
		"no name, many tools": {"```xml\n<parameter name=\"path\">/tmp/x.md</parameter>\n```", []string{"write_file", "bash"}},
		"hallucinated tool":   {"```xml\n<tool name=\"delete_everything\">\n<parameter name=\"path\">/tmp</parameter>\n```", []string{"write_file"}},
		"no args at all":      {"```xml\n<tool name=\"write_file\"></tool>\n```", []string{"write_file"}},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			if name, args, ok := extractXMLToolNarration(tc.text, tc.declared); ok {
				t.Fatalf("extractXMLToolNarration(%s) = (%q, %v, true), want rejected", label, name, args)
			}
		})
	}
}

// Content with markdown fences, quotes, backslashes must survive verbatim.
func TestExtractXMLToolNarrationPreservesVerbatim(t *testing.T) {
	content := "# Guide\n\n```bash\necho \"hi\" && printf '%s\\n' \"{a:1}\"\n```\n"
	text := "```xml\n<invoke tool=\"write_file\">\n<parameter name=\"path\">/tmp/g.md</parameter>\n<parameter name=\"content\">" + content + "</parameter>\n```"
	name, args, ok := extractXMLToolNarration(text, []string{"write_file"})
	if !ok {
		t.Fatal("not ok")
	}
	if name != "write_file" {
		t.Fatalf("name = %q", name)
	}
	if args["content"] != content {
		t.Fatalf("content mutated:\n  got  %q\n  want %q", args["content"], content)
	}
}

func TestExtractChatStreamTextConcatenatesDeltas(t *testing.T) {
	raw := []byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"```xml\\n\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"ignore me\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"<parameter name=\\\"path\\\">/tmp/x.md</parameter>\"}}]}\n\n" +
		"data: [DONE]\n\n")
	got := extractChatStreamText(raw)
	if got != "```xml\n<parameter name=\"path\">/tmp/x.md</parameter>" {
		t.Fatalf("text = %q", got)
	}
}

func TestBuildSalvagedChatStreamShape(t *testing.T) {
	stream := buildSalvagedChatStream("grok-4.6", "write_file",
		map[string]string{"path": "/tmp/x.md", "content": "body"},
		Usage{Reported: true, InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ReasoningTokens: 7})
	if len(stream) == 0 {
		t.Fatal("empty stream")
	}
	got := string(stream)
	for _, want := range []string{
		`"finish_reason":"tool_calls"`, `"name":"write_file"`,
		`/tmp/x.md`, `data: [DONE]`, `"reasoning_tokens":7`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stream missing %q:\n%s", want, got)
		}
	}
	// The finish frame must not precede the tool call.
	if strings.Index(got, `"finish_reason":"tool_calls"`) < strings.Index(got, `"tool_calls":[`) {
		t.Fatal("finish frame precedes the tool call frame")
	}
}

// End-to-end: a degraded Chat SSE stream is replaced by a structured call;
// an unsalvageable stream is replayed byte-for-byte for the deliver-last path.
func TestSalvageToolCallStreamEndToEnd(t *testing.T) {
	narration := "```xml\n<parameter name=\"path\">/tmp/sv.md</parameter>\n<parameter name=\"content\">body\n```"
	contentJSON, _ := json.Marshal(narration)
	degraded := "data: {\"choices\":[{\"delta\":{\"content\":" + string(contentJSON) + "}}]}\n\ndata: [DONE]\n\n"

	body, ok := salvageToolCallStream(io.NopCloser(strings.NewReader(degraded)), []string{"write_file"}, "grok-4.6", Usage{})
	if !ok {
		t.Fatal("salvage = false, want true")
	}
	got, _ := io.ReadAll(body)
	if !strings.Contains(string(got), `"tool_calls":[`) || !strings.Contains(string(got), "/tmp/sv.md") {
		t.Fatalf("salvaged stream missing call:\n%s", got)
	}

	prose := "data: {\"choices\":[{\"delta\":{\"content\":\"run tool write_file with path is /tmp/x content is y\"}}]}\n\ndata: [DONE]\n\n"
	replay, ok := salvageToolCallStream(io.NopCloser(strings.NewReader(prose)), []string{"write_file"}, "grok-4.6", Usage{})
	if ok {
		t.Fatal("salvage = true for prose, want false")
	}
	back, _ := io.ReadAll(replay)
	if string(back) != prose {
		t.Fatalf("deliver-last replay mutated:\n got %q\nwant %q", back, prose)
	}
}
