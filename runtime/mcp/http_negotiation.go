package mcp

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type mediaRange struct {
	mediaType    string
	mediaSubtype string
	quality      float64
}

type mediaPreference struct {
	specificity int
	quality     float64
}

const (
	streamableJSONMediaType = "application/json"
	streamableSSEMediaType  = "text/event-stream"
)

// StreamableHTTPNegotiation checks that a POST request accepts the configured
// response type before it enters the official MCP SDK handler.
func StreamableHTTPNegotiation(next http.Handler, jsonResponse bool) http.Handler {
	responseType := streamableSSEMediaType
	if jsonResponse {
		responseType = streamableJSONMediaType
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		jsonPreference, ssePreference := streamableMediaPreferences(r.Header.Values("Accept"))
		responseAccepted := ssePreference.quality > 0
		if jsonResponse {
			responseAccepted = jsonPreference.quality > 0
		}
		if !responseAccepted {
			w.Header().Add("Vary", "Accept")
			http.Error(w, "not acceptable: MCP server responds with "+responseType, http.StatusNotAcceptable)
			return
		}
		if jsonPreference.quality > 0 && ssePreference.quality > 0 {
			next.ServeHTTP(w, r)
			return
		}
		prepared := r.Clone(r.Context())
		prepared.Header.Set("Accept", streamableJSONMediaType+", "+streamableSSEMediaType)
		next.ServeHTTP(w, prepared)
	})
}

func streamableMediaPreferences(values []string) (mediaPreference, mediaPreference) {
	jsonPreference := mediaPreference{specificity: -1}
	ssePreference := mediaPreference{specificity: -1}
	for _, value := range values {
		for {
			raw, remaining, more := cutHeaderValue(value, ',')
			accepted, ok := parseMediaRange(raw)
			if ok {
				jsonPreference = preferMediaRange(jsonPreference, accepted, "application", "json")
				ssePreference = preferMediaRange(ssePreference, accepted, "text", "event-stream")
			}
			if !more {
				break
			}
			value = remaining
		}
	}
	return jsonPreference, ssePreference
}

func parseMediaRange(raw string) (mediaRange, bool) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil || hasMediaRangeParameters(raw) {
		return mediaRange{}, false
	}
	acceptedType, acceptedSubtype, ok := strings.Cut(strings.ToLower(mediaType), "/")
	if !ok {
		return mediaRange{}, false
	}
	quality, ok := mediaRangeQuality(params)
	if !ok {
		return mediaRange{}, false
	}
	return mediaRange{mediaType: acceptedType, mediaSubtype: acceptedSubtype, quality: quality}, true
}

func hasMediaRangeParameters(raw string) bool {
	_, parameters, exists := cutHeaderValue(raw, ';')
	for exists {
		parameter, remaining, more := cutHeaderValue(parameters, ';')
		name, _, hasValue := strings.Cut(parameter, "=")
		if hasValue && strings.EqualFold(strings.TrimSpace(name), "q") {
			return false
		}
		if strings.TrimSpace(parameter) != "" {
			return true
		}
		parameters = remaining
		exists = more
	}
	return false
}

func preferMediaRange(current mediaPreference, accepted mediaRange, targetType, targetSubtype string) mediaPreference {
	specificity := mediaRangeSpecificity(accepted.mediaType, accepted.mediaSubtype, targetType, targetSubtype)
	if specificity < 0 {
		return current
	}
	candidate := mediaPreference{specificity: specificity, quality: accepted.quality}
	if betterMediaPreference(candidate, current) {
		return candidate
	}
	return current
}

func mediaRangeSpecificity(acceptedType, acceptedSubtype, targetType, targetSubtype string) int {
	switch {
	case acceptedType == targetType && acceptedSubtype == targetSubtype:
		return 2
	case acceptedType == targetType && acceptedSubtype == "*":
		return 1
	case acceptedType == "*" && acceptedSubtype == "*":
		return 0
	default:
		return -1
	}
}

func mediaRangeQuality(params map[string]string) (float64, bool) {
	rawQuality, exists := params["q"]
	if !exists {
		return 1, true
	}
	quality, err := strconv.ParseFloat(rawQuality, 64)
	if err != nil || quality < 0 || quality > 1 {
		return 0, false
	}
	return quality, true
}

func betterMediaPreference(candidate, current mediaPreference) bool {
	return candidate.specificity > current.specificity ||
		(candidate.specificity == current.specificity && candidate.quality > current.quality)
}

// cutHeaderValue splits value at the first unquoted delimiter.
func cutHeaderValue(value string, delimiter byte) (string, string, bool) {
	quoted := false
	escaped := false
	for i := range len(value) {
		switch {
		case escaped:
			escaped = false
		case quoted && value[i] == '\\':
			escaped = true
		case value[i] == '"':
			quoted = !quoted
		case !quoted && value[i] == delimiter:
			return value[:i], value[i+1:], true
		}
	}
	return value, "", false
}
