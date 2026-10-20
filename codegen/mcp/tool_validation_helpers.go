package codegen

import (
	"fmt"

	"github.com/CaliLuke/loom/expr"
)

// collectTopLevelValidations extracts required fields and enum values for a top-level object payload.
func collectTopLevelValidations(attr *expr.AttributeExpr) ([]RequiredField, []EnumField) {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return nil, nil
	}
	if ut, ok := attr.Type.(expr.UserType); ok {
		return collectTopLevelValidations(ut.Attribute())
	}
	obj, ok := attr.Type.(*expr.Object)
	if !ok {
		return nil, nil
	}
	req := []RequiredField{}
	fields, enums := collectTopLevelValidationFields(obj)
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
	return req, enums
}

func collectTopLevelValidationFields(obj *expr.Object) (map[string]*expr.AttributeExpr, []EnumField) {
	fields := map[string]*expr.AttributeExpr{}
	enums := []EnumField{}
	for _, nat := range *obj {
		fields[nat.Name] = nat.Attribute
		if vals := collectEnumValues(nat.Attribute); len(vals) > 0 {
			enums = append(enums, EnumField{Name: nat.Name, Values: vals})
		}
	}
	return fields, enums
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
