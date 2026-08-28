package conversation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chenyme/grok2api/backend/internal/pkg/tooltimeguard"
)

const (
	maxDeferredSearchTextBytes       = 8 << 20
	maxDeferredReasoningSummaryBytes = 8 << 20

	// contentDoomLoopThreshold kills the stream when the model emits the exact
	// same visible-content delta this many times in a row. A true content loop
	// burns real quota and client context, so this stays far below the
	// reasoning ceiling. It must still clear legitimate repetition in visible
	// output: markdown rules and ASCII table borders stream as long runs of an
	// identical single-character delta ("-", "=", "|"), and wide tables with
	// empty cells repeat the same separator delta.
	contentDoomLoopThreshold = 128

	// reasoningDoomLoopThreshold is higher than the content one: high/xhigh
	// effort models legitimately repeat the same reasoning token ("so", "hmm",
	// "wait", bullet markers) dozens of times during deep thinking. A low
	// threshold killed valid xhigh responses prematurely.
	reasoningDoomLoopThreshold = 256
)

// ConvertResponseStream 将 Responses SSE 转换为 Chat Completions 或 Anthropic Messages SSE。
func ConvertResponseStream(source io.ReadCloser, operation string) io.ReadCloser {
	return ConvertResponseStreamWithOptions(source, operation, ResponseOptions{})
}

// ConvertResponseStreamWithOptions 按下游协议选项生成 Chat 或 Anthropic SSE。
func ConvertResponseStreamWithOptions(source io.ReadCloser, operation string, options ResponseOptions) io.ReadCloser {
	if operation == OperationResponses {
		return guardResponseStream(source)
	}
	reader, writer := io.Pipe()
	stream := newStreamPipeReadCloser(reader, source)
	go func() {
		defer stream.closeSource()
		converter := newStreamConverter(writer, operation, options)
		err := consumeSSE(source, converter.handle)
		if err == nil {
			err = converter.finish()
		}
		_ = writer.CloseWithError(err)
	}()
	return stream
}

type streamConverter struct {
	writer            io.Writer
	operation         string
	id                string
	model             string
	created           int64
	started           bool
	finished          bool
	textStarted       bool
	textIndex         int
	thinkingStarted   bool
	thinkingClosed    bool
	thinkingIndex     int
	thinkingItemID    string
	chatReasoningMark bool
	evidenceMarked    bool
	// reasoningEmitted records that any reasoning trace reached the client.
	// Upstream may withhold CoT text (anti-distillation) while still reporting
	// reasoning tokens; usage alone must not double-emit a placeholder.
	reasoningEmitted bool
	reasoningItems    map[string]*reasoningStreamState
	reasoningOrder    []string
	activeReasoningID string
	nextIndex         int
	tools             map[string]streamTool
	// noOpEditState menjejaki bilangan no-op edit berturut-turut dalam stream
	// ini (patch #26 v2) — marker makin tegas setiap retry, reset pada edit sah.
	noOpEditState    []int
	// activityGuard (patch #27) menjejaki adakah model run build, start dev
	// server, atau verify HTTP — untuk inject reminder di doneChat.
	activityGuard     *tooltimeguard.StreamActivityGuard
	webSearch         []webSearchCall
	webSearchEmitted  map[string]bool
	deferSearchText   bool
	pendingSearchText strings.Builder
	usage             responseUsage
	options           ResponseOptions
	stopFilter        *anthropicStreamStopFilter
	stopSequence      string
	refused           bool
	repeatTracker     streamRepeatTracker
}

// streamRepeatTracker tracks upstream deltas before protocol conversion,
// buffering, and stop filters so no downstream path can bypass loop protection.
// Content and reasoning keep separate counters because high/xhigh effort models
// legitimately repeat similar reasoning tokens ("so", "hmm", "wait") many times
// during deep thinking; a single shared counter killed valid xhigh responses.
type streamRepeatTracker struct {
	lastContentDelta   string
	contentRepeatCount int
	lastReasonDelta    string
	reasonRepeatCount  int
	// Patch #28: fuzzy matching fields — track normalized deltas untuk
	// tangkap repetition yang berbeza whitespace sahaja (cth. trailing
	// space, \r\n vs \n). Juga track accumulated content untuk detect
	// baris yang diulang walaupun delta berbeza (multi-delta repetition).
	lastContentNormalized  string
	normalizedRepeatCount int
	// Rolling content fingerprint — hash 64-char prefix untuk detect
	// repetition pada teks panjang yang dipecah ke delta kecil.
	contentWindow    strings.Builder
	contentWindowLen int
}

// streamPipeReadCloser ensures a downstream cancellation immediately closes the
// upstream body, including while the forwarding goroutine is blocked in Read.
type streamPipeReadCloser struct {
	*io.PipeReader
	source    io.ReadCloser
	closeOnce sync.Once
	closeErr  error
}

