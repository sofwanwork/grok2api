package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	inferencedomain "github.com/chenyme/grok2api/backend/internal/domain/inference"
	modeldomain "github.com/chenyme/grok2api/backend/internal/domain/model"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	neterrorpkg "github.com/chenyme/grok2api/backend/internal/pkg/neterror"
)

const (
	ErrorQualityDegraded             = "quality_degraded"
	ErrorToolCallDegraded            = "tool_call_degraded"
	ErrorSilentThinking              = "silent_thinking"
	qualityRetryFailOpen             = "fail_open"
	qualityRetryFailClosed           = "fail_closed"
	defaultQualityMaxAttempts        = 6
	defaultQualityHoldTimeout        = 30 * time.Second
	defaultQualityMinOutput          = int64(8)
	defaultMissingThinkingCooldown   = 12 * time.Hour
	lastErrorMissingThinking         = accountdomain.LastErrorMissingThinking
	lastErrorMissingThinkingDisabled = accountdomain.LastErrorMissingThinkingDisabled
	// An empty stream that idles while held is treated as an account-quality
	// failure: the request can still rotate before any bytes reach the client.
	qualityIdleAccountCooldown = 15 * time.Minute
	// Tool-call degradation was measured at roughly a 50% rate, so three
	// attempts recover about 87% of requests. Higher values mostly multiply
	// token spend for diminishing returns.
	defaultToolDegradationMaxAttempts = 3
	// defaultSilentThinkingMaxAttempts bounds silent-thinking retries. The
	// behaviour is stochastic; two attempts (original + one rotate) already
	// recover the common case without multiplying token spend on a prompt
	// that just burned a full tool-call chain.
	defaultSilentThinkingMaxAttempts = 2
	// silentThinkingMinReasoningTokens is the floor of reasoning evidence
	// before a near-empty answer is classified as silent thinking. Streams
	// that barely thought are just short replies and must stay delivered.
	silentThinkingMinReasoningTokens = int64(64)
)

var (
	errQualityDegraded    = errors.New("Respons upstream tiada penaakulan")
	errQualityEmptyStream = errors.New("Respons berstrim upstream kosong")
	// errQualityHeaderBudget marks a response-header budget early abort
	// (patch #20). It deliberately does NOT wrap context.Canceled: only the
	// per-attempt child context is cancelled — the parent request is still
	// alive, and misclassifying it as a client cancellation would wrongly
	// terminate the whole retry loop.
	errQualityHeaderBudget = errors.New("Respons upstream terlalu lambat pada peringkat header")
	// errToolCallDegraded drives a retry only. It is never surfaced to the
	// client, because an exhausted degradation budget delivers the body.
	errToolCallDegraded = errors.New("Upstream menghuraikan panggilan alat sebagai teks")
	// errSilentThinking also drives a retry only; an exhausted budget
	// delivers the near-empty body so the answer is not lost entirely.
	errSilentThinking = errors.New("Upstream berfikir tetapi tiada jawapan")
)

