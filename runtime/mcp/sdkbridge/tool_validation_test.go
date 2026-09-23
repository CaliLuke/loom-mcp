package sdkbridge

import (
	"testing"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateToolArgumentsPreservesNumericAndNullContracts(t *testing.T) {
	const schema = `{"type":"object","properties":{
		"limit":{"type":"integer","default":50,"minimum":1,"maximum":200},
		"ratio":{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":1},
		"nullable":{"type":["integer","null"],"minimum":1},
		"large":{"type":"integer","minimum":9007199254740992,"maximum":9007199254740994},
		"unsigned":{"type":"integer","minimum":0,"maximum":18446744073709551615}
	}}`
	for _, tt := range []struct {
		name      string
		arguments string
		valid     bool
	}{
		{"absent", `{}`, true},
		{"inclusive bounds", `{"limit":1}`, true},
		{"inclusive maximum", `{"limit":200}`, true},
		{"explicit zero", `{"limit":0}`, false},
		{"negative", `{"limit":-1}`, false},
		{"above maximum", `{"limit":201}`, false},
		{"nonnullable null", `{"limit":null}`, false},
		{"nullable null", `{"nullable":null}`, true},
		{"nullable constraint", `{"nullable":0}`, false},
		{"exclusive minimum", `{"ratio":0}`, false},
		{"exclusive maximum", `{"ratio":1}`, false},
		{"fractional value", `{"ratio":0.5}`, true},
		{"large integer", `{"large":9007199254740993}`, true},
		{"large overflow", `{"large":9007199254740995}`, false},
		{"unsigned maximum", `{"unsigned":18446744073709551615}`, true},
		{"unsigned overflow", `{"unsigned":18446744073709551616}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolArguments([]byte(tt.arguments), schema)
			if tt.valid {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, "invalid_params", loom.ErrorRemedyCode(err))
			assert.NotEmpty(t, loom.ErrorSafeMessage(err))
		})
	}
}
