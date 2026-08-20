package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BodyLogConfig controls request/response body logging. Disabled by default.
type BodyLogConfig struct {
	Enabled      bool
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// BodyLog captures request and response bodies for debugging when enabled.
// It buffers up to MaxBodyBytes and logs them with the request ID.
func BodyLog(cfg BodyLogConfig) gin.HandlerFunc {
	if !cfg.Enabled || cfg.Logger == nil {
		return func(c *gin.Context) { c.Next() }
	}
	maxBytes := cfg.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	return func(c *gin.Context) {
		requestID, _ := c.Get(RequestIDKey)
		startedAt := time.Now()

		// Capture request body
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxBytes))
			c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))
		}

		// Capture response body
		responseWriter := &bodyLogResponseWriter{ResponseWriter: c.Writer, body: bytes.NewBuffer(nil), limit: maxBytes}
		c.Writer = responseWriter

		c.Next()

		// Log after handler completes
		fields := []any{
			"request_id", requestID,
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		}
		if len(requestBody) > 0 {
			fields = append(fields, "request_body", truncateForLog(requestBody, maxBytes))
		}
		if responseWriter.body.Len() > 0 {
			fields = append(fields, "response_body", truncateForLog(responseWriter.body.Bytes(), maxBytes))
		}
		cfg.Logger.Info("http_body_log", fields...)
	}
}

type bodyLogResponseWriter struct {
	gin.ResponseWriter
	body  *bytes.Buffer
	limit int64
}

func (w *bodyLogResponseWriter) Write(data []byte) (int, error) {
	if int64(w.body.Len())+int64(len(data)) <= w.limit {
		w.body.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func truncateForLog(data []byte, limit int64) string {
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	// Only log valid JSON or short text
	trimmed := bytes.TrimSpace(data)
	if json.Valid(trimmed) {
		return string(trimmed)
	}
	if len(trimmed) <= 1024 {
		return string(trimmed)
	}
	return strings.Repeat("x", 32) + "..." // redact large non-JSON payloads
}
