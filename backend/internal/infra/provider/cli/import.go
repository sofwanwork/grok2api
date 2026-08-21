package cli

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

const (
	credentialImportProvider     = "grok_build"
	maxCredentialImportAccounts  = 10000
	maxImportedRefreshTokenBytes = 16 << 10
)

type credentialImportDocument struct {
	Accounts []importedCredentialEntry `json:"accounts"`
}

type importedCredentialEntry struct {
	Provider     string `json:"provider"`
	Name         string `json:"name"`
	ClientID     string `json:"client_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresAt    string `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
	Email        string `json:"email"`
	Subject      string `json:"sub"`
	UserID       string `json:"user_id"`
	PrincipalID  string `json:"principal_id"`
	TeamID       string `json:"team_id"`
}

func marshalCredentials(values []provider.CredentialSeed) ([]byte, error) {
	document := credentialImportDocument{Accounts: make([]importedCredentialEntry, 0, len(values))}
	for _, value := range values {
		entry := importedCredentialEntry{
			Provider: credentialImportProvider, Name: value.Name, ClientID: value.OIDCClientID,
			AccessToken: value.AccessToken, RefreshToken: value.RefreshToken, TokenType: "Bearer",
			Email: value.Email, UserID: value.UserID, TeamID: value.TeamID,
		}
		if !value.ExpiresAt.IsZero() {
			entry.ExpiresAt = value.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		document.Accounts = append(document.Accounts, entry)
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("Menyahkod kredensial akaun: %w", err)
	}
	return append(data, '\n'), nil
}

func parseImportedCredentials(data []byte) ([]provider.CredentialSeed, error) {
	entries, err := parseImportedCredentialEntries(data)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("Tiada akaun dalam kredensial akaun")
	}

	result := make([]provider.CredentialSeed, 0, len(entries))
	for index, entry := range entries {
		seed, err := normalizeImportedCredential(entry)
		if err != nil {
			return nil, fmt.Errorf("Akaun ke-%d: %w", index+1, err)
		}
		result = append(result, seed)
	}
	return result, nil
}

func parseImportedCredentialEntries(data []byte) ([]importedCredentialEntry, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	entries, sequenceErr := parseImportedCredentialJSONSequence(data)
	if sequenceErr == nil {
		return entries, nil
	}
	if errors.Is(sequenceErr, provider.ErrCredentialLimit) {
		return nil, sequenceErr
	}
	if entries, recognized, err := parseLooseCredentialDocument(data); recognized {
		return entries, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed != "" && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return parsePlainTextRefreshTokens(trimmed)
	}
	return nil, sequenceErr
}

// parsePlainTextRefreshTokens accepts one refresh token per line. The optional
// rt= and refresh_token= prefixes make common exports directly importable.
func parsePlainTextRefreshTokens(value string) ([]importedCredentialEntry, error) {
	scanner := bufio.NewScanner(strings.NewReader(value))
	scanner.Buffer(make([]byte, 64*1024), maxImportedRefreshTokenBytes+1)
	entries := make([]importedCredentialEntry, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if key, token, ok := strings.Cut(line, "="); ok {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "rt", "refresh_token":
				line = strings.TrimSpace(token)
			}
		}
		if line == "" {
			return nil, fmt.Errorf("refresh token tak boleh kosong")
		}
		if len(line) > maxImportedRefreshTokenBytes {
			return nil, fmt.Errorf("Panjang refresh token tidak boleh melebihi %d bait", maxImportedRefreshTokenBytes)
		}
		if strings.IndexFunc(line, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("refresh token tidak boleh mengandungi aksara ruang kosong")
		}
		if len(entries) >= maxCredentialImportAccounts {
			return nil, fmt.Errorf("%w: import maksimum %d akaun setiap kali", provider.ErrCredentialLimit, maxCredentialImportAccounts)
		}
		entries = append(entries, importedCredentialEntry{Provider: credentialImportProvider, RefreshToken: line})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("Membaca teks refresh token: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Tiada refresh token dalam teks")
	}
	return entries, nil
}

func parseImportedCredentialJSONSequence(data []byte) ([]importedCredentialEntry, error) {
	return provider.DecodeCredentialJSONEntries[importedCredentialEntry](data, credentialImportProvider, maxCredentialImportAccounts)
}

func parseImportedCredentialJSONValue(data []byte) ([]importedCredentialEntry, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, err
	}

	if accounts, batch := shape["accounts"]; batch {
		var entries []importedCredentialEntry
		if err := json.Unmarshal(accounts, &entries); err != nil {
			return nil, fmt.Errorf("Huraian kredensial akaun secara pukal: %w", err)
		}
		return entries, nil
	}

	var entry importedCredentialEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("Huraian kredensial OAuth: %w", err)
	}
	return []importedCredentialEntry{entry}, nil
}

func appendImportedCredentialEntries(target *[]importedCredentialEntry, values []importedCredentialEntry) error {
	if len(values) > maxCredentialImportAccounts-len(*target) {
		return fmt.Errorf("%w: import maksimum %d akaun setiap kali", provider.ErrCredentialLimit, maxCredentialImportAccounts)
	}
	*target = append(*target, values...)
	return nil
}

