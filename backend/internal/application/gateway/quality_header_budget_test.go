package gateway

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	clientkeyapp "github.com/chenyme/grok2api/backend/internal/application/clientkey"
	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	clientkey "github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
)

// slowHeaderBuildAdapter streams a healthy thinking body but delays the
// response header per account. This is the observed benak shape: upstream
// holds headers back until generation completes, while healthy thinking
// streams return headers in seconds.
type slowHeaderBuildAdapter struct {
	mu        sync.Mutex
	attempts  []uint64
	headerFor map[uint64]time.Duration
	bodyFor   map[uint64]string
}

func (a *slowHeaderBuildAdapter) Provider() accountdomain.Provider { return accountdomain.ProviderBuild }
func (a *slowHeaderBuildAdapter) Definition() provider.Definition {
	definition := testConversationDefinition(accountdomain.ProviderBuild)
	definition.Credential.Refresh = false
	return definition
}
func (a *slowHeaderBuildAdapter) ForwardResponse(ctx context.Context, request provider.ResponseResourceRequest) (*provider.Response, error) {
	a.mu.Lock()
	a.attempts = append(a.attempts, request.Credential.ID)
	delay := a.headerFor[request.Credential.ID]
	body := a.bodyFor[request.Credential.ID]
	a.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &provider.Response{
		StatusCode: http.StatusOK, Status: "200 OK",
		Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}, nil
}
func (a *slowHeaderBuildAdapter) Attempts() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]uint64, len(a.attempts))
	copy(out, a.attempts)
	return out
}

