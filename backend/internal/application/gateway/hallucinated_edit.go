package gateway

import (
	"encoding/json"
	"strings"
)

// Patch #24: hallucinated-edit detector.
//
// Latar (round 6, 27 Ogos): model claim DUA KALI berturut-turut
// "Wah gila, landing page dah siap guna. Aku dah replace dengan design
// yang aku buat" dan "aku dah edit fail page.tsx dengan kod yang aku
// tulis tadi" — sementara tiada SATU PUN tool write/edit call dalam
// keseluruhan sesi. Tiada build, tiada verify, tiada fail disentuh.
// Ini claim-hallucination: model generate teks yang seolah-olah dia sudah
// menggunakan tool, padahal dia tak pernah memanggilnya.
//
// Detector ini mengimbas history request daripada client (OpenCode):
// jika assistant text terakhir dalam history mengandungi frasa
// klaim-tulis tanpa tool_calls dalam turn yang sama, ia dianggap
// claim palsu — trailer HTTP, log berstruktur, dan audit flag dikeluarkan
// supaya user boleh melihat amaran ini secara eksplisit dan tidak
// bergantung pada jawapan yang bohong.

// editClaimPatterns ialah frasa lowercase yang menandakan model mendakwa
// dia telah menulis/mengubah fail. Senarai ini mesti kekal konservatif —
// frasa generik seperti "siap" atau "done" tidak disertakan kerana
// false-positive terlalu tinggi (jawapan sah "Selesai." bukan klaim-tulis).
var editClaimPatterns = []string{
	"dah siap",
	"dah edit",
	"dah replace",
	"dah gantikan",
	"dah tulis",
	"dah buatkan",
	"dah siapkan",
	"aku dah edit",
	"aku dah replace",
	"aku dah tulis",
	"aku dah buatkan",
	"files yang aku edit",
	"fail yang aku edit",
	"fail yang aku tulis",
	"fail yang aku buatkan",
	"kod yang aku tulis",
	"i've written",
	"i've edited",
	"i've replaced",
	"i have written",
	"i have edited",
	"i have replaced",
	"just written",
	"just edited",
	"just replaced",
	"edited the file",
	"updated the file",
	"replaced the file",
	"wrote the file",
}

// maxEditClaimInspectionBytes hadkan pemeriksaan history supaya request
// gergasi tidak membazir CPU.
const maxEditClaimInspectionBytes = 2 << 20 // 2 MiB (history panjang wajar)

// HallucinatedEditClaim mengimbas request body OpenAI Chat Completions
// ({"messages":[...]}) dan melaporkan true jika teks assistant dalam turn
// sebelum prompt terkini mengandungi frasa klaim-tulis TANPA tool_calls
// dalam mesej assistant yang sama. History dalam format Responses
// ({"input":[...]}) tidak disokong — ia mengandungi item function_call
// berasingan yang sukar dipisahkan dari message text; OpenCode Chat adalah
// sasaran utama patch ini.
func HallucinatedEditClaim(body []byte) bool {
	if len(body) == 0 || len(body) > maxEditClaimInspectionBytes {
		return false
	}
	var envelope struct {
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Messages) == 0 {
		return false
	}
	for i := len(envelope.Messages) - 1; i >= 0; i-- {
		m := envelope.Messages[i]
		if m.Role != "assistant" {
			continue
		}
		// Hanya mesej assistant dengan TIADA tool_calls yang mencurigakan —
		// klaim dalam turn yang betul-betul ada tool calls adalah sah.
		if len(m.ToolCalls) > 0 && strings.TrimSpace(string(m.ToolCalls)) != "" && string(m.ToolCalls) != "null" {
			return false
		}
		text := strings.ToLower(extractAssistantText(m.Content))
		if text == "" {
			return false
		}
		for _, pattern := range editClaimPatterns {
			if strings.Contains(text, pattern) {
				return true
			}
		}
		return false
	}
	return false
}

// extractAssistantText menyatukan kandungan teks daripada medan content
// assistant yang mungkin string biasa atau array blok Chat
// ({"type":"text","text":"..."}).
func extractAssistantText(content json.RawMessage) string {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}