// QualityRetryRuntime is the isolated request-path withhold/retry policy.
// Zero Enabled leaves production behavior unchanged.
type QualityRetryRuntime struct {
	Enabled         bool
	MaxAttempts     int
	HoldTimeout     time.Duration
	MinOutputTokens int64
	OnExhausted     string
	AccountCooldown time.Duration
	// IdleAccountCooldown is applied to truly empty upstream streams
	// (idle timeout / empty peek). Missing-thinking still uses AccountCooldown.
	IdleAccountCooldown time.Duration
	// ToolDegradationEnabled retries streams where the model narrates a tool
	// call in prose instead of emitting one. Independent of the missing-thinking
	// policy: degradation is stochastic upstream behaviour, not an account
	// fault, so it never cools or disables an account.
	ToolDegradationEnabled bool
	// ToolDegradationMaxAttempts bounds those retries. Measured degradation is
	// roughly one in two requests, so a small number already recovers most of
	// them without multiplying token spend.
	ToolDegradationMaxAttempts int
	// SilentThinkingEnabled retries completed streams that produced real
	// reasoning but almost no visible answer (observed on grok-4.6 xhigh
	// after long tool chains). Independent of missing-thinking: the model
	// thought, it just never spoke. Stochastic upstream behaviour, so the
	// account is never cooled or disabled.
	SilentThinkingEnabled bool
	// SilentThinkingMaxAttempts bounds those retries under its own budget.
	SilentThinkingMaxAttempts int
	// MissingThinkingDisabled turns off the withhold verdict when the peek runs
	// only for tool degradation. It is an opt-out (pointer) so existing callers
	// that construct the struct literally keep the original missing-thinking
	// behaviour; the production loop sets it only for degradation-only holds.
	MissingThinkingDisabled *bool
	// DeclaredClientTools is the per-request set of client-executed tool names.
	// Empty disables degradation detection.
	DeclaredClientTools []string
	// HoldKeepalive is the interval between SSE keepalive comments injected
	// while a quality hold is buffering the stream. Long silent thinking phases
	// (12–135s on grok-4.6) otherwise look like a dead connection to clients
	// with short idle timeouts (OpenCode retries and the user sees duplicate
	// answers). Zero disables keepalives.
	HoldKeepalive time.Duration
	// EarlyHeaderAbort (patch #20) bounds the wait for upstream response
	// HEADERS on each streaming attempt when the quality hold is armed.
	// Healthy thinking streams return headers in seconds (header wait is
	// independent of generation length); degraded (benak) paths hold headers
	// back until generation completes, which can take tens of seconds.
	// A header still missing after this budget aborts the attempt early and
	// rotates, moving the benak verdict from first-byte to header stage.
	// INSTRUMENT-FIRST: the default (0) only logs header arrival timings so we
	// can validate the signal on our own pool before arming a real budget.
	// Non-streaming requests are exempt (their headers only arrive when
	// generation completes, by protocol design).
	EarlyHeaderAbort time.Duration
	// DegradeCircuitThreshold (patch #23) is the circuit-breaker for a
	// withhold storm: after this many consecutive QualityWithhold verdicts
	// in the same request (default 2), the next withhold is delivered
	// fail-open (DeliverLast) instead of burning another account and
	// returning 503. In a design-refine phase where the client retries the
	// same heavy prompt, 4+ consecutive degraded retries across accounts
	// mean a 503 loop with no answer — accepting the still-readable benak
	// body beats a dead session. 0 disables the circuit (full
	// withhold-to-the-end behavior). Account penalties (cooldown) are
	// NOT waived — only the client-visible 503 loop is broken.
	DegradeCircuitThreshold int
}

// qualityHeaderBudget returns the active response-header budget for this
// attempt (patch #20). Zero means "log-only instrumentation": header arrival
// times are recorded on every attempt, but nothing is aborted. The budget
// applies to streaming attempts while the quality hold is armed; non-streaming
// requests always return 0 because their headers legitimately arrive only
// when generation completes.
func qualityHeaderBudget(cfg QualityRetryRuntime, holdEnabled, streaming bool) time.Duration {
	if !holdEnabled || !streaming || cfg.EarlyHeaderAbort <= 0 {
		return 0
	}
	return cfg.EarlyHeaderAbort
}

// QualityStreamSignals is the hold classifier input. Tests drive this
// directly and via ObserveQualityChunk on SSE fixtures.
type QualityStreamSignals struct {
	HasThinking bool
	// ReasoningStarted is an empty reasoning item or the Chat SSE stub
	// `: grok2api-reasoning-start`. That is not proof of thinking: 降智
	// still emits the stub, then dumps visible tokens with usage 0.
	ReasoningStarted bool
	VisibleTokens    int64
	ReasoningTokens  int64
	OutputTokens     int64
	Terminal         bool
	HoldExpired      bool
	// VisibleRunes is the raw visible rune count before the /4 token
	// estimate. Patch #13 uses it to know when the tool-narration window has
	// been fully observed, so a post-deadline release cannot outrun the
	// degradation detector.
	VisibleRunes int64
	// SawToolCall reports a structured tool/function call in the stream.
	SawToolCall bool
	// VisibleText is the leading visible text, used only to detect tool-call
	// degradation (prose narration instead of a structured call).
	VisibleText string
}

