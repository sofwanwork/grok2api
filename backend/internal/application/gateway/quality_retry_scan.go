package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chenyme/grok2api/backend/internal/domain/audit"
)

const (
	qualityProtocolChat                = "chat"
	qualityProtocolResponses           = "responses"
	qualityProtocolAnthropic           = "anthropic"
	qualityReasoningSSEComment         = ": grok2api-reasoning-start"
	qualityReasoningEvidenceSSEComment = ": grok2api-reasoning-evidence"
	qualityKeepaliveSSEComment         = ": grok2api-keepalive"
	qualityHoldMaxBufferBytes          = 4 << 20
)

type qualityScanState struct {
	protocol         string
	pending          []byte
	hasThinking      bool
	reasoningStarted bool
	visibleRunes     int
	aggregateRunes   int
	semanticOutput   bool
	reasoningTokens  int64
	outputTokens     int64
	usage            Usage
	responseID       string
	terminal         bool
	// sawToolCall records a structured tool/function call. semanticOutput is
	// not a substitute: plain assistant text also sets that flag, and this must
	// stay false for a prose-only stream so tool degradation can be detected.
	sawToolCall bool
	// visibleText is the leading visible text, capped at
	// maxToolNarrationCapture. Only the beginning matters: every observed
	// degradation starts narrating immediately.
	visibleText []byte
	// firstEvidenceAt records when the upstream first showed generation
	// evidence (reasoning marker, thinking delta, visible text, or a tool
	// call). The audit first-token timestamp must use this moment — not the
	// later replay time — so a held stream reports honest TTFT.
	firstEvidenceAt time.Time
}

// maxToolNarrationCapture bounds retained visible text. It must exceed
// toolNarrationWindow plus the longest realistic tool name so a marker near the
// window edge is still followed by enough text to match.
const maxToolNarrationCapture = 512

func (s *qualityScanState) noteToolCall() {
	if s == nil {
		return
	}
	s.sawToolCall = true
	s.semanticOutput = true
}

// noteFirstEvidence latches the wall-clock moment the upstream first proved it
// was generating: a reasoning marker, a thinking delta, visible text, or a
// structured tool call. Called via defer from ObserveQualityChunk so the
// timestamp reflects chunk arrival, not a later classification decision.
func (s *qualityScanState) noteFirstEvidence() {
	if s == nil || !s.firstEvidenceAt.IsZero() {
		return
	}
	if s.reasoningStarted || s.hasThinking || s.visibleRunes > 0 || s.sawToolCall || s.semanticOutput {
		s.firstEvidenceAt = time.Now()
	}
}

func (s *qualityScanState) captureVisibleText(text string) {
	if s == nil || text == "" || len(s.visibleText) >= maxToolNarrationCapture {
		return
	}
	remaining := maxToolNarrationCapture - len(s.visibleText)
	if len(text) > remaining {
		text = text[:remaining]
	}
	s.visibleText = append(s.visibleText, text...)
}

type qualityReadResult struct {
	data []byte
	err  error
}

// qualityReadPump is the sole reader of the upstream body. It lets the hold
// timer win while an upstream Read is blocked, then remains the continuation
// reader for the response body after the held prefix is replayed.
type qualityReadPump struct {
	source    io.ReadCloser
	results   chan qualityReadResult
	done      chan struct{}
	closeOnce sync.Once
	pending   []byte
	finalErr  error
}

func newQualityReadPump(source io.ReadCloser) *qualityReadPump {
	pump := &qualityReadPump{
		source:  source,
		results: make(chan qualityReadResult),
		done:    make(chan struct{}),
	}
	go pump.run()
	return pump
}

// startHoldKeepalive writes periodic SSE comment chunks directly to the
// downstream client while a hold is buffering, so clients with short idle
// timeouts (OpenCode aborts and retries a silent long-thinking stream,
// surfacing duplicate answers) see the connection as alive. The comments are
// written straight to the client sink, never into the held prefix, so the
// replayed body stays byte-identical to the upstream stream and the quality
// scanner never sees them. The returned stop function blocks until the
// writer goroutine has fully exited, so no keepalive write can race the
// final body copy. A nil or failing sink disables injection.
func startHoldKeepalive(sink func([]byte) bool, interval time.Duration) func() {
	if sink == nil || interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		payload := []byte(qualityKeepaliveSSEComment + "\n\n")
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if !sink(payload) {
					// Client is gone or the transport cannot write.
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
		wg.Wait()
	}
}

