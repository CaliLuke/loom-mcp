// Package prompt contains runtime prompt registry and override resolution logic.
package prompt

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"
)

const scopeFingerprintVersion = "v1:"

// ScopeFingerprint returns the stable identity of an exact label scope. Map
// iteration order and delimiter-like content do not affect the result.
func ScopeFingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	encoded := make([]byte, 0)
	for _, key := range keys {
		encoded = appendFingerprintPart(encoded, key)
		encoded = appendFingerprintPart(encoded, labels[key])
	}
	digest := sha256.Sum256(encoded)
	return scopeFingerprintVersion + hex.EncodeToString(digest[:])
}

// ScopeMatches reports whether overrideScope applies to requestedScope.
//
// Contract:
//   - SessionID must match exactly when overrideScope.SessionID is set.
//   - Every label in overrideScope.Labels must exist with the same value in
//     requestedScope.Labels.
func ScopeMatches(overrideScope Scope, requestedScope Scope) bool {
	if overrideScope.SessionID != "" && overrideScope.SessionID != requestedScope.SessionID {
		return false
	}
	for key, value := range overrideScope.Labels {
		if requestedScope.Labels[key] != value {
			return false
		}
	}
	return true
}

// ScopePrecedence returns override precedence for conflict resolution.
//
// Higher values are more specific:
//   - Session-scoped overrides outrank non-session overrides.
//   - For the same session dimension, more constrained label sets outrank
//     less constrained ones.
func ScopePrecedence(scope Scope) int {
	precedence := len(scope.Labels)
	if scope.SessionID != "" {
		// Session is the strongest runtime-managed scope dimension.
		precedence += 1000
	}
	return precedence
}

func appendFingerprintPart(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(value)))
	return append(dst, value...)
}
