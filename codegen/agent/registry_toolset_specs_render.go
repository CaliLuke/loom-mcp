package codegen

import (
	"strconv"
	"strings"

	"github.com/CaliLuke/loom/codegen"
)

func registryToolsetSpecsSection(data registryToolsetSpecsFileData) codegen.Section {
	return codegen.NewRenderSection("registry-toolset-specs", func() string {
		source := registryToolsetSpecsTemplateSource
		versionBlock := ""
		if data.Registry != nil && data.Registry.Version != "" {
			versionBlock = "\n// Version is the pinned version for this toolset.\nconst Version = " + strconv.Quote(data.Registry.Version) + "\n"
		}
		replacer := strings.NewReplacer(
			"__QUALIFIED_NAME__", strconv.Quote(data.QualifiedName),
			"__REGISTRY_NAME__", strconv.Quote(data.Registry.RegistryName),
			"__TOOLSET_NAME__", strconv.Quote(data.Registry.ToolsetName),
			"__SERVICE_NAME__", strconv.Quote(data.ServiceName),
			"__VERSION_BLOCK__", versionBlock,
		)
		return replacer.Replace(source)
	})
}

const registryToolsetSpecsTemplateSource = `// RegistryClient defines the interface for fetching toolset data from a registry.
type RegistryClient interface {
	GetToolset(ctx context.Context, name string) (*ToolsetSchema, error)
}

// ToolsetSchema represents a toolset fetched from the registry.
type ToolsetSchema struct {
	Name        string
	Description string
	Tools       []ToolSchema
}

// ToolSchema represents a tool definition from the registry.
type ToolSchema struct {
	Name            string
	Title           string
	Description     string
	Tags            []string
	PayloadTypeName string
	PayloadSchema   []byte
	ResultTypeName  string
	ResultSchema    []byte
}

// SchemaValidationError represents a validation error with structured details.
type SchemaValidationError = tools.SchemaValidationError

// SchemaValidationErrors collects multiple validation errors.
type SchemaValidationErrors = tools.SchemaValidationErrors

// RegistryToolsetID is the identifier for this registry-backed toolset.
const RegistryToolsetID = __QUALIFIED_NAME__

// RegistryName is the name of the registry source.
const RegistryName = __REGISTRY_NAME__

// ToolsetName is the name of the toolset in the registry.
const ToolsetName = __TOOLSET_NAME__
__VERSION_BLOCK__
var (
	specs     []tools.ToolSpec
	specIndex = make(map[tools.Ident]*tools.ToolSpec)
	metadata  []policy.ToolMetadata
	populated bool
	frozen    bool
	mu        sync.RWMutex
)

// Specs returns a snapshot of the tool specifications discovered from the registry.
func Specs() []tools.ToolSpec {
	mu.RLock()
	defer mu.RUnlock()
	return append([]tools.ToolSpec(nil), specs...)
}

// FreezeSpecs returns the discovered specifications and prevents later refreshes.
// Runtime registration calls this so its immutable catalog cannot drift.
func FreezeSpecs() ([]tools.ToolSpec, error) {
	mu.Lock()
	defer mu.Unlock()
	if !populated || len(specs) == 0 {
		return nil, fmt.Errorf("registry toolset %q must be discovered before registration", RegistryToolsetID)
	}
	frozen = true
	return append([]tools.ToolSpec(nil), specs...), nil
}

// DiscoverAndPopulate fetches tool schemas from the registry and populates
// the Specs slice. This function should be called during agent initialization
// before the agent starts processing requests.
//
// The function may refresh the cached specifications until FreezeSpecs is
// called during runtime registration. Refreshes after registration fail.
func DiscoverAndPopulate(ctx context.Context, client RegistryClient) error {
	mu.RLock()
	isFrozen := frozen
	mu.RUnlock()
	if isFrozen {
		return fmt.Errorf("registry toolset %q is frozen after registration", RegistryToolsetID)
	}
	toolset, err := client.GetToolset(ctx, ToolsetName)
	if err != nil {
		return fmt.Errorf("discover toolset %q from registry %q: %w", ToolsetName, RegistryName, err)
	}
	if toolset == nil {
		return fmt.Errorf("toolset %q not found in registry %q", ToolsetName, RegistryName)
	}

	mu.Lock()
	defer mu.Unlock()
	if frozen {
		return fmt.Errorf("registry toolset %q is frozen after registration", RegistryToolsetID)
	}

	specs = make([]tools.ToolSpec, 0, len(toolset.Tools))
	specIndex = make(map[tools.Ident]*tools.ToolSpec, len(toolset.Tools))
	metadata = make([]policy.ToolMetadata, 0, len(toolset.Tools))

	for _, tool := range toolset.Tools {
		spec := tools.ToolSpec{
			Name:        tools.Ident(tool.Name),
			Service:     __SERVICE_NAME__,
			Toolset:     ToolsetName,
			Description: tool.Description,
			Tags:        tool.Tags,
			Payload: tools.TypeSpec{
				Name:   tool.PayloadTypeName,
				Schema: tool.PayloadSchema,
				Codec:  tools.JSONCodec[any]{},
			},
			Result: tools.TypeSpec{
				Name:   tool.ResultTypeName,
				Schema: tool.ResultSchema,
				Codec:  tools.JSONCodec[any]{},
			},
		}
		specs = append(specs, spec)
		specIndex[spec.Name] = &specs[len(specs)-1]
		metadata = append(metadata, policy.ToolMetadata{
			ID:          spec.Name,
			Title:       tool.Title,
			Description: tool.Description,
			Tags:        tool.Tags,
			BudgetClass: policy.ToolBudgetClassBudgeted,

		})
	}
	populated = true

	return nil
}

// Names returns the identifiers of all discovered tools.
func Names() []tools.Ident {
	mu.RLock()
	defer mu.RUnlock()

	names := make([]tools.Ident, 0, len(specIndex))
	for name := range specIndex {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return string(names[i]) < string(names[j])
	})
	return names
}

// Spec returns the specification for the named tool if present.
func Spec(name tools.Ident) (*tools.ToolSpec, bool) {
	mu.RLock()
	defer mu.RUnlock()

	spec, ok := specIndex[name]
	if !ok {
		return nil, false
	}
	copy := *spec
	return &copy, true
}

// PayloadSchema returns the JSON schema for the named tool payload.
func PayloadSchema(name tools.Ident) ([]byte, bool) {
	mu.RLock()
	defer mu.RUnlock()

	spec, ok := specIndex[name]
	if !ok {
		return nil, false
	}
	return spec.Payload.Schema, true
}

// ResultSchema returns the JSON schema for the named tool result.
func ResultSchema(name tools.Ident) ([]byte, bool) {
	mu.RLock()
	defer mu.RUnlock()

	spec, ok := specIndex[name]
	if !ok {
		return nil, false
	}
	return spec.Result.Schema, true
}

// Metadata exposes policy metadata for the discovered tools.
func Metadata() []policy.ToolMetadata {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]policy.ToolMetadata, len(metadata))
	copy(out, metadata)
	return out
}

// ValidatePayload validates a tool payload against its registry-provided schema.
// Returns nil if the payload is valid or if no schema is available.
func ValidatePayload(name tools.Ident, payload any) error {
	schema, ok := PayloadSchema(name)
	if !ok || len(schema) == 0 {
		return nil
	}
	return validateAgainstSchema(schema, payload, "payload")
}

// ValidateResult validates a tool result against its registry-provided schema.
// Returns nil if the result is valid or if no schema is available.
func ValidateResult(name tools.Ident, result any) error {
	schema, ok := ResultSchema(name)
	if !ok || len(schema) == 0 {
		return nil
	}
	return validateAgainstSchema(schema, result, "result")
}

func validateAgainstSchema(schema []byte, data any, context string) error {
	if err := tools.ValidateJSONSchema(schema, data); err != nil {
		return fmt.Errorf("validate %s against registry schema: %w", context, err)
	}
	return nil
}

`
