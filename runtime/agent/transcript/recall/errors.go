package recall

type (
	// Code is a stable model-facing recall outcome.
	Code string
	// Error carries a structured code without exposing parser or store details.
	Error struct {
		Code Code `json:"code"`
	}
)

const (
	// CodeBudgetExhausted reports an exhausted read or discovery allowance.
	CodeBudgetExhausted Code = "recall_budget_exhausted"
	// CodeNotFound reports an ID or selector absent from the reachable chain.
	CodeNotFound Code = "recall_not_found"
	// CodeUnauthorized reports session or authorizer denial.
	CodeUnauthorized Code = "recall_unauthorized"
	// CodeUnavailable reports content removed by retention.
	CodeUnavailable Code = "recall_unavailable"
	// CodeInvalidInput reports malformed arguments or cursor encoding.
	CodeInvalidInput Code = "recall_invalid_input"
	// CodeUnsupportedPart reports a non-text part pending multimodal recall.
	CodeUnsupportedPart Code = "recall_unsupported_part"
)

// Error returns the stable code, without caller-authored input fragments.
func (e *Error) Error() string {
	return string(e.Code)
}

func invalidInput() error {
	return &Error{Code: CodeInvalidInput}
}
