package agenttest

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ArgsEqual compares payloads encoded through the tool codec after JSON
// canonicalization. Want is encoded once. Its encoding error is returned on
// every match, becoming an unmatched-call StubError when used with When.
func ArgsEqual[P, R any](tool tools.TypedTool[P, R], want P) ArgMatch[P] {
	wanted, wantErr := canonicalStubArgs(tool.Payload, want)
	return func(received P) error {
		if wantErr != nil {
			return fmt.Errorf("ArgsEqual: encode want: %w", wantErr)
		}
		actual, err := canonicalStubArgs(tool.Payload, received)
		if err != nil {
			return fmt.Errorf("ArgsEqual: encode received: %w", err)
		}
		if !bytes.Equal(wanted, actual) {
			return fmt.Errorf("ArgsEqual: want %s, received %s", wanted, actual)
		}
		return nil
	}
}

// ArgsWhere matches a payload using a predicate and describes rejected calls.
func ArgsWhere[P any](description string, predicate func(P) bool) ArgMatch[P] {
	return func(payload P) error {
		if predicate == nil {
			return errors.New("ArgsWhere: nil predicate")
		}
		if !predicate(payload) {
			return errors.New(description)
		}
		return nil
	}
}

func canonicalStubArgs[P any](codec tools.JSONCodec[P], value P) ([]byte, error) {
	if codec.ToJSON == nil {
		return nil, errors.New("payload encoder is required")
	}
	encoded, err := codec.ToJSON(value)
	if err != nil {
		return nil, err
	}
	owned := jsontext.Value(bytes.Clone(encoded))
	if err := owned.Canonicalize(jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)); err != nil {
		return nil, err
	}
	return owned, nil
}

func matchStubArgs[P any](matches []ArgMatch[P], payload P) error {
	for _, match := range matches {
		if match == nil {
			return errors.New("nil argument matcher")
		}
		if err := match(payload); err != nil {
			return err
		}
	}
	return nil
}
