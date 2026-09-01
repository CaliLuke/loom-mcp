// Package sdkclient adapts an official MCP SDK server session to loom-mcp's
// transport-neutral server-to-client runtime contracts.
package sdkclient

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	inputRequestIDPrefix = "loom-input-"
	maxInputResponses    = 64
	maxRequestStateBytes = 1 << 20
	requestStateKeyBytes = 32
	requestStateVersion  = 1
	elicitActionAccept   = "accept"
	elicitActionCancel   = "cancel"
	elicitActionDecline  = "decline"
)

// ClientFeaturesOptions configures official MCP multi-round-trip input state.
type ClientFeaturesOptions struct {
	// InputResponses contains client responses supplied on this retry.
	InputResponses mcp.InputResponseMap
	// RequestState is the opaque state returned by the previous input-required
	// result.
	RequestState string
	// RequestStateKey is the stable 32-byte key used to encrypt and authenticate
	// request state. Replicas that serve the same MCP endpoint must share it.
	RequestStateKey []byte
	// RequestMethod and RequestParams bind state to the original logical
	// request, preventing replay against another operation or payload.
	RequestMethod string
	RequestParams any
}

// WithClientFeatures stores all supported SDK-backed client feature adapters in
// ctx. A nil session leaves ctx unchanged.
func WithClientFeatures(ctx context.Context, session *mcp.ServerSession, opts ClientFeaturesOptions) context.Context {
	if session == nil {
		return ctx
	}
	roundTrip := newInputRoundTrip(opts)
	ctx = mcpruntime.WithElicitor(ctx, sessionElicitor{roundTrip: roundTrip})
	return mcpruntime.WithProgressReporter(ctx, sessionProgressReporter{session: session})
}

type sessionElicitor struct {
	roundTrip *inputRoundTrip
}

type sessionProgressReporter struct {
	session *mcp.ServerSession
}

type inputRoundTrip struct {
	responses       map[string]jsontext.Value
	requests        map[string]persistedInputRequest
	pending         map[string]struct{}
	requestStateKey []byte
	requestStateAAD []byte
	next            int
	err             error
}

// persistedRequestState is portable protected state containing issued input
// contracts, the exact pending round, and prior client-supplied responses.
type persistedRequestState struct {
	Version   int                              `json:"version"`
	Responses map[string]jsontext.Value        `json:"responses,omitempty"`
	Requests  map[string]persistedInputRequest `json:"requests,omitempty"`
	Pending   []string                         `json:"pending,omitempty"`
}

type persistedInputRequest struct {
	Method string         `json:"method"`
	Params jsontext.Value `json:"params"`
}

type inputRequiredError struct {
	requests     mcp.InputRequestMap
	requestState string
}

type invalidClientInputError struct {
	err error
}

// InputRequired extracts an official SDK input-required result from err.
func InputRequired(err error) (mcp.InputRequestMap, string, bool) {
	var target *inputRequiredError
	if !errors.As(err, &target) {
		return nil, "", false
	}
	return target.requests, target.requestState, true
}

func (e sessionElicitor) Elicit(ctx context.Context, req mcpruntime.ElicitRequest) (*mcpruntime.ElicitResult, error) {
	if e.roundTrip == nil {
		return nil, mcpruntime.ErrElicitorUnavailable
	}
	params := &mcp.ElicitParams{
		ElicitationID:   req.ElicitationID,
		Message:         req.Message,
		Mode:            req.Mode,
		RequestedSchema: req.RequestedSchema,
		URL:             req.URL,
	}
	result, found, err := e.roundTrip.elicitationResponse(params)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, e.roundTrip.require(params)
	}
	return &mcpruntime.ElicitResult{
		Action:  result.Action,
		Content: result.Content,
	}, nil
}

func (r sessionProgressReporter) ReportProgress(ctx context.Context, token any, update mcpruntime.ProgressUpdate) error {
	if r.session == nil {
		return mcpruntime.ErrProgressReporterUnavailable
	}
	return r.session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: token,
		Progress:      update.Progress,
		Total:         update.Total,
		Message:       update.Message,
	})
}

