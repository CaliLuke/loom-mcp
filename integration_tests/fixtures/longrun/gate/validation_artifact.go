package suite

import (
	"encoding/json"
	"path/filepath"
)

// ValidationPath separates each declared profile's v2 evidence from the v1 archive.
func ValidationPath(dir, configuration string) string {
	return filepath.Join(dir, "gate", "validation_"+configuration+".json")
}

// WriteValidation retains successful or failed structural evidence once.
// ReadValidationFor refuses failed evidence as calibration input.
func WriteValidation(dir string, validation Validation) error {
	if err := validateConfigurationName(validation.Configuration); err != nil {
		return err
	}
	data, err := json.MarshalIndent(validation, "", "  ")
	if err != nil {
		return err
	}
	return writeOnce(ValidationPath(dir, validation.Configuration), append(data, '\n'))
}

// ReadValidationFor verifies the complete current configuration manifest.
func ReadValidationFor(dir, configuration string) (Validation, error) {
	var validation Validation
	if err := validateConfigurationName(configuration); err != nil {
		return validation, err
	}
	data, err := readFile(dir, filepath.Join("gate", filepath.Base(ValidationPath(dir, configuration))))
	if err != nil {
		return validation, err
	}
	if err := decodeArtifact(data, &validation); err != nil {
		return validation, err
	}
	return validation, VerifyValidation(dir, configuration, validation)
}
