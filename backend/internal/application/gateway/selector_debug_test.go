package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
	account "github.com/chenyme/grok2api/backend/internal/domain/account"
	"path/filepath"
)

func TestDebugFreeBuildQuota(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "debug-free.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	freeAccount, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "free", SourceKey: "free", EncryptedAccessToken: "encrypted",
		Enabled: true, AuthStatus: account.AuthStatusActive, Priority: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	superAccount, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "super", SourceKey: "super", EncryptedAccessToken: "encrypted",
		Enabled: true, AuthStatus: account.AuthStatusActive, Priority: 100, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := accounts.SaveBilling(ctx, account.Billing{AccountID: freeAccount.ID, PlanName: "free", SyncedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveBilling(ctx, account.Billing{AccountID: superAccount.ID, MonthlyLimit: 140, SyncedAt: now}); err != nil {
		t.Fatal(err)
	}

	candidates, err := accounts.ListRoutingCandidates(ctx, account.ProviderBuild, 0, "grok-4.5", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		t.Logf("candidate %d: Billing=%+v QuotaWindow=%+v IsKnownFreeBuild=%v",
			c.Credential.ID, c.Billing, c.QuotaWindow, c.IsKnownFreeBuild())
	}

	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), memory.NewStickyStore(), nil, time.Hour, time.Second, time.Minute)
	selector.UpdatePreferFreeBuild(true)
	lease, err := selector.Acquire(ctx, account.ProviderBuild, 0, "grok-4.5", "", "new-session", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("selected: %d (want free=%d)", lease.Credential.ID, freeAccount.ID)
	lease.Release()
}