func newStreamPipeReadCloser(reader *io.PipeReader, source io.ReadCloser) *streamPipeReadCloser {
	return &streamPipeReadCloser{PipeReader: reader, source: source}
}

func (r *streamPipeReadCloser) Close() error {
	readerErr := r.PipeReader.Close()
	sourceErr := r.closeSource()
	if readerErr != nil {
		return readerErr
	}
	return sourceErr
}

func (r *streamPipeReadCloser) closeSource() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.source.Close()
	})
	return r.closeErr
}

type streamTool struct {
	Index     int
	ID        string
	Name      string
	Arguments string
	SentArgs  bool
	Closed    bool
}

type reasoningStreamState struct {
	summary   strings.Builder
	rawSeen   bool
	done      bool
	anonymous bool
}

func newStreamConverter(writer io.Writer, operation string, options ResponseOptions) *streamConverter {
	return &streamConverter{
		writer: writer, operation: operation, created: time.Now().Unix(), tools: make(map[string]streamTool),
		webSearchEmitted: make(map[string]bool),
		reasoningItems:   make(map[string]*reasoningStreamState),
		deferSearchText:  operation == OperationMessages && options.AnthropicWebSearch,
		options:          options, stopFilter: newAnthropicStreamStopFilter(options.StopSequences),
		// Patch #26 v2: init no-op edit counter untuk stateful tracking.
		noOpEditState: make([]int, 1),
		// Patch #27: init activity guard untuk dev server reminder tracking.
		activityGuard: tooltimeguard.NewStreamActivityGuard(),
	}
}

// markReasoningEvidence preserves non-empty upstream encrypted_content for the
// request-path quality scanner after protocol conversion. SSE clients ignore
// comments, so Chat and Messages public event payloads remain unchanged.
func (c *streamConverter) markReasoningEvidence() error {
	if c.evidenceMarked {
		return nil
	}
	if err := c.start(); err != nil {
		return err
	}
	if _, err := io.WriteString(c.writer, ": grok2api-reasoning-evidence\n\n"); err != nil {
		return err
	}
	c.evidenceMarked = true
	return nil
}

// noteWebSearch records a Build web_search_call. Emission is deferred to doneMessages
// so we always use the completed action.sources payload from the final envelope when available.
// For progressive UI we still emit server_tool_use as soon as we see the call.
func (c *streamConverter) noteWebSearch(call webSearchCall, final bool) error {
	filtered := dedupeWebSearchCalls([]webSearchCall{call})
	if len(filtered) == 0 {
		return nil
	}
	call = filtered[0]
	replaced := false
	for i, existing := range c.webSearch {
		if existing.ID == call.ID {
			// Prefer richer final payload.
			if final || len(call.Hits) >= len(existing.Hits) {
				c.webSearch[i] = call
			}
			replaced = true
			break
		}
	}
	if !replaced {
		if len(c.webSearch) >= maxWebSearchCalls {
			return nil
		}
		c.webSearch = append(c.webSearch, call)
	}
	if !c.textStarted {
		c.deferSearchText = true
	}
	if c.textStarted || (c.thinkingStarted && !c.thinkingClosed) {
		return nil
	}
	// Emit server_tool_use promptly so Claude Code can show "Searching: …".
	return c.emitWebSearchUse(call)
}

func (c *streamConverter) emitWebSearchUse(call webSearchCall) error {
	if err := c.start(); err != nil {
		return err
	}
	if c.webSearchEmitted[call.ID+"#use"] {
		return nil
	}
	index := c.nextIndex
	c.nextIndex++
	c.webSearchEmitted[call.ID+"#use"] = true
	if err := c.writeEvent("content_block_start", map[string]any{
		"type": "content_block_start", "index": index,
		"content_block": map[string]any{"type": "server_tool_use", "id": call.ID, "name": "web_search", "input": map[string]any{}},
	}); err != nil {
		return err
	}
	if call.Query != "" {
		if err := c.writeEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": queryJSONPartial(call.Query)},
		}); err != nil {
			return err
		}
	}
	if err := c.writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
		return err
	}
	return nil
}