func newInputRoundTrip(opts ClientFeaturesOptions) *inputRoundTrip {
	roundTrip := &inputRoundTrip{
		responses:       make(map[string]jsontext.Value),
		requests:        make(map[string]persistedInputRequest),
		pending:         make(map[string]struct{}),
		requestStateKey: append([]byte(nil), opts.RequestStateKey...),
	}
	aad, err := requestStateBinding(opts.RequestMethod, opts.RequestParams)
	if err != nil {
		roundTrip.err = err
		return roundTrip
	}
	roundTrip.requestStateAAD = aad
	if len(opts.InputResponses) > 0 && opts.RequestState == "" {
		roundTrip.err = invalidClientInput(errors.New("MCP input responses require server-issued requestState"))
		return roundTrip
	}
	if err := roundTrip.restoreRequestState(opts.RequestState); err != nil {
		roundTrip.err = invalidClientInput(err)
		return roundTrip
	}
	roundTrip.err = invalidClientInput(roundTrip.addInputResponses(opts.InputResponses))
	return roundTrip
}

func (r *inputRoundTrip) restoreRequestState(requestState string) error {
	if requestState == "" {
		return nil
	}
	data, err := decodeRequestState(requestState, r.requestStateKey, r.requestStateAAD)
	if err != nil {
		return fmt.Errorf("decode MCP requestState: %w", err)
	}
	var state persistedRequestState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode MCP requestState payload: %w", err)
	}
	if state.Version != requestStateVersion {
		return fmt.Errorf("unsupported MCP requestState version %d", state.Version)
	}
	for id, response := range state.Responses {
		r.responses[id] = response
	}
	for id, request := range state.Requests {
		r.requests[id] = request
	}
	for _, id := range state.Pending {
		if _, duplicate := r.pending[id]; duplicate {
			return fmt.Errorf("MCP requestState contains duplicate pending request %q", id)
		}
		if _, ok := r.requests[id]; !ok {
			return fmt.Errorf("MCP requestState pending request %q has no contract", id)
		}
		r.pending[id] = struct{}{}
	}
	return nil
}

func (r *inputRoundTrip) addInputResponses(responses mcp.InputResponseMap) error {
	if len(responses) > maxInputResponses {
		return fmt.Errorf("MCP input response count exceeds %d", maxInputResponses)
	}
	if len(responses) != len(r.pending) {
		return fmt.Errorf("MCP input response count %d does not match pending request count %d", len(responses), len(r.pending))
	}
	for id := range r.pending {
		if _, ok := responses[id]; !ok {
			return fmt.Errorf("MCP input response is missing pending request %q", id)
		}
	}
	for id, response := range responses {
		if _, ok := r.pending[id]; !ok {
			return fmt.Errorf("MCP input response %q was not requested", id)
		}
		elicitation, ok := response.(*mcp.ElicitResult)
		if !ok {
			return fmt.Errorf("MCP input response %q has type %T; want *mcp.ElicitResult", id, response)
		}
		if err := validateElicitResult(id, elicitation); err != nil {
			return err
		}
		data, err := json.Marshal(elicitation)
		if err != nil {
			return fmt.Errorf("encode MCP input response %q: %w", id, err)
		}
		r.responses[id] = data
	}
	if len(r.responses) > maxInputResponses {
		return fmt.Errorf("MCP input response count exceeds %d", maxInputResponses)
	}
	if len(r.responses) == 0 {
		return nil
	}
	_, err := encodePersistedRequestState(r.responses, r.requests, pendingInputRequestIDs(r.pending), r.requestStateKey, r.requestStateAAD)
	return err
}

func (r *inputRoundTrip) elicitationResponse(request mcp.InputRequest) (*mcp.ElicitResult, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	id := r.nextID()
	data, ok := r.responses[id]
	if !ok {
		return nil, false, nil
	}
	pending, ok := r.requests[id]
	if !ok {
		return nil, false, invalidClientInput(fmt.Errorf("MCP input response %q has no pending request", id))
	}
	current, err := persistInputRequest(request)
	if err != nil {
		return nil, false, err
	}
	if pending.Method != current.Method || !bytes.Equal(pending.Params, current.Params) {
		return nil, false, invalidClientInput(fmt.Errorf("MCP input response %q does not match the pending request", id))
	}
	var result mcp.ElicitResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false, invalidClientInput(fmt.Errorf("decode MCP input response %q: %w", id, err))
	}
	if err := validateElicitResult(id, &result); err != nil {
		return nil, false, invalidClientInput(err)
	}
	return &result, true, nil
}

