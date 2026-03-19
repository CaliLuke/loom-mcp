package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"goa.design/goa/v3/codegen"
	goahttp "goa.design/goa/v3/http"
)

type (
	// Notification describes a server-initiated status update that can be
	// broadcast to connected MCP clients via the Events stream. It carries a
	// machine-usable type, an optional human-readable message, and optional
	// structured data.
	Notification struct {
		Type    string  `json:"type"`
		Message *string `json:"message,omitempty"`
		Data    any     `json:"data,omitempty"`
	}
)

// EncodeJSONToString encodes v into JSON using the provided encoder factory.
// The factory should produce an Encoder bound to the given ResponseWriter.
func EncodeJSONToString(
	ctx context.Context,
	newEncoder func(context.Context, http.ResponseWriter) goahttp.Encoder,
	v any,
) (string, error) {
	data, err := MarshalCanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MarshalCanonicalJSON encodes v into JSON using explicit JSON tags when
// present and otherwise falling back to snake_case field names for exported
// struct fields.
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

// CoerceQuery converts a URL query map into a JSON-friendly object:
// - Repeated parameters become arrays preserving input order
// - "true"/"false" (case-insensitive) become booleans
// - RFC3339/RFC3339Nano values become time.Time
// - Numeric strings become int64 or float64 when obvious
// It does not coerce "0"/"1" to booleans.
func CoerceQuery(m map[string][]string) map[string]any {
	out := make(map[string]any, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := m[k]
		if len(vals) == 1 {
			out[k] = coerce(vals[0])
			continue
		}
		arr := make([]any, len(vals))
		for i := range vals {
			arr[i] = coerce(vals[i])
		}
		out[k] = arr
	}
	return out
}

func coerce(s string) any {
	// Trim but preserve original if no coercion applies.
	t := s
	if t == "" {
		return ""
	}
	// Booleans: only true/false, case-insensitive.
	if strings.EqualFold(t, "true") {
		return true
	}
	if strings.EqualFold(t, "false") {
		return false
	}
	// RFC3339 timestamps.
	if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, t); err == nil {
		return ts
	}
	// Numbers: prefer int if it looks integral; otherwise float.
	if looksIntegral(t) {
		if i, err := strconv.ParseInt(t, 10, 64); err == nil {
			return i
		}
	}
	if looksFloat(t) {
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
	}
	return s
}

func looksIntegral(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func looksFloat(s string) bool {
	// Heuristic: contains a dot or exponent. Delegate validation to ParseFloat.
	return strings.ContainsAny(s, ".eE")
}

type bufferResponseWriter struct {
	headers http.Header
	buf     bytes.Buffer
}

func (w *bufferResponseWriter) Header() http.Header {
	if w.headers == nil {
		w.headers = make(http.Header)
	}
	return w.headers
}

// WriteHeader is a no-op because only the body is captured for encoding.
func (w *bufferResponseWriter) WriteHeader(statusCode int)  {}
func (w *bufferResponseWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func normalizeJSONValue(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		if v.CanInterface() {
			if marshaler, ok := v.Interface().(json.Marshaler); ok {
				var out any
				data, err := marshaler.MarshalJSON()
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(data, &out); err != nil {
					return nil, err
				}
				return out, nil
			}
		}
		v = v.Elem()
	}
	if v.CanInterface() {
		if marshaler, ok := v.Interface().(json.Marshaler); ok {
			var out any
			data, err := marshaler.MarshalJSON()
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	switch v.Kind() {
	case reflect.Bool,
		reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return v.Interface(), nil
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface(), nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			item, err := normalizeJSONValue(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key()
			if key.Kind() != reflect.String {
				continue
			}
			item, err := normalizeJSONValue(iter.Value())
			if err != nil {
				return nil, err
			}
			out[key.String()] = item
		}
		return out, nil
	case reflect.Struct:
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
	default:
		return v.Interface(), nil
	}
}

func assignCanonicalValue(raw any, dst reflect.Value) error {
	if !dst.CanSet() {
		return nil
	}
	if raw == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assignCanonicalValue(raw, dst.Elem())
	}
	if dst.CanAddr() {
		if unmarshaler, ok := dst.Addr().Interface().(json.Unmarshaler); ok {
			data, err := MarshalCanonicalJSON(raw)
			if err != nil {
				return err
			}
			return unmarshaler.UnmarshalJSON(data)
		}
	}
	switch dst.Kind() {
	case reflect.Interface:
		dst.Set(reflect.ValueOf(raw))
		return nil
	case reflect.Struct:
		obj, ok := raw.(map[string]any)
		if !ok {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		fields := map[string]int{}
		fieldNames := map[int]string{}
		t := dst.Type()
		for i := 0; i < dst.NumField(); i++ {
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
	case reflect.Slice:
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
	case reflect.Array:
		arr, ok := raw.([]any)
		if !ok {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		if len(arr) != dst.Len() {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		for i, item := range arr {
			if err := assignCanonicalValue(item, dst.Index(i)); err != nil {
				return wrapIndexError(i, err)
			}
		}
		return nil
	case reflect.Map:
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
	case reflect.String:
		s, ok := raw.(string)
		if !ok {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := raw.(bool)
		if !ok {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := toInt64(raw)
		if err != nil {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		if dst.OverflowInt(n) {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := toUint64(raw)
		if err != nil {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		if dst.OverflowUint(n) {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		dst.SetUint(n)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := toFloat64(raw)
		if err != nil {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		if dst.OverflowFloat(f) {
			return &json.UnmarshalTypeError{Value: jsonValueKind(raw), Type: dst.Type()}
		}
		dst.SetFloat(f)
		return nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, dst.Addr().Interface())
	}
}

func wrapFieldError(name string, err error) error {
	if ute, ok := err.(*json.UnmarshalTypeError); ok && ute.Field == "" {
		clone := *ute
		clone.Field = name
		return &clone
	}
	return err
}

func wrapIndexError(idx int, err error) error {
	if ute, ok := err.(*json.UnmarshalTypeError); ok && ute.Field == "" {
		clone := *ute
		clone.Field = strconv.Itoa(idx)
		return &clone
	}
	return err
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
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Struct:
		return v.IsZero()
	default:
		return false
	}
}

func isImplicitOmitNilField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
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