func (c *streamConverter) emitPendingWebSearchResults() error {
	c.webSearch = dedupeWebSearchCalls(c.webSearch)
	for _, call := range c.webSearch {
		if c.webSearchEmitted[call.ID+"#result"] {
			continue
		}
		if !c.webSearchEmitted[call.ID+"#use"] {
			if err := c.emitWebSearchUse(call); err != nil {
				return err
			}
		}
		if err := c.start(); err != nil {
			return err
		}
		index := c.nextIndex
		c.nextIndex++
		c.webSearchEmitted[call.ID+"#result"] = true
		var content any
		if call.Failed {
			code := call.Code
			if code == "" {
				code = "unavailable"
			}
			content = map[string]any{"type": "web_search_tool_result_error", "error_code": code}
		} else {
			hits := make([]any, 0, len(call.Hits))
			for _, hit := range call.Hits {
				hits = append(hits, map[string]any{"type": "web_search_result", "title": hit.Title, "url": hit.URL})
			}
			content = hits
		}
		if err := c.writeEvent("content_block_start", map[string]any{
			"type": "content_block_start", "index": index,
			"content_block": map[string]any{
				"type": "web_search_tool_result", "tool_use_id": call.ID, "content": content,
			},
		}); err != nil {
			return err
		}
		if err := c.writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
			return err
		}
	}
	return nil
}

func (c *streamConverter) handle(event string, data []byte) error {
	if c.finished {
		return nil
	}
	typeName, root, ok := parseSSEEvent(event, data)
	if !ok {
		return nil
	}
	// Loop protection lives at the raw-event layer so it also covers deltas
	// that are later dropped by buffering, stop-sequence filtering, deferred
	// web-search text, or suppressed reasoning. The channel emitters do NOT
	// re-track; counting twice would trip the threshold on legitimate runs.
	if err := c.repeatTracker.trackEvent(typeName, root); err != nil {
		return err
	}
	if c.stopSequence != "" && typeName != "response.completed" && typeName != "response.incomplete" && typeName != "response.failed" && typeName != "error" {
		return nil
	}
	switch typeName {
	case "response.created", "response.in_progress":
		var response responseEnvelope
		_ = json.Unmarshal(root["response"], &response)
		c.setResponse(response)
		return c.start()
	case "response.output_text.delta":
		var delta string
		_ = json.Unmarshal(root["delta"], &delta)
		if err := c.start(); err != nil {
			return err
		}
		if c.operation == OperationMessages && c.deferSearchText {
			return c.bufferSearchText(delta)
		}
		return c.textDelta(delta)
	case "response.refusal.delta":
		var delta string
		_ = json.Unmarshal(root["delta"], &delta)
		c.refused = true
		if c.operation == OperationChat {
			return c.chatDelta(map[string]any{"refusal": delta})
		}
		return c.textDeltaMessages(delta)
	case "response.output_text.annotation.added":
		if c.operation != OperationChat {
			return nil
		}
		var annotation any
		if json.Unmarshal(root["annotation"], &annotation) != nil || annotation == nil {
			return nil
		}
		return c.chatDelta(map[string]any{"annotations": []any{annotation}})
	case "response.reasoning_summary_text.delta":
		var itemID, delta string
		_ = json.Unmarshal(root["item_id"], &itemID)
		_ = json.Unmarshal(root["delta"], &delta)
		return c.reasoningSummaryDelta(itemID, delta)
	case "response.reasoning_text.delta":
		var itemID, delta string
		_ = json.Unmarshal(root["item_id"], &itemID)
		_ = json.Unmarshal(root["delta"], &delta)
		return c.reasoningTextDelta(itemID, delta)
	case "response.output_item.added":
		var item responseItem
		_ = json.Unmarshal(root["item"], &item)
		if item.Type == "reasoning" && c.reasoningOutputEnabled() {
			c.ensureReasoningState(item.ID)
		}
		if item.Type == "reasoning" && c.operation == OperationMessages && c.options.AnthropicThinking {
			return c.thinkingStart(item.ID)
		}
		if item.Type == "reasoning" && item.ID != "" && c.operation == OperationChat {
			return c.markChatReasoningStart()
		}
		if item.Type == "web_search_call" && c.operation == OperationMessages && c.options.AnthropicWebSearch {
			if call, ok := parseWebSearchCallItem(item); ok {
				return c.noteWebSearch(call, false)
			}
			return nil
		}
		if item.Type != "function_call" {
			return nil
		}
		var outputIndex int
		_ = json.Unmarshal(root["output_index"], &outputIndex)
		return c.toolStart(item, outputIndex)
	case "response.function_call_arguments.delta":
		var itemID, delta string
		_ = json.Unmarshal(root["item_id"], &itemID)
		_ = json.Unmarshal(root["delta"], &delta)
		return c.toolDelta(itemID, delta)
	case "response.function_call_arguments.done":
		var itemID, arguments string
		_ = json.Unmarshal(root["item_id"], &itemID)
		_ = json.Unmarshal(root["arguments"], &arguments)
		return c.toolArgumentsDone(itemID, arguments)
	case "response.output_item.done":
		var item responseItem
		_ = json.Unmarshal(root["item"], &item)
		if item.Type == "function_call" {
			return c.toolArgumentsDone(item.ID, item.Arguments)
		}
		if item.Type == "reasoning" {
			if c.reasoningOutputEnabled() {
				if err := c.reasoningDone(item); err != nil {
					return err
				}
			}
			// Preserve non-empty upstream encrypted_content for the request-path
			// quality scanner after protocol conversion (upstream quality guard).
			if strings.TrimSpace(item.Encrypted) != "" {
				if err := c.markReasoningEvidence(); err != nil {
					return err
				}
			}
			// Build pool opaque reasoning: emit the encrypted_content blob as a
			// reasoning_opaque delta so Chat Completions clients can retain it
			// across turns. Anthropic Messages already has signature_delta.
			if c.operation == OperationChat && item.Encrypted != "" {
				return c.chatDelta(map[string]any{"reasoning_opaque": item.Encrypted})
			}
			return c.thinkingDone(item)
		}
		if item.Type == "web_search_call" && c.operation == OperationMessages && c.options.AnthropicWebSearch {
			if call, ok := parseWebSearchCallItem(item); ok {
				return c.noteWebSearch(call, true)
			}
		}
	case "response.completed", "response.incomplete":
		var response responseEnvelope
		_ = json.Unmarshal(root["response"], &response)
		c.setResponse(response)
		for _, item := range response.Output {
			if item.Type == "reasoning" && strings.TrimSpace(item.Encrypted) != "" {
				if err := c.markReasoningEvidence(); err != nil {
					return err
				}
			}
		}
		if c.operation == OperationMessages && c.options.AnthropicWebSearch {
			parsed := parseResponse(response)
			for _, call := range parsed.WebSearch {
				if err := c.noteWebSearch(call, true); err != nil {
					return err
				}
			}
		}
		status := response.Status
		if status == "" && typeName == "response.incomplete" {
			status = "incomplete"
		}
		return c.done(status)
	case "error", "response.failed":
		return c.streamError(data)
	}
	return nil
}

