package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom/codegen"
	goahttp "github.com/CaliLuke/loom/http"
)

// EncodeJSONToString encodes v into JSON using the provided encoder factory.
// The factory should produce an Encoder bound to the given ResponseWriter.
func EncodeJSONToString(
	ctx context.Context,
	newEncoder func(context.Context, http.ResponseWriter) goahttp.Encoder,
	v any,
) (string, error) {
	_ = ctx
	_ = newEncoder
	data, err := MarshalCanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MarshalCanonicalJSON encodes v into JSON using explicit JSON tags when
// present and otherwise falling back to snake_case field names for exported
// struct fields.
//
// Contract:
// - Map keys must be strings (including named string aliases).
// - Unsupported map key kinds fail fast instead of being silently dropped.
func MarshalCanonicalJSON(v any) ([]byte, error) {
	if v == nil {
		return json.Marshal(nil)
	}
	if marshaler, ok := v.(json.Marshaler); ok {
		return marshaler.MarshalJSON()
	}
	normalized, err := normalizeJSONValue(reflect.ValueOf(v))
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// UnmarshalCanonicalJSON decodes JSON into dst using explicit JSON tags when
// present and otherwise matching snake_case keys to exported struct fields.
func UnmarshalCanonicalJSON(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON data")
		}
		return err
	}
	v := reflect.ValueOf(dst)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(dst)}
	}
	return assignCanonicalValue(raw, v.Elem())
}

func normalizeJSONValue(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if normalized, handled, err := normalizeMarshaledValue(v); handled || err != nil {
		return normalized, err
	}
	switch v.Kind() {
	case reflect.Bool,
		reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return v.Interface(), nil
	case reflect.Invalid:
		return nil, nil
	case reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.Interface, reflect.Pointer, reflect.UnsafePointer:
		return v.Interface(), nil
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface(), nil
		}
		fallthrough
	case reflect.Array:
		return normalizeJSONArray(v)
	case reflect.Map:
		return normalizeJSONMap(v)
	case reflect.Struct:
		return normalizeJSONStruct(v)
	default:
		return v.Interface(), nil
	}
}

func assignCanonicalValue(raw any, dst reflect.Value) error {
	if !dst.CanSet() {
		return nil
	}
	if err, handled := assignCanonicalNilOrPointer(raw, dst); handled {
		return err
	}
	if err, handled := assignViaJSONUnmarshaler(raw, dst); handled {
		return err
	}
	return assignCanonicalByKind(raw, dst)
}

func assignCanonicalByKind(raw any, dst reflect.Value) error {
	switch dst.Kind() {
	case reflect.Invalid:
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	case reflect.Interface:
		dst.Set(reflect.ValueOf(raw))
		return nil
	case reflect.Struct:
		return assignCanonicalStruct(raw, dst)
	case reflect.Slice:
		return assignCanonicalSlice(raw, dst)
	case reflect.Array:
		return assignCanonicalArray(raw, dst)
	case reflect.Map:
		return assignCanonicalMap(raw, dst)
	case reflect.String:
		return assignCanonicalString(raw, dst)
	case reflect.Bool:
		return assignCanonicalBool(raw, dst)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return assignCanonicalInt(raw, dst)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return assignCanonicalUint(raw, dst)
	case reflect.Float32, reflect.Float64:
		return assignCanonicalFloat(raw, dst)
	case reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.Pointer, reflect.UnsafePointer:
		return assignCanonicalViaJSON(raw, dst)
	default:
		return assignCanonicalViaJSON(raw, dst)
	}
}

func assignCanonicalViaJSON(raw any, dst reflect.Value) error {
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst.Addr().Interface())
}

func assignCanonicalNilOrPointer(raw any, dst reflect.Value) (error, bool) {
	if raw == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil, true
	}
	if dst.Kind() != reflect.Pointer {
		return nil, false
	}
	if dst.IsNil() {
		dst.Set(reflect.New(dst.Type().Elem()))
	}
	return assignCanonicalValue(raw, dst.Elem()), true
}

