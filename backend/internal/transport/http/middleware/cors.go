package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig controls which origins may call the gateway from a browser.
// Empty AllowedOrigins keeps the API private (no CORS headers emitted).
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

// DefaultCORSConfig returns the permissive default for the public inference
// surface. The gateway is OpenAI-compatible and clients often run in browsers;
// the default allows any origin with standard headers and no credentials.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type", "X-API-Key", "X-Requested-With", "anthropic-version", "x-grok-turn-idx"},
		ExposedHeaders: []string{"Content-Type", "X-Request-Id", "X-Grok2API-Compatibility-Warnings", "X-Grok2API-Transfer-Error", "Retry-After"},
		MaxAgeSeconds:  86400,
	}
}

// CORS adds Cross-Origin Resource Sharing headers for the public API.
// The preflight handler answers OPTIONS directly; actual requests get
// Access-Control-Allow-* headers so browser-based agents can call /v1/*.
func CORS(cfg CORSConfig) gin.HandlerFunc {
	allowAll := len(cfg.AllowedOrigins) == 0
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		allowed[strings.ToLower(strings.TrimSpace(origin))] = struct{}{}
	}
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	exposed := strings.Join(cfg.ExposedHeaders, ", ")

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}

		allowOrigin := ""
		if allowAll {
			allowOrigin = "*"
		} else if _, ok := allowed[strings.ToLower(origin)]; ok {
			allowOrigin = origin
		}

		if allowOrigin == "" {
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", allowOrigin)
		if allowOrigin != "*" {
			c.Header("Vary", "Origin")
		}
		if cfg.AllowCredentials && allowOrigin != "*" {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposed != "" {
			c.Header("Access-Control-Expose-Headers", exposed)
		}

		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", methods)
			c.Header("Access-Control-Allow-Headers", headers)
			if cfg.MaxAgeSeconds > 0 {
				c.Header("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAgeSeconds))
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