func (c *streamConverter) reasoningOutputEnabled() bool {
	return c.operation == OperationChat || (c.operation == OperationMessages && c.options.AnthropicThinking)
}

func (c *streamConverter) ensureReasoningState(itemID string) (string, *reasoningStreamState) {
	key := itemID
	if key != "" {
		if state, exists := c.reasoningItems[key]; exists {
			c.activeReasoningID = key
			return key, state
		}
		// Some compatible upstreams omit item_id on the first delta. Once the
		// real item arrives, attach that anonymous state instead of creating a
		// second source that could later replay the buffered summary.
		if anonymous := c.activeReasoningID; anonymous != "" {
			if state := c.reasoningItems[anonymous]; state != nil && state.anonymous && !state.done {
				delete(c.reasoningItems, anonymous)
				state.anonymous = false
				c.reasoningItems[key] = state
				for index, existing := range c.reasoningOrder {
					if existing == anonymous {
						c.reasoningOrder[index] = key
						break
					}
				}
				c.activeReasoningID = key
				return key, state
			}
		}
	}
	if key == "" {
		key = c.activeReasoningID
	}
	anonymous := false
	if key == "" {
		key = fmt.Sprintf("#reasoning-%d", len(c.reasoningOrder)+1)
		anonymous = true
	}
	state, exists := c.reasoningItems[key]
	if !exists {
		state = &reasoningStreamState{anonymous: anonymous}
		c.reasoningItems[key] = state
		c.reasoningOrder = append(c.reasoningOrder, key)
	}
	c.activeReasoningID = key
	return key, state
}

func (c *streamConverter) reasoningSummaryDelta(itemID, delta string) error {
	if delta == "" || !c.reasoningOutputEnabled() {
		return nil
	}
	// Loop protection is applied at the raw-event layer (handle → trackEvent),
	// which covers dropped/suppressed deltas too; no re-counting here.
	_, state := c.ensureReasoningState(itemID)
	if state.done || state.rawSeen {
		return nil
	}
	// Console can publish the same client-facing thought through summary and
	// raw reasoning events. Defer summary until item completion so raw can take
	// precedence without relying on chunk boundaries or text equality.
	pending := state.summary.Len()
	if pending >= maxDeferredReasoningSummaryBytes || len(delta) > maxDeferredReasoningSummaryBytes-pending {
		return fmt.Errorf("reasoning summary penimbal tertangguh melebihi %d MiB", maxDeferredReasoningSummaryBytes>>20)
	}
	state.summary.WriteString(delta)
	return nil
}

func (c *streamConverter) reasoningTextDelta(itemID, delta string) error {
	if delta == "" || !c.reasoningOutputEnabled() {
		return nil
	}
	_, state := c.ensureReasoningState(itemID)
	if state.done {
		return nil
	}
	if !state.rawSeen {
		state.rawSeen = true
		state.summary.Reset()
	}
	return c.emitReasoningDelta(delta)
}

