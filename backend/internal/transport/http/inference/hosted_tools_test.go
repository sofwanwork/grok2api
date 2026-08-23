package inference

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/application/gateway"
	"github.com/gin-gonic/gin"
)

func TestDeclaredHostedToolsDetectsServerExecutedFamilies(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "chat web_search", body: `{"tools":[{"type":"web_search"}]}`, want: "web_search"},
		{name: "openai preview alias", body: `{"tools":[{"type":"web_search_preview_2025_03_11"}]}`, want: "web_search"},
		{name: "anthropic dated alias", body: `{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, want: "web_search"},
		{name: "code interpreter", body: `{"tools":[{"type":"code_interpreter","container":{"type":"auto"}}]}`, want: "code_interpreter"},
		{name: "x_search", body: `{"tools":[{"type":"x_search"}]}`, want: "x_search"},
		{name: "image generation", body: `{"tools":[{"type":"image_generation"}]}`, want: "image_generation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := declaredHostedTools([]byte(test.body))
			if len(got) != 1 || got[0] != test.want {
				t.Fatalf("declaredHostedTools = %#v, want [%s]", got, test.want)
			}
		})
	}
}

// Client-executed tools must never arm the warning: the gateway hands their
// calls back to the caller, so zero server-side execution is correct.
func TestDeclaredHostedToolsIgnoresClientExecutedTools(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "no tools", body: `{"model":"grok-4.6"}`},
		{name: "empty tools", body: `{"tools":[]}`},
		{name: "function tool", body: `{"tools":[{"type":"function","function":{"name":"bash"}}]}`},
		{name: "shell tool", body: `{"tools":[{"type":"shell","environment":{"type":"local"}}]}`},
		{name: "legacy local_shell", body: `{"tools":[{"type":"local_shell"}]}`},
		{name: "mcp tool", body: `{"tools":[{"type":"mcp","server_label":"x"}]}`},
		{name: "anthropic bash", body: `{"tools":[{"type":"bash_20250124","name":"bash"}]}`},
		{name: "custom tool", body: `{"tools":[{"type":"custom","name":"x"}]}`},
		{name: "malformed body", body: `{"tools":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := declaredHostedTools([]byte(test.body)); len(got) != 0 {
				t.Fatalf("declaredHostedTools = %#v, want none", got)
			}
		})
	}
}

func TestDeclaredHostedToolsDeduplicatesAndSorts(t *testing.T) {
	body := `{"tools":[{"type":"web_search"},{"type":"x_search"},{"type":"web_search_preview"},{"type":"function"}]}`
	got := declaredHostedTools([]byte(body))
	if len(got) != 2 || got[0] != "web_search" || got[1] != "x_search" {
		t.Fatalf("declaredHostedTools = %#v, want [web_search x_search]", got)
	}
}

// Oversized bodies are dominated by history, not tool declarations, and the
// diagnostic is advisory: skip parsing rather than spend the CPU.
func TestDeclaredHostedToolsSkipsOversizedBodies(t *testing.T) {
	padding := strings.Repeat("a", maxHostedToolInspectionBytes)
	body := `{"tools":[{"type":"web_search"}],"input":"` + padding + `"}`
	if got := declaredHostedTools([]byte(body)); len(got) != 0 {
		t.Fatalf("declaredHostedTools = %#v, want none for oversized body", got)
	}
}

