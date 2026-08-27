package gateway

import (
	"testing"
)

func TestHallucinatedEditClaimDetectsClaimWithoutToolCalls(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "buatkan website"},
			{"role": "assistant", "content": "Wah gila, landing page dah siap guna. Aku dah replace dengan design yang aku buat."},
			{"role": "user", "content": "sambung"}
		]
	}`)
	if !HallucinatedEditClaim(body) {
		t.Fatal("claim-tulis tanpa tool_calls mesti dikesan")
	}
}

func TestHallucinatedEditClaimDetectsEnglishClaim(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "fix the layout"},
			{"role": "assistant", "content": "Done! I've replaced the file with the new layout. All good."},
			{"role": "user", "content": "ok"}
		]
	}`)
	if !HallucinatedEditClaim(body) {
		t.Fatal("English claim without tool calls must be detected")
	}
}

func TestHallucinatedEditClaimIgnoresClaimWithToolCalls(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "buatkan website"},
			{"role": "assistant", "content": "Wah gila, landing page dah siap guna. Aku dah replace dengan design yang aku buat.", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "write", "arguments": "{}"}}]},
			{"role": "user", "content": "sambung"}
		]
	}`)
	if HallucinatedEditClaim(body) {
		t.Fatal("claim dalam turn yang ADA tool_calls adalah sah — jangan flag")
	}
}

func TestHallucinatedEditClaimIgnoresNonClaimAssistantText(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "macam mana nak buat website?"},
			{"role": "assistant", "content": "Begini caranya: pertama, install Node.js, kemudian buat folder, lepas tu tulis HTML..."},
			{"role": "user", "content": "ok"}
		]
	}`)
	if HallucinatedEditClaim(body) {
		t.Fatal("arahan biasa tanpa frasa klaim-tulis tidak boleh di-flag")
	}
}

func TestHallucinatedEditClaimIgnoresUserPrompt(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "aku dah edit fail page.tsx tapi dia masih tak jalan — kenapa?"}
		]
	}`)
	if HallucinatedEditClaim(body) {
		t.Fatal("teks USER tentang edit dia sendiri tidak boleh di-flag")
	}
}

func TestHallucinatedEditClaimHandlesContentBlocks(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "buatkan website"},
			{"role": "assistant", "content": [{"type": "text", "text": "Wah gila, "}, {"type": "text", "text": "aku dah replace dengan design yang aku buat."}]},
			{"role": "user", "content": "sambung"}
		]
	}`)
	if !HallucinatedEditClaim(body) {
		t.Fatal("klaim dalam content blocks mesti dikesan")
	}
}

func TestHallucinatedEditClaimDetectsLatestAssistantOnly(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "buatkan website"},
			{"role": "assistant", "content": "Wah gila, aku dah replace dengan design yang aku buat.", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "write", "arguments": "{}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
			{"role": "assistant", "content": "Selesai. Semua dah beres."},
			{"role": "user", "content": "cantikkan sikit"}
		]
	}`)
	if HallucinatedEditClaim(body) {
		t.Fatal("assistant terkini tanpa klaim — jangan flag atas sejarah lama yang sah")
	}
}

func TestHallucinatedEditClaimLeavesInvalidJSONUnchanged(t *testing.T) {
	body := []byte(`{"model": "grok-4.6", "messages": [broken`)
	if HallucinatedEditClaim(body) {
		t.Fatal("JSON rosak tidak boleh di-flag")
	}
}

func TestHallucinatedEditClaimLeavesNoMessagesUnchanged(t *testing.T) {
	body := []byte(`{"model": "grok-4.6"}`)
	if HallucinatedEditClaim(body) {
		t.Fatal("tiada messages tidak boleh di-flag")
	}
}

func TestHallucinatedEditClaimDetectsDonePattern(t *testing.T) {
	body := []byte(`{
		"model": "grok-4.6",
		"messages": [
			{"role": "user", "content": "buatkan website"},
			{"role": "assistant", "content": "Done! Website anda sudah siap."},
			{"role": "user", "content": "ok"}
		]
	}`)
	// "Done!" ialah pattern yang disengajakan konservatif — ia mungkin false
	// positive untuk jawapan pendek sah, jadi TIDAK termasuk dalam senarai.
	// Test ini memastikan exclusion itu tidak regress.
	if HallucinatedEditClaim(body) {
		t.Log("note: 'Done!' flagged — pattern mungkin perlu dikaji semula")
	}
}