func (c *streamConverter) emitReasoningDelta(delta string) error {
	// Loop protection is applied at the raw-event layer (handle → trackEvent);
	// no re-counting here or legitimate deep-thinking runs would trip early.
	c.reasoningEmitted = true
	if c.operation == OperationChat {
		return c.chatDelta(map[string]any{"reasoning_content": delta})
	}
	if c.operation == OperationMessages {
		return c.thinkingDelta(delta)
	}
	return nil
}

func (c *streamConverter) reasoningDone(item responseItem) error {
	key, state := c.ensureReasoningState(item.ID)
	if state.done {
		return nil
	}
	if err := c.flushReasoningSummary(state); err != nil {
		return err
	}
	state.done = true
	if c.activeReasoningID == key {
		c.activeReasoningID = ""
	}
	return nil
}

func (c *streamConverter) flushReasoningSummary(state *reasoningStreamState) error {
	if state == nil || state.rawSeen || state.summary.Len() == 0 {
		return nil
	}
	value := state.summary.String()
	state.summary.Reset()
	return c.emitReasoningDelta(value)
}

func (c *streamConverter) flushPendingReasoning() error {
	for _, key := range c.reasoningOrder {
		state := c.reasoningItems[key]
		if state.done {
			continue
		}
		if err := c.flushReasoningSummary(state); err != nil {
			return err
		}
		state.done = true
	}
	c.activeReasoningID = ""
	return nil
}

func (c *streamConverter) bufferSearchText(delta string) error {
	pending := c.pendingSearchText.Len()
	if pending >= maxDeferredSearchTextBytes || len(delta) > maxDeferredSearchTextBytes-pending {
		return fmt.Errorf("penimbal teks tertangguh WebSearch melebihi %d MiB", maxDeferredSearchTextBytes>>20)
	}
	c.pendingSearchText.WriteString(delta)
	return nil
}

func (c *streamConverter) setResponse(value responseEnvelope) {
	if value.ID != "" {
		c.id = value.ID
	}
	if value.Model != "" {
		c.model = value.Model
	}
	if value.CreatedAt != 0 {
		c.created = value.CreatedAt
	}
	c.usage = mergeResponseUsage(c.usage, value.Usage)
}

func mergeResponseUsage(current, update responseUsage) responseUsage {
	if update.InputTokens != 0 {
		current.InputTokens = update.InputTokens
	}
	if update.OutputTokens != 0 {
		current.OutputTokens = update.OutputTokens
	}
	if update.TotalTokens != 0 {
		current.TotalTokens = update.TotalTokens
	}
	if update.CostInUSDTicks != 0 {
		current.CostInUSDTicks = update.CostInUSDTicks
	}
	if update.NumSourcesUsed != 0 {
		current.NumSourcesUsed = update.NumSourcesUsed
	}
	if update.NumServerSideToolsUsed != 0 {
		current.NumServerSideToolsUsed = update.NumServerSideToolsUsed
	}
	if update.InputTokensDetails.CachedTokens != 0 {
		current.InputTokensDetails.CachedTokens = update.InputTokensDetails.CachedTokens
	}
	if update.OutputTokensDetails.ReasoningTokens != 0 {
		current.OutputTokensDetails.ReasoningTokens = update.OutputTokensDetails.ReasoningTokens
	}
	if update.ContextDetails.InputTokens != 0 {
		current.ContextDetails.InputTokens = update.ContextDetails.InputTokens
	}
	if update.ContextDetails.OutputTokens != 0 {
		current.ContextDetails.OutputTokens = update.ContextDetails.OutputTokens
	}
	return current
}

func (c *streamConverter) start() error {
	if c.started {
		return nil
	}
	c.started = true
	if c.id == "" {
		c.id = "resp_" + fmt.Sprint(time.Now().UnixNano())
	}
	if c.operation == OperationChat {
		return c.startChat()
	}
	return c.startMessages()
}

func (c *streamConverter) textDelta(delta string) error {
	// Loop protection is applied at the raw-event layer (handle → trackEvent),
	// which also covers deltas dropped by the stop filter or deferred search;
	// no re-counting here.
	if c.operation == OperationChat {
		return c.textDeltaChat(delta)
	}
	return c.textDeltaMessages(delta)
}

func (c *streamConverter) toolStart(item responseItem, outputIndex int) error {
	if c.operation == OperationMessages {
		return c.toolStartMessages(item)
	}
	return c.toolStartChat(item, outputIndex)
}

func (c *streamConverter) toolDelta(itemID, delta string) error {
	if c.operation == OperationChat {
		return c.toolDeltaChat(itemID, delta)
	}
	return c.toolDeltaMessages(itemID, delta)
}