// QualityVerdict is the hold decision for one upstream stream.
type QualityVerdict string

const (
	QualityWait     QualityVerdict = "wait"
	QualityDeliver  QualityVerdict = "deliver"
	QualityWithhold QualityVerdict = "withhold"
	// QualityToolDegraded is a prose-narrated tool call. Kept separate from
	// QualityWithhold so it never reaches applyMissingThinkingPenalty.
	QualityToolDegraded QualityVerdict = "tool_degraded"
	// QualitySilentThinking marks a completed stream that thought (real
	// reasoning evidence) but produced almost no visible answer. Observed on
	// grok-4.6 xhigh after a long tool-call chain: reasoning_tokens > 0 yet
	// the final content delta is a single token, so the agent loop exits with
	// an empty-looking reply. Like tool degradation it is stochastic upstream
	// behaviour, not an account fault: retry, never penalise.
	QualitySilentThinking QualityVerdict = "silent_thinking"
)

// QualityRetryAction is what the attempt loop does with a withhold verdict.
type QualityRetryAction string

const (
	QualityActionDeliver     QualityRetryAction = "deliver"
	QualityActionDeliverLast QualityRetryAction = "deliver_last"
	QualityActionRetry       QualityRetryAction = "retry"
	QualityActionReject      QualityRetryAction = "reject"
)

func normalizeQualityRetry(cfg QualityRetryRuntime) QualityRetryRuntime {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultQualityMaxAttempts
	}
	if cfg.HoldTimeout <= 0 {
		cfg.HoldTimeout = defaultQualityHoldTimeout
	}
	if cfg.MinOutputTokens <= 0 {
		cfg.MinOutputTokens = defaultQualityMinOutput
	}
	if cfg.AccountCooldown <= 0 {
		cfg.AccountCooldown = defaultMissingThinkingCooldown
	}
	if cfg.IdleAccountCooldown <= 0 {
		cfg.IdleAccountCooldown = qualityIdleAccountCooldown
	}
	if cfg.ToolDegradationMaxAttempts <= 0 {
		cfg.ToolDegradationMaxAttempts = defaultToolDegradationMaxAttempts
	}
	if cfg.SilentThinkingMaxAttempts <= 0 {
		cfg.SilentThinkingMaxAttempts = defaultSilentThinkingMaxAttempts
	}
	if cfg.DegradeCircuitThreshold < 0 {
		cfg.DegradeCircuitThreshold = 0
	}
	cfg.OnExhausted = normalizeQualityExhaustionPolicy(cfg.OnExhausted)
	if cfg.HoldKeepalive < 0 {
		cfg.HoldKeepalive = 0
	}
	return cfg
}

// degradeCircuitOpen reports whether the withhold-storm circuit-breaker
// (patch #23) should trip. consecutiveWithholds is the count of consecutive
// QualityWithhold verdicts already seen in this request; when it reaches
// the threshold, the NEXT withhold must be delivered fail-open instead of
// burning another account in a 503 loop. Threshold 0 keeps the circuit off.
func degradeCircuitOpen(consecutiveWithholds int, cfg QualityRetryRuntime) bool {
	return cfg.DegradeCircuitThreshold > 0 && consecutiveWithholds >= cfg.DegradeCircuitThreshold
}

// missingThinkingEnabled resolves the opt-out to the effective verdict switch.
func (cfg QualityRetryRuntime) missingThinkingEnabled() bool {
	return cfg.MissingThinkingDisabled == nil || !*cfg.MissingThinkingDisabled
}

