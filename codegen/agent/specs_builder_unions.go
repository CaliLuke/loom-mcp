package codegen

import (
	"sort"

	"github.com/CaliLuke/loom-mcp/v2/codegen/internal/jsontransport"

	"github.com/CaliLuke/loom/codegen"
	goaexpr "github.com/CaliLuke/loom/expr"
)

// collectUnionSumTypes walks att and records all union sum types referenced by
// the attribute graph. Union types are keyed by hash so they are emitted once
// per specs package.
func (b *toolSpecBuilder) collectUnionSumTypes(scope *codegen.NameScope, att *goaexpr.AttributeExpr) {
	if b == nil || scope == nil || att == nil {
		return
	}
	if b.unions == nil {
		b.unions = make(map[string]*jsontransport.UnionType)
	}
	seen := make(map[string]struct{})
	jsontransport.CollectUnions(att, scope, b.unions, seen, false)
}

// collectTransportUnionSumTypes walks a transport-localized attribute graph and
// records all union sum types referenced by it. This is used to emit
// toolset-local http/unions.go without leaking service gen/types references.
func (b *toolSpecBuilder) collectTransportUnionSumTypes(scope *codegen.NameScope, att *goaexpr.AttributeExpr) {
	if b == nil || scope == nil || att == nil {
		return
	}
	if b.transportUnions == nil {
		b.transportUnions = make(map[string]*jsontransport.UnionType)
	}
	seen := make(map[string]struct{})
	jsontransport.CollectUnions(att, scope, b.transportUnions, seen, true)
}

// unionTypes returns the collected union sum types in deterministic order.
func (b *toolSpecBuilder) unionTypes() []*jsontransport.UnionType {
	if b == nil || len(b.unions) == 0 {
		return nil
	}
	out := make([]*jsontransport.UnionType, 0, len(b.unions))
	for _, u := range b.unions {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// transportUnionTypes returns the collected transport union sum types in
// deterministic order.
func (b *toolSpecBuilder) transportUnionTypes() []*jsontransport.UnionType {
	if b == nil || len(b.transportUnions) == 0 {
		return nil
	}
	out := make([]*jsontransport.UnionType, 0, len(b.transportUnions))
	for _, u := range b.transportUnions {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}
