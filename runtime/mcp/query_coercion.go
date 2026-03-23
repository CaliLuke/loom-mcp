package mcp

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// CoerceQuery converts a URL query map into a JSON-friendly object:
// - Repeated parameters become arrays preserving input order
// - "true"/"false" (case-insensitive) become booleans
// - RFC3339/RFC3339Nano values become time.Time
// - Numeric strings become int64 or float64 when obvious
// It does not coerce "0"/"1" to booleans.
func CoerceQuery(m map[string][]string) map[string]any {
	out := make(map[string]any, len(m))
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		vals := m[key]
		if len(vals) == 1 {
			out[key] = coerceQueryValue(vals[0])
			continue
		}
		arr := make([]any, len(vals))
		for i := range vals {
			arr[i] = coerceQueryValue(vals[i])
		}
		out[key] = arr
	}
	return out
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