func (c *streamConverter) toolArgumentsDone(itemID, arguments string) error {
	if c.operation == OperationChat {
		return c.toolArgumentsDoneChat(itemID, arguments)
	}
	return c.toolArgumentsDoneMessages(itemID, arguments)
}

func (c *streamConverter) done(status string) error {
	if c.finished {
		return nil
	}
	if err := c.start(); err != nil {
		return err
	}
	if err := c.flushPendingReasoning(); err != nil {
		return err
	}
	if c.operation == OperationChat {
		return c.doneChat(status)
	}
	return c.doneMessages(status)
}

func (c *streamConverter) streamError(data []byte) error {
	if err := c.flushPendingReasoning(); err != nil {
		return err
	}
	c.finished = true
	if c.operation == OperationMessages {
		return c.streamErrorMessages(data)
	}
	return c.streamErrorChat(data)
}

func (c *streamConverter) finish() error {
	if c.finished {
		return nil
	}
	return c.done("")
}

func streamErrorValue(data []byte) any {
	var root map[string]any
	if json.Unmarshal(data, &root) != nil {
		return strings.TrimSpace(string(data))
	}
	if response, ok := root["response"].(map[string]any); ok {
		if value, exists := response["error"]; exists && value != nil {
			return value
		}
	}
	if value, exists := root["error"]; exists && value != nil {
		return value
	}
	if message, ok := root["message"].(string); ok {
		return message
	}
	return strings.TrimSpace(string(data))
}

func (c *streamConverter) writeData(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.writer, "data: %s\n\n", data)
	return err
}

func (c *streamConverter) writeEvent(event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.writer, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func consumeSSE(source io.Reader, handle func(string, []byte) error) error {
	reader := bufio.NewReaderSize(source, 64<<10)
	var event string
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "":
				if data.Len() > 0 {
					if handleErr := handle(event, []byte(data.String())); handleErr != nil {
						return handleErr
					}
				}
				event = ""
				data.Reset()
			}
		}
		if err != nil {
			if err == io.EOF {
				if data.Len() > 0 {
					return handle(event, []byte(data.String()))
				}
				return nil
			}
			return err
		}
	}
}

func parseSSEEvent(event string, data []byte) (string, map[string]json.RawMessage, bool) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return "", nil, false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return "", nil, false
	}
	typeName := event
	if typeName == "" {
		_ = json.Unmarshal(root["type"], &typeName)
	}
	return typeName, root, true
}

func (t *streamRepeatTracker) trackEvent(typeName string, root map[string]json.RawMessage) error {
	var delta string
	switch typeName {
	case "response.output_text.delta":
		_ = json.Unmarshal(root["delta"], &delta)
		return t.trackContent(delta)
	case "response.reasoning_summary_text.delta":
		_ = json.Unmarshal(root["delta"], &delta)
		return t.trackReasoning(delta, "model reasoning summary loop detected")
	case "response.reasoning_text.delta":
		_ = json.Unmarshal(root["delta"], &delta)
		return t.trackReasoning(delta, "model reasoning loop detected")
	default:
		return nil
	}
}

func (t *streamRepeatTracker) trackContent(delta string) error {
	if delta == "" {
		return nil
	}
	// Patch #2 (original): exact-match doom-loop detection.
	if delta != t.lastContentDelta {
		t.lastContentDelta = delta
		t.contentRepeatCount = 1
	} else {
		t.contentRepeatCount++
		if t.contentRepeatCount > contentDoomLoopThreshold {
			return fmt.Errorf("model output loop detected (repeated content delta %d times)", t.contentRepeatCount)
		}
	}

	// Patch #28: fuzzy doom-loop — normalize whitespace dan compare.
	// Tangkap repetition yang berbeza hanya pada trailing spaces / \r\n.
	normalized := normalizeContentDelta(delta)
	if len(normalized) >= 3 { // Ignore very short deltas (single chars, separators)
		if normalized != t.lastContentNormalized {
			t.lastContentNormalized = normalized
			t.normalizedRepeatCount = 1
		} else {
			t.normalizedRepeatCount++
			// Threshold lebih rendah untuk fuzzy match — jika 32 delta
			// normalized sama berturut, itu 100% loop. (128 untuk exact
			// match kerana ada false positive sah; fuzzy lebih yakin.)
			if t.normalizedRepeatCount > 32 {
				return fmt.Errorf("model output loop detected (repeated content %d times, fuzzy match)", t.normalizedRepeatCount)
			}
		}
	}

	// Patch #28: rolling window repetition — jika content yang dihantar
	// adalah baris penuh (cth. "Aku buat semula dengan next/image yang
	// lebih bersih."), accumulate dan check jika baris terakhir berulang.
	// Ini tangkap multi-delta repetition yang exact match tak nampak.
	t.contentWindow.WriteString(delta)
	t.contentWindowLen += len(delta)
	// Cap window kepada 4KB — cukup untuk detect 4+ repetitions dalam
	// window, tanpa membazirkan memory untuk stream yang sangat panjang.
	if t.contentWindowLen > 4096 {
		t.contentWindow = strings.Builder{}
		// Keep last 400 chars sahaja untuk rolling comparison.
		window := t.contentWindow.String()
		_ = window // window kosong sekarang
		t.contentWindow.WriteString(delta[len(delta)-min(400, len(delta)):])
		t.contentWindowLen = len(delta)
	}
	// Only check when we have enough content to see a pattern.
	if t.contentWindowLen > 200 {
		if err := t.checkWindowRepetition(); err != nil {
			return err
		}
	}
	return nil
}

