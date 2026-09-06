package codex

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
)

const maxJWTBytes = 64 << 10
const codexAuthClaim = "https://api.openai.com/auth"

// Credentials contains application-owned credentials for one Codex request.
type Credentials struct {
	AccessToken string
	AccountID   string
	Residency   string
}

// CredentialSource resolves current Codex credentials for each logical request.
type CredentialSource interface {
	Credentials(context.Context) (Credentials, error)
}

// CredentialSourceFunc adapts a function into a CredentialSource.
type CredentialSourceFunc func(context.Context) (Credentials, error)

// Credentials resolves credentials through f.
func (f CredentialSourceFunc) Credentials(ctx context.Context) (Credentials, error) {
	return f(ctx)
}

type jwtAuthClaims struct {
	AccountID        string `json:"chatgpt_account_id"`
	DataResidency    string `json:"chatgpt_data_residency"`
	ComputeResidency string `json:"chatgpt_compute_residency"`
}

func resolveCredentials(ctx context.Context, source CredentialSource) (Credentials, error) {
	credentials, err := source.Credentials(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Credentials{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return Credentials{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Credentials{}, context.DeadlineExceeded
		}
		return Credentials{}, errors.New("codex: credential source failed")
	}
	credentials.AccessToken = strings.TrimSpace(credentials.AccessToken)
	credentials.AccountID = strings.TrimSpace(credentials.AccountID)
	credentials.Residency = strings.TrimSpace(credentials.Residency)
	if credentials.AccessToken == "" {
		return Credentials{}, errors.New("codex: access token is required")
	}
	if len(credentials.AccessToken) > maxJWTBytes {
		return Credentials{}, errors.New("codex: access token exceeds 64 KiB")
	}
	if credentials.AccountID != "" && credentials.Residency != "" {
		return credentials, nil
	}
	claims, ok := decodeJWTClaims(credentials.AccessToken)
	if credentials.AccountID == "" && ok {
		credentials.AccountID = strings.TrimSpace(claims.AccountID)
	}
	if credentials.Residency == "" && ok {
		credentials.Residency = strings.TrimSpace(claims.DataResidency)
		if credentials.Residency == "" {
			credentials.Residency = strings.TrimSpace(claims.ComputeResidency)
		}
	}
	if credentials.AccountID == "" {
		return Credentials{}, errors.New("codex: account or workspace ID is required")
	}
	return credentials, nil
}

func decodeJWTClaims(token string) (jwtAuthClaims, bool) {
	if len(token) > maxJWTBytes {
		return jwtAuthClaims{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" || len(parts[1]) > maxJWTBytes {
		return jwtAuthClaims{}, false
	}
	for _, index := range []int{0, 2} {
		if _, err := base64.RawURLEncoding.Strict().DecodeString(parts[index]); err != nil {
			return jwtAuthClaims{}, false
		}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(decoded) > maxJWTBytes {
		return jwtAuthClaims{}, false
	}
	var envelope map[string]jsontext.Value
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		return jwtAuthClaims{}, false
	}
	payload, ok := envelope[codexAuthClaim]
	if !ok {
		return jwtAuthClaims{}, false
	}
	var claims jwtAuthClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtAuthClaims{}, false
	}
	return claims, true
}