func (s *Service) UpdateQualityRetry(cfg QualityRetryRuntime) {
	normalized := normalizeQualityRetry(cfg)
	s.qualityRetry.Store(&normalized)
}

func (s *Service) qualityRetryConfig() QualityRetryRuntime {
	if s == nil {
		return normalizeQualityRetry(QualityRetryRuntime{})
	}
	if value := s.qualityRetry.Load(); value != nil {
		return *value
	}
	return normalizeQualityRetry(QualityRetryRuntime{})
}

// ClassifyQualityHold decides whether a held stream may be forwarded.
// Streamed thinking always delivers: reasoning/summary deltas, or a
// reasoning item with encrypted_content. Usage.reasoning_tokens alone
// does not — degraded upstreams fill that field without ciphertext or
// deltas. A finished sample with enough visible output and no streamed
// thinking is withheld.
// Short replies below minOutput are delivered so "ok"/"yes" is not retried.
// A hold timeout with no visible output is not fail-open: keep waiting for
// more bytes or a stream abort so an empty hang is not flushed as HTTP 200.
//
// An empty reasoning stub is not thinking. Before the hold deadline, wait for
// real evidence or a terminal event. If the deadline expires while the stream
// is still open and already has visible output, the result is inconclusive:
// release it without penalizing the account. A stub-only empty stream keeps
// waiting for idle/terminal handling. This keeps HoldTimeout a real latency
// bound without reopening the empty-stream 200 response path.
func ClassifyQualityHold(sig QualityStreamSignals, minOutput int64) QualityVerdict {
	if minOutput <= 0 {
		minOutput = defaultQualityMinOutput
	}
	if sig.HasThinking {
		return QualityDeliver
	}
	// Prefer observed/derived visible output. Total output includes reasoning
	// tokens, which are deliberately not trusted as quality evidence above. If
	// the stream exposed no visible count at all, retain OutputTokens as a
	// compatibility fallback for terminal usage-only responses.
	output := sig.VisibleTokens
	if output <= 0 {
		output = sig.OutputTokens
	}
	enough := output >= minOutput
	if sig.ReasoningStarted && !sig.Terminal {
		if sig.HoldExpired && output > 0 {
			return QualityDeliver
		}
		return QualityWait
	}
	if sig.Terminal {
		if output <= 0 {
			return QualityWait
		}
		if enough {
			return QualityWithhold
		}
		return QualityDeliver
	}
	if enough {
		return QualityWithhold
	}
	if sig.HoldExpired {
		if output <= 0 {
			return QualityWait
		}
		if enough {
			return QualityWithhold
		}
		return QualityDeliver
	}
	return QualityWait
}

// qualityPeekAbortError prefers the idle-timeout cause over a plain
// context.Canceled so the attempt loop can retry instead of treating the
// abort as a client 499.
func qualityPeekAbortError(ctx context.Context, err error) error {
	if ctx != nil {
		if cause := context.Cause(ctx); neterrorpkg.IsUpstreamStreamIdleTimeout(cause) {
			return cause
		}
	}
	if neterrorpkg.IsUpstreamStreamIdleTimeout(err) {
		return err
	}
	if err != nil {
		return err
	}
	if ctx != nil {
		return ctx.Err()
	}
	return nil
}

// isClientRequestCancel reports a real client disconnect. Upstream idle
// timeouts cancel the same context and must not be classified as 499.
func isClientRequestCancel(ctx context.Context, err error) bool {
	if neterrorpkg.IsUpstreamStreamIdleTimeout(err) {
		return false
	}
	if ctx != nil && neterrorpkg.IsUpstreamStreamIdleTimeout(context.Cause(ctx)) {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled)
}