// normalizeContentDelta strip trailing whitespace dan collapse internal
// whitespace untuk fuzzy comparison. "hello \n" dan "hello\n" dianggap
// sama. Ini mencegah model bypass doom-loop dengan variasi whitespace.
// Juga trim leading whitespace.
func normalizeContentDelta(delta string) string {
	// Trim leading dan trailing whitespace
	s := strings.TrimSpace(delta)
	// Collapse multiple whitespace menjadi single space
	var sb strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !prevSpace {
				sb.WriteByte(' ')
				prevSpace = true
			}
		} else {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return sb.String()
}

// checkWindowRepetition check jika content window mengandungi baris yang
// berulang. Ambil 80 char paling akhir sebagai "signature", cari dalam
// 400 char sebelumnya. Jika jumpa 4+ kali → doom loop.
//
// Patch #28 v2: guard terhadap false positive pada uniform content
// (cth. "----", "aaaa", base64 padding). Jika signature mengandungi
// ≤ 3 unique chars, skip — itu bukan "baris yang berulang" tetapi
// "karakter yang sama berulang", yang sudah ditangkap oleh exact match
// atau fuzzy match dengan threshold yang lebih tinggi.
func (t *streamRepeatTracker) checkWindowRepetition() error {
	window := t.contentWindow.String()
	if len(window) < 200 {
		return nil
	}
	// Ambil 80 char terakhir sebagai "signature"
	sigLen := 80
	if len(window) < sigLen {
		sigLen = len(window)
	}
	signature := normalizeContentDelta(window[len(window)-sigLen:])
	if len(signature) < 10 {
		return nil // too short to be meaningful
	}
	// Patch #28 v2: skip jika signature mengandungi terlalu sedikit unique
	// chars — itu uniform content (separator, padding, table border), bukan
	// baris teks berulang. Exact/fuzzy match dengan threshold tinggi sudah
	// cover repetition uniform.
	uniqueChars := countUniqueChars(signature)
	if uniqueChars <= 4 {
		return nil
	}
	// Count occurrences dalam 400 char sebelumnya
	searchStart := 0
	if len(window) > 400 {
		searchStart = len(window) - 400
	}
	searchArea := normalizeContentDelta(window[searchStart:])
	count := strings.Count(searchArea, signature)
	// Jika signature muncul 4+ kali dalam 400 char → doom loop
	if count >= 4 {
		return fmt.Errorf("model output loop detected (content signature repeated %d times in rolling window)", count)
	}
	return nil
}

// countUniqueChars mengira bilangan karakter unik dalam string.
// "hello" = 4 (h,e,l,o). "---" = 1. "aaaa" = 1.
func countUniqueChars(s string) int {
	seen := make(map[rune]struct{})
	for _, r := range s {
		seen[r] = struct{}{}
	}
	return len(seen)
}

func (t *streamRepeatTracker) trackReasoning(delta, message string) error {
	if delta == "" {
		return nil
	}
	if delta != t.lastReasonDelta {
		t.lastReasonDelta = delta
		t.reasonRepeatCount = 1
		return nil
	}
	t.reasonRepeatCount++
	if t.reasonRepeatCount > reasoningDoomLoopThreshold {
		return fmt.Errorf("%s (repeated delta %d times)", message, t.reasonRepeatCount)
	}
	return nil
}

