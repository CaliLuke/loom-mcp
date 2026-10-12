package longrun

import (
	"context"
	"fmt"
	"os"
	"strings"

	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/features/model/anthropic"
	"github.com/CaliLuke/loom-mcp/v2/features/model/bedrock"
	"github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// Configuration declares constructors for the three fixed runtime model IDs.
type Configuration struct {
	ID                                string
	Planner, PlannerSmall, Summariser func(context.Context) (model.Client, error)
}

// ProviderConfiguration resolves explicit model choices; construction stays lazy.
// No live provider is constructed before Harness validates LiveModel opt-in.
func ProviderConfiguration(id string, faults fake.Faults) (Configuration, error) {
	if id != "offline" && id != "anthropic" && id != "bedrock-anthropic" && id != "openai" && id != "gemini" {
		return Configuration{}, fmt.Errorf("unknown provider configuration %q", id)
	}
	factory := func(role string) func(context.Context) (model.Client, error) {
		return func(ctx context.Context) (model.Client, error) {
			var provider model.Provider
			var err error
			if id == "offline" {
				provider = &fake.Reader{Faults: faults}
				if role == "summariser" {
					provider = &fake.Summariser{Faults: faults}
				}
			} else {
				provider, err = liveProvider(ctx, id, role)
			}
			if err != nil {
				return nil, err
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
	return Configuration{ID: id, Planner: factory("planner"), PlannerSmall: factory("planner_small"), Summariser: factory("summariser")}, nil
}
func liveProvider(_ context.Context, id, role string) (model.Provider, error) {
	key := "LONGRUN_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + strings.ToUpper(role)
	name := os.Getenv(key)
	if name == "" {
		return nil, fmt.Errorf("%s must name the live model", key)
	}
	switch id {
	case "anthropic":
		return anthropic.NewFromAPIKey(os.Getenv("ANTHROPIC_API_KEY"), name)
	case "openai":
		return openai.NewFromAPIKey(os.Getenv("OPENAI_API_KEY"), name)
	case "gemini":
		return gemini.NewFromAPIKey(os.Getenv("GEMINI_API_KEY"), name)
	case "bedrock-anthropic":
		region := os.Getenv("AWS_REGION")
		access, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
		if region == "" || access == "" || secret == "" {
			return nil, fmt.Errorf("Bedrock requires AWS_REGION and environment AWS credentials")
		}
		cfg := aws.Config{Region: region, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: access, SecretAccessKey: secret, SessionToken: os.Getenv("AWS_SESSION_TOKEN"), Source: "longrun environment"}, nil
		})}
		return bedrock.New(bedrockruntime.NewFromConfig(cfg), bedrock.Options{DefaultModel: name, MaxTokens: 4096})
	default:
		return nil, fmt.Errorf("unknown provider %s", id)
	}
}