func (r *inputRoundTrip) require(request mcp.InputRequest) error {
	id := inputRequestIDPrefix + fmt.Sprint(r.next-1)
	persisted, err := persistInputRequest(request)
	if err != nil {
		return err
	}
	requests := make(map[string]persistedInputRequest, len(r.requests)+1)
	for requestID, prior := range r.requests {
		requests[requestID] = prior
	}
	requests[id] = persisted
	requestState, err := encodePersistedRequestState(
		r.responses,
		requests,
		[]string{id},
		r.requestStateKey,
		r.requestStateAAD,
	)
	if err != nil {
		return err
	}
	return &inputRequiredError{
		requests:     mcp.InputRequestMap{id: request},
		requestState: requestState,
	}
}

func (r *inputRoundTrip) nextID() string {
	id := inputRequestIDPrefix + fmt.Sprint(r.next)
	r.next++
	return id
}

func (*inputRequiredError) InputRequired() {}

func (e *inputRequiredError) Error() string {
	return "MCP client input required"
}

func (*invalidClientInputError) InvalidClientInput() {}

func (e *invalidClientInputError) Error() string {
	return e.err.Error()
}

func (e *invalidClientInputError) Unwrap() error {
	return e.err
}

func invalidClientInput(err error) error {
	if err == nil {
		return nil
	}
	return &invalidClientInputError{err: err}
}

func encodePersistedRequestState(responses map[string]jsontext.Value, requests map[string]persistedInputRequest, pending []string, key, aad []byte) (string, error) {
	state := persistedRequestState{
		Version:   requestStateVersion,
		Responses: responses,
		Requests:  requests,
		Pending:   pending,
	}
	data, err := marshalCanonicalState(state)
	if err != nil {
		return "", fmt.Errorf("encode MCP requestState: %w", err)
	}
	encoded, err := encryptRequestState(data, key, aad)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxRequestStateBytes {
		return "", fmt.Errorf("encoded MCP requestState exceeds %d bytes", maxRequestStateBytes)
	}
	return encoded, nil
}

func pendingInputRequestIDs(pending map[string]struct{}) []string {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	return ids
}

func persistInputRequest(request mcp.InputRequest) (persistedInputRequest, error) {
	params, ok := request.(*mcp.ElicitParams)
	if !ok || params == nil {
		return persistedInputRequest{}, fmt.Errorf("unsupported MCP input request type %T", request)
	}
	data, err := marshalCanonicalState(params)
	if err != nil {
		return persistedInputRequest{}, fmt.Errorf("encode MCP pending input request: %w", err)
	}
	return persistedInputRequest{
		Method: "elicitation/create",
		Params: data,
	}, nil
}

func encryptRequestState(data, key, aad []byte) (string, error) {
	aead, err := requestStateAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate MCP requestState nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, data, aad)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func decodeRequestState(state string, key, aad []byte) ([]byte, error) {
	if len(state) > maxRequestStateBytes {
		return nil, fmt.Errorf("payload exceeds %d bytes", maxRequestStateBytes)
	}
	sealed, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil {
		return nil, err
	}
	aead, err := requestStateAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("encrypted payload is too short")
	}
	nonce := sealed[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, sealed[aead.NonceSize():], aad)
	if err != nil {
		return nil, fmt.Errorf("verify and decrypt payload: %w", err)
	}
	return plaintext, nil
}

func requestStateAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != requestStateKeyBytes {
		return nil, fmt.Errorf("MCP requestState key must be exactly %d bytes", requestStateKeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create MCP requestState cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create MCP requestState AEAD: %w", err)
	}
	return aead, nil
}

func requestStateBinding(method string, params any) ([]byte, error) {
	binding := struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{
		Method: method,
		Params: params,
	}
	data, err := marshalCanonicalState(binding)
	if err != nil {
		return nil, fmt.Errorf("encode MCP requestState binding: %w", err)
	}
	return data, nil
}

func marshalCanonicalState(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	canonical := jsontext.Value(data)
	if err := canonical.Canonicalize(); err != nil {
		return nil, err
	}
	return []byte(canonical), nil
}

func validateElicitResult(id string, result *mcp.ElicitResult) error {
	if result == nil {
		return fmt.Errorf("MCP input response %q is nil; want *mcp.ElicitResult", id)
	}
	switch result.Action {
	case elicitActionAccept:
		return nil
	case elicitActionDecline, elicitActionCancel:
		if len(result.Content) > 0 {
			return fmt.Errorf("MCP input response %q has content for %q action", id, result.Action)
		}
		return nil
	default:
		return fmt.Errorf("MCP input response %q has invalid elicitation action %q", id, result.Action)
	}
}
