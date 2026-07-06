// Package claudecaps centralizes Claude model capability rules shared by
// Anthropic adapters across providers.
package claudecaps

import (
	"strconv"
	"strings"
)

// TemperatureSupported reports whether modelID accepts the temperature
// sampling parameter. Newer Claude generations reject sampling controls.
func TemperatureSupported(modelID string) bool {
	if IsFableGeneration(modelID) {
		return false
	}
	if gen, minor, hasMinor, ok := familyVersion(modelID, "claude-opus-"); ok {
		if gen >= 5 {
			return false
		}
		return gen != 4 || !hasMinor || minor < 7
	}
	if gen, _, _, ok := familyVersion(modelID, "claude-sonnet-"); ok {
		return gen < 5
	}
	if gen, _, _, ok := familyVersion(modelID, "claude-haiku-"); ok {
		return gen < 5
	}
	return true
}

// AdaptiveThinkingRequired reports whether modelID requires adaptive thinking
// instead of the legacy type:"enabled" + budget_tokens shape.
func AdaptiveThinkingRequired(modelID string) bool {
	if IsFableGeneration(modelID) {
		return true
	}
	gen, minor, hasMinor, ok := familyVersion(modelID, "claude-opus-")
	if !ok {
		return false
	}
	return gen >= 5 || (gen == 4 && hasMinor && minor >= 6)
}

// IsFableGeneration reports whether modelID belongs to the Claude 5
// Fable/Mythos generation.
func IsFableGeneration(modelID string) bool {
	_, _, _, fable := familyVersion(modelID, "claude-fable-")
	_, _, _, mythos := familyVersion(modelID, "claude-mythos-")
	return fable || mythos
}

func familyVersion(modelID string, marker string) (gen int, minor int, hasMinor bool, ok bool) {
	start := strings.Index(modelID, marker)
	if start < 0 {
		return 0, 0, false, false
	}
	rest := modelID[start+len(marker):]
	gen, rest, ok = takeVersionSegment(rest)
	if !ok {
		return 0, 0, false, false
	}
	if strings.HasPrefix(rest, "-") {
		if m, _, mok := takeVersionSegment(rest[1:]); mok {
			return gen, m, true, true
		}
	}
	return gen, 0, false, true
}

func takeVersionSegment(s string) (n int, rest string, ok bool) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 || end > 2 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, s, false
	}
	return n, s[end:], true
}