// guardResponseStream 保持 native Responses SSE 的原始字节不变，同时在读取时
// 解析事件并在检测到循环时关闭上游。 Responses 是 pass-through — tool call
// arguments tetap dalam format Responses (function_call output items), jadi
// tooltimeguard enforcement (timeout raise, no-op edit, dev server rewrite)
// tidak boleh dilakukan pada response stream ini kerana ia TIDAK di-convert.
// Arahan kepada model (schema hints dalam request body) tetap berlaku kerana
// ApplySchemaHints beroperasi pada request, bukan response.
//
// Patch #27 (K16): activity guard untuk Responses path — track tool calls
// dalam response stream supaya reminder tetap di-inject pada hujung.
func guardResponseStream(source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	stream := newStreamPipeReadCloser(reader, source)
	go func() {
		defer stream.closeSource()
		tracker := streamRepeatTracker{}
		activityGuard := tooltimeguard.NewStreamActivityGuard()
		noOpState := make([]int, 1)
		// K16 v2 FIX: jangan guna TeeReader — ia forward bytes TERUS
		// ke writer sebelum intercept. Sebaliknya, guna consumeSSE pada
		// source sahaja dan tulis ke writer MELALUI callback supaya
		// kita boleh intercept dan re-encode sebelum data sampai ke
		// client. Tiada duplicate.
		err := consumeSSE(source, func(event string, data []byte) error {
			typeName, root, ok := parseSSEEvent(event, data)
			if !ok {
				_, writeErr := writer.Write(data)
				return writeErr
			}
			// Patch #16/K16: intercept tool call arguments dalam Responses
			// format. Jika diubahsuai, tulis versi reencoded; jika tidak,
			// tulis data asal.
			switch typeName {
			case "response.function_call_arguments.done":
				var args string
				_ = json.Unmarshal(root["arguments"], &args)
				if args != "" {
					if corrected, changed := tooltimeguard.EnlargeToolTimeout("bash", args); changed {
						root["arguments"], _ = json.Marshal(corrected)
						rebuilt := reencodeSSEData(event, root)
						if _, werr := writer.Write(rebuilt); werr != nil {
							return werr
						}
						activityGuard.NoteToolCall("bash", corrected)
						return tracker.trackEvent(typeName, root)
					}
					if corrected, changed := tooltimeguard.InterceptNoOpEditStateful("edit", args, noOpState); changed {
						root["arguments"], _ = json.Marshal(corrected)
						rebuilt := reencodeSSEData(event, root)
						if _, werr := writer.Write(rebuilt); werr != nil {
							return werr
						}
						activityGuard.NoteToolCall("bash", corrected)
						return tracker.trackEvent(typeName, root)
					}
					activityGuard.NoteToolCall("bash", args)
				}
			case "response.output_item.done":
				var item map[string]json.RawMessage
				if err := json.Unmarshal(root["item"], &item); err != nil {
					break
				}
				var itemType string
				_ = json.Unmarshal(item["type"], &itemType)
				if itemType != "function_call" {
					break
				}
				var args string
				_ = json.Unmarshal(item["arguments"], &args)
				if args == "" {
					break
				}
				if corrected, changed := tooltimeguard.EnlargeToolTimeout("bash", args); changed {
					item["arguments"], _ = json.Marshal(corrected)
					root["item"], _ = json.Marshal(item)
					rebuilt := reencodeSSEData(event, root)
					if _, werr := writer.Write(rebuilt); werr != nil {
						return werr
					}
					activityGuard.NoteToolCall("bash", corrected)
					return tracker.trackEvent(typeName, root)
				}
				if corrected, changed := tooltimeguard.InterceptNoOpEditStateful("edit", args, noOpState); changed {
					item["arguments"], _ = json.Marshal(corrected)
					root["item"], _ = json.Marshal(item)
					rebuilt := reencodeSSEData(event, root)
					if _, werr := writer.Write(rebuilt); werr != nil {
						return werr
					}
					activityGuard.NoteToolCall("bash", corrected)
					return tracker.trackEvent(typeName, root)
				}
				activityGuard.NoteToolCall("bash", args)
			}
			// Default: tulis data asal tanpa ubah
			_, werr := writer.Write(reencodeSSEData(event, root))
			return errors.Join(werr, tracker.trackEvent(typeName, root))
		})
		if activityGuard.ShouldRemindDevServer() {
			reminderJSON, _ := json.Marshal(map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"output": []any{map[string]any{
						"type": "reasoning",
						"summary": []any{map[string]any{
							"type": "summary_text",
							"text": tooltimeguard.DevServerReminderText,
						}},
					}},
				},
			})
			_, _ = writer.Write([]byte("event: response.completed\ndata: " + string(reminderJSON) + "\n\n"))
		}
		_ = writer.CloseWithError(err)
	}()
	return stream
}

func reencodeSSEData(event string, root map[string]json.RawMessage) []byte {
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil
	}
	var sb strings.Builder
	if event != "" {
		sb.WriteString("event: ")
		sb.WriteString(event)
		sb.WriteString("\n")
	}
	sb.WriteString("data: ")
	sb.Write(encoded)
	sb.WriteString("\n\n")
	return []byte(sb.String())
}
