// Package cancellation classifies bounded error graphs without relying on the
// permissive traversal performed by errors.Is and errors.As.
package cancellation

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"go.temporal.io/sdk/temporal"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type errorIdentity struct {
	typ reflect.Type
	ptr uintptr
}

// Inspection is the result of one bounded error-graph traversal.
type Inspection struct {
	Valid                  bool
	OnlyCancellation       bool
	ContainsCancellation   bool
	OnlyContextTermination bool
	Matched                bool
}

// Matcher identifies graph nodes during an inspection. The text is the safe
// result of the node's first Error call. A matcher panic makes the graph invalid.
type Matcher func(err error, text string) bool

type graphClassifier struct {
	active        map[errorIdentity]struct{}
	leaves        int
	cancellations int
	terminations  int
	visits        int
	matcher       Matcher
	matched       bool
}

const (
	maxDepth    = 64
	maxVisits   = 256
	maxChildren = 256
)

var (
	// ErrInvalidErrorGraph is the static replacement for an error graph that is
	// unsafe to inspect or format.
	ErrInvalidErrorGraph = errors.New("invalid error graph")
)

// Only reports whether every leaf in a bounded error graph is an exact context
// or Temporal cancellation. Malformed, cyclic, or oversized graphs are ordinary
// failures rather than cancellations.
func Only(err error) bool {
	return Inspect(err, nil).OnlyCancellation
}

// Contains reports whether a valid bounded error graph has at least one exact
// context or Temporal cancellation leaf.
func Contains(err error) bool {
	return Inspect(err, nil).ContainsCancellation
}

// OnlyContextTermination reports whether every leaf in a valid bounded error
// graph is an exact cancellation or context deadline.
func OnlyContextTermination(err error) bool {
	return Inspect(err, nil).OnlyContextTermination
}

// Valid reports whether err has a bounded, acyclic graph with safe unwrap
// behavior. Nil is not an error graph.
func Valid(err error) bool {
	return Inspect(err, nil).Valid
}

// Exact reports whether err and target have the same comparable dynamic type
// and value. It never compares an error with an uncomparable dynamic value.
func Exact(err, target error) bool {
	errType := reflect.TypeOf(err)
	targetType := reflect.TypeOf(target)
	if errType == nil || errType != targetType || !errType.Comparable() {
		return false
	}
	//nolint:errorlint // Exact intentionally compares one already-unwrapped node.
	return err == target
}

// Sanitize replaces invalid error graphs with a static safe failure.
func Sanitize(err error) error {
	if err == nil || Inspect(err, nil).Valid {
		return err
	}
	return ErrInvalidErrorGraph
}

// Inspect classifies err and optionally matches nodes in one bounded traversal.
// Nil, malformed, cyclic, oversized, or panicking graphs are invalid.
func Inspect(err error, matcher Matcher) Inspection {
	if err == nil {
		return Inspection{}
	}
	classifier := graphClassifier{
		active:  make(map[errorIdentity]struct{}),
		matcher: matcher,
	}
	if !classifier.visit(err, 0) {
		return Inspection{}
	}
	return Inspection{
		Valid:                  true,
		OnlyCancellation:       classifier.leaves > 0 && classifier.leaves == classifier.cancellations,
		ContainsCancellation:   classifier.cancellations > 0,
		OnlyContextTermination: classifier.leaves > 0 && classifier.leaves == classifier.terminations,
		Matched:                classifier.matched,
	}
}

func safeErrorText(err error) (text string, safe bool) {
	defer func() {
		if recover() != nil {
			text = ""
			safe = false
		}
	}()
	return err.Error(), true
}

func (c *graphClassifier) visit(err error, depth int) bool {
	if err == nil {
		return true
	}
	if isNilErrorValue(err) || depth > maxDepth {
		return false
	}
	c.visits++
	if c.visits > maxVisits {
		return false
	}
	if identity, ok := identityOf(err); ok {
		if _, exists := c.active[identity]; exists {
			return false
		}
		c.active[identity] = struct{}{}
		defer delete(c.active, identity)
	}
	errorText, safe := safeErrorText(err)
	if !safe {
		return false
	}
	matched, safe := matchError(c.matcher, err, errorText)
	if !safe {
		return false
	}
	c.matched = c.matched || matched
	children, unwrapErr := unwrapChildren(err)
	if unwrapErr != nil {
		return false
	}
	if hasNonNilError(children) {
		for _, child := range children {
			if child != nil && !c.visit(child, depth+1) {
				return false
			}
		}
		return true
	}
	c.leaves++
	if isCancellationLeaf(err) {
		c.cancellations++
		c.terminations++
	} else if isContextTerminationLeaf(err) {
		c.terminations++
	}
	return true
}

func matchError(matcher Matcher, err error, errorText string) (matched bool, safe bool) {
	if matcher == nil {
		return false, true
	}
	defer func() {
		if recover() != nil {
			matched = false
			safe = false
		}
	}()
	return matcher(err, errorText), true
}

func hasNonNilError(errs []error) bool {
	for _, err := range errs {
		if err != nil {
			return true
		}
	}
	return false
}

func isCancellationLeaf(err error) bool {
	if Exact(err, context.Canceled) {
		return true
	}
	//nolint:errorlint // Temporal cancellation is recognized only at an exact leaf.
	_, ok := err.(*temporal.CanceledError)
	return ok
}

func isContextTerminationLeaf(err error) bool {
	if Exact(err, context.DeadlineExceeded) {
		return true
	}
	code, ok := grpcLeafCode(err)
	if !ok {
		return false
	}
	return code == grpcCodes.Canceled || code == grpcCodes.DeadlineExceeded
}

func grpcLeafCode(err error) (code grpcCodes.Code, ok bool) {
	defer func() {
		if recover() != nil {
			code = grpcCodes.Unknown
			ok = false
		}
	}()
	statusCarrier, ok := err.(interface{ GRPCStatus() *grpcStatus.Status })
	if !ok {
		return grpcCodes.Unknown, false
	}
	status := statusCarrier.GRPCStatus()
	if status == nil {
		return grpcCodes.Unknown, false
	}
	return status.Code(), true
}

func identityOf(err error) (errorIdentity, bool) {
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		if value.IsNil() {
			return errorIdentity{}, false
		}
		return errorIdentity{typ: value.Type(), ptr: value.Pointer()}, true
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Array, reflect.Interface, reflect.String, reflect.Struct:
		return errorIdentity{}, false
	}
	panic(fmt.Sprintf("cancellation: unhandled reflect kind %s", value.Kind()))
}

func isNilErrorValue(err error) bool {
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Array, reflect.Interface, reflect.String, reflect.Struct:
		return false
	}
	panic(fmt.Sprintf("cancellation: unhandled reflect kind %s", value.Kind()))
}

func unwrapChildren(err error) (children []error, resultErr error) {
	defer func() {
		if recover() != nil {
			children = nil
			resultErr = fmt.Errorf("unwrap panicked")
		}
	}()
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children = joined.Unwrap()
		if len(children) > maxChildren {
			return nil, fmt.Errorf("unwrap returned %d children, maximum is %d", len(children), maxChildren)
		}
		for _, child := range children {
			if child == nil {
				return nil, fmt.Errorf("unwrap returned a nil child")
			}
		}
		return children, nil
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return []error{wrapped.Unwrap()}, nil
	}
	return nil, nil
}
