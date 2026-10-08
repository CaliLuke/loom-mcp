package tools

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// SchemaValidationError represents a validation error with structured details.
type SchemaValidationError struct {
	Path    string
	Message string
	Value   any
}

// SchemaValidationErrors collects multiple validation errors.
type SchemaValidationErrors struct {
	Errors []*SchemaValidationError
}

type localSchemaLoader struct{}

// ValidateJSONSchema validates a JSON-compatible value against a JSON Schema document.
// Local references resolve against the supplied document; external references fail closed.
func ValidateJSONSchema(schema []byte, value any) error {
	if !jsontext.Value(schema).IsValid() {
		return errors.New("schema is not valid JSON")
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(localSchemaLoader{})
	if err := compiler.AddResource("schema://loom/registry.json", document); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := compiler.Compile("schema://loom/registry.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}

	rawValue, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawValue))
	if err != nil {
		return fmt.Errorf("parse value: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		var validationErr *jsonschema.ValidationError
		if errors.As(err, &validationErr) {
			validationErrors := &SchemaValidationErrors{}
			appendSchemaValidationErrors(validationErrors, validationErr, instance)
			if validationErrors.HasErrors() {
				return validationErrors
			}
		}
		return err
	}
	return nil
}

// Error formats the validation error with its instance path when present.
func (e *SchemaValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// Error joins the collected validation failures into one message.
func (e *SchemaValidationErrors) Error() string {
	if len(e.Errors) == 0 {
		return "validation failed"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	messages := make([]string, 0, len(e.Errors))
	for _, validationErr := range e.Errors {
		messages = append(messages, validationErr.Error())
	}
	return fmt.Sprintf("validation failed: %s", strings.Join(messages, "; "))
}

// Add appends one validation failure to the collection.
func (e *SchemaValidationErrors) Add(path, message string, value any) {
	e.Errors = append(e.Errors, &SchemaValidationError{
		Path:    path,
		Message: message,
		Value:   value,
	})
}

// HasErrors reports whether the collection contains validation failures.
func (e *SchemaValidationErrors) HasErrors() bool {
	return len(e.Errors) > 0
}

// appendSchemaValidationErrors flattens library causes while retaining each invalid leaf value.
func appendSchemaValidationErrors(errors *SchemaValidationErrors, validationErr *jsonschema.ValidationError, instance any) {
	switch validationErr.ErrorKind.(type) {
	case *kind.OneOf, *kind.AnyOf:
		path, value := schemaInstancePath(validationErr.InstanceLocation, instance)
		errors.Add(path, validationErr.Error(), value)
		return
	}

	if len(validationErr.Causes) > 0 {
		for _, cause := range validationErr.Causes {
			appendSchemaValidationErrors(errors, cause, instance)
		}
		return
	}
	path, value := schemaInstancePath(validationErr.InstanceLocation, instance)
	switch validationKind := validationErr.ErrorKind.(type) {
	case *kind.Required:
		for _, missing := range validationKind.Missing {
			errors.Add(joinSchemaPath(path, missing), "missing required field", nil)
		}
	case *kind.AdditionalProperties:
		object, _ := value.(map[string]any)
		for _, property := range validationKind.Properties {
			errors.Add(joinSchemaPath(path, property), "additional property not allowed", object[property])
		}
	case *kind.MinItems, *kind.MaxItems:
		errors.Add(path, validationErr.Error(), nil)
	default:
		errors.Add(path, validationErr.Error(), value)
	}
}

// schemaInstancePath formats decoded instance-location segments and finds their corresponding value.
func schemaInstancePath(location []string, instance any) (string, any) {
	path := ""
	for _, segment := range location {
		switch current := instance.(type) {
		case map[string]any:
			path = joinSchemaPath(path, segment)
			instance = current[segment]
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(current) {
				return joinSchemaPath(path, segment), nil
			}
			path += fmt.Sprintf("[%d]", index)
			instance = current[index]
		default:
			return joinSchemaPath(path, segment), nil
		}
	}
	return path, instance
}

func joinSchemaPath(base, field string) string {
	if base == "" {
		return field
	}
	return base + "." + field
}

func (localSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external schema reference %q is unsupported", location)
}
