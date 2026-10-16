package opik

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func readResponse(response *http.Response, method, path string) (map[string]jsontext.Value, int, error) {
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err := response.Body.Close(); err != nil {
		return nil, response.StatusCode, err
	}
	if readErr != nil {
		return nil, response.StatusCode, readErr
	}
	if response.StatusCode == http.StatusNotFound && method == http.MethodGet {
		return nil, response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, fmt.Errorf("opik %s %s: HTTP %d", method, path, response.StatusCode)
	}
	var result map[string]jsontext.Value
	if len(data) != 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, response.StatusCode, fmt.Errorf("opik response: %w", err)
		}
	}
	return result, response.StatusCode, nil
}

func verifyMetadata(data jsontext.Value, expected any) error {
	var remote map[string]jsontext.Value
	if err := json.Unmarshal(data, &remote); err != nil {
		return fmt.Errorf("opik: invalid existing metadata: %w", err)
	}
	metadata := expected.(map[string]any)
	for key, value := range metadata {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !sameJSON(remote[key], encoded) {
			return fmt.Errorf("opik: existing metadata mismatch at %s", key)
		}
	}
	return nil
}

// sameJSON compares structural metadata independently of object member order.
func sameJSON(a, b []byte) bool {
	left, right := append(jsontext.Value(nil), a...), append(jsontext.Value(nil), b...)
	if err := left.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return false
	}
	if err := right.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

// request never retries a remote mutation implicitly. Export retries use saved
// resource identities and GET verification to resolve an uncertain response.
func (c *Client) request(ctx context.Context, method, path string, body any) (map[string]jsontext.Value, int, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body, json.Deterministic(true))
		if err != nil {
			return nil, 0, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.config.Endpoint, "/")+"/v1/private"+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		request.Header.Set("authorization", c.config.APIKey)
	}
	if c.config.Workspace != "" {
		request.Header.Set("Comet-Workspace", c.config.Workspace)
	}
	response, err := c.config.HTTPClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	return readResponse(response, method, path)
}

// ensure verifies an existing resource or creates the same saved identity.
func (c *Client) ensure(ctx context.Context, collection, id string, body map[string]any, fields []string) error {
	remote, status, err := c.request(ctx, http.MethodGet, collection+"/"+id, nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		var payload any = body
		if collection == "/experiments/items" {
			payload = map[string]any{"experiment_items": []any{body}}
		}
		_, _, err = c.request(ctx, http.MethodPost, collection, payload)
		return err
	}
	for _, field := range fields {
		expected, err := json.Marshal(body[field], json.Deterministic(true))
		if err != nil {
			return err
		}
		if field == fieldMetadata {
			if err := verifyMetadata(remote[field], body[field]); err != nil {
				return err
			}
		} else if !sameJSON(remote[field], expected) {
			return fmt.Errorf("opik: existing %s/%s identity mismatch at %s", collection, id, field)
		}
	}
	return nil
}
