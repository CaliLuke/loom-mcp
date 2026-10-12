package scenarios

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"example.com/longrun/env"
	"example.com/longrun/gen/longrun/toolsets/dispatch_planner"
	"example.com/longrun/gen/longrun/toolsets/ledger_clerk"
	"example.com/longrun/gen/longrun/toolsets/ops_deployer"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

func stubTools(h *agenttest.Harness, m *env.Materialized) {
	handle := func(step, target string) (string, error) {
		if step == "final" {
			data, err := json.Marshal(env.Directive{Step: "answer", Then: target})
			return "NEXT:" + string(data), err
		}
		result, ok := m.Results[step]
		if !ok {
			return "", fmt.Errorf("unknown materialised step %s", step)
		}
		return result, nil
	}
	agenttest.Stub(h, ops_deployer.ListServicesTool).Handle(func(_ context.Context, p *ops_deployer.ListServicesPayload) (ops_deployer.ListServicesResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, ops_deployer.ReadServiceLogTool).Handle(func(_ context.Context, p *ops_deployer.ReadServiceLogPayload) (ops_deployer.ReadServiceLogResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, ops_deployer.DeployTool).Handle(func(_ context.Context, p *ops_deployer.DeployPayload) (ops_deployer.DeployResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, ledger_clerk.SearchInvoicesTool).Handle(func(_ context.Context, p *ledger_clerk.SearchInvoicesPayload) (ledger_clerk.SearchInvoicesResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, ledger_clerk.ReadInvoiceTool).Handle(func(_ context.Context, p *ledger_clerk.ReadInvoicePayload) (ledger_clerk.ReadInvoiceResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, ledger_clerk.PostPaymentTool).Handle(func(_ context.Context, p *ledger_clerk.PostPaymentPayload) (ledger_clerk.PostPaymentResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, dispatch_planner.ListShipmentsTool).Handle(func(_ context.Context, p *dispatch_planner.ListShipmentsPayload) (dispatch_planner.ListShipmentsResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, dispatch_planner.ReadManifestTool).Handle(func(_ context.Context, p *dispatch_planner.ReadManifestPayload) (dispatch_planner.ReadManifestResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
	agenttest.Stub(h, dispatch_planner.BookCarrierTool).Handle(func(_ context.Context, p *dispatch_planner.BookCarrierPayload) (dispatch_planner.BookCarrierResult, error) {
		result, err := handle(p.Step, p.Target)
		return result, err
	}).Always()
}
