package sdkbridge

import loom "github.com/CaliLuke/loom/pkg"

// ValidateToolArguments checks raw arguments against a generated input schema
// before service dispatch. It preserves JSON number precision and the difference
// between absent fields and explicit null. Compiled schemas are shared by calls.
func ValidateToolArguments(arguments []byte, schemaDocument string) error {
	if err := validateInputSchema(arguments, schemaDocument); err != nil {
		return loom.WithErrorRemedy(loom.PermanentError("invalid_params", "Tool arguments do not match the input schema."), &loom.ErrorRemedy{
			Code:        "invalid_params",
			SafeMessage: "Tool arguments do not match the input schema.",
		})
	}
	return nil
}
