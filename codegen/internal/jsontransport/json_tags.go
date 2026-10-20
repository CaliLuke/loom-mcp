package jsontransport

import (
	"strings"

	"github.com/CaliLuke/loom/expr"
)

// CloneWithJSONTags returns a deep copy of the provided attribute where:
//   - all object fields (including nested objects) carry json struct tags whose
//     names match the DSL field names, and
//   - any `struct:pkg:*` locator metadata is removed so synthesized transport
//     types are treated as local to the specs package.
//
// Transport types are model-facing: they must encode/decode using the JSON
// schema property names and must preserve missing-vs-zero semantics via pointer
// primitives (controlled by NameScope.GoTypeDef's ptr flag, not by this helper).
//
// Existing struct:tag:json metadata is preserved so DSL authors can override
// tags explicitly using Meta or design-specific helpers.
func CloneWithJSONTags(att *expr.AttributeExpr) *expr.AttributeExpr {
	if att == nil || att.Type == nil || att.Type == expr.Empty {
		return att
	}

	// Work on a deep copy so that we never mutate the shared Loom design
	// expressions used by other generators.
	cloned := expr.DupAtt(att)

	normalizeTransportAttrRecursive(cloned)

	return cloned
}

// normalizeTransportAttrRecursive:
//   - removes struct:pkg:* locators so nested user types can be materialized
//     locally, and
//   - ensures object fields carry json:name tags matching the DSL field names.
func normalizeTransportAttrRecursive(att *expr.AttributeExpr) {
	normalizeTransportAttrRecursiveSeen(att, make(map[*expr.AttributeExpr]struct{}))
}

// normalizeTransportAttrRecursiveSeen preserves one normalization pass per
// attribute node so recursive user types terminate while shared nested fields
// still receive their edge-level JSON tags from the parent object walk.
func normalizeTransportAttrRecursiveSeen(att *expr.AttributeExpr, seen map[*expr.AttributeExpr]struct{}) {
	if att == nil || att.Type == nil || att.Type == expr.Empty {
		return
	}
	if _, ok := seen[att]; ok {
		return
	}
	seen[att] = struct{}{}

	if len(att.Meta) > 0 {
		for k := range att.Meta {
			if strings.HasPrefix(k, "struct:pkg:") {
				delete(att.Meta, k)
			}
		}
		if len(att.Meta) == 0 {
			att.Meta = nil
		}
	}

	obj := expr.AsObject(att.Type)
	if obj == nil {
		switch dt := att.Type.(type) {
		case expr.UserType:
			normalizeTransportAttrRecursiveSeen(dt.Attribute(), seen)
		case *expr.Array:
			normalizeTransportAttrRecursiveSeen(dt.ElemType, seen)
		case *expr.Map:
			normalizeTransportAttrRecursiveSeen(dt.KeyType, seen)
			normalizeTransportAttrRecursiveSeen(dt.ElemType, seen)
		case *expr.Union:
			for _, nat := range dt.Values {
				if nat == nil {
					continue
				}
				normalizeTransportAttrRecursiveSeen(nat.Attribute, seen)
			}
		}
		return
	}

	for _, nat := range *obj {
		if nat == nil || nat.Attribute == nil {
			continue
		}
		if nat.Attribute.Meta == nil {
			nat.Attribute.Meta = make(expr.MetaExpr)
		}
		// Only set the json name when no explicit json tag metadata was provided
		// in the DSL.
		//
		// Use "struct:tag:json:name" (not "struct:tag:json") so Loom can append
		// ",omitempty" automatically for fields that are not required by their
		// parent object.
		if _, ok := nat.Attribute.Meta["struct:tag:json"]; !ok {
			if _, ok := nat.Attribute.Meta["struct:tag:json:name"]; !ok {
				nat.Attribute.Meta["struct:tag:json:name"] = []string{nat.Name}
			}
		}
		normalizeTransportAttrRecursiveSeen(nat.Attribute, seen)
	}
}
