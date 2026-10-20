package jsontransport

import (
	"sort"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

// LocalizeNestedTypes clones a normalized transport graph and returns its nested
// named types in deterministic order. The root retains its caller-owned name.
func LocalizeNestedTypes(att *expr.AttributeExpr) (*expr.AttributeExpr, []*expr.UserTypeExpr, error) {
	if att == nil || att.Type == nil || att.Type == expr.Empty {
		return att, nil, nil
	}
	cloned := expr.DupAtt(att)
	localByID := make(map[string]*expr.UserTypeExpr)
	err := codegen.Walk(cloned, func(a *expr.AttributeExpr) error {
		if a == nil || a.Type == nil || a.Type == expr.Empty {
			return nil
		}
		// Do not rewrite the root type itself; only localize nested user types.
		if a == cloned {
			return nil
		}
		ut, ok := a.Type.(expr.UserType)
		if !ok || ut == nil {
			return nil
		}

		id := ut.ID()
		if cached, ok := localByID[id]; ok {
			a.Type = cached
			return nil
		}
		// Preserve the authored name until NameScope assigns Go identifiers.
		// Pre-Goifying would collapse distinct names such as foo_bar and fooBar
		// before the scope can assign collision suffixes.
		base := expr.DupAtt(ut.Attribute())
		normalizeTransportAttrRecursive(base)
		delete(base.Meta, "struct:type:name")
		local := &expr.UserTypeExpr{
			AttributeExpr: base,
			TypeName:      ut.Name() + "Transport",
		}
		localByID[id] = local
		a.Type = local
		return nil
	})

	if err != nil {
		return nil, nil, err
	}
	localIDs := make([]string, 0, len(localByID))
	for id := range localByID {
		localIDs = append(localIDs, id)
	}
	sort.Strings(localIDs)
	locals := make([]*expr.UserTypeExpr, 0, len(localIDs))
	for _, id := range localIDs {
		locals = append(locals, localByID[id])
	}
	return cloned, locals, nil
}
