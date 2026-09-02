package cancellation

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type (
	singleCycleError struct {
		next error
	}

	joinedCycleError struct {
		children []error
	}

	typedNilError struct{}

	manyChildrenError struct {
		children []error
	}

	panickingUnwrapError struct{}

	panickingErrorText struct{}

	panickingGRPCStatusError struct{}

	safeWrapperError struct {
		child error
	}

	statefulUnwrapError struct {
		calls int
	}

	uncomparableSliceError []string

	uncomparableMapError map[string]string

	uncomparableStructError struct {
		values []string
	}
)

func (*singleCycleError) Error() string {
	return "single cycle"
}

func (e *singleCycleError) Unwrap() error {
	return e.next
}

func (*joinedCycleError) Error() string {
	return "joined cycle"
}

func (e *joinedCycleError) Unwrap() []error {
	return e.children
}

func (*typedNilError) Error() string {
	return "typed nil"
}

func (*manyChildrenError) Error() string {
	return "many children"
}

func (e *manyChildrenError) Unwrap() []error {
	return e.children
}

func (*panickingUnwrapError) Error() string {
	return "panicking unwrap"
}

func (*panickingUnwrapError) Unwrap() error {
	panic("broken error")
}

func (*panickingErrorText) Error() string {
	panic("broken error text")
}

func (*panickingGRPCStatusError) Error() string {
	return "panicking grpc status"
}

func (*panickingGRPCStatusError) GRPCStatus() *grpcStatus.Status {
	panic("broken grpc status")
}

func (*safeWrapperError) Error() string {
	return "safe wrapper"
}

func (e *safeWrapperError) Unwrap() error {
	return e.child
}

func (*statefulUnwrapError) Error() string {
	return "stateful unwrap"
}

func (e *statefulUnwrapError) Unwrap() error {
	e.calls++
	if e.calls > 1 {
		panic("unwrap called more than once")
	}
	return context.Canceled
}

func (uncomparableSliceError) Error() string {
	return "uncomparable slice"
}

func (uncomparableMapError) Error() string {
	return "uncomparable map"
}

func (uncomparableStructError) Error() string {
	return "uncomparable struct"
}

func TestOnlyClassifiesCompleteErrorGraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", want: false},
		{name: "context cancellation", err: context.Canceled, want: true},
		{name: "wrapped context cancellation", err: fmt.Errorf("wrapped: %w", context.Canceled), want: true},
		{name: "temporal cancellation", err: temporal.NewCanceledError("canceled"), want: true},
		{name: "joined cancellations", err: errors.Join(context.Canceled, temporal.NewCanceledError("canceled")), want: true},
		{name: "mixed cancellation and failure", err: errors.Join(context.Canceled, errors.New("cleanup failed")), want: false},
		{name: "deadline", err: context.DeadlineExceeded, want: false},
		{name: "ordinary failure", err: errors.New("failed"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, Only(test.err))
		})
	}
}

func TestContainsDistinguishesMixedCancellation(t *testing.T) {
	t.Parallel()

	require.True(t, Contains(context.Canceled))
	require.True(t, Contains(errors.Join(context.Canceled, errors.New("cleanup failed"))))
	require.False(t, Contains(context.DeadlineExceeded))
	single := &singleCycleError{}
	single.next = single
	require.False(t, Contains(single))
}

func TestOnlyContextTermination(t *testing.T) {
	t.Parallel()

	require.True(t, OnlyContextTermination(context.Canceled))
	require.True(t, OnlyContextTermination(context.DeadlineExceeded))
	require.True(t, OnlyContextTermination(errors.Join(context.Canceled, context.DeadlineExceeded)))
	require.True(t, OnlyContextTermination(errors.Join(
		grpcStatus.Error(grpcCodes.Canceled, "canceled"),
		grpcStatus.Error(grpcCodes.DeadlineExceeded, "deadline"),
	)))
	require.False(t, OnlyContextTermination(errors.Join(context.Canceled, errors.New("cleanup failed"))))
	require.False(t, OnlyContextTermination(errors.Join(grpcStatus.Error(grpcCodes.Canceled, "canceled"), errors.New("cleanup failed"))))
	require.NotPanics(t, func() {
		require.False(t, OnlyContextTermination(&panickingErrorText{}))
		require.False(t, OnlyContextTermination(&panickingGRPCStatusError{}))
	})
	require.False(t, OnlyContextTermination(nil))
}

