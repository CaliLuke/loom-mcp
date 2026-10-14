// Package env materialises versioned, deterministic task environments.
package env

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

// Parameters is the complete generator input stored in each item.
type Parameters struct {
	Scenario    string `json:"scenario"`
	Template    string `json:"template"`
	Wording     int    `json:"wording"`
	Seed        uint64 `json:"seed"`
	Steps       int    `json:"steps"`
	ResultBytes int    `json:"result_bytes"`
}

// Domain supplies independently worded tools and tasks.
type Domain struct {
	Name, List, Read, Action string
	Wordings                 []string
}

// MaterializerVersion binds the pools to their exact interpretation.
const MaterializerVersion = "k19c-v4"

// Domains returns the three task templates.
func Domains() []Domain {
	return []Domain{
		{"ops_deployer", "list_services", "read_service_log", "deploy", []string{"Audit the rollout and deploy the approved target.", "Review service evidence before releasing.", "Complete the operational change using the latest approval."}},
		{"ledger_clerk", "search_invoices", "read_invoice", "post_payment", []string{"Reconcile the ledger and pay the approved account.", "Inspect invoice evidence before posting payment.", "Complete the payment using the latest authorisation."}},
		{"dispatch_planner", "list_shipments", "read_manifest", "book_carrier", []string{"Check the manifests and book the approved carrier.", "Review shipment evidence before dispatch.", "Complete the booking using the latest routing decision."}},
	}
}

// ScenarioIDs returns the contract's enumerated scenarios, including the reported model change.
func ScenarioIDs(config string) []string {
	return []string{"fact_early", "obsolete_index_line", "obsolete_collapsed", "obsolete_buried", "obsolete_cross_run", "large_result_middle", "decisions_dependencies", "recall_pressure", "multimodal_" + strings.ReplaceAll(config, "-", "_"), "no_summariser", "summariser_rejected", "session_three_runs", "model_change"}
}

// GatedScenarios excludes the reported model change.
func GatedScenarios(config string) []string {
	return slices.DeleteFunc(ScenarioIDs(config), func(s string) bool { return s == "model_change" })
}

// ReportedScenarios lists informational scenarios excluded from release criteria.
func ReportedScenarios() []string {
	return []string{"model_change"}
}

// Generate creates disjoint pilot, held-out and smoke items without touching disk.
func Generate(seed uint64) (map[string][]eval.Item, error) {
	pools := map[string][]eval.Item{}
	ids := ScenarioIDs("offline")
	for _, config := range []string{"anthropic", "bedrock-anthropic", "openai", "gemini"} {
		ids = append(ids, "multimodal_"+strings.ReplaceAll(config, "-", "_"))
	}
	for _, scenario := range ids {
		ordinal := 0
		for _, part := range []struct {
			name string
			n    int
		}{{"pilot", 6}, {"look1", 12}, {"look2", 12}, {"look3", 24}, {"smoke", 3}} {
			if scenario == "model_change" && part.name == "pilot" {
				continue
			}
			for n := range part.n {
				d := Domains()[n%3]
				itemSeed := seed
				if part.name == "smoke" {
					itemSeed ^= 0x9e3779b97f4a7c15
				}
				p := Parameters{scenario, d.Name, (n / 3) % 3, itemSeed + uint64(ordinal), 18, 10000}
				if scenario == "model_change" {
					p.ResultBytes = 2000
				}
				if scenario == "session_three_runs" {
					p.Steps = 18
					p.ResultBytes = 9000
				}
				data, err := json.Marshal(p)
				if err != nil {
					return nil, err
				}
				item := eval.Item{ID: fmt.Sprintf("%s.%s.%d", scenario, d.Name, ordinal), Input: d.Wordings[p.Wording], Data: data}
				pools[scenario+"/"+part.name] = append(pools[scenario+"/"+part.name], item)
				ordinal++
			}
		}
	}
	return pools, nil
}