func normalizeMarshaledValue(v reflect.Value) (any, bool, error) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, true, nil
		}
		if normalized, handled, err := normalizeJSONMarshaler(v); handled || err != nil {
			return normalized, true, err
		}
		v = v.Elem()
	}
	return normalizeJSONMarshaler(v)
}

func normalizeJSONMarshaler(v reflect.Value) (any, bool, error) {
	if !v.CanInterface() {
		return nil, false, nil
	}
	marshaler, ok := v.Interface().(json.Marshaler)
	if !ok {
		return nil, false, nil
	}
	var out any
	data, err := marshaler.MarshalJSON()
	if err != nil {
		return nil, true, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, true, err
	}
	return out, true, nil
}

func normalizeJSONArray(v reflect.Value) (any, error) {
	out := make([]any, v.Len())
	for i := 0; i < v.Len(); i++ {
		item, err := normalizeJSONValue(v.Index(i))
		if err != nil {
			return nil, err
		}
		out[i] = item
	}
	return out, nil
}

func normalizeJSONMap(v reflect.Value) (any, error) {
	if v.IsNil() {
		return nil, nil
	}
	out := make(map[string]any, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		key, err := canonicalMapKey(iter.Key())
		if err != nil {
			return nil, err
		}
		item, err := normalizeJSONValue(iter.Value())
		if err != nil {
			return nil, err
		}
		out[key] = item
	}
	return out, nil
}

func normalizeJSONStruct(v reflect.Value) (any, error) {
	out := make(map[string]any, v.NumField())
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, omitempty, explicit, skip := jsonFieldName(field)
		if skip {
			continue
		}
		fv := v.Field(i)
		if !explicit && isImplicitOmitNilField(fv) {
			continue
		}
		if omitempty && isJSONEmptyValue(fv) {
			continue
		}
		item, err := normalizeJSONValue(fv)
		if err != nil {
			return nil, err
		}
		out[name] = item
	}
	return out, nil
}

func assignViaJSONUnmarshaler(raw any, dst reflect.Value) (error, bool) {
	if !dst.CanAddr() {
		return nil, false
	}
	unmarshaler, ok := dst.Addr().Interface().(json.Unmarshaler)
	if !ok {
		return nil, false
	}
	data, err := MarshalCanonicalJSON(raw)
	if err != nil {
		return err, true
	}
	return unmarshaler.UnmarshalJSON(data), true
}

func assignCanonicalStruct(raw any, dst reflect.Value) error {
	obj, ok := raw.(map[string]any)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	fields, fieldNames := jsonStructFields(dst.Type())
	for key, val := range obj {
		idx, ok := fields[key]
		if !ok {
			return fmt.Errorf("json: unknown field %q", key)
		}
		if err := assignCanonicalValue(val, dst.Field(idx)); err != nil {
			return wrapFieldError(fieldNames[idx], err)
		}
	}
	return nil
}

func jsonStructFields(t reflect.Type) (map[string]int, map[int]string) {
	fields := map[string]int{}
	fieldNames := map[int]string{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, _, _, skip := jsonFieldName(field)
		if skip {
			continue
		}
		fields[name] = i
		fieldNames[i] = name
	}
	return fields, fieldNames
}

func assignCanonicalSlice(raw any, dst reflect.Value) error {
	arr, ok := raw.([]any)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	if dst.Type().Elem().Kind() == reflect.Uint8 {
		data, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, dst.Addr().Interface())
	}
	slice := reflect.MakeSlice(dst.Type(), len(arr), len(arr))
	for i, item := range arr {
		if err := assignCanonicalValue(item, slice.Index(i)); err != nil {
			return wrapIndexError(i, err)
		}
	}
	dst.Set(slice)
	return nil
}