func TestOnlyRejectsMalformedAndOversizedGraphs(t *testing.T) {
	t.Parallel()

	single := &singleCycleError{}
	single.next = single
	joined := &joinedCycleError{}
	joined.children = []error{context.Canceled, joined}
	nilChild := &joinedCycleError{children: []error{context.Canceled, nil}}
	nestedPanic := &safeWrapperError{child: &joinedCycleError{children: []error{context.Canceled, &panickingErrorText{}}}}
	children := make([]error, maxChildren+1)
	for index := range children {
		children[index] = context.Canceled
	}
	var typedNil *typedNilError
	tests := []error{
		typedNil,
		single,
		joined,
		nilChild,
		nestedPanic,
		&manyChildrenError{children: children},
		&panickingUnwrapError{},
		&panickingErrorText{},
		deepCancellation(maxDepth + 1),
	}
	for _, err := range tests {
		require.False(t, Only(err))
		require.False(t, Valid(err))
	}
	require.True(t, Valid(errors.New("ordinary")))
}

func TestInspectTraversesOnceAndBoundsMatcher(t *testing.T) {
	t.Parallel()

	stateful := &statefulUnwrapError{}
	inspection := Inspect(stateful, func(err error, _ string) bool {
		return Exact(err, context.Canceled)
	})
	require.True(t, inspection.Valid)
	require.True(t, inspection.OnlyCancellation)
	require.True(t, inspection.ContainsCancellation)
	require.True(t, inspection.Matched)
	require.Equal(t, 1, stateful.calls)

	require.NotPanics(t, func() {
		inspection = Inspect(errors.New("ordinary"), func(error, string) bool {
			panic("broken matcher")
		})
	})
	require.False(t, inspection.Valid)
}

func TestSanitizeReplacesInvalidGraphs(t *testing.T) {
	t.Parallel()

	ordinary := errors.New("ordinary")
	require.Same(t, ordinary, Sanitize(ordinary))
	require.NoError(t, Sanitize(nil))

	cyclic := &singleCycleError{}
	cyclic.next = cyclic
	require.ErrorIs(t, Sanitize(cyclic), ErrInvalidErrorGraph)
	require.ErrorIs(t, Sanitize(&panickingUnwrapError{}), ErrInvalidErrorGraph)
	require.ErrorIs(t, Sanitize(&panickingErrorText{}), ErrInvalidErrorGraph)
}

func TestClassificationAcceptsUncomparableErrorValues(t *testing.T) {
	t.Parallel()

	tests := []error{
		uncomparableSliceError{"value"},
		uncomparableMapError{"key": "value"},
		uncomparableStructError{values: []string{"value"}},
	}
	for _, err := range tests {
		t.Run(err.Error(), func(t *testing.T) {
			t.Parallel()
			require.NotPanics(t, func() {
				inspection := Inspect(err, nil)
				require.True(t, inspection.Valid)
				require.False(t, inspection.OnlyCancellation)
				require.False(t, inspection.ContainsCancellation)
				require.False(t, inspection.OnlyContextTermination)
				require.False(t, Only(err))
				require.False(t, Contains(err))
				require.False(t, OnlyContextTermination(err))
				require.EqualError(t, Sanitize(err), err.Error())
				require.False(t, Exact(err, context.Canceled))
			})
		})
	}
}

func deepCancellation(depth int) error {
	var err error = context.Canceled
	for range depth {
		err = fmt.Errorf("wrapped: %w", err)
	}
	return err
}
