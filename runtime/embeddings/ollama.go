package embeddings

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Ollama calls the Ollama /api/embed endpoint. It is safe for concurrent use.
type Ollama struct {
	endpoint string
	model    string
	http     *http.Client
}

const maxResponseBytes = 16 << 20

// NewOllama validates configuration. A nil client selects a 30-second timeout.
// The supplied client remains caller-owned. Requests disable silent truncation.
func NewOllama(baseURL, model string, client *http.Client) (*Ollama, error) {
	address, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || address.Host == "" || address.User != nil ||
		(address.Scheme != "http" && address.Scheme != "https") || address.RawQuery != "" || address.Fragment != "" {
		return nil, errors.New("invalid Ollama base URL")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("embedding model is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Ollama{endpoint: strings.TrimRight(address.String(), "/") + "/api/embed", model: strings.TrimSpace(model), http: client}, nil
}

// Embed returns a vector for text.
func (c *Ollama) Embed(ctx context.Context, text string) ([]float64, error) {
	vectors, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// EmbedBatch returns one valid, equally sized vector per input text. Provider
// errors expose status only; neither bodies nor request URLs appear in errors.
func (c *Ollama) EmbedBatch(ctx context.Context, texts []string) (_ [][]float64, err error) {
	if len(texts) == 0 {
		return nil, errors.New("embedding input is empty")
	}
	body, err := json.Marshal(struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Truncate bool     `json:"truncate"`
	}{c.model, texts, false})
	if err != nil {
		return nil, errors.New("encode embedding request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("create embedding request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("embedding transport failed")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, errors.New("close embedding response"))
		}
	}()
	vectors, err := readOllamaResponse(resp, len(texts))
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return vectors, err
}

func readOllamaResponse(resp *http.Response, count int) (_ [][]float64, err error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding provider returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, errors.New("embedding response unreadable or too large")
	}
	var response struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.New("invalid embedding response")
	}
	if len(response.Embeddings) != count {
		return nil, errors.New("embedding response count mismatch")
	}
	for _, vector := range response.Embeddings {
		if err := Validate(vector, len(response.Embeddings[0])); err != nil {
			return nil, err
		}
	}
	return response.Embeddings, nil
}