func assignCanonicalArray(raw any, dst reflect.Value) error {
	arr, ok := raw.([]any)
	if !ok || len(arr) != dst.Len() {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	for i, item := range arr {
		if err := assignCanonicalValue(item, dst.Index(i)); err != nil {
			return wrapIndexError(i, err)
		}
	}
	return nil
}

func assignCanonicalMap(raw any, dst reflect.Value) error {
	obj, ok := raw.(map[string]any)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	if dst.Type().Key().Kind() != reflect.String {
		return &json.UnmarshalTypeError{Value: "object", Type: dst.Type()}
	}
	m := reflect.MakeMapWithSize(dst.Type(), len(obj))
	for key, val := range obj {
		elem := reflect.New(dst.Type().Elem()).Elem()
		if err := assignCanonicalValue(val, elem); err != nil {
			return wrapFieldError(key, err)
		}
		m.SetMapIndex(reflect.ValueOf(key), elem)
	}
	dst.Set(m)
	return nil
}

func assignCanonicalString(raw any, dst reflect.Value) error {
	value, ok := raw.(string)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	dst.SetString(value)
	return nil
}

func assignCanonicalBool(raw any, dst reflect.Value) error {
	value, ok := raw.(bool)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	dst.SetBool(value)
	return nil
}

func assignCanonicalInt(raw any, dst reflect.Value) error {
	value, err := toInt64(raw)
	if err != nil || dst.OverflowInt(value) {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	dst.SetInt(value)
	return nil
}

func assignCanonicalUint(raw any, dst reflect.Value) error {
	value, err := toUint64(raw)
	if err != nil || dst.OverflowUint(value) {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	dst.SetUint(value)
	return nil
}

func assignCanonicalFloat(raw any, dst reflect.Value) error {
	value, err := toFloat64(raw)
	if err != nil || dst.OverflowFloat(value) {
		return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
	}
	dst.SetFloat(value)
	return nil
}

func wrapFieldError(name string, err error) error {
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) && ute.Field == "" {
		clone := *ute
		clone.Field = name
		return &clone
	}
	return err
}

func wrapIndexError(idx int, err error) error {
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) && ute.Field == "" {
		clone := *ute
		clone.Field = strconv.Itoa(idx)
		return &clone
	}
	return err
}

func canonicalMapKey(v reflect.Value) (string, error) {
	if !v.IsValid() {
		return "", fmt.Errorf("unsupported map key type: <invalid>")
	}
	if v.Kind() != reflect.String {
		return "", fmt.Errorf("unsupported map key type: %s", v.Type())
	}
	return v.String(), nil
}

func jsonFieldName(field reflect.StructField) (name string, omitempty, explicit, skip bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false, true
	}
	if tag != "" {
		explicit = true
		parts := strings.Split(tag, ",")
		if parts[0] != "" {
			name = parts[0]
		}
		for _, part := range parts[1:] {
			if part == "omitempty" {
				omitempty = true
			}
		}
	}
	if name == "" {
		name = codegen.SnakeCase(field.Name)
	}
	return name, omitempty, explicit, false
}

func isJSONEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Complex64, reflect.Complex128:
		return v.Complex() == 0
	case reflect.Array, reflect.String:
		return v.Len() == 0
	case reflect.Chan:
		return v.IsNil()
	case reflect.Func:
		return v.IsNil()
	case reflect.UnsafePointer:
		return v.IsZero()
	case reflect.Map, reflect.Slice:
		return v.IsNil() || v.Len() == 0
	case reflect.Struct:
		return v.IsZero()
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	default:
		return false
	}
}

func isImplicitOmitNilField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Array, reflect.Chan, reflect.Func, reflect.String, reflect.Struct, reflect.UnsafePointer:
		return false
	default:
		return false
	}
}

func toInt64(raw any) (int64, error) {
	switch v := raw.(type) {
	case json.Number:
		return v.Int64()
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func toUint64(raw any) (uint64, error) {
	switch v := raw.(type) {
	case json.Number:
		return strconv.ParseUint(v.String(), 10, 64)
	case float64:
		if v < 0 || math.Trunc(v) != v {
			return 0, fmt.Errorf("not an unsigned integer")
		}
		return uint64(v), nil
	case uint64:
		return v, nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("negative integer")
		}
		return uint64(v), nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("negative integer")
		}
		return uint64(v), nil
	default:
		return 0, fmt.Errorf("not an unsigned integer")
	}
}

func toFloat64(raw any) (float64, error) {
	switch v := raw.(type) {
	case json.Number:
		return v.Float64()
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case int:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}

func jsonValueKind(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number, float64, int, int64, uint64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return reflect.TypeOf(raw).String()
	}
}
