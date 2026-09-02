package model

import (
	"context"
	"errors"
	"reflect"
)

type (
	validatedClient struct {
		provider Provider
	}

	validatedCountingClient struct {
		*validatedClient
		counter TokenCounter
	}
)

// NewClient constructs a model client that applies one immutable request
// contract to every raw provider completion and stream.
func NewClient(provider Provider) (Client, error) {
	if isNilModelValue(provider) {
		return nil, errors.New("model provider is required")
	}
	client := &validatedClient{provider: provider}
	if counter, ok := provider.(TokenCounter); ok && !isNilModelValue(counter) {
		return &validatedCountingClient{validatedClient: client, counter: counter}, nil
	}
	return client, nil
}

// Complete validates the request before provider work and accepts only output
// that satisfies the resulting request contract.
func (c *validatedClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	request, err := cloneModelRequest(req)
	if err != nil {
		return nil, err
	}
	request.Stream = false
	contract, err := newRequestContract(request)
	if err != nil {
		return nil, err
	}
	resp, err := c.provider.Complete(ctx, request)
	if err != nil {
		return nil, err
	}
	return contract.ValidateResponse(resp)
}

// Stream validates the request before provider work and wraps the raw stream in
// request-scoped protocol validation.
func (c *validatedClient) Stream(ctx context.Context, req *Request) (ValidatedStreamer, error) {
	request, err := cloneModelRequest(req)
	if err != nil {
		return nil, err
	}
	request.Stream = true
	contract, err := newRequestContract(request)
	if err != nil {
		return nil, err
	}
	stream, err := c.provider.Stream(ctx, request)
	if err != nil {
		if !isNilModelValue(stream) {
			return nil, errors.Join(err, stream.Close())
		}
		return nil, err
	}
	validated, err := contract.validateStream(ctx, stream)
	if err != nil {
		if !isNilModelValue(stream) {
			return nil, errors.Join(err, stream.Close())
		}
		return nil, err
	}
	return validated, nil
}

// CountTokens validates the same request contract before delegating to the
// provider's optional exact token-counting capability.
func (c *validatedCountingClient) CountTokens(ctx context.Context, req *Request) (TokenCount, error) {
	request, err := cloneModelRequest(req)
	if err != nil {
		return TokenCount{}, err
	}
	if _, err := newRequestContract(request); err != nil {
		return TokenCount{}, err
	}
	count, err := c.counter.CountTokens(ctx, request)
	if err != nil {
		return TokenCount{}, err
	}
	if count.InputTokens < 0 {
		return TokenCount{}, errors.New("model provider returned a negative input token count")
	}
	return count, nil
}

func isNilModelValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Array,
		reflect.String,
		reflect.Struct,
		reflect.UnsafePointer:
		return false
	}
	panic("unreachable reflect kind")
}
