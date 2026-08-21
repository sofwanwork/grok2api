package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

const (
	maxImportAccounts = 10000
	maxSSOTokenBytes  = 16 << 10
)

type importDocument struct {
	Provider string        `json:"provider"`
	Accounts []importEntry `json:"accounts"`
}

type importEntry struct {
	Name              string `json:"name"`
	Email             string `json:"email,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	SSOToken          string `json:"sso_token"`
	Token             string `json:"token"`
	CloudflareCookies string `json:"cloudflare_cookies"`
}

func parseImportedCredentials(data []byte) ([]provider.CredentialSeed, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("Tiada akaun Grok Console dalam fail akaun")
	}
	// 「[」为 JSON 保留前缀：顶层裸数组走 JSON 解析，避免被当成纯文本 token 静默导入。
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return parsePlainTextCredentials(trimmed)
	}
	entries, err := provider.DecodeCredentialJSONEntries[importEntry](data, string(account.ProviderConsole), maxImportAccounts)
	if err != nil {
		return nil, fmt.Errorf("Huraian JSON akaun Grok Console: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Tiada akaun Grok Console dalam fail akaun")
	}
	seen := make(map[string]struct{}, len(entries))
	result := make([]provider.CredentialSeed, 0, len(entries))
	for index, entry := range entries {
		token := sanitizeSSOToken(firstNonEmpty(entry.SSOToken, entry.Token))
		if token == "" {
			return nil, fmt.Errorf("Akaun ke-%d tiada sso_token", index+1)
		}
		if len(token) > maxSSOTokenBytes {
			return nil, fmt.Errorf("sso_token akaun ke-%d melebihi 16 KiB", index+1)
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = "Grok Console " + security.HashToken(token)[:8]
		}
		seed := credentialSeed(name, token)
		seed.Email = strings.TrimSpace(entry.Email)
		seed.UserID = strings.TrimSpace(entry.UserID)
		seed.CloudflareCookies = entry.CloudflareCookies
		result = append(result, seed)
	}
	return result, nil
}

func parsePlainTextCredentials(value string) ([]provider.CredentialSeed, error) {
	lines := strings.Split(value, "\n")
	seen := make(map[string]struct{}, len(lines))
	result := make([]provider.CredentialSeed, 0, len(lines))
	for index, line := range lines {
		token := sanitizeSSOToken(line)
		if token == "" {
			continue
		}
		if len(token) > maxSSOTokenBytes {
			return nil, fmt.Errorf("sso token pada baris ke-%d melebihi 16 KiB", index+1)
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, credentialSeed("Grok Console "+security.HashToken(token)[:8], token))
		if len(result) > maxImportAccounts {
			return nil, provider.ErrCredentialLimit
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("Tiada sso token yang sah dalam teks")
	}
	return result, nil
}

func credentialSeed(name, token string) provider.CredentialSeed {
	return provider.CredentialSeed{
		Provider: account.ProviderConsole, AuthType: account.AuthTypeSSO, Name: name,
		SourceKey: "console-sso:" + security.HashToken(token), AccessToken: token,
	}
}

func marshalCredentials(values []provider.CredentialSeed) ([]byte, error) {
	document := importDocument{Provider: string(account.ProviderConsole), Accounts: make([]importEntry, 0, len(values))}
	for _, value := range values {
		document.Accounts = append(document.Accounts, importEntry{Name: value.Name, Email: value.Email, UserID: value.UserID, SSOToken: value.AccessToken, CloudflareCookies: value.CloudflareCookies})
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func sanitizeSSOToken(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "sso=") {
		value = strings.TrimSpace(value[len("sso="):])
	}
	if token, _, found := strings.Cut(value, ";"); found {
		value = token
	}
	return strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
