package gateway

import (
	"fmt"
	"strings"
	"testing"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
)

// softIdentity derives an identity through the no-seed fallback path, which is
// what IDE clients that send no session signal actually hit.
func softIdentity(t *testing.T, body string) buildSessionIdentity {
	t.Helper()
	return resolveBuildSessionIdentity(7, accountdomain.ProviderBuild, "grok-4.6", "", "", []byte(body))
}

func chatBody(system, firstUser string) string {
	var builder strings.Builder
	builder.WriteString(`{"model":"grok-4.6","messages":[`)
	if system != "" {
		builder.WriteString(`{"role":"system","content":"` + system + `"},`)
	}
	builder.WriteString(`{"role":"user","content":"` + firstUser + `"}]}`)
	return builder.String()
}

// Without a client session signal the identity falls back to a message-prefix
// hash. It must be marked soft (so Composer can replace it), carry no replay
// key, and stay stable across retries of an identical body.
func TestSoftSessionIdentityIsMarkedSoftAndStable(t *testing.T) {
	first := softIdentity(t, chatBody("rules", "hello there"))
	if first.upstreamID == "" || first.affinityKey == "" {
		t.Fatalf("soft identity was not derived: %#v", first)
	}
	if !first.soft {
		t.Fatalf("identity must be marked soft so Composer can replace it: %#v", first)
	}
	if first.replayKey != "" {
		t.Fatalf("soft sessions must not claim a replay key: %#v", first)
	}
	if repeated := softIdentity(t, chatBody("rules", "hello there")); repeated != first {
		t.Fatalf("identity changed across retries: %#v vs %#v", first, repeated)
	}
}

// The symptom this patch fixes: conversations that opened with similar text
// collided onto one upstream conversation, which leaked context between chats
// (the "repeats the same closing phrase in every new chat" report). Distinct
// opening text must therefore yield distinct identities.
func TestSoftSessionIdentitySeparatesDistinctOpeningText(t *testing.T) {
	base := softIdentity(t, chatBody("rules", "hello there"))
	for name, body := range map[string]string{
		"different first user":   chatBody("rules", "completely different question"),
		"different system":       chatBody("other rules", "hello there"),
		"system removed":         chatBody("", "hello there"),
		"trailing text appended": chatBody("rules", "hello there and more"),
	} {
		got := softIdentity(t, body)
		if got.upstreamID == base.upstreamID || got.affinityKey == base.affinityKey {
			t.Fatalf("%s collided with base: %#v vs %#v", name, got, base)
		}
	}
}

// Soft sessions stay tenant-isolated: the same opening text from a different
// API key, provider or model must never share an upstream conversation.
func TestSoftSessionIdentityStaysTenantIsolated(t *testing.T) {
	body := []byte(chatBody("rules", "hello there"))
	base := resolveBuildSessionIdentity(7, accountdomain.ProviderBuild, "grok-4.6", "", "", body)
	for name, value := range map[string]buildSessionIdentity{
		"client":   resolveBuildSessionIdentity(8, accountdomain.ProviderBuild, "grok-4.6", "", "", body),
		"provider": resolveBuildSessionIdentity(7, accountdomain.ProviderConsole, "grok-4.6", "", "", body),
		"model":    resolveBuildSessionIdentity(7, accountdomain.ProviderBuild, "grok-4.5", "", "", body),
	} {
		if value.upstreamID == base.upstreamID || value.affinityKey == base.affinityKey {
			t.Fatalf("%s was not isolated: %#v vs %#v", name, value, base)
		}
	}
}

// The version tag is baked into every hash source, so bumping it invalidates
// all prior soft sessions. That is the mechanism that gave colliding
// conversations a fresh upstream session, and it must not silently regress.
//
// Verified by rebuilding the documented source string here: if the version stops
// being part of the digest input, or the format changes, this fails.
func TestSoftSessionIdentityVersionIsBakedIntoDigest(t *testing.T) {
	if buildSessionIdentityVersion == "" {
		t.Fatal("version tag must not be empty: hashes would stop being invalidatable")
	}
	const (
		clientKeyID = uint64(7)
		model       = "grok-4.6"
		system      = "rules"
		firstUser   = "hello there"
	)
	got := softIdentity(t, chatBody(system, firstUser))

	withVersion := fmt.Sprintf("grok2api:build-soft-session:%s:%d:%s:%s:%s:%s",
		buildSessionIdentityVersion, clientKeyID, accountdomain.ProviderBuild, model, system, firstUser)
	if got.upstreamID != digestUUID(withVersion) {
		t.Fatalf("soft session digest source changed: %#v", got)
	}

	// Drop the version tag and the digest must move, proving it is load-bearing.
	withoutVersion := fmt.Sprintf("grok2api:build-soft-session::%d:%s:%s:%s:%s",
		clientKeyID, accountdomain.ProviderBuild, model, system, firstUser)
	if got.upstreamID == digestUUID(withoutVersion) {
		t.Fatal("version tag is not part of the digest: bumping it would not invalidate sessions")
	}
}

// An anchorless body cannot produce a meaningful prefix hash, so no identity is
// claimed and the request falls back to normal account selection.
func TestSoftSessionIdentityDeclinesWithoutAnchors(t *testing.T) {
	for name, body := range map[string]string{
		"nil body":         "",
		"no messages":      `{"model":"grok-4.6"}`,
		"empty messages":   `{"model":"grok-4.6","messages":[]}`,
		"system only":      `{"model":"grok-4.6","messages":[{"role":"system","content":"rules"}]}`,
		"blank first user": `{"model":"grok-4.6","messages":[{"role":"user","content":""}]}`,
		"malformed":        `{"messages":`,
	} {
		var raw []byte
		if body != "" {
			raw = []byte(body)
		}
		got := resolveBuildSessionIdentity(7, accountdomain.ProviderBuild, "grok-4.6", "", "", raw)
		if got != (buildSessionIdentity{}) {
			t.Fatalf("%s produced an identity: %#v", name, got)
		}
	}
}

// The first-user anchor is truncated to 200 runes, so two chats that share a
// long opening still collide by design. Pinned so the trade-off is visible:
// affinity and prompt-cache reuse depend on this, but it is the same mechanism
// that caused the original cross-chat leak.
func TestSoftSessionIdentityCollidesOnSharedLongPrefix(t *testing.T) {
	shared := strings.Repeat("a", 220)
	first := softIdentity(t, chatBody("rules", shared+"tail one"))
	second := softIdentity(t, chatBody("rules", shared+"tail two"))
	if first.upstreamID != second.upstreamID {
		t.Fatal("long prefixes now diverge past 200 runes: update this test and the anchor limit note")
	}
}
