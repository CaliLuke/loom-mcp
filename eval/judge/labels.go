package judge

// Label classifies whether the rendered subject supports a claim.
type Label string

const (
	// Entailed means the subject supports the claim.
	Entailed Label = "entailed"
	// Contradicted means the subject conflicts with the claim.
	Contradicted Label = "contradicted"
	// NotAddressed means the subject does not address the claim.
	NotAddressed Label = "not_addressed"
	// Indeterminate means the subject addresses the claim but cannot settle it.
	Indeterminate Label = "indeterminate"
)

// Valid reports whether l is one of the four supported labels.
func (l Label) Valid() bool {
	switch l {
	case Entailed, Contradicted, NotAddressed, Indeterminate:
		return true
	default:
		return false
	}
}