func (p *qualityReadPump) run() {
	defer close(p.results)
	buf := make([]byte, 4096)
	for {
		n, err := p.source.Read(buf)
		if n == 0 && err == nil {
			continue
		}
		result := qualityReadResult{err: err}
		if n > 0 {
			result.data = append([]byte(nil), buf[:n]...)
		}
		select {
		case p.results <- result:
		case <-p.done:
			return
		}
		if err != nil {
			return
		}
	}
}

func (p *qualityReadPump) Read(dst []byte) (int, error) {
	for len(p.pending) == 0 {
		if p.finalErr != nil {
			return 0, p.finalErr
		}
		result, ok := <-p.results
		if !ok {
			p.finalErr = io.EOF
			return 0, io.EOF
		}
		p.pending = result.data
		p.finalErr = result.err
		if len(p.pending) == 0 && p.finalErr != nil {
			return 0, p.finalErr
		}
	}
	n := copy(dst, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}

func (p *qualityReadPump) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.done)
		err = p.source.Close()
	})
	return err
}

func qualityProtocolForOperation(operation audit.Operation) string {
	switch operation {
	case audit.OperationChat:
		return qualityProtocolChat
	case audit.OperationMessages:
		return qualityProtocolAnthropic
	default:
		return qualityProtocolResponses
	}
}

func (s *qualityScanState) signals() QualityStreamSignals {
	visibleRunes := max(s.visibleRunes, s.aggregateRunes)
	visible := int64((visibleRunes + 3) / 4)
	if s.usage.Reported {
		fromUsage := s.usage.OutputTokens - s.usage.ReasoningTokens
		if fromUsage > visible {
			visible = fromUsage
		}
	}
	output := s.outputTokens
	if s.usage.Reported && s.usage.OutputTokens > output {
		output = s.usage.OutputTokens
	}
	// Usage.reasoning_tokens is not proof of thinking. 降智 accounts report
	// hundreds of reasoning tokens on completed while the stream never sent
	// reasoning_text / reasoning_summary deltas (TUI shows no thoughts).
	return QualityStreamSignals{
		HasThinking:      s.hasThinking,
		ReasoningStarted: s.reasoningStarted || s.hasThinking,
		VisibleRunes:     int64(visibleRunes),
		VisibleTokens:    visible,
		ReasoningTokens:  max(s.reasoningTokens, s.usage.ReasoningTokens),
		OutputTokens:     output,
		Terminal:         s.terminal,
		SawToolCall:      s.sawToolCall,
		VisibleText:      string(s.visibleText),
	}
}

