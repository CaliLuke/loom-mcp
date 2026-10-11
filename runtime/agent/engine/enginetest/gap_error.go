package enginetest

// GapError reports an observed behavioral difference from the engine contract.
// Only this error type can satisfy an executable Factory.KnownGaps entry.
// Deadlines, cancellations and expired scenario contexts always fail the test.
type GapError struct {
	Err error
}

// Error describes the observed behavioral difference.
func (e *GapError) Error() string {
	return e.Err.Error()
}

// Unwrap preserves the underlying error, including deadline or cancellation errors.
func (e *GapError) Unwrap() error {
	return e.Err
}