// DecideQualityRetry caps withhold recovery at maxAttempts (default 6:
// original + five extra accounts). The last withhold
// (attemptIndex == maxAttempts-1) is fail-open unless OnExhausted is fail_closed.
func DecideQualityRetry(verdict QualityVerdict, attemptIndex, maxAttempts int, onExhausted string) QualityRetryAction {
	if verdict != QualityWithhold {
		return QualityActionDeliver
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultQualityMaxAttempts
	}
	if attemptIndex < 0 {
		attemptIndex = 0
	}
	if attemptIndex < maxAttempts-1 {
		return QualityActionRetry
	}
	// attemptIndex == maxAttempts-1 (or past it): do not retry again.
	if normalizeQualityExhaustionPolicy(onExhausted) == qualityRetryFailClosed {
		return QualityActionReject
	}
	return QualityActionDeliverLast
}

// BoundQualityRetry turns a Retry into DeliverLast/Reject when the routing
// loop has no remaining account slot, so the already-held body is not dropped
// on continue-into-exhausted-loop.
func BoundQualityRetry(action QualityRetryAction, hasNextRoutingAttempt bool, onExhausted string) QualityRetryAction {
	if action != QualityActionRetry || hasNextRoutingAttempt {
		return action
	}
	if normalizeQualityExhaustionPolicy(onExhausted) == qualityRetryFailClosed {
		return QualityActionReject
	}
	return QualityActionDeliverLast
}

func normalizeQualityExhaustionPolicy(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), qualityRetryFailOpen) {
		return qualityRetryFailOpen
	}
	return qualityRetryFailClosed
}

// QualityCommit is the single attempt-loop decision for a held stream.
type QualityCommit struct {
	Action   QualityRetryAction
	Audit    bool
	KeepBody bool
}

// CommitQualityHold is the shipped withhold/retry/commit unit. The attempt
// loop must not re-derive this from Decide+Bound+switch.
func CommitQualityHold(verdict QualityVerdict, qualityAttempt, maxAttempts int, hasNextRouting bool, onExhausted string) QualityCommit {
	action := BoundQualityRetry(
		DecideQualityRetry(verdict, qualityAttempt, maxAttempts, onExhausted),
		hasNextRouting,
		onExhausted,
	)
	switch action {
	case QualityActionRetry, QualityActionReject:
		return QualityCommit{Action: action, Audit: true, KeepBody: false}
	case QualityActionDeliverLast:
		return QualityCommit{Action: action, Audit: false, KeepBody: true}
	default:
		return QualityCommit{Action: QualityActionDeliver, Audit: false, KeepBody: true}
	}
}

func shouldHoldQualityStream(input Input, ownership *inferencedomain.ResponseOwnership, route modeldomain.Route, operation audit.Operation, cfg QualityRetryRuntime) bool {
	if !cfg.Enabled || !input.Streaming || input.ForcedEgressNodeID != 0 || ownership != nil || input.skipQualityHold {
		return false
	}
	switch operation {
	case audit.OperationChat, audit.OperationResponses, audit.OperationMessages, "":
	default:
		return false
	}
	// TUI compaction is a normal /v1/responses body (no compaction_trigger).
	// Keep this defensive body check in addition to skipQualityHold so a caller
	// that bypasses CreateResponse cannot withhold a 100s+ summary as missing-thinking.
	if isResponsesCompactionRequest(input.Body) {
		return false
	}
	if route.Provider != accountdomain.ProviderBuild && route.Provider != accountdomain.ProviderConsole {
		return false
	}
	// Client-executed tools are safe to hold: their calls have not reached the
	// client yet, and completed results in the next request are immutable input.
	// Hosted tools are different. Retrying them can repeat an upstream search,
	// sandbox run, image job, or remote MCP call, so retain the old no-replay
	// safety boundary for any request that declares one.
	if qualityRequestHasReplayUnsafeHostedTools(input.Body) {
		return false
	}
	// Aliases are rewritten before this gate, so inspect the effective request
	// body instead of only the reasoning-capable base model. In particular,
	// grok-4.3-none becomes grok-4.3 plus an explicit disabled setting.
	if qualityRequestDisablesReasoning(input.Body) {
		return false
	}
	if modeldomain.SupportsReasoningForProvider(route.Provider, input.PublicModel) {
		return true
	}
	return modeldomain.SupportsReasoningForProvider(route.Provider, route.UpstreamModel)
}

