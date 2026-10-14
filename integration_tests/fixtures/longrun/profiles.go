package longrun

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	runtime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

// ConfigurationProfile declares exact role identities and offline capabilities.
// It never constructs a live provider or reads credentials.
type ConfigurationProfile struct {
	Version       int                                    `json:"version"`
	Configuration string                                 `json:"configuration"`
	Roles         map[string]gate.RoleIdentity           `json:"roles"`
	Supports      map[string]model.ToolResultPartSupport `json:"supports"`
}

// ResolveProfile captures explicit model choices before provider construction.
func ResolveProfile(configuration string) (ConfigurationProfile, error) {
	if !knownConfiguration(configuration) {
		return ConfigurationProfile{}, fmt.Errorf("unknown provider profile %q", configuration)
	}
	profile := ConfigurationProfile{Version: 1, Configuration: configuration, Roles: make(map[string]gate.RoleIdentity), Supports: make(map[string]model.ToolResultPartSupport)}
	for _, role := range []string{"planner", "planner_small", "summariser"} {
		name := os.Getenv(roleEnvironment(configuration, role))
		if configuration == "offline" {
			name = map[string]string{"planner": "reader", "planner_small": "reader-small", "summariser": "summariser"}[role]
		}
		if name == "" || strings.TrimSpace(name) != name {
			return ConfigurationProfile{}, fmt.Errorf("%s must name an exact model", roleEnvironment(configuration, role))
		}
		identity := gate.RoleIdentity{Provider: configuration, Raw: name, Normalized: model.NormalizeProviderModel(configuration, name)}
		support, err := profileSupport(identity)
		if err != nil {
			return ConfigurationProfile{}, err
		}
		profile.Roles[role] = identity
		profile.Supports[role] = support
	}
	return profile, nil
}

// ProfileConfiguration constructs only request-reading offline clients with the
// recorded configuration's support and model identities.
func ProfileConfiguration(profile ConfigurationProfile, faults fake.Faults) (Configuration, error) {
	if err := validateProfile(profile); err != nil {
		return Configuration{}, err
	}
	factory := func(role string) func(context.Context) (model.Client, error) {
		support := profile.Supports[role]
		support = support.Intersect(support)
		limits := model.ContextLimits{ContextWindow: 1000000}
		if role == "planner_small" {
			// The synthetic smaller model must still hold the uncompacted
			// baseline; compacted model-change runs keep their 8k pretend window.
			limits.ContextWindow = 128000
		}
		if role == "summariser" {
			limits.ContextWindow = 200000
		}
		declared := fake.ModelProfile{Provider: profile.Configuration, Model: profile.Roles[role].Raw, Support: support, Limits: limits}
		return func(context.Context) (model.Client, error) {
			var provider model.Provider = &fake.Reader{Faults: faults, Profile: &declared}
			if role == "summariser" {
				provider = &fake.Summariser{Faults: faults, Profile: &declared}
			}
			client, err := model.NewClient(provider)
			if err != nil {
				return nil, err
			}
			if role == "summariser" {
				return fake.Reject(client), nil
			}
			return client, nil
		}
	}
	return Configuration{ID: profile.Configuration, Planner: factory("planner"), PlannerSmall: factory("planner_small"), Summariser: factory("summariser")}, nil
}

// ProfileHarness executes offline structural proof for a declared profile.
func ProfileHarness(profile ConfigurationProfile, faults fake.Faults) eval.HarnessFunc {
	configuration, configurationErr := ProfileConfiguration(profile, faults)
	return func(ctx context.Context, _ eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		if configurationErr != nil {
			return nil, configurationErr
		}
		opts = append(opts, agenttest.WithRunTimeout(2*time.Minute))
		return agenttest.NewHarness(ctx, func(ctx context.Context, rt *runtime.Runtime) error {
			for _, entry := range []struct {
				id        string
				newClient func(context.Context) (model.Client, error)
			}{{"planner", configuration.Planner}, {"planner_small", configuration.PlannerSmall}, {"summariser", configuration.Summariser}} {
				client, err := entry.newClient(ctx)
				if err != nil {
					return err
				}
				if err := rt.RegisterModel(entry.id, client); err != nil {
					return err
				}
			}
			return Register(ctx, rt)
		}, opts...)
	}
}

func validateProfile(profile ConfigurationProfile) error {
	if profile.Version != 1 || !knownConfiguration(profile.Configuration) || len(profile.Roles) != 3 || len(profile.Supports) != 3 {
		return fmt.Errorf("invalid or incomplete provider profile")
	}
	for _, role := range []string{"planner", "planner_small", "summariser"} {
		identity := profile.Roles[role]
		if identity.Provider != profile.Configuration || identity.Raw == "" || strings.TrimSpace(identity.Raw) != identity.Raw || identity.Normalized != model.NormalizeProviderModel(identity.Provider, identity.Raw) {
			return fmt.Errorf("invalid profile role %s", role)
		}
		expected, err := profileSupport(identity)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(expected, profile.Supports[role]) {
			return fmt.Errorf("profile support mismatch for %s", role)
		}
	}
	return nil
}

func profileSupport(identity gate.RoleIdentity) (model.ToolResultPartSupport, error) {
	switch identity.Provider {
	case "offline":
		return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, DocumentFormats: []model.DocumentFormat{"pdf"}, MaxImageBytes: 1 << 20, MaxDocumentBytes: 1 << 20}, nil
	case "anthropic":
		return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"gif", "jpeg", "png", "webp"}, DocumentFormats: []model.DocumentFormat{"pdf"}, MaxImageBytes: 5_000_000, MaxDocumentBytes: 24_000_000}, nil
	case "bedrock-anthropic":
		if !strings.HasPrefix(identity.Normalized, "anthropic.claude-") {
			return model.ToolResultPartSupport{}, fmt.Errorf("unknown Bedrock Anthropic model profile %q", identity.Raw)
		}
		return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"gif", "jpeg", "png", "webp"}, DocumentFormats: []model.DocumentFormat{"csv", "doc", "docx", "html", "md", "pdf", "txt", "xls", "xlsx"}, DocumentURISchemes: []string{"s3"}, MaxImageBytes: 3_750_000, MaxDocumentBytes: 4_500_000}, nil
	case "openai", "gemini":
		return model.ToolResultPartSupport{}, nil
	default:
		return model.ToolResultPartSupport{}, fmt.Errorf("unknown provider profile %q", identity.Provider)
	}
}

func knownConfiguration(id string) bool {
	switch id {
	case "offline", "anthropic", "bedrock-anthropic", "openai", "gemini":
		return true
	default:
		return false
	}
}

func roleEnvironment(configuration, role string) string {
	return "LONGRUN_" + strings.ToUpper(strings.ReplaceAll(configuration, "-", "_")) + "_" + strings.ToUpper(role)
}
