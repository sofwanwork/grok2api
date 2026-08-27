package account

import "time"

// BuildBillingQuotaWindow membina QuotaWindow sintetik daripada Billing
// snapshot untuk provider Grok Build (patch #25).
//
// Latar: Build menggunakan QuotaBilling (account_billing_snapshots), bukan
// account_quota_windows — jadi selector tidak pernah mempunyai QuotaWindow
// untuk Build, dan comparator quota (quotaKnown/quotaAvailable) tidak aktif.
// Akaun tanpa QuotaWindow dipilih buta mengikut priority + failureCount,
// menyebabkan beban bertindih pada akaun yang sama sehingga hang quota.
//
// Fungsi ini menukar Billing snapshot sedia ada menjadi QuotaWindow supaya
// comparator quota berfungsi untuk Build tanpa mengubah adapter sync.
//
// PENTING: MonthlyLimit == 0 bermakna "tiada data limit", BUKAN "tiada quota".
// Menghasilkan window dengan Remaining 0 untuk kes itu akan salah tafsir
// akaun Free tanpa data sebagai quota habis — jadi nil dikembalikan supaya
// akaun kekal dalam fallback (priority/failureCount) seperti sebelum patch.
func BuildBillingQuotaWindow(credential Credential, billing Billing) *QuotaWindow {
	if billing.MonthlyLimit <= 0 {
		return nil
	}
	remaining := billing.Remaining()
	usagePercent := billing.CreditUsagePercent
	if usagePercent <= 0 && billing.MonthlyLimit > 0 && billing.Used > 0 {
		usagePercent = billing.Used / billing.MonthlyLimit * 100
	}
	return &QuotaWindow{
		AccountID:    credential.ID,
		Mode:         "build_weekly",
		Remaining:    int(remaining),
		Total:        int(billing.MonthlyLimit),
		UsagePercent: usagePercent,
		ResetAt:      parseBillingPeriodEnd(billing),
		SyncedAt:     &billing.SyncedAt,
		Source:       QuotaSourceUpstream,
		UpdatedAt:    billing.SyncedAt,
	}
}

// parseBillingPeriodEnd mengeluarkan masa tamat window daripada medan
// usage_period_end Billing (ISO string). Nil jika tiada.
func parseBillingPeriodEnd(billing Billing) *time.Time {
	if billing.UsagePeriodEnd == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, billing.UsagePeriodEnd); err == nil {
		return &t
	}
	return nil
}
