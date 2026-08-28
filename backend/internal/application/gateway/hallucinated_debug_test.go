package gateway

import "testing"

func TestDebugHallucinatedLiveFixture(t *testing.T) {
	// Fixture tepat seperti request live yang dihantar
	body := []byte(`{"model":"grok-4.6-low","stream":false,"messages":[{"role":"user","content":"buatkan website"},{"role":"assistant","content":"aku dah edit fail page.tsx dan landing page dah siap"},{"role":"user","content":"ok"}]}`)
	got := HallucinatedEditClaim(body)
	t.Logf("HallucinatedEditClaim(live fixture) = %v", got)
	if !got {
		t.Fatal("live fixture mesti trigger detector")
	}
}