// ObserveQualityChunk feeds one SSE chunk into the hold classifier state.
// This is the shipped scanner used by peekQualityStream.
func ObserveQualityChunk(state *qualityScanState, chunk []byte) {
	if state == nil || len(chunk) == 0 {
		return
	}
	defer state.noteFirstEvidence()
	state.pending = append(state.pending, chunk...)
	for {
		index := bytes.IndexByte(state.pending, '\n')
		if index < 0 {
			if len(state.pending) > 1<<20 {
				state.pending = nil
			}
			return
		}
		line := bytes.TrimSpace(state.pending[:index])
		state.pending = state.pending[index+1:]
		if len(line) == 0 {
			continue
		}
		if bytes.Equal(line, []byte(qualityReasoningSSEComment)) {
			// Timing stub only. 降智 still emits this, then usage.reasoning_tokens=0.
			state.reasoningStarted = true
			continue
		}
		if bytes.Equal(line, []byte(qualityReasoningEvidenceSSEComment)) {
			// Protocol converters cannot expose encrypted_content in every public
			// JSON contract. This internal SSE comment preserves that evidence.
			state.reasoningStarted = true
			state.hasThinking = true
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(payload, []byte("[DONE]")) {
			state.terminal = true
			continue
		}
		observeQualityPayload(state, payload)
	}
}

func observeQualityPayload(state *qualityScanState, payload []byte) {
	switch state.protocol {
	case qualityProtocolChat:
		observeQualityChat(state, payload)
	case qualityProtocolAnthropic:
		observeQualityAnthropic(state, payload)
	default:
		observeQualityResponses(state, payload)
	}
}

func observeQualityChat(state *qualityScanState, payload []byte) {
	var event struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
				ThinkingContent  string `json:"thinking_content"`
				ToolCalls        []any  `json:"tool_calls"`
				FunctionCall     any    `json:"function_call"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens            int64 `json:"prompt_tokens"`
			CompletionTokens        int64 `json:"completion_tokens"`
			TotalTokens             int64 `json:"total_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	if state.responseID == "" {
		state.responseID = event.ID
	}
	if event.Usage != nil {
		state.usage.Reported = true
		state.usage.InputTokens = event.Usage.PromptTokens
		state.usage.OutputTokens = event.Usage.CompletionTokens
		state.usage.ReasoningTokens = event.Usage.CompletionTokensDetails.ReasoningTokens
		state.usage.TotalTokens = event.Usage.TotalTokens
		state.usage.ResponseModel = event.Model
		state.outputTokens = event.Usage.CompletionTokens
		state.reasoningTokens = event.Usage.CompletionTokensDetails.ReasoningTokens
	}
	for _, choice := range event.Choices {
		delta := choice.Delta
		if strings.TrimSpace(delta.Reasoning) != "" || strings.TrimSpace(delta.ReasoningContent) != "" || strings.TrimSpace(delta.ThinkingContent) != "" {
			state.hasThinking = true
		}
		if delta.Content != "" {
			noteVisibleContent(state, delta.Content)
		}
		if len(delta.ToolCalls) > 0 || delta.FunctionCall != nil {
			state.noteToolCall()
		}
		if choice.FinishReason != "" {
			state.terminal = true
		}
	}
}

type qualityResponsesOutputItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	EncryptedContent string `json:"encrypted_content"`
	Content          []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

func noteResponsesReasoningItem(state *qualityScanState, item qualityResponsesOutputItem) {
	if !strings.EqualFold(strings.TrimSpace(item.Type), "reasoning") {
		return
	}
	if strings.TrimSpace(item.ID) != "" {
		state.reasoningStarted = true
	}
	if strings.TrimSpace(item.EncryptedContent) != "" {
		state.hasThinking = true
	}
}

