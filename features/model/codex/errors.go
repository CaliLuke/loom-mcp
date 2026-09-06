package codex

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

var safeProviderValue = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)

type transportFailure struct {
	operation string
	cause     error
}

func (e *transportFailure) Error() string {
	return fmt.Sprintf("codex %s transport failed", e.operation)
}

func (e *transportFailure) Unwrap() error {
	return e.cause
}

func fallbackable(err error) bool {
	var failure *transportFailure
	return errors.As(err, &failure)
}

func normalizeHTTPError(status int, header http.Header, body []byte, cause error, credentials Credentials) error {
	kind := model.ProviderErrorKindUnknown
	retryable := false
	message := "provider request failed"
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = model.ProviderErrorKindAuth
		message = "authentication or authorization failed"
	case status == http.StatusTooManyRequests:
		kind = model.ProviderErrorKindRateLimited
		retryable = true
		message = "rate limit exceeded"
	case status >= 400 && status < 500:
		kind = model.ProviderErrorKindInvalidRequest
		message = "provider rejected the request"
	case status >= 500:
		kind = model.ProviderErrorKindUnavailable
		retryable = true
		message = "provider is unavailable"
	}
	code := redactProviderValue(safeErrorCode(body), credentials)
	requestID := redactProviderValue(safeHeaderValue(header.Get("x-request-id")), credentials)
	if requestID == "" {
		requestID = redactProviderValue(safeHeaderValue(header.Get("request-id")), credentials)
	}
	providerErr := model.NewProviderError(codexProvider, "responses", status, kind, code, message, requestID, retryable, cause)
	if status == http.StatusTooManyRequests {
		return errors.Join(model.ErrRateLimited, providerErr)
	}
	return providerErr
}

func normalizeStreamProviderError(status int, code, requestID string, cause error, credentials Credentials) error {
	if status == 0 {
		status = statusFromCode(code)
	}
	header := make(http.Header)
	if safe := redactProviderValue(safeHeaderValue(requestID), credentials); safe != "" {
		header.Set("x-request-id", safe)
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": redactProviderValue(safeHeaderValue(code), credentials)}})
	return normalizeHTTPError(status, header, body, cause, credentials)
}

func normalizeTransportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return model.NewProviderError(
		codexProvider,
		operation,
		0,
		model.ProviderErrorKindUnavailable,
		"",
		"provider transport is unavailable",
		"",
		true,
		err,
	)
}

func safeErrorCode(body []byte) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	if code := safeHeaderValue(envelope.Error.Code); code != "" {
		return code
	}
	return safeHeaderValue(envelope.Error.Type)
}

func safeHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if !safeProviderValue.MatchString(value) {
		return ""
	}
	return value
}

func redactProviderValue(value string, credentials Credentials) string {
	for _, sensitive := range []string{credentials.AccessToken, credentials.AccountID, credentials.Residency} {
		if sensitive != "" && strings.Contains(value, sensitive) {
			return ""
		}
	}
	return value
}

func statusFromCode(code string) int {
	switch strings.ToLower(code) {
	case "rate_limit_exceeded", "usage_limit_reached", "usage_not_included":
		return http.StatusTooManyRequests
	case "authentication_error", "unauthorized":
		return http.StatusUnauthorized
	case "permission_denied", wireForbiddenCode:
		return http.StatusForbidden
	case "invalid_request_error", "invalid_request":
		return http.StatusBadRequest
	default:
		return 0
	}
}