func TestHostedToolsUnexecutedWarnsOnlyWithCompleteEvidence(t *testing.T) {
	declared := []string{"web_search"}
	for _, test := range []struct {
		name     string
		declared []string
		usage    gateway.Usage
		want     bool
	}{
		{
			name:     "declared but upstream ran nothing",
			declared: declared,
			usage:    gateway.Usage{Reported: true, OutputTokens: 120},
			want:     true,
		},
		{
			name:     "no hosted tools declared",
			declared: nil,
			usage:    gateway.Usage{Reported: true, OutputTokens: 120},
			want:     false,
		},
		{
			name:     "server side tool executed",
			declared: declared,
			usage:    gateway.Usage{Reported: true, OutputTokens: 120, NumServerSideToolsUsed: 1},
			want:     false,
		},
		{
			// Sources prove a search ran even when the tool counter stays zero.
			name:     "sources prove execution",
			declared: declared,
			usage:    gateway.Usage{Reported: true, OutputTokens: 120, NumSourcesUsed: 3},
			want:     false,
		},
		{
			// A failed or interrupted response has no usage: the tools are not
			// the cause and must not be blamed.
			name:     "usage never reported",
			declared: declared,
			usage:    gateway.Usage{OutputTokens: 120},
			want:     false,
		},
		{
			name:     "empty response produced no output",
			declared: declared,
			usage:    gateway.Usage{Reported: true},
			want:     false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hostedToolsUnexecuted(test.declared, test.usage); got != test.want {
				t.Fatalf("hostedToolsUnexecuted = %v, want %v", got, test.want)
			}
		})
	}
}

// writeHostedToolResult drives the real response writer so the trailer contract
// is verified end to end rather than only at the decision function.
func writeHostedToolResult(t *testing.T, declared []string, body string, protocol streamProtocol, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if len(declared) > 0 {
		c.Set(hostedToolsContextKey, declared)
	}
	handler := &Handler{}
	handler.writeProtocolResult(c, &gateway.Result{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Finalize:   func(gateway.Usage, string, string) {},
	}, stream, false, protocol, "")
	return recorder
}

const hostedToolUnusedChatBody = `{"id":"chatcmpl_1","object":"chat.completion","model":"grok-4.6",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"I searched the web."},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":40,"total_tokens":50,"num_server_side_tools_used":0,"num_sources_used":0}}`

func TestWriteProtocolResultAnnouncesHostedToolTrailer(t *testing.T) {
	recorder := writeHostedToolResult(t, []string{"web_search"}, hostedToolUnusedChatBody, streamProtocolChat, false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	// The trailer must be announced in the header block, before the body, or
	// clients are not permitted to read it.
	if announced := recorder.Header().Get("Trailer"); !strings.Contains(announced, hostedToolWarningTrailer) {
		t.Fatalf("Trailer announcement = %q, want it to include %s", announced, hostedToolWarningTrailer)
	}
	warning := recorder.Header().Get(hostedToolWarningTrailer)
	if !strings.Contains(warning, hostedToolNotExecutedWarning) || !strings.Contains(warning, "tools=web_search") {
		t.Fatalf("%s = %q, want %s with tools=web_search", hostedToolWarningTrailer, warning, hostedToolNotExecutedWarning)
	}
}

// The upstream reported real search activity, so the response is trustworthy
// and must carry no warning.
func TestWriteProtocolResultOmitsWarningWhenToolsExecuted(t *testing.T) {
	body := strings.Replace(hostedToolUnusedChatBody, `"num_server_side_tools_used":0`, `"num_server_side_tools_used":2`, 1)
	recorder := writeHostedToolResult(t, []string{"web_search"}, body, streamProtocolChat, false)
	if warning := recorder.Header().Get(hostedToolWarningTrailer); warning != "" {
		t.Fatalf("%s = %q, want empty when tools executed", hostedToolWarningTrailer, warning)
	}
}

// Requests without hosted tools must not even announce the trailer, keeping the
// diagnostic free of routine noise.
func TestWriteProtocolResultSkipsTrailerWithoutHostedTools(t *testing.T) {
	recorder := writeHostedToolResult(t, nil, hostedToolUnusedChatBody, streamProtocolChat, false)
	if announced := recorder.Header().Get("Trailer"); strings.Contains(announced, hostedToolWarningTrailer) {
		t.Fatalf("Trailer announcement = %q, want no hosted-tool trailer", announced)
	}
	if warning := recorder.Header().Get(hostedToolWarningTrailer); warning != "" {
		t.Fatalf("%s = %q, want empty", hostedToolWarningTrailer, warning)
	}
}

// Streaming is the path Codex and Claude Code actually use, so the trailer has
// to survive SSE usage parsing too.
func TestWriteProtocolResultWarnsOnStreamingHostedTools(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"grok-4.6","choices":[{"index":0,"delta":{"content":"answer"}}]}`, "",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"grok-4.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":40,"total_tokens":50,"num_server_side_tools_used":0,"num_sources_used":0}}`, "",
		"data: [DONE]", "", "",
	}, "\n")
	recorder := writeHostedToolResult(t, []string{"web_search"}, stream, streamProtocolChat, true)
	warning := recorder.Header().Get(hostedToolWarningTrailer)
	if !strings.Contains(warning, hostedToolNotExecutedWarning) {
		t.Fatalf("%s = %q, want %s on the streaming path", hostedToolWarningTrailer, warning, hostedToolNotExecutedWarning)
	}
}