func observeQualityResponses(state *qualityScanState, payload []byte) {
	var event struct {
		Type     string                     `json:"type"`
		Delta    string                     `json:"delta"`
		Item     qualityResponsesOutputItem `json:"item"`
		Response *struct {
			ID     string                       `json:"id"`
			Model  string                       `json:"model"`
			Output []qualityResponsesOutputItem `json:"output"`
			Usage  *struct {
				OutputTokens        int64 `json:"output_tokens"`
				InputTokens         int64 `json:"input_tokens"`
				TotalTokens         int64 `json:"total_tokens"`
				OutputTokensDetails struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	switch event.Type {
	case "response.completed", "response.incomplete", "response.failed":
		state.terminal = true
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if strings.TrimSpace(event.Delta) != "" {
			state.hasThinking = true
		}
	case "response.output_item.added", "response.output_item.done":
		noteResponsesReasoningItem(state, event.Item)
		state.aggregateRunes = max(state.aggregateRunes, observeQualityResponsesOutputItem(state, event.Item))
	case "response.output_text.delta":
		if event.Delta != "" {
			noteVisibleContent(state, event.Delta)
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta", "response.mcp_call_arguments.delta":
		if event.Delta != "" {
			state.noteToolCall()
		}
	}
	if event.Response != nil {
		if state.responseID == "" {
			state.responseID = event.Response.ID
		}
		for _, item := range event.Response.Output {
			noteResponsesReasoningItem(state, item)
		}
		if event.Response.Usage != nil {
			state.usage.Reported = true
			state.usage.InputTokens = event.Response.Usage.InputTokens
			state.usage.OutputTokens = event.Response.Usage.OutputTokens
			state.usage.ReasoningTokens = event.Response.Usage.OutputTokensDetails.ReasoningTokens
			state.usage.TotalTokens = event.Response.Usage.TotalTokens
			state.usage.ResponseModel = event.Response.Model
			state.outputTokens = event.Response.Usage.OutputTokens
			state.reasoningTokens = event.Response.Usage.OutputTokensDetails.ReasoningTokens
		}
		aggregateRunes := 0
		for _, item := range event.Response.Output {
			aggregateRunes += observeQualityResponsesOutputItem(state, item)
		}
		state.aggregateRunes = max(state.aggregateRunes, aggregateRunes)
	}
}

func observeQualityResponsesOutputItem(state *qualityScanState, item qualityResponsesOutputItem) int {
	if state == nil {
		return 0
	}
	visibleRunes := 0
	switch item.Type {
	case "", "reasoning":
		return 0
	case "message":
		for _, content := range item.Content {
			text := content.Text
			if text == "" {
				text = content.Refusal
			}
			if text != "" {
				visibleRunes += utf8.RuneCountInString(text)
				state.semanticOutput = true
				continue
			}
			if content.Type != "" && content.Type != "output_text" && content.Type != "refusal" {
				state.semanticOutput = true
			}
		}
	default:
		// Function, shell, MCP and other call items are meaningful output even
		// when the provider omits usage and argument-delta events.
		state.noteToolCall()
	}
	return visibleRunes
}

func observeQualityAnthropic(state *qualityScanState, payload []byte) {
	var event struct {
		Type         string `json:"type"`
		ContentBlock struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Data string `json:"data"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
			Signature   string `json:"signature"`
		} `json:"delta"`
		Usage *struct {
			OutputTokens        int64 `json:"output_tokens"`
			OutputTokensDetails struct {
				ThinkingTokens int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	switch event.Type {
	case "message_stop":
		state.terminal = true
	case "content_block_start":
		switch event.ContentBlock.Type {
		case "thinking":
			state.reasoningStarted = true
		case "redacted_thinking":
			state.reasoningStarted = true
			if strings.TrimSpace(event.ContentBlock.Data) != "" {
				state.hasThinking = true
			}
		case "text":
			if event.ContentBlock.Text != "" {
				noteVisibleContent(state, event.ContentBlock.Text)
				state.semanticOutput = true
			}
		case "":
		default:
			// tool_use / server_tool_use / mcp_tool_use blocks open here, and
			// input_json_delta may never follow for a zero-argument call.
			state.noteToolCall()
		}
	case "content_block_delta":
		if event.Delta.Type == "thinking_delta" && strings.TrimSpace(event.Delta.Thinking) != "" {
			state.hasThinking = true
		}
		if event.Delta.Type == "signature_delta" && strings.TrimSpace(event.Delta.Signature) != "" {
			// Anthropic Messages represents Responses encrypted_content as a
			// signature delta. A non-empty signature is encrypted thinking proof.
			state.reasoningStarted = true
			state.hasThinking = true
		}
		if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
			noteVisibleContent(state, event.Delta.Text)
		}
		if event.Delta.Type == "input_json_delta" && event.Delta.PartialJSON != "" {
			state.noteToolCall()
		}
	}
	if event.Usage != nil {
		state.usage.Reported = true
		state.usage.OutputTokens = event.Usage.OutputTokens
		state.usage.ReasoningTokens = event.Usage.OutputTokensDetails.ThinkingTokens
		state.outputTokens = event.Usage.OutputTokens
		state.reasoningTokens = event.Usage.OutputTokensDetails.ThinkingTokens
	}
}

func noteVisibleContent(state *qualityScanState, text string) {
	if text == "" {
		return
	}
	state.visibleRunes += utf8.RuneCountInString(text)
	state.captureVisibleText(text)
}

func peekQualityStream(ctx context.Context, body io.ReadCloser, protocol string, cfg QualityRetryRuntime, keepaliveSink func([]byte) bool) (io.ReadCloser, QualityVerdict, Usage, string, time.Time, error) {
	cfg = normalizeQualityRetry(cfg)
	if body == nil {
		return io.NopCloser(bytes.NewReader(nil)), QualityWait, Usage{}, "", time.Time{}, errQualityEmptyStream
	}
	pump := newQualityReadPump(body)
	stopKeepalive := startHoldKeepalive(keepaliveSink, cfg.HoldKeepalive)
	defer stopKeepalive()
	state := qualityScanState{protocol: protocol}
	var held bytes.Buffer
	holdTimer := time.NewTimer(cfg.HoldTimeout)
	defer holdTimer.Stop()
	// holdExpired latches the one-shot hold deadline. Once the deadline has
	// fired, evidence that arrives later (the common shape for grok-4.6: a
	// long silent thinking phase pushes the reasoning-evidence marker past
	// the timer) still gets a chance to release the hold. Patch #13.
	holdExpired := false
	for {
		sig := state.signals()
		// Degradation is checked first, but only WINS when it actually fires.
		// When it does not fire we must not return Deliver: that would bypass
		// the missing-thinking classifier. Fall through to it instead.
		if len(cfg.DeclaredClientTools) > 0 {
			if _, degraded := toolCallDegraded(cfg.DeclaredClientTools, sig.VisibleText, sig.SawToolCall); degraded {
				return newPrefixReplay(&held, pump), QualityToolDegraded, state.usage, state.responseID, state.firstEvidenceAt, nil
			}
		}
		if cfg.missingThinkingEnabled() {
			// When degradation detection is active, a thinking stub alone must not
			// deliver: Grok announces reasoning before narrating a call in prose,
			// and delivering here would skip that check. Hold until the text is
			// conclusive (degraded above, or a real call / terminal below).
			// Patch #13: after the hold deadline has fired, real thinking
			// evidence plus a fully observed clean narration window releases the
			// hold. The rune guard is load-bearing: degraded narrations start at
			// visible-text offset 0, so once the first toolNarrationWindow runes
			// are clean the stream can no longer match the degradation window —
			// releasing only then keeps late narrations catchable. Without this
			// release path, late evidence leaves the loop waiting for EOF, so
			// the whole answer is buffered and pasted once instead of streaming.
			if sig.HasThinking && holdExpired && sig.VisibleRunes >= toolNarrationWindow {
				return newPrefixReplay(&held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
			}
			// Patch #16: finished stream that thought but produced almost no
			// visible answer — retry under its own budget before the
			// missing-thinking classifier delivers it.
			if classifySilentThinking(sig, cfg.MinOutputTokens, cfg.DeclaredClientTools, cfg.SilentThinkingEnabled) {
				return newPrefixReplay(&held, pump), QualitySilentThinking, state.usage, state.responseID, state.firstEvidenceAt, nil
			}
			if len(cfg.DeclaredClientTools) > 0 && !sig.SawToolCall && !sig.Terminal {
				// keep waiting for text that proves or disproves degradation
			} else if verdict := ClassifyQualityHold(sig, cfg.MinOutputTokens); verdict != QualityWait {
				return newPrefixReplay(&held, pump), verdict, state.usage, state.responseID, state.firstEvidenceAt, nil
			}
		} else if sig.SawToolCall || sig.HasThinking || (sig.Terminal && sig.OutputTokens > 0) {
			// Degradation-only peek: release as soon as the stream proves it is
			// not degraded, so latency is not tied to the hold timeout.
			return newPrefixReplay(&held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
		}
		// A completed empty stream must rotate immediately. Waiting for idle
		// timeout after response.completed / [DONE] surfaces HTTP 200 with 0
		// tokens and makes Grok TUI retry for 50–120s.
		if sig.Terminal {
			return finishQualityPeek(&held, pump, &state, cfg)
		}

		select {
		case <-ctx.Done():
			_ = pump.Close()
			return io.NopCloser(bytes.NewReader(held.Bytes())), QualityWait, state.usage, state.responseID, state.firstEvidenceAt, qualityPeekAbortError(ctx, ctx.Err())
		case <-holdTimer.C:
			sig.HoldExpired = true
			holdExpired = true // Patch #13: latch so late evidence can still release.
			if !cfg.missingThinkingEnabled() {
				// Degradation-only peek. A stream that already carries evidence
				// (text, a tool call, thinking) cannot become degraded by waiting
				// longer, so release it. A stub-only empty stream can still gain
				// the evidence a later verdict needs, so keep waiting instead of
				// flushing an empty HTTP 200 — same as the original behaviour.
				if sig.SawToolCall || sig.HasThinking || sig.VisibleTokens > 0 || sig.OutputTokens > 0 || sig.Terminal {
					return newPrefixReplay(&held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
				}
				// otherwise keep waiting
			} else if verdict := ClassifyQualityHold(sig, cfg.MinOutputTokens); verdict != QualityWait {
				return newPrefixReplay(&held, pump), verdict, state.usage, state.responseID, state.firstEvidenceAt, nil
			}
		case result, ok := <-pump.results:
			if !ok {
				return finishQualityPeek(&held, pump, &state, cfg)
			}
			if len(result.data) > 0 {
				if held.Len()+len(result.data) > qualityHoldMaxBufferBytes {
					_, _ = held.Write(result.data)
					return newPrefixReplay(&held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
				}
				_, _ = held.Write(result.data)
				ObserveQualityChunk(&state, result.data)
			}
			if result.err == io.EOF {
				return finishQualityPeek(&held, pump, &state, cfg)
			}
			if result.err != nil {
				_ = pump.Close()
				return io.NopCloser(bytes.NewReader(held.Bytes())), QualityWait, state.usage, state.responseID, state.firstEvidenceAt, qualityPeekAbortError(ctx, result.err)
			}
		}
	}
}

func finishQualityPeek(held *bytes.Buffer, pump *qualityReadPump, state *qualityScanState, cfg QualityRetryRuntime) (io.ReadCloser, QualityVerdict, Usage, string, time.Time, error) {
	if state == nil {
		return io.NopCloser(bytes.NewReader(nil)), QualityWait, Usage{}, "", time.Time{}, errQualityEmptyStream
	}
	if len(state.pending) > 0 {
		// Process a final valid SSE data line even when the upstream omitted its
		// trailing newline.
		ObserveQualityChunk(state, []byte{'\n'})
	}
	state.terminal = true
	signals := state.signals()
	// A stream can finish before the peek loop observes the narration, so the
	// degradation check is repeated here on the complete sample.
	if len(cfg.DeclaredClientTools) > 0 {
		if _, degraded := toolCallDegraded(cfg.DeclaredClientTools, signals.VisibleText, signals.SawToolCall); degraded {
			return newPrefixReplay(held, pump), QualityToolDegraded, state.usage, state.responseID, state.firstEvidenceAt, nil
		}
	}
	if !signals.HasThinking && signals.ReasoningTokens <= 0 && signals.OutputTokens <= 0 && signals.VisibleTokens <= 0 {
		if state.semanticOutput {
			return newPrefixReplay(held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
		}
		return newPrefixReplay(held, pump), QualityWait, state.usage, state.responseID, state.firstEvidenceAt, errQualityEmptyStream
	}
	// Patch #16: a finished stream that thought but produced almost no visible
	// answer gets its own retry verdict before the missing-thinking
	// classifier, which would deliver it as "thinking is proof enough".
	if classifySilentThinking(signals, cfg.MinOutputTokens, cfg.DeclaredClientTools, cfg.SilentThinkingEnabled) {
		return newPrefixReplay(held, pump), QualitySilentThinking, state.usage, state.responseID, state.firstEvidenceAt, nil
	}
	// Without the missing-thinking policy a completed stream is always
	// delivered: withholding here would drop a usable body.
	if !cfg.missingThinkingEnabled() {
		return newPrefixReplay(held, pump), QualityDeliver, state.usage, state.responseID, state.firstEvidenceAt, nil
	}
	return newPrefixReplay(held, pump), ClassifyQualityHold(signals, cfg.MinOutputTokens), state.usage, state.responseID, state.firstEvidenceAt, nil
}

func newPrefixReplay(held *bytes.Buffer, rest io.ReadCloser) io.ReadCloser {
	if rest == nil {
		rest = io.NopCloser(bytes.NewReader(nil))
	}
	if held == nil || held.Len() == 0 {
		return rest
	}
	return &replayReadCloser{Reader: io.MultiReader(bytes.NewReader(held.Bytes()), rest), source: rest}
}
