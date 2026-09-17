package codex

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type webSocketSource struct {
	ctx       context.Context
	conn      *websocket.Conn
	idle      time.Duration
	stopClose func() bool
	closeOnce sync.Once
	closeErr  error
}

func (c *Client) startWebSocket(ctx context.Context, built *builtRequest, credentials Credentials) (eventSource, error) { //nolint:maintidx // One function owns upgrade, cancellation, and initial write cleanup.
	header := make(http.Header)
	applyCommonHeaders(header, credentials, c.version)
	header.Set("OpenAI-Beta", webSocketBetaHeader)
	if built.lite {
		header.Set(responsesLiteHeader, "true")
	}
	conn, response, err := c.wsDialer.DialContext(ctx, codexResponsesWSURL, header)
	if err != nil {
		if response != nil {
			errorBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxHTTPErrorBodyBytes+1))
			closeErr := response.Body.Close()
			if len(errorBody) > maxHTTPErrorBodyBytes {
				errorBody = errorBody[:maxHTTPErrorBodyBytes]
			}
			return nil, normalizeHTTPError(response.StatusCode, response.Header, errorBody, errors.Join(err, readErr, closeErr), credentials)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transportFailure{operation: "WebSocket upgrade", cause: err}
	}
	source := &webSocketSource{ctx: ctx, conn: conn, idle: c.idleTimeout}
	source.stopClose = context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	closeStart := func(startErr error) (eventSource, error) {
		source.stopClose()
		closeErr := conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.Join(startErr, closeErr)
	}

	conn.SetReadLimit(maxStreamEventBytes)
	writeDeadline := time.Now().Add(c.idleTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(writeDeadline) {
		writeDeadline = contextDeadline
	}
	if err := conn.SetWriteDeadline(writeDeadline); err != nil {
		return closeStart(&transportFailure{operation: "WebSocket write deadline", cause: err})
	}
	if err := conn.WriteMessage(websocket.TextMessage, built.wsBody); err != nil {
		return closeStart(&transportFailure{operation: "WebSocket write", cause: err})
	}
	return source, nil
}

func (s *webSocketSource) Next() ([]byte, error) {
	deadline := time.Now().Add(s.idle)
	if contextDeadline, ok := s.ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return nil, &transportFailure{operation: "WebSocket deadline", cause: err}
	}
	messageType, data, err := s.conn.ReadMessage()
	if err != nil {
		if s.ctx.Err() != nil {
			return nil, s.ctx.Err()
		}
		if fallbackableWebSocketRead(err) {
			return nil, &transportFailure{operation: "WebSocket receive", cause: err}
		}
		return nil, invalidStreamError()
	}
	if messageType != websocket.TextMessage || len(data) > maxStreamEventBytes {
		return nil, invalidStreamError()
	}
	return data, nil
}

func fallbackableWebSocketRead(err error) bool {
	if errors.Is(err, websocket.ErrReadLimit) {
		return false
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code == websocket.CloseAbnormalClosure
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func (s *webSocketSource) Close() error {
	s.closeOnce.Do(func() {
		if s.stopClose != nil {
			s.stopClose()
		}
		s.closeErr = s.conn.Close()
	})
	return s.closeErr
}
