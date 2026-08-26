package gateway

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDeclaredClientToolNamesCollectsFunctionTools(t *testing.T) {
	body := []byte(`{"model":"grok-4.6","tools":[
		{"type":"function","function":{"name":"write_file"}},
		{"type":"function","function":{"name":"BASH"}},
		{"name":"legacy_tool"},
		{"type":"custom","name":"custom_tool"}
	]}`)
	got := declaredClientToolNames(body)
	want := []string{"write_file", "bash", "legacy_tool", "custom_tool"}
	if len(got) != len(want) {
		t.Fatalf("declaredClientToolNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("declaredClientToolNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Hosted tools must never enter this path: retrying them can repeat an upstream
// search, sandbox run, or image job.
func TestDeclaredClientToolNamesIgnoresHostedTools(t *testing.T) {
	for _, kind := range []string{
		"web_search", "web_search_20250305", "x_search", "code_interpreter",
		"code_execution", "image_generation", "collections_search",
		"file_search", "mcp", "shell", "local_shell",
	} {
		body := []byte(`{"tools":[{"type":"` + kind + `","name":"` + kind + `"}]}`)
		if got := declaredClientToolNames(body); len(got) != 0 {
			t.Errorf("declaredClientToolNames(%s) = %v, want none", kind, got)
		}
	}
}

func TestDeclaredClientToolNamesSkipsOversizedBodies(t *testing.T) {
	filler := strings.Repeat("x", maxToolDegradationInspectionBytes)
	body := []byte(`{"pad":"` + filler + `","tools":[{"type":"function","function":{"name":"bash"}}]}`)
	if got := declaredClientToolNames(body); got != nil {
		t.Fatalf("declaredClientToolNames(oversized) = %v, want nil", got)
	}
}

// The literal prose captured from a degraded grok-4.6 stream during live
// testing. If detection ever stops matching these, the retry silently stops
// working.
func TestToolCallDegradedMatchesObservedNarration(t *testing.T) {
	observed := []string{
		"tool call write_file with path is /tmp/notes.md content is # Linux Shell Scripting Guide\n\nShell scripting is",
		"run tool write_file with path is /tmp/notes.md content is # Linux Shell Scripting: A Comprehensive Guide",
		"invoke tool write_file with path is /tmp/g.md content is # Guide\n## Section 1\n```bash\necho \"line 1\"",
	}
	for _, text := range observed {
		name, degraded := toolCallDegraded([]string{"write_file"}, text, false)
		if !degraded {
			t.Errorf("toolCallDegraded(%q...) = false, want true", text[:40])
			continue
		}
		if name != "write_file" {
			t.Errorf("tool name = %q, want write_file", name)
		}
	}
}

// A real structured call must never be retried, even when the assistant also
// narrates what it is doing.
func TestToolCallDegradedIgnoresRealToolCall(t *testing.T) {
	if _, degraded := toolCallDegraded([]string{"write_file"},
		"run tool write_file with the content you asked for, saving it now to disk", true); degraded {
		t.Fatal("toolCallDegraded() = true with a structured tool call present, want false")
	}
}

func TestToolCallDegradedRequiresDeclaredTools(t *testing.T) {
	if _, degraded := toolCallDegraded(nil,
		"run tool write_file with path is /tmp/a.md and a long body of content", false); degraded {
		t.Fatal("toolCallDegraded() = true without declared tools, want false")
	}
}

// Ordinary answers must not be retried; a false positive doubles token spend
// and latency for a response that was already correct.
func TestToolCallDegradedIgnoresNormalProse(t *testing.T) {
	cases := map[string]string{
		"explains tool calling": "Tool calling lets a model request a function. You declare bash and the client runs it, returning output.",
		"mentions name only":    "The write_file helper is useful when you need to persist generated text somewhere on disk for later.",
		"marker appears late":   strings.Repeat("Here is a detailed explanation of shell scripting basics. ", 6) + "run tool write_file with path",
		"name without marker":   "I finished writing the guide and saved everything into write_file as you requested earlier today.",
		"marker but other tool": "run tool unknown_helper with path is /tmp/x.md and some content that goes on for a while",
	}
	for label, text := range cases {
		if _, degraded := toolCallDegraded([]string{"write_file", "bash"}, text, false); degraded {
			t.Errorf("%s: toolCallDegraded() = true, want false", label)
		}
	}
}

// Very short text may simply precede the tool_calls delta.
func TestToolCallDegradedIgnoresTooShortText(t *testing.T) {
	if _, degraded := toolCallDegraded([]string{"write_file"}, "run tool write_file", false); degraded {
		t.Fatal("toolCallDegraded() = true on a too-short sample, want false")
	}
}

func TestToolNameStandsAloneRejectsSubstringHits(t *testing.T) {
	// "read" inside "already" must not satisfy the declared tool "read".
	text := "invoke tool but i already handled that request for you without any further help"
	if _, degraded := toolCallDegraded([]string{"read"}, text, false); degraded {
		t.Fatal("toolCallDegraded() matched a substring inside a larger word, want false")
	}
	// The same declared name as a standalone word must match.
	standalone := "invoke tool read with the path set to /tmp/notes.md so the guide can be loaded"
	if _, degraded := toolCallDegraded([]string{"read"}, standalone, false); !degraded {
		t.Fatal("toolCallDegraded() = false for a standalone tool name, want true")
	}
}

// Bare verb markers ("call ", "run ") are broad, so they must not fire on
// ordinary assistant answers that discuss commands or files. A false positive
// here doubles token spend and latency on a response that was already correct.
func TestToolCallDegradedIgnoresProseWithBareVerbs(t *testing.T) {
	cases := map[string]string{
		"reports a finished run":  "I ran bash: the kernel is 3.6.6 and the working directory is /tmp/opencode as requested.",
		"explains how to run":     "To run bash commands you generally need a shell available on the host machine somewhere.",
		"suggests running later":  "You can run bash yourself if you prefer; the command is short and safe to execute now.",
		"summarises tool output":  "The bash output shows /tmp/opencode, which is the directory you asked me to inspect for you.",
		"mentions file path only": "I saved the notes and the write_file destination was /tmp/cap.md, so check there for output.",
	}
	for label, text := range cases {
		if name, degraded := toolCallDegraded([]string{"bash", "write_file"}, text, false); degraded {
			t.Errorf("%s: toolCallDegraded() = true (name=%q), want false\n  text: %s", label, name, text)
		}
	}
}

// The peek must surface a degradation verdict the moment narration appears,
// without waiting for the stream to finish.
func TestPeekQualityStreamDetectsDegradationEarly(t *testing.T) {
	t.Parallel()
	body := io.NopCloser(strings.NewReader(sse(
		`data: {"choices":[{"delta":{"content":"run tool write_file with path is /tmp/a.md content is # Guide\n\nMore text"}}]}`,
		`data: {"choices":[{"delta":{"content":"and even more prose that would keep the stream open"}}]}`,
		`data: {"usage":{"completion_tokens":40}}`,
		"data: [DONE]",
	)))
	cfg := QualityRetryRuntime{
		MinOutputTokens: 32, HoldTimeout: time.Second,
		DeclaredClientTools: []string{"write_file"},
	}
	replay, verdict, _, _, _, err := peekQualityStream(context.Background(), body, qualityProtocolChat, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if verdict != QualityToolDegraded {
		t.Fatalf("verdict=%s, want tool_degraded", verdict)
	}
	// The held prefix must replay so the body is not lost on deliver-last.
	got, _ := io.ReadAll(replay)
	if !strings.Contains(string(got), "write_file") {
		t.Fatalf("replay lost narration: %q", got)
	}
}

// A stream that emits a real tool call must never be retried as degraded.
func TestPeekQualityStreamDoesNotRetryRealToolCall(t *testing.T) {
	t.Parallel()
	body := io.NopCloser(strings.NewReader(sse(
		`data: {"choices":[{"delta":{"content":"let me call the tool now"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"function":{"name":"write_file","arguments":"{\"path\":\"/tmp/a.md\"}"}}]}}]}`,
		`data: {"usage":{"completion_tokens":12}}`,
		"data: [DONE]",
	)))
	cfg := QualityRetryRuntime{
		MinOutputTokens: 8, HoldTimeout: time.Second,
		DeclaredClientTools: []string{"write_file"},
	}
	replay, verdict, _, _, _, err := peekQualityStream(context.Background(), body, qualityProtocolChat, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if verdict == QualityToolDegraded {
		t.Fatal("verdict=tool_degraded for a real tool call, want deliver")
	}
}

// Normal prose must not be classified as degradation even when tools are declared.
func TestPeekQualityStreamIgnoresPlainAnswer(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("answer ", 40)
	body := io.NopCloser(strings.NewReader(sse(
		`data: {"choices":[{"delta":{"content":"`+content+`"}}]}`,
		`data: {"usage":{"completion_tokens":40}}`,
		"data: [DONE]",
	)))
	cfg := QualityRetryRuntime{
		MinOutputTokens: 8, HoldTimeout: time.Second,
		DeclaredClientTools: []string{"bash"},
	}
	replay, verdict, _, _, _, err := peekQualityStream(context.Background(), body, qualityProtocolChat, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if verdict == QualityToolDegraded {
		t.Fatal("verdict=tool_degraded for a plain answer, want deliver")
	}
}

// DecideToolDegradationRetry retries while budget remains, then delivers last —
// it never rejects, so an exhausted budget still returns the readable body.
func TestDecideToolDegradationRetryBounds(t *testing.T) {
	t.Parallel()
	if got := DecideToolDegradationRetry(0, 3, true); got != QualityActionRetry {
		t.Fatalf("attempt 0 action=%s, want retry", got)
	}
	if got := DecideToolDegradationRetry(1, 3, true); got != QualityActionRetry {
		t.Fatalf("attempt 1 action=%s, want retry", got)
	}
	if got := DecideToolDegradationRetry(2, 3, true); got != QualityActionDeliverLast {
		t.Fatalf("attempt 2 action=%s, want deliver_last", got)
	}
	// No remaining account slot must not retry either.
	if got := DecideToolDegradationRetry(0, 3, false); got != QualityActionDeliverLast {
		t.Fatalf("no-next action=%s, want deliver_last", got)
	}
}
