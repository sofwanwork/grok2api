package conversation

import (
	"io"
	"strings"
	"testing"
)

// Upstream withholds CoT text (anti-distillation) while usage still reports
// reasoning tokens. The stream must surface a visible placeholder instead of a
// silently empty trace, on both public protocols.
func TestDoneChatEmitsReasoningPlaceholderWhenTraceWithheld(t *testing.T) {
	var output strings.Builder
	converter := newStreamConverter(&output, OperationChat, ResponseOptions{})
	if err := converter.start(); err != nil {
		t.Fatal(err)
	}
	if err := converter.textDelta("hello"); err != nil {
		t.Fatal(err)
	}
	converter.usage.OutputTokensDetails.ReasoningTokens = 1644
	if err := converter.doneChat("completed"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "reasoning_content") {
		t.Fatalf("missing reasoning_content placeholder: %s", got)
	}
	if !strings.Contains(got, "[thinking: 1644 tokens") {
		t.Fatalf("placeholder missing token count: %s", got)
	}
	// The placeholder must arrive before the finish frame, not after [DONE].
	if idx := strings.Index(got, "[thinking:"); idx < 0 || strings.Index(got, `"finish_reason":"stop"`) < idx {
		t.Fatalf("placeholder must precede the finish frame: %s", got)
	}
}

// A real streamed trace must never be followed by a second placeholder.
func TestDoneChatSkipsPlaceholderWhenTraceStreamed(t *testing.T) {
	var output strings.Builder
	converter := newStreamConverter(&output, OperationChat, ResponseOptions{})
	if err := converter.start(); err != nil {
		t.Fatal(err)
	}
	if err := converter.reasoningTextDelta("rs_1", "genuine chain of thought"); err != nil {
		t.Fatal(err)
	}
	if err := converter.textDelta("answer"); err != nil {
		t.Fatal(err)
	}
	converter.usage.OutputTokensDetails.ReasoningTokens = 1644
	if err := converter.doneChat("completed"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "[thinking:") {
		t.Fatalf("placeholder emitted despite a streamed trace: %s", output.String())
	}
}

// Zero reasoning tokens means the model did not think; no placeholder.
func TestDoneChatSkipsPlaceholderWithoutReasoningTokens(t *testing.T) {
	var output strings.Builder
	converter := newStreamConverter(&output, OperationChat, ResponseOptions{})
	if err := converter.start(); err != nil {
		t.Fatal(err)
	}
	if err := converter.textDelta("plain answer"); err != nil {
		t.Fatal(err)
	}
	if err := converter.doneChat("completed"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "reasoning_content") {
		t.Fatalf("placeholder emitted without reasoning tokens: %s", output.String())
	}
}

func TestDoneMessagesEmitsThinkingPlaceholderWhenTraceWithheld(t *testing.T) {
	var output strings.Builder
	converter := newStreamConverter(&output, OperationMessages, ResponseOptions{})
	if err := converter.start(); err != nil {
		t.Fatal(err)
	}
	if err := converter.textDelta("hello"); err != nil {
		t.Fatal(err)
	}
	converter.usage.OutputTokensDetails.ReasoningTokens = 512
	if err := converter.doneMessages("completed"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "thinking_delta") {
		t.Fatalf("missing thinking_delta placeholder: %s", got)
	}
	if !strings.Contains(got, "[thinking: 512 tokens") {
		t.Fatalf("placeholder missing token count: %s", got)
	}
	// The synthetic block must be complete and precede message_delta.
	for _, event := range []string{"content_block_start", "content_block_delta", "content_block_stop"} {
		if idx := strings.Index(got, "event: "+event); idx < 0 || idx > strings.Index(got, "event: message_delta") {
			t.Fatalf("%s missing or misplaced: %s", event, got)
		}
	}
}

// A previously started thinking block (even signature-only) suppresses the
// placeholder: the client already saw a thinking block for this message.
func TestDoneMessagesSkipsPlaceholderWhenThinkingBlockStreamed(t *testing.T) {
	var output strings.Builder
	converter := newStreamConverter(io.Discard, OperationMessages, ResponseOptions{})
	if err := converter.start(); err != nil {
		t.Fatal(err)
	}
	// Simulate the signature-only path: thinkingStarted set by a prior block.
	converter.thinkingStarted = true
	converter.thinkingClosed = true
	converter.usage.OutputTokensDetails.ReasoningTokens = 512
	if err := converter.doneMessages("completed"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "[thinking:") {
		t.Fatalf("placeholder emitted despite an existing thinking block")
	}
}
