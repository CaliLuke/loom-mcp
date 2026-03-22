package codegen

import (
	"fmt"
	"sort"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
)

// buildResourceQueryFields computes the statically known resource query plan so
// the template can emit direct query assembly without rediscovering payload
// structure at runtime.
func buildResourceQueryFields(payload *expr.AttributeExpr) ([]*ResourceQueryField, error) {
	definitions := collectResourceQueryFieldDefinitions(payload)
	if len(definitions) == 0 {
		return nil, fmt.Errorf(
			"payload must define at least one top-level primitive or array-of-primitive query field",
		)
	}
	return resourceQueryFieldPlan(definitions)
}

func collectResourceQueryFieldDefinitions(payload *expr.AttributeExpr) map[string]resourceQueryFieldDefinition {
	definitions := make(map[string]resourceQueryFieldDefinition)
	collectResourceQueryFields(payload, payload, definitions, make(map[string]struct{}))
	return definitions
}

func resourceQueryFieldPlan(definitions map[string]resourceQueryFieldDefinition) ([]*ResourceQueryField, error) {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]*ResourceQueryField, 0, len(names))
	for _, name := range names {
		field, err := newResourceQueryField(name, definitions[name])
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// collectResourceQueryFields flattens the top-level resource payload across
// direct fields, bases, and references so generated query assembly preserves
// the original payload surface without runtime rediscovery.
func collectResourceQueryFields(
	root *expr.AttributeExpr,
	att *expr.AttributeExpr,
	fields map[string]resourceQueryFieldDefinition,
	seen map[string]struct{},
) {
	if att == nil || att.Type == nil {
		return
	}
	hash := att.Type.Hash()
	if _, ok := seen[hash]; ok {
		return
	}
	seen[hash] = struct{}{}
	for _, base := range att.Bases {
		collectResourceQueryFields(root, attributeDataType(base), fields, seen)
	}
	for _, ref := range att.References {
		collectResourceQueryFields(root, attributeDataType(ref), fields, seen)
	}
	object := expr.AsObject(att.Type)
	if object == nil {
		return
	}
	for _, named := range *object {
		required := att.IsRequired(named.Name) || root.IsRequired(named.Name)
		fields[named.Name] = resourceQueryFieldDefinition{
			Attribute:        named.Attribute,
			Required:         required,
			PrimitivePointer: !required && att.IsPrimitivePointer(named.Name, true),
		}
	}
}

// newResourceQueryField converts one flattened payload field into a concrete
// query-rendering plan for the client adapter template.
func newResourceQueryField(name string, definition resourceQueryFieldDefinition) (*ResourceQueryField, error) {
	fieldName := codegen.Goify(name, true)
	if array := expr.AsArray(definition.Attribute.Type); array != nil {
		formatKind, err := resourceQueryFormatKind(name, array.ElemType.Type)
		if err != nil {
			return nil, err
		}
		return &ResourceQueryField{
			QueryKey:       name,
			GuardExpr:      fmt.Sprintf("len(payload.%s) > 0", fieldName),
			CollectionExpr: fmt.Sprintf("payload.%s", fieldName),
			ValueExpr:      "value",
			FormatKind:     formatKind,
			Repeated:       true,
		}, nil
	}

	formatKind, err := resourceQueryFormatKind(name, definition.Attribute.Type)
	if err != nil {
		return nil, err
	}
	field := &ResourceQueryField{
		QueryKey:   name,
		ValueExpr:  fmt.Sprintf("payload.%s", fieldName),
		FormatKind: formatKind,
	}
	if definition.Required {
		return field, nil
	}
	if definition.PrimitivePointer {
		field.GuardExpr = fmt.Sprintf("payload.%s != nil", fieldName)
		field.ValueExpr = fmt.Sprintf("*payload.%s", fieldName)
		return field, nil
	}
	field.GuardExpr = resourceQueryZeroGuardExpr(formatKind, fieldName)
	return field, nil
}

// attributeDataType recovers the full attribute metadata for base and reference
// types when they are modeled as named user types.
func attributeDataType(dt expr.DataType) *expr.AttributeExpr {
	if userType, ok := dt.(expr.UserType); ok {
		return userType.Attribute()
	}
	return &expr.AttributeExpr{Type: dt}
}

// resourceQueryFormatKind classifies one supported scalar query value so the
// template can emit direct string formatting without runtime JSON marshalling.
func resourceQueryFormatKind(fieldName string, dt expr.DataType) (string, error) {
	underlying := resourceQueryUnderlyingType(dt)
	if array := expr.AsArray(underlying); array != nil {
		return "", fmt.Errorf(
			`field %q uses nested array query values; expected primitive or array of primitive values`,
			fieldName,
		)
	}
	if !expr.IsPrimitive(underlying) {
		return "", fmt.Errorf(
			`field %q uses unsupported resource query type %q; expected primitive or array of primitive values`,
			fieldName,
			underlying.Name(),
		)
	}
	switch underlying.Kind() {
	case expr.StringKind:
		return resourceQueryFormatString, nil
	case expr.BooleanKind:
		return resourceQueryFormatBool, nil
	case expr.IntKind, expr.Int32Kind, expr.Int64Kind:
		return resourceQueryFormatInt, nil
	case expr.UIntKind, expr.UInt32Kind, expr.UInt64Kind:
		return resourceQueryFormatUint, nil
	case expr.Float32Kind:
		return resourceQueryFormatFloat32, nil
	case expr.Float64Kind:
		return resourceQueryFormatFloat64, nil
	case expr.BytesKind,
		expr.ArrayKind,
		expr.ObjectKind,
		expr.MapKind,
		expr.UnionKind,
		expr.UserTypeKind,
		expr.ResultTypeKind,
		expr.AnyKind:
		return "", fmt.Errorf(
			`field %q uses unsupported resource query type %q; expected string, bool, int, uint, float, or arrays of those values`,
			fieldName,
			underlying.Name(),
		)
	}
	return "", fmt.Errorf(
		`field %q uses unsupported resource query type %q; expected string, bool, int, uint, float, or arrays of those values`,
		fieldName,
		underlying.Name(),
	)
}

// resourceQueryZeroGuardExpr returns the direct zero-value guard for optional
// non-pointer scalar query fields.
func resourceQueryZeroGuardExpr(formatKind string, fieldName string) string {
	switch formatKind {
	case resourceQueryFormatString:
		return fmt.Sprintf(`payload.%s != ""`, fieldName)
	case resourceQueryFormatBool:
		return fmt.Sprintf("payload.%s", fieldName)
	default:
		return fmt.Sprintf("payload.%s != 0", fieldName)
	}
}

// resourceQueryUnderlyingType resolves aliases so query-field guard selection
// follows the concrete runtime kind that Goa will generate.
func resourceQueryUnderlyingType(dt expr.DataType) expr.DataType {
	switch actual := dt.(type) {
	case *expr.UserTypeExpr:
		return resourceQueryUnderlyingType(actual.Type)
	case *expr.ResultTypeExpr:
		return resourceQueryUnderlyingType(actual.Type)
	default:
		return actual
	}
}
