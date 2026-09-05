package codegen

import (
	"fmt"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

// collectTopLevelValidations extracts required fields and enum values for a top-level object payload.
func collectTopLevelValidations(attr *expr.AttributeExpr) ([]RequiredField, []EnumField, []DefaultField) {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return nil, nil, nil
	}
	if ut, ok := attr.Type.(expr.UserType); ok {
		return collectTopLevelValidations(ut.Attribute())
	}
	obj, ok := attr.Type.(*expr.Object)
	if !ok {
		return nil, nil, nil
	}
	req := []RequiredField{}
	fields, enums, defaults := collectTopLevelValidationFields(obj)
	if attr.Validation != nil {
		for _, name := range attr.Validation.Required {
			if field, ok := fields[name]; ok {
				req = append(req, RequiredField{Name: name, AllowsNull: expr.AllowsNull(field)})
			}
		}
	}
	reqSet := map[string]struct{}{}
	if attr.Validation != nil {
		for _, n := range attr.Validation.Required {
			reqSet[n] = struct{}{}
		}
	}
	for i := range enums {
		_, isReq := reqSet[enums[i].Name]
		hasDefault := fields[enums[i].Name] != nil && fields[enums[i].Name].DefaultValue != nil
		enums[i].Pointer = !isReq && !hasDefault
	}
	return req, enums, defaults
}

func collectTopLevelValidationFields(obj *expr.Object) (map[string]*expr.AttributeExpr, []EnumField, []DefaultField) {
	fields := map[string]*expr.AttributeExpr{}
	enums := []EnumField{}
	defaults := []DefaultField{}
	for _, nat := range *obj {
		fields[nat.Name] = nat.Attribute
		if nat.Attribute.DefaultValue != nil {
			if def, ok := topLevelDefaultField(nat.Name, nat.Attribute); ok {
				defaults = append(defaults, def)
			}
		}
		if vals := collectEnumValues(nat.Attribute); len(vals) > 0 {
			enums = append(enums, EnumField{Name: nat.Name, Values: vals})
		}
	}
	return fields, enums, defaults
}

func collectEnumValues(attr *expr.AttributeExpr) []string {
	if attr == nil || attr.Validation == nil || len(attr.Validation.Values) == 0 {
		return nil
	}
	vals := make([]string, 0, len(attr.Validation.Values))
	for _, v := range attr.Validation.Values {
		vals = append(vals, fmt.Sprint(v))
	}
	if len(vals) == 0 {
		return nil
	}
	return vals
}

func topLevelDefaultField(name string, attr *expr.AttributeExpr) (DefaultField, bool) {
	if attr == nil || attr.Type == nil || attr.DefaultValue == nil {
		return DefaultField{}, false
	}
	goName := codegen.Goify(name, true)
	actual := attr.Type
	if ut, ok := actual.(expr.UserType); ok {
		actual = ut.Attribute().Type
	}
	switch actual {
	case expr.String:
		def, ok := attr.DefaultValue.(string)
		if !ok {
			return DefaultField{}, false
		}
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%q", def), Kind: "string"}, true
	case expr.Boolean:
		def, ok := attr.DefaultValue.(bool)
		if !ok {
			return DefaultField{}, false
		}
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%t", def), Kind: "bool"}, true
	case expr.Int, expr.Int32, expr.Int64, expr.UInt, expr.UInt32, expr.UInt64:
		return DefaultField{Name: name, GoName: goName, Literal: fmt.Sprintf("%v", attr.DefaultValue), Kind: "int"}, true
	default:
		return DefaultField{}, false
	}
}
