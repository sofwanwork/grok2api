package inference

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	upstreamws "github.com/bogdanfinn/websocket"
	"github.com/chenyme/grok2api/backend/internal/application/gateway"
	"github.com/gin-gonic/gin"
	clientws "github.com/gorilla/websocket"
)

const voiceWSMessageLimit = 16 << 20

var voiceWSUpgrader = clientws.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	CheckOrigin: func(*http.Request) bool {
		return true
	},
	EnableCompression: true,
}

// voiceWSRateLimiters provides per-client-key token bucket rate limiting for
// WebSocket connections. Each key gets a small bucket to prevent abuse while
// allowing legitimate reconnects.
var voiceWSRateLimiters = struct {
	sync.Mutex
	limiters map[uint64]*voiceWSTokenBucket
}{limiters: make(map[uint64]*voiceWSTokenBucket)}

type voiceWSTokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	mu         sync.Mutex
}

func newVoiceWSTokenBucket(maxTokens, refillRate float64) *voiceWSTokenBucket {
	return &voiceWSTokenBucket{tokens: maxTokens, maxTokens: maxTokens, refillRate: refillRate, lastRefill: time.Now()}
}

func (b *voiceWSTokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = min(b.maxTokens, b.tokens+elapsed*b.refillRate)
	b.lastRefill = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func voiceWSRateLimiter(clientKeyID uint64) *voiceWSTokenBucket {
	voiceWSRateLimiters.Lock()
	defer voiceWSRateLimiters.Unlock()
	if limiter, exists := voiceWSRateLimiters.limiters[clientKeyID]; exists {
		return limiter
	}
	// 10 connections burst, refill 1 per 6 seconds (10 per minute sustained)
	limiter := newVoiceWSTokenBucket(10, 1.0/6.0)
	voiceWSRateLimiters.limiters[clientKeyID] = limiter
	return limiter
}

func (h *Handler) proxyRealtimeWebSocket(c *gin.Context) {
	h.proxyVoiceWebSocket(c, "/realtime")
}

func (h *Handler) proxySTTWebSocket(c *gin.Context) {
	if !clientws.IsWebSocketUpgrade(c.Request) {
		writeOpenAIError(c, http.StatusMethodNotAllowed, "invalid_request", "Antara muka strim STT memerlukan WebSocket Upgrade")
		return
	}
	h.proxyVoiceWebSocket(c, "/stt")
}

func (h *Handler) proxyVoiceWebSocket(c *gin.Context, pathValue string) {
	if !clientws.IsWebSocketUpgrade(c.Request) {
		writeOpenAIError(c, http.StatusBadRequest, "invalid_request", "Permintaan bukan WebSocket Upgrade yang sah")
		return
	}
	clientKey, requestID, ok := requestIdentity(c)
	if !ok {
		return
	}
	// Rate limit WebSocket connections per client key.
	if limiter := voiceWSRateLimiter(clientKey.ID); limiter != nil && !limiter.Allow() {
		writeOpenAIError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Frekuensi sambungan WebSocket terhad")
		return
	}
	model := strings.TrimSpace(c.Query("model"))
	session, err := h.gateway.OpenVoiceWebSocket(c.Request.Context(), gateway.VoiceWebSocketInput{
		RequestID: requestID, ClientKey: clientKey, PublicModel: model, Path: pathValue,
	})
	if err != nil {
		writeGatewayError(c, err)
		return
	}

	clientConn, err := voiceWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		session.Finalize(gateway.VoiceWebSocketOutcome{ErrorCode: "client_upgrade_failed"})
		return
	}
	clientConn.SetReadLimit(voiceWSMessageLimit)
	session.Conn.SetReadLimit(voiceWSMessageLimit)

	var once sync.Once
	var outcomeMu sync.Mutex
	outcome := gateway.VoiceWebSocketOutcome{}
	closeAll := func() {
		once.Do(func() {
			_ = clientConn.Close()
			if session.Conn != nil {
				_ = session.Conn.Close()
			}
			outcomeMu.Lock()
			finalOutcome := outcome
			outcomeMu.Unlock()
			session.Finalize(finalOutcome)
		})
	}
	defer closeAll()

	type pumpResult struct {
		upstreamSide bool
		result       voiceWSPumpResult
	}
	errCh := make(chan pumpResult, 2)
	go func() {
		errCh <- pumpResult{result: proxyVoiceWSPump(func() (int, []byte, error) {
			return clientConn.ReadMessage()
		}, session.Conn.WriteMessage)}
	}()
	go func() {
		errCh <- pumpResult{upstreamSide: true, result: proxyVoiceWSPump(func() (int, []byte, error) {
			messageType, payload, readErr := session.Conn.ReadMessage()
			if readErr == nil && pathValue == "/stt" {
				if duration, ok := streamingSTTDuration(payload); ok {
					outcomeMu.Lock()
					outcome.AudioDurationSeconds = max(outcome.AudioDurationSeconds, duration)
					outcomeMu.Unlock()
				}
			}
			return messageType, payload, readErr
		}, clientConn.WriteMessage)}
	}()
	first := <-errCh
	if !isNormalVoiceWSClose(first.result.err) {
		outcomeMu.Lock()
		if (first.upstreamSide && !first.result.writeFailed) || (!first.upstreamSide && first.result.writeFailed) {
			outcome.ErrorCode = "upstream_stream_interrupted"
			outcome.UpstreamFailed = true
		} else {
			outcome.ErrorCode = "client_stream_interrupted"
		}
		outcomeMu.Unlock()
	}
}

func streamingSTTDuration(payload []byte) (float64, bool) {
	var event struct {
		Type     string  `json:"type"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || strings.TrimSpace(event.Type) != "transcript.done" {
		return 0, false
	}
	if event.Duration <= 0 || math.IsNaN(event.Duration) || math.IsInf(event.Duration, 0) {
		return 0, false
	}
	return event.Duration, true
}

type voiceWSPumpResult struct {
	err         error
	writeFailed bool
}

func proxyVoiceWSPump(read func() (int, []byte, error), write func(int, []byte) error) voiceWSPumpResult {
	for {
		messageType, payload, err := read()
		if err != nil {
			return voiceWSPumpResult{err: err}
		}
		if err := write(messageType, payload); err != nil {
			return voiceWSPumpResult{err: err, writeFailed: true}
		}
	}
}

func isNormalVoiceWSClose(err error) bool {
	if err == nil || err == io.EOF {
		return true
	}
	return clientws.IsCloseError(err, clientws.CloseNormalClosure, clientws.CloseGoingAway) ||
		upstreamws.IsCloseError(err, upstreamws.CloseNormalClosure, upstreamws.CloseGoingAway)
}