// The audit record must receive the warning, and it must arrive before Finalize
// runs, otherwise the diagnostic would never be persisted.
func TestWriteProtocolResultRecordsHostedToolWarningBeforeFinalize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(hostedToolsContextKey, []string{"web_search", "x_search"})

	recorded := ""
	recordedBeforeFinalize := false
	finalized := false
	handler := &Handler{}
	handler.writeProtocolResult(c, &gateway.Result{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(hostedToolUnusedChatBody)),
		RecordHostedToolWarning: func(warning string) {
			recorded = warning
			recordedBeforeFinalize = !finalized
		},
		Finalize: func(usage gateway.Usage, _, errorCode string) {
			finalized = true
			// The response is a genuine 2xx: the warning must not be reported as
			// a failure or the success-rate accounting would be corrupted.
			if errorCode != "" {
				t.Errorf("errorCode = %q, want empty for a successful hosted-tool warning", errorCode)
			}
			if usage.OutputTokens != 40 {
				t.Errorf("usage.OutputTokens = %d, want 40", usage.OutputTokens)
			}
		},
	}, false, false, streamProtocolChat, "")

	if recorded != "web_search,x_search" {
		t.Fatalf("recorded warning = %q, want %q", recorded, "web_search,x_search")
	}
	if !recordedBeforeFinalize {
		t.Fatal("hosted tool warning was recorded after Finalize; the audit record would miss it")
	}
	if !finalized {
		t.Fatal("Finalize was never called")
	}
}

// A nil recorder is valid for paths without audit recording; the trailer must
// still be emitted and nothing may panic.
func TestWriteProtocolResultToleratesNilHostedToolRecorder(t *testing.T) {
	recorder := writeHostedToolResult(t, []string{"web_search"}, hostedToolUnusedChatBody, streamProtocolChat, false)
	if warning := recorder.Header().Get(hostedToolWarningTrailer); !strings.Contains(warning, hostedToolNotExecutedWarning) {
		t.Fatalf("%s = %q, want the trailer even without an audit recorder", hostedToolWarningTrailer, warning)
	}
}

// Executed tools must leave the audit record untouched.
func TestWriteProtocolResultSkipsAuditWhenToolsExecuted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(hostedToolsContextKey, []string{"web_search"})
	body := strings.Replace(hostedToolUnusedChatBody, `"num_sources_used":0`, `"num_sources_used":4`, 1)

	recordCalls := 0
	handler := &Handler{}
	handler.writeProtocolResult(c, &gateway.Result{
		StatusCode:              http.StatusOK,
		Header:                  http.Header{"Content-Type": []string{"application/json"}},
		Body:                    io.NopCloser(strings.NewReader(body)),
		RecordHostedToolWarning: func(string) { recordCalls++ },
		Finalize:                func(gateway.Usage, string, string) {},
	}, false, false, streamProtocolChat, "")

	if recordCalls != 0 {
		t.Fatalf("RecordHostedToolWarning called %d times, want 0 when sources prove execution", recordCalls)
	}
}