// TestQualityHeaderBudgetAbortsSlowHeaderAndRotates: account 0 holds its
// header longer than the budget (the benak signature); the budget aborts it
// at the header stage and the retry delivers account 1's healthy stream —
// without waiting out account 0's full header delay.
func TestQualityHeaderBudgetAbortsSlowHeaderAndRotates(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "header-budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accountRepo := relational.NewAccountRepository(database)
	modelRepo := relational.NewModelRepository(database)
	auditRepo := relational.NewAuditRepository(database)
	responseRepo := relational.NewResponseRepository(database)
	keyRepo := relational.NewClientKeyRepository(database)

	credentials := make([]accountdomain.Credential, 0, 2)
	for index, name := range []string{"header-slow", "header-fast"} {
		credential, _, createErr := accountRepo.UpsertByIdentity(ctx, accountdomain.Credential{
			Provider: accountdomain.ProviderBuild, Name: name, SourceKey: name, EncryptedAccessToken: name,
			EncryptedRefreshToken: "refresh-" + name, ExpiresAt: time.Now().Add(time.Hour),
			Enabled: true, AuthStatus: accountdomain.AuthStatusActive, Priority: 200 - index, MaxConcurrent: 1,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		credentials = append(credentials, credential)
	}
	if err := modelRepo.UpsertDiscovered(ctx, accountdomain.ProviderBuild, []string{"grok-4.6"}); err != nil {
		t.Fatal(err)
	}
	for _, credential := range credentials {
		if err := modelRepo.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-4.6"}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	clientKey, err := keyRepo.Create(ctx, clientkey.Key{
		Name: "header-budget-key", Prefix: "hbud", SecretHash: strings.Repeat("a", 64), EncryptedSecret: "encrypted",
		Enabled: true, RPMLimit: 120, MaxConcurrent: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	thinking := sse(
		`data: {"choices":[{"delta":{"thinking_content":"plan"}}]}`,
		`data: {"choices":[{"delta":{"content":"good answer after header rotate"}}]}`,
		`data: {"usage":{"completion_tokens":80,"completion_tokens_details":{"reasoning_tokens":40}}}`,
		"data: [DONE]",
	)
	adapter := &slowHeaderBuildAdapter{
		headerFor: map[uint64]time.Duration{
			credentials[0].ID: 800 * time.Millisecond, // benak shape: header waits for generation
			credentials[1].ID: 0,
		},
		bodyFor: map[uint64]string{
			credentials[0].ID: thinking,
			credentials[1].ID: thinking,
		},
	}
	registry := provider.NewRegistry(adapter)
	sticky := memory.NewStickyStore()
	accountService := accountapp.NewService(accountRepo, auditRepo, memory.NewDeviceSessionStore(), sticky, registry, testCipher(t), nil)
	selector := NewSelector(accountRepo, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
	service := NewService(modelRepo, auditRepo, accountService, clientkeyapp.NewService(nil, nil, nil, 60, 4, nil), registry, selector, responseRepo, 3)
	service.UpdateQualityRetry(QualityRetryRuntime{
		Enabled: true, MaxAttempts: 3, MinOutputTokens: 32,
		OnExhausted: qualityRetryFailOpen, HoldTimeout: time.Second,
		EarlyHeaderAbort: 150 * time.Millisecond,
	})

	started := time.Now()
	result, err := service.CreateChatCompletion(ctx, Input{
		RequestID: "req-header-budget", ClientKey: clientKey, PublicModel: "grok-4.6", Streaming: true,
		Body: []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"write a game"}],"stream":true}`),
	})
	if err != nil {
		t.Fatalf("budget abort should rotate and deliver, err=%v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", result.StatusCode)
	}
	body, _ := io.ReadAll(result.Body)
	result.Finalize(Usage{Reported: true, OutputTokens: 80, ReasoningTokens: 40}, "chat-ok", "")
	_ = result.Body.Close()
	if !strings.Contains(string(body), "good answer after header rotate") {
		t.Fatalf("client must receive the healthy account body, got %s", body)
	}
	if elapsed := time.Since(started); elapsed >= 800*time.Millisecond {
		t.Fatalf("budget abort must not wait out the full 800ms header delay, elapsed=%s", elapsed)
	}
	attempts := adapter.Attempts()
	if len(attempts) != 2 || attempts[0] != credentials[0].ID || attempts[1] != credentials[1].ID {
		t.Fatalf("expected slow-header abort then rotate, attempts=%v", attempts)
	}
}

// TestQualityHeaderBudgetDisabledDoesNotAbort: budget 0 (instrument-only)
// never aborts — a slow header waits out the full delay and delivers, because
// the default must be behavior-neutral logging only.
func TestQualityHeaderBudgetDisabledDoesNotAbort(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "header-budget-off.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accountRepo := relational.NewAccountRepository(database)
	modelRepo := relational.NewModelRepository(database)
	auditRepo := relational.NewAuditRepository(database)
	responseRepo := relational.NewResponseRepository(database)
	keyRepo := relational.NewClientKeyRepository(database)

	credential, _, err := accountRepo.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "header-off", SourceKey: "header-off", EncryptedAccessToken: "header-off",
		EncryptedRefreshToken: "refresh-header-off", ExpiresAt: time.Now().Add(time.Hour),
		Enabled: true, AuthStatus: accountdomain.AuthStatusActive, Priority: 200, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := modelRepo.UpsertDiscovered(ctx, accountdomain.ProviderBuild, []string{"grok-4.6"}); err != nil {
		t.Fatal(err)
	}
	if err := modelRepo.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-4.6"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	clientKey, err := keyRepo.Create(ctx, clientkey.Key{
		Name: "header-off-key", Prefix: "hbof", SecretHash: strings.Repeat("b", 64), EncryptedSecret: "encrypted",
		Enabled: true, RPMLimit: 120, MaxConcurrent: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	thinking := sse(
		`data: {"choices":[{"delta":{"thinking_content":"plan"}}]}`,
		`data: {"choices":[{"delta":{"content":"slow but healthy"}}]}`,
		`data: {"usage":{"completion_tokens":80,"completion_tokens_details":{"reasoning_tokens":40}}}`,
		"data: [DONE]",
	)
	adapter := &slowHeaderBuildAdapter{
		headerFor: map[uint64]time.Duration{credential.ID: 400 * time.Millisecond},
		bodyFor:   map[uint64]string{credential.ID: thinking},
	}
	registry := provider.NewRegistry(adapter)
	sticky := memory.NewStickyStore()
	accountService := accountapp.NewService(accountRepo, auditRepo, memory.NewDeviceSessionStore(), sticky, registry, testCipher(t), nil)
	selector := NewSelector(accountRepo, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
	service := NewService(modelRepo, auditRepo, accountService, clientkeyapp.NewService(nil, nil, nil, 60, 4, nil), registry, selector, responseRepo, 3)
	service.UpdateQualityRetry(QualityRetryRuntime{
		Enabled: true, MaxAttempts: 3, MinOutputTokens: 32,
		OnExhausted: qualityRetryFailOpen, HoldTimeout: time.Second,
		EarlyHeaderAbort: 0, // instrument-only: no aborts
	})

	result, err := service.CreateChatCompletion(ctx, Input{
		RequestID: "req-header-off", ClientKey: clientKey, PublicModel: "grok-4.6", Streaming: true,
		Body: []byte(`{"model":"grok-4.6","messages":[{"role":"user","content":"write a game"}],"stream":true}`),
	})
	if err != nil {
		t.Fatalf("budget 0 must deliver after the slow header, err=%v", err)
	}
	body, _ := io.ReadAll(result.Body)
	result.Finalize(Usage{Reported: true, OutputTokens: 80, ReasoningTokens: 40}, "chat-ok", "")
	_ = result.Body.Close()
	if !strings.Contains(string(body), "slow but healthy") {
		t.Fatalf("slow-header stream must still deliver, got %s", body)
	}
	if len(adapter.Attempts()) != 1 {
		t.Fatalf("no rotation expected with budget 0, attempts=%v", adapter.Attempts())
	}
}

// TestQualityHeaderBudgetZeroForNonStreaming: non-streaming requests are
// exempt from the header budget regardless of the configured value — their
// headers legitimately arrive only when generation completes.
func TestQualityHeaderBudgetZeroForNonStreaming(t *testing.T) {
	cfg := QualityRetryRuntime{Enabled: true, EarlyHeaderAbort: 5 * time.Second}
	if budget := qualityHeaderBudget(cfg, true, false); budget != 0 {
		t.Fatalf("non-streaming must be exempt, budget=%s", budget)
	}
	if budget := qualityHeaderBudget(cfg, false, true); budget != 0 {
		t.Fatalf("hold-disabled must be exempt, budget=%s", budget)
	}
	if budget := qualityHeaderBudget(cfg, true, true); budget != 5*time.Second {
		t.Fatalf("armed streaming must carry the budget, budget=%s", budget)
	}
	if cfg := (QualityRetryRuntime{Enabled: true}); qualityHeaderBudget(cfg, true, true) != 0 {
		t.Fatal("budget 0 must stay instrument-only")
	}
}