// shouldHoldForToolDegradation gates degradation retries. Unlike the
// missing-thinking gate it does not require a reasoning-capable model, because
// degradation was observed on ordinary tool requests. Hosted tools are still
// excluded: replaying them can repeat an upstream search or sandbox run.
func shouldHoldForToolDegradation(input Input, ownership *inferencedomain.ResponseOwnership, operation audit.Operation, cfg QualityRetryRuntime) bool {
	if !cfg.Enabled || !cfg.ToolDegradationEnabled || !input.Streaming {
		return false
	}
	if input.ForcedEgressNodeID != 0 || ownership != nil || input.skipQualityHold {
		return false
	}
	switch operation {
	case audit.OperationChat, audit.OperationResponses, audit.OperationMessages, "":
	default:
		return false
	}
	if isResponsesCompactionRequest(input.Body) {
		return false
	}
	return !qualityRequestHasReplayUnsafeHostedTools(input.Body)
}

// DecideToolDegradationRetry retries while attempts remain and otherwise
// delivers the last body. It never rejects: the prose answer is still readable,
// so failing closed here would replace a usable response with an error.
func DecideToolDegradationRetry(attemptIndex, maxAttempts int, hasNextRouting bool) QualityRetryAction {
	if maxAttempts <= 0 {
		maxAttempts = defaultToolDegradationMaxAttempts
	}
	if attemptIndex < 0 {
		attemptIndex = 0
	}
	if attemptIndex < maxAttempts-1 && hasNextRouting {
		return QualityActionRetry
	}
	return QualityActionDeliverLast
}

// DecideSilentThinkingRetry mirrors the tool-degradation budget: retry while
// attempts and routing remain, then deliver the last body. It never rejects —
// the near-empty body still carries reasoning evidence the client can show.
func DecideSilentThinkingRetry(attemptIndex, maxAttempts int, hasNextRouting bool) QualityRetryAction {
	if maxAttempts <= 0 {
		maxAttempts = defaultSilentThinkingMaxAttempts
	}
	if attemptIndex < 0 {
		attemptIndex = 0
	}
	if attemptIndex < maxAttempts-1 && hasNextRouting {
		return QualityActionRetry
	}
	return QualityActionDeliverLast
}

// classifySilentThinking reports whether a completed stream "thought but never
// spoke": real reasoning evidence, a terminal event, and visible output below
// the minimum while the model had actual tools to work with. The reasoning
// floor keeps genuinely short replies delivered. Only requests that declared
// client-executed tools qualify — a plain prompt with a one-token answer is a
// legal (if terse) completion.
func classifySilentThinking(sig QualityStreamSignals, minOutput int64, declaredClientTools []string, enabled bool) bool {
	if !enabled || len(declaredClientTools) == 0 || !sig.Terminal {
		return false
	}
	if !sig.HasThinking {
		return false
	}
	if sig.SawToolCall {
		// A structured call is a productive answer; the next round-trip
		// belongs to the client, not to this retry.
		return false
	}
	if sig.ReasoningTokens < silentThinkingMinReasoningTokens {
		return false
	}
	if minOutput <= 0 {
		minOutput = defaultQualityMinOutput
	}
	return sig.VisibleTokens < minOutput
}

func qualityRequestHasReplayUnsafeHostedTools(body []byte) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return false
	}
	if raw, exists := payload["web_search_options"]; exists && raw != nil {
		return true
	}
	if raw, exists := payload["mcp_servers"]; exists && raw != nil {
		servers, ok := raw.([]any)
		if !ok || len(servers) > 0 {
			return true
		}
	}
	if qualityToolListHasReplayUnsafeHostedTool(payload["tools"]) {
		return true
	}
	// Responses Tool Search can load declarations later in the request. Only
	// inspect additional_tools items; arbitrary user/schema objects may also
	// contain a field named "tools" and must not affect the retry policy.
	items, _ := payload["input"].([]any)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || jsonNodeString(item["type"]) != "additional_tools" {
			continue
		}
		if qualityToolListHasReplayUnsafeHostedTool(item["tools"]) {
			return true
		}
	}
	return false
}