// parseLooseCredentialDocument supports account dumps shaped like
// { "accounts": [ followed by one complete object per line, even when the
// producer omitted commas or the final closing brackets. This compatibility
// path is intentionally limited to that recognizable wrapper.
func parseLooseCredentialDocument(data []byte) ([]importedCredentialEntry, bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), max(len(data), 64*1024))
	entries := make([]importedCredentialEntry, 0)
	nonEmptyLine := 0
	lineNumber := 0
	closed := false
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		nonEmptyLine++
		if nonEmptyLine == 1 {
			if line != "{" {
				return nil, false, nil
			}
			continue
		}
		if nonEmptyLine == 2 {
			if compactLooseJSONLine(line) != `"accounts":[` {
				return nil, false, nil
			}
			continue
		}
		if isLooseDocumentClosing(line) {
			closed = true
			continue
		}
		if closed {
			return nil, true, fmt.Errorf("Huraian kredensial akaun secara pukal pada baris %d: masih ada kandungan selepas penanda penutup", lineNumber)
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, ","))
		values, err := parseImportedCredentialJSONValue([]byte(line))
		if err != nil {
			return nil, true, fmt.Errorf("Huraian kredensial akaun secara pukal pada baris %d: %w", lineNumber, err)
		}
		if err := appendImportedCredentialEntries(&entries, values); err != nil {
			return nil, true, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, true, fmt.Errorf("Membaca kredensial akaun secara pukal: %w", err)
	}
	if nonEmptyLine < 2 {
		return nil, false, nil
	}
	return entries, true, nil
}

func compactLooseJSONLine(value string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(value)
}

func isLooseDocumentClosing(value string) bool {
	switch compactLooseJSONLine(value) {
	case "]", "],", "}", "},", "]}", "]},":
		return true
	default:
		return false
	}
}

func normalizeImportedCredential(entry importedCredentialEntry) (provider.CredentialSeed, error) {
	providerName := strings.ToLower(strings.TrimSpace(entry.Provider))
	if providerName == "" {
		providerName = credentialImportProvider
	}
	if providerName != credentialImportProvider {
		return provider.CredentialSeed{}, fmt.Errorf("Provider %q belum disokong", entry.Provider)
	}
	accessToken := strings.TrimSpace(entry.AccessToken)
	refreshToken := strings.TrimSpace(entry.RefreshToken)
	if accessToken == "" && refreshToken == "" {
		return provider.CredentialSeed{}, fmt.Errorf("Sekurang-kurangnya satu daripada access_token atau refresh_token mesti disediakan")
	}
	if entry.TokenType != "" && !strings.EqualFold(strings.TrimSpace(entry.TokenType), "Bearer") {
		return provider.CredentialSeed{}, fmt.Errorf("token_type %q belum disokong", entry.TokenType)
	}

	claims := decodeJWTClaims(firstNonEmpty(entry.IDToken, accessToken))
	userID := firstNonEmpty(entry.UserID, entry.PrincipalID, entry.Subject, stringClaim(claims, "sub"))
	email := firstNonEmpty(entry.Email, stringClaim(claims, "email"))
	teamID := firstNonEmpty(entry.TeamID, stringClaim(claims, "team_id"))
	expiresAt, err := importedCredentialExpiry(entry, claims)
	if err != nil {
		return provider.CredentialSeed{}, err
	}
	clientID := firstNonEmpty(entry.ClientID, defaultOAuthClientID)
	identity := firstNonEmpty(userID, strings.ToLower(email), teamID, refreshToken, accessToken)
	sourceKey := "import:" + security.HashToken(strings.Join([]string{providerName, clientID, identity}, "|"))

	return provider.CredentialSeed{
		Name: firstNonEmpty(entry.Name, email, userID, "Grok Build account"), Email: email, UserID: userID, TeamID: teamID,
		SourceKey: sourceKey, OIDCClientID: clientID, AccessToken: accessToken, RefreshToken: refreshToken, ExpiresAt: expiresAt,
	}, nil
}

func importedCredentialExpiry(entry importedCredentialEntry, claims map[string]any) (time.Time, error) {
	if strings.TrimSpace(entry.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.ExpiresAt))
		if err != nil {
			return time.Time{}, fmt.Errorf("expires_at mesti masa RFC3339: %w", err)
		}
		return parsed.UTC(), nil
	}
	if expiresAt, ok := numericDateClaim(claims, "exp"); ok {
		return expiresAt, nil
	}
	if entry.ExpiresIn < 0 {
		return time.Time{}, fmt.Errorf("expires_in tidak boleh kurang daripada sifar")
	}
	if entry.ExpiresIn > int64((365*24*time.Hour)/time.Second) {
		return time.Time{}, fmt.Errorf("expires_in melebihi julat yang munasabah")
	}
	if entry.ExpiresIn > 0 {
		return time.Now().UTC().Add(time.Duration(entry.ExpiresIn) * time.Second), nil
	}
	return time.Time{}, nil
}

func numericDateClaim(claims map[string]any, key string) (time.Time, bool) {
	value, ok := claims[key].(float64)
	if !ok || value <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(value), 0).UTC(), true
}

func decodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func stringClaim(claims map[string]any, key string) string {
	value, _ := claims[key].(string)
	return value
}
