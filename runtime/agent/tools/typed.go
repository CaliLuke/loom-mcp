package tools

// TypedTool describes a tool's identifier and its typed JSON codecs.
type TypedTool[P, R any] struct {
	Name    Ident
	Payload JSONCodec[P]
	Result  JSONCodec[R]
}

// Named identifies a tool without requiring its payload or result types.
type Named interface {
	Ident() Ident
}

// Ident returns the canonical identifier of the tool.
func (t TypedTool[P, R]) Ident() Ident {
	return t.Name
}