func qualityToolListHasReplayUnsafeHostedTool(value any) bool {
	tools, ok := value.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		kind := jsonNodeString(tool["type"])
		switch kind {
		case "", "function", "custom", "local_shell", "apply_patch", "tool_search":
			// These declarations only ask the model to return a call. Execution
			// happens in the client after the held response is committed.
			continue
		case "shell":
			environment, _ := tool["environment"].(map[string]any)
			if jsonNodeString(environment["type"]) != "local" {
				return true
			}
		case "namespace":
			if qualityToolListHasReplayUnsafeHostedTool(tool["tools"]) {
				return true
			}
		default:
			// Default to no replay for every server/native tool, including types
			// added by future protocol versions that this gateway does not know yet.
			return true
		}
	}
	return false
}

func jsonNodeString(value any) string {
	text, _ := value.(string)
	return strings.ToLower(strings.TrimSpace(text))
}

func qualityRequestDisablesReasoning(body []byte) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	if jsonStringEquals(payload["reasoning_effort"], modeldomain.ReasoningEffortNone) {
		return true
	}
	for _, key := range []string{"reasoning", "output_config", "thinking"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(payload[key], &nested) != nil {
			continue
		}
		if jsonStringEquals(nested["effort"], modeldomain.ReasoningEffortNone) || jsonStringEquals(nested["type"], "disabled") {
			return true
		}
		var budget int64
		if raw, ok := nested["budget_tokens"]; ok && json.Unmarshal(raw, &budget) == nil && budget == 0 {
			return true
		}
	}
	return jsonStringEquals(payload["thinking"], "disabled")
}

func jsonStringEquals(raw json.RawMessage, want string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.EqualFold(strings.TrimSpace(value), want)
}

func (s *Service) applyMissingThinkingPenalty(ctx context.Context, requestID string, credential accountdomain.Credential, cooldown time.Duration) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizationTimeout)
	defer cancel()
	action, err := s.selector.markMissingThinking(writeCtx, credential, cooldown)
	if err != nil {
		s.logger.Error("quality_degraded_penalty_failed", "request_id", requestID, "account_id", credential.ID, "action", action, "error", err)
		return
	}
	switch action {
	case missingThinkingPenaltyDisabled:
		s.logger.Info("quality_degraded_disabled", "request_id", requestID, "account_id", credential.ID)
	case missingThinkingPenaltyCooled:
		s.logger.Info("quality_degraded_cooldown", "request_id", requestID, "account_id", credential.ID, "cooldown", cooldown.String())
	}
}

func (s *Service) recordQualityDegraded(ctx context.Context, base audit.Record, credential accountdomain.Credential, usage Usage, startedAt time.Time, trace *infraegress.Trace, provider accountdomain.Provider) {
	record := base
	record.EventID = newAuditEventID()
	accountID := credential.ID
	record.AccountID = &accountID
	record.AccountName = credential.Name
	record.StatusCode = http.StatusOK
	record.ErrorCode = ErrorQualityDegraded
	record.OutputTokens = usage.OutputTokens
	record.ReasoningTokens = usage.ReasoningTokens
	record.TotalTokens = usage.TotalTokens
	record.InputTokens = usage.InputTokens
	if usage.Reported {
		record.UsageSource = audit.UsageSourceUpstream
	}
	record.DurationMS = time.Since(startedAt).Milliseconds()
	record.CreatedAt = time.Now().UTC()
	applyAuditEgress(&record, trace, provider)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizationTimeout)
	defer cancel()
	if err := s.audits.Create(writeCtx, record); err != nil {
		s.logger.Error("quality_degraded_audit_failed", "event_id", record.EventID, "request_id", record.RequestID, "error", err)
	}
}
