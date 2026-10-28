package jsontransport

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/uniongen"
	"github.com/CaliLuke/loom/codegen/unionjson"
	"github.com/CaliLuke/loom/expr"
)

// UnionType couples Loom's shared union renderer with its local validators.
type UnionType struct {
	uniongen.Type
	// Validators contains the local named validation functions used by matching.
	Validators []string
}

// CollectUnions records every reachable union once in the supplied registry.
func CollectUnions(
	att *expr.AttributeExpr,
	scope *codegen.NameScope,
	unions map[string]*UnionType,
	seen map[string]struct{},
	pointer bool,
) {
	if att == nil || att.Type == nil || att.Type == expr.Empty {
		return
	}
	switch dt := att.Type.(type) {
	case expr.UserType:
		if dt == nil {
			return
		}
		if _, ok := seen[dt.ID()]; ok {
			return
		}
		seen[dt.ID()] = struct{}{}
		CollectUnions(dt.Attribute(), scope, unions, seen, pointer)
	case *expr.Object:
		for _, nat := range *dt {
			if nat == nil {
				continue
			}
			CollectUnions(nat.Attribute, scope, unions, seen, pointer)
		}
	case *expr.Array:
		CollectUnions(dt.ElemType, scope, unions, seen, pointer)
	case *expr.Map:
		CollectUnions(dt.KeyType, scope, unions, seen, pointer)
		CollectUnions(dt.ElemType, scope, unions, seen, pointer)
	case *expr.Union:
		hash := dt.Hash()
		if _, ok := unions[hash]; !ok {
			unions[hash] = buildUnionTypeData(dt, scope, pointer)
		}
		for _, nat := range dt.Values {
			if nat == nil {
				continue
			}
			CollectUnions(nat.Attribute, scope, unions, seen, pointer)
		}
	}
}

func buildUnionTypeData(u *expr.Union, scope *codegen.NameScope, pointer bool) *UnionType {
	att := &expr.AttributeExpr{Type: u}
	name := scope.GoTypeName(att)
	kindName := scope.Unique(name + "Kind")

	fields := make([]*uniongen.Field, 0, len(u.Values))
	for _, nat := range u.Values {
		if nat == nil || nat.Attribute == nil {
			continue
		}
		fieldName := codegen.Goify(nat.Name, true)
		var pkg string
		if tloc := codegen.UserTypeLocation(nat.Attribute.Type); tloc != nil {
			pkg = tloc.PackageName()
		}
		fieldType := scope.GoFullTypeRef(nat.Attribute, pkg)
		kindConst := kindName + codegen.Goify(nat.Name, true)
		fields = append(fields, &uniongen.Field{
			Name:       nat.Name,
			KindConst:  kindConst,
			FieldName:  fieldName,
			FieldType:  fieldType,
			TypeTag:    expr.UnionVariantTag(nat),
			AllowsNull: expr.AllowsNull(nat.Attribute),
		})
	}

	result := &UnionType{Type: uniongen.Type{
		Name:     name,
		KindName: kindName,
		Fields:   fields,
		TypeKey:  u.GetTypeKey(),
		ValueKey: u.GetValueKey(),
		Untagged: u.Untagged,
		JSONOnly: true,
	}}
	if u.Untagged {
		ctx := codegen.NewAttributeContext(pointer, false, !pointer, "", scope)
		ctx.ValidationPrefix = "validate" + name
		branches := make([]*unionjson.Branch, len(fields))
		for i, field := range fields {
			branches[i] = &unionjson.Branch{Type: field.FieldType, Field: field.FieldName, Kind: field.KindConst,
				Validate: codegen.ValidationCode(u.Values[i].Attribute, nil, ctx, true, false, false, "v")}
		}
		result.JSON = unionjson.Analyze(name, u, branches, expr.AttributeName, false)
		validators := make(map[string]struct{})
		if err := codegen.Walk(att, func(a *expr.AttributeExpr) error {
			if ut, ok := a.Type.(expr.UserType); ok {
				localCtx := ctx.Dup()
				localCtx.Pointer = pointer && !expr.IsPrimitive(ut)
				validator := codegen.NamedValidationFunction(a, localCtx, "body")
				if _, emitted := validators[validator.Name]; emitted {
					return nil
				}
				validators[validator.Name] = struct{}{}
				body := codegen.ValidationCode(ut.Attribute(), ut, validator.Context(localCtx), true, expr.IsAlias(ut), false, "body")
				if body != "" {
					result.Validators = append(result.Validators, validator.Definition(body))
				}
			}
			return nil
		}); err != nil {
			panic(err)
		}
	}
	return result
}
