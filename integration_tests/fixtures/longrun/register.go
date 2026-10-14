// Package longrun registers the generated long-running evaluation agents.
package longrun

import (
	"context"
	"time"

	"example.com/longrun/fake"
	"example.com/longrun/gen/longrun/agents/dispatch_planner"
	"example.com/longrun/gen/longrun/agents/dispatch_planner_nosum"
	"example.com/longrun/gen/longrun/agents/dispatch_planner_reject"
	"example.com/longrun/gen/longrun/agents/ledger_clerk"
	"example.com/longrun/gen/longrun/agents/ledger_clerk_nosum"
	"example.com/longrun/gen/longrun/agents/ledger_clerk_reject"
	"example.com/longrun/gen/longrun/agents/ops_deployer"
	"example.com/longrun/gen/longrun/agents/ops_deployer_nosum"
	"example.com/longrun/gen/longrun/agents/ops_deployer_reject"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	runtime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

// Harness builds fresh clients and an isolated runtime for each attempt.
func Harness(config string, faults fake.Faults) eval.HarnessFunc {
	if config == "offline" {
		profile, err := ResolveProfile(config)
		if err != nil {
			return func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
				return nil, err
			}
		}
		return ProfileHarness(profile, faults)
	}
	return func(ctx context.Context, _ eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		opts = append(opts, agenttest.WithRunTimeout(2*time.Minute))
		if config != "offline" {
			for _, id := range []string{"planner", "planner_small", "summariser"} {
				opts = append(opts, agenttest.LiveModel(id))
			}
		}
		return agenttest.NewHarness(ctx, func(ctx context.Context, rt *runtime.Runtime) error {
			configuration, err := ProviderConfiguration(config, faults)
			if err != nil {
				return err
			}
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

// Register wires all nine generated registrations and the shared planner.
func Register(ctx context.Context, rt *runtime.Runtime) error {
	p := &Planner{}
	cm := &runtime.ContextModel{ID: "summariser"}
	if err := ops_deployer.RegisterUsedToolsets(ctx, rt); err != nil {
		return err
	}
	if err := ops_deployer.RegisterOpsDeployerAgent(ctx, rt, ops_deployer.OpsDeployerAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	if err := ops_deployer_nosum.RegisterOpsDeployerNosumAgent(ctx, rt, ops_deployer_nosum.OpsDeployerNosumAgentConfig{Planner: p}); err != nil {
		return err
	}
	if err := ops_deployer_reject.RegisterOpsDeployerRejectAgent(ctx, rt, ops_deployer_reject.OpsDeployerRejectAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	if err := ledger_clerk.RegisterUsedToolsets(ctx, rt); err != nil {
		return err
	}
	if err := ledger_clerk.RegisterLedgerClerkAgent(ctx, rt, ledger_clerk.LedgerClerkAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	if err := ledger_clerk_nosum.RegisterLedgerClerkNosumAgent(ctx, rt, ledger_clerk_nosum.LedgerClerkNosumAgentConfig{Planner: p}); err != nil {
		return err
	}
	if err := ledger_clerk_reject.RegisterLedgerClerkRejectAgent(ctx, rt, ledger_clerk_reject.LedgerClerkRejectAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	if err := dispatch_planner.RegisterUsedToolsets(ctx, rt); err != nil {
		return err
	}
	if err := dispatch_planner.RegisterDispatchPlannerAgent(ctx, rt, dispatch_planner.DispatchPlannerAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	if err := dispatch_planner_nosum.RegisterDispatchPlannerNosumAgent(ctx, rt, dispatch_planner_nosum.DispatchPlannerNosumAgentConfig{Planner: p}); err != nil {
		return err
	}
	if err := dispatch_planner_reject.RegisterDispatchPlannerRejectAgent(ctx, rt, dispatch_planner_reject.DispatchPlannerRejectAgentConfig{Planner: p, ContextModel: cm}); err != nil {
		return err
	}
	return nil
}
