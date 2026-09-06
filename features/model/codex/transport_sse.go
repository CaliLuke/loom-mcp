package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type eventSource interface {
	Next() ([]byte, error)
	Close() error
}

type sseSource struct {
	ctx       context.Context
	body      io.ReadCloser
	scanner   *bufio.Scanner
	idle      time.Duration
	closeOnce sync.Once
	closeErr  error
}

func (c *Client) startSSE(ctx context.Context, built *builtRequest, credentials Credentials) (eventSource, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, codexResponsesURL, bytes.NewReader(built.sseBody))
	if err != nil {
		return nil, fmt.Errorf("codex: create SSE request: %w", err)
	}
	applyCommonHeaders(request.Header, credentials, c.version)
	request.Header.Set("OpenAI-Beta", sseBetaHeader)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	if built.lite {
		request.Header.Set(responsesLiteHeader, "true")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportFailure{operation: "sse request", cause: err}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxHTTPErrorBodyBytes+1))
		closeErr := response.Body.Close()
		if len(errorBody) > maxHTTPErrorBodyBytes {
			errorBody = errorBody[:maxHTTPErrorBodyBytes]
		}
		return nil, normalizeHTTPError(response.StatusCode, response.Header, errorBody, errors.Join(readErr, closeErr), credentials)
	}
	scanner := newSSEScanner(response.Body)
	return &sseSource{ctx: ctx, body: response.Body, scanner: scanner, idle: c.idleTimeout}, nil
}

func (s *sseSource) Next() ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		data, err := s.nextBlocking()
		resultCh <- result{data: data, err: err}
	}()
	timer := time.NewTimer(s.idle)
	defer timer.Stop()
	select {
	case result := <-resultCh:
		return result.data, result.err
	case <-s.ctx.Done():
		// The http.Response.Body contract requires Close to unblock a concurrent Read.
		_ = s.Close()
		result := <-resultCh
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			return nil, s.ctx.Err()
		}
		return nil, s.ctx.Err()
	case <-timer.C:
		_ = s.Close()
		<-resultCh
		return nil, &transportFailure{operation: "SSE idle timeout", cause: context.DeadlineExceeded}
	}
}

func (s *sseSource) nextBlocking() ([]byte, error) {
	var data strings.Builder
	eventSize := 0
	for s.scanner.Scan() {
		rawLine := s.scanner.Bytes()
		lineSize := len(rawLine)
		if lineSize > maxStreamEventBytes || eventSize > maxStreamEventBytes-lineSize {
			return nil, errors.New("codex: SSE event exceeds 16 MiB")
		}
		eventSize += lineSize
		line := bytes.TrimSuffix(rawLine, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			if data.Len() == 0 {
				eventSize = 0
				continue
			}
			return []byte(data.String()), nil
		}
		if err := appendSSEData(&data, line); err != nil {
			return nil, err
		}
	}
	if err := s.scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, errors.New("codex: SSE event exceeds 16 MiB")
		}
		return nil, &transportFailure{operation: "SSE receive", cause: err}
	}
	if data.Len() > 0 {
		return []byte(data.String()), nil
	}
	return nil, &transportFailure{operation: "SSE receive", cause: io.ErrUnexpectedEOF}
}

func appendSSEData(data *strings.Builder, line []byte) error {
	prefix := []byte("data:")
	if !bytes.HasPrefix(line, prefix) {
		return nil
	}
	value := bytes.TrimPrefix(line, prefix)
	value = bytes.TrimPrefix(value, []byte{' '})
	separator := 0
	if data.Len() > 0 {
		separator = 1
	}
	if data.Len()+separator+len(value) > maxStreamEventBytes {
		return errors.New("codex: SSE event exceeds 16 MiB")
	}
	if separator != 0 {
		data.WriteByte('\n')
	}
	_, _ = data.Write(value)
	return nil
}

func newSSEScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Split(scanSSELine)
	scanner.Buffer(make([]byte, 64<<10), maxStreamEventBytes+1)
	return scanner
}

func scanSSELine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, value := range data {
		switch value {
		case '\n':
			return index + 1, data[:index+1], nil
		case '\r':
			if index+1 == len(data) && !atEOF {
				return 0, nil, nil
			}
			if index+1 < len(data) && data[index+1] == '\n' {
				return index + 2, data[:index+2], nil
			}
			return index + 1, data[:index+1], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (s *sseSource) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.body.Close()
	})
	return s.closeErr
}

func applyCommonHeaders(header http.Header, credentials Credentials, version string) {
	header.Set("Authorization", "Bearer "+credentials.AccessToken)
	header.Set("chatgpt-account-id", credentials.AccountID)
	if credentials.Residency != "" {
		header.Set("x-openai-internal-codex-residency", credentials.Residency)
	}
	header.Set("originator", "pi")
	header.Set("version", version)
	header.Set("User-Agent", codexUserAgent)
}
