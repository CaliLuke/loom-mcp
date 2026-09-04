package mcp

import (
	"strconv"
	"strings"
	"time"
)

// CoerceQuery converts a URL query map into a JSON-friendly object. It infers
// scalar types from values and preserves repeated parameters in input order.
// It does not coerce "0" or "1" to booleans.
func CoerceQuery(m map[string][]string) map[string]any {
	return CoerceQueryTyped(m, nil)
}

// QueryField describes the query shape declared by a resource payload schema.
type QueryField struct {
	String        bool
	Unsigned      bool
	Float         bool
	Repeated      bool
	Bits          int
	DefaultValues []string
}

// CoerceQueryTyped converts a URL query map into a JSON-friendly object while
// preserving strings and array shape declared by fields. Other values use the
// same inference as CoerceQuery. Repeated parameters preserve input order.
func CoerceQueryTyped(m map[string][]string, fields map[string]QueryField) map[string]any {
	out := make(map[string]any, len(m)+len(fields))
	for key, field := range fields {
		if _, present := m[key]; !present && field.DefaultValues != nil {
			out[key] = coerceTypedQueryValues(field.DefaultValues, field)
		}
	}
	for key, vals := range m {
		out[key] = coerceTypedQueryValues(vals, fields[key])
	}
	return out
}

func coerceTypedQueryValues(values []string, field QueryField) any {
	if len(values) == 1 && !field.Repeated {
		return coerceTypedQueryValue(values[0], field)
	}
	array := make([]any, len(values))
	for index := range values {
		array[index] = coerceTypedQueryValue(values[index], field)
	}
	return array
}

func coerceTypedQueryValue(value string, field QueryField) any {
	if field.String {
		return value
	}
	if field.Unsigned && looksIntegral(value) {
		bits := field.Bits
		if bits == 0 {
			bits = 64
		}
		if parsed, err := strconv.ParseUint(value, 10, bits); err == nil {
			return parsed
		}
	}
	if !field.Unsigned && !field.Float && field.Bits > 0 && looksIntegral(value) {
		if parsed, err := strconv.ParseInt(value, 10, field.Bits); err == nil {
			return parsed
		}
	}
	if field.Float {
		bits := field.Bits
		if bits == 0 {
			bits = 64
		}
		if parsed, err := strconv.ParseFloat(value, bits); err == nil {
			if bits == 32 {
				return float32(parsed)
			}
			return parsed
		}
	}
	return coerceQueryValue(value)
}

func coerceQueryValue(s string) any {
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "true") {
		return true
	}
	if strings.EqualFold(s, "false") {
		return false
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts
	}
	if looksIntegral(s) {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	if looksFloat(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
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
	return strings.ContainsAny(s, ".eE")
}
