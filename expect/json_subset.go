package expect

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"math/big"
	"strings"
)

type decimalNumber string

// preserveNumbers overrides only numbers; other JSON values use v2 decoding.
var preserveNumbers = json.WithUnmarshalers(json.UnmarshalFromFunc(func(decoder *jsontext.Decoder, value *any) error {
	if decoder.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	number, err := decoder.ReadValue()
	if err != nil {
		return err
	}
	*value = decimalNumber(number)
	return nil
}))

// decodeJSON validates a single JSON value and preserves number lexemes.
func decodeJSON(data []byte) (any, error) {
	var value any
	if err := json.Unmarshal(data, &value, preserveNumbers); err != nil {
		return nil, err
	}
	return value, nil
}

// jsonSubset uses exact recursive equality within arrays, including their objects.
func jsonSubset(expected, actual any, exact bool) bool {
	switch value := expected.(type) {
	case map[string]any:
		object, ok := actual.(map[string]any)
		if !ok || (exact && len(value) != len(object)) {
			return false
		}
		for name, wanted := range value {
			got, exists := object[name]
			if !exists || !jsonSubset(wanted, got, exact) {
				return false
			}
		}
		return true
	case []any:
		array, ok := actual.([]any)
		if !ok || len(value) != len(array) {
			return false
		}
		for i, wanted := range value {
			if !jsonSubset(wanted, array[i], true) {
				return false
			}
		}
		return true
	case decimalNumber:
		number, ok := actual.(decimalNumber)
		if !ok {
			return false
		}
		digits, exponent := decimalValue(value)
		otherDigits, otherExponent := decimalValue(number)
		return digits == otherDigits && exponent == otherExponent
	case string:
		text, ok := actual.(string)
		return ok && text == value
	case bool:
		boolean, ok := actual.(bool)
		return ok && boolean == value
	case nil:
		return actual == nil
	default:
		panic("expect: unexpected decoded JSON type")
	}
}

// decimalValue represents a validated JSON number as signed significant digits
// and a decimal exponent. It never rounds or expands a potentially huge power.
func decimalValue(number decimalNumber) (string, string) {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(string(number)), "e")
	exponent := new(big.Int)
	if hasExponent {
		if _, ok := exponent.SetString(exponentText, 10); !ok {
			panic("expect: invalid validated JSON exponent")
		}
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fractional, hasFraction := strings.Cut(mantissa, ".")
	if hasFraction {
		exponent.Sub(exponent, big.NewInt(int64(len(fractional))))
	}
	digits := strings.TrimLeft(whole+fractional, "0")
	if digits == "" {
		return "0", "0"
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent.Add(exponent, big.NewInt(int64(len(digits)-len(trimmed))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent.String()
}
