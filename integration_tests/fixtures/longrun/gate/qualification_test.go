package suite_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/stretchr/testify/require"
)

func TestShippedContextQualifications(t *testing.T) {
	for _, qualification := range compaction.Qualifications() {
		t.Run(qualification.Config, func(t *testing.T) {
			require.NoError(t, verifyQualificationEvidence(t, "..", qualification))
		})
	}
}

func TestQualificationRequiresCommittedEvidence(t *testing.T) {
	for _, config := range []string{"anthropic", "bedrock-anthropic", "openai", "gemini", "offline", "../outside"} {
		t.Run(config, func(t *testing.T) {
			q := compaction.Qualification{Config: config, Models: []string{"exact-model"}, Tier: compaction.Evict, Date: "2026-10-12"}
			require.Error(t, verifyQualificationEvidence(t, t.TempDir(), q))
		})
	}
}

// Attestations bind the canonical Freeze.Hash and raw calibration/report hashes.
// Stored reports use gate/report_<config>.json; gate/evidence_<config> contains
// the existing eval store layout. Verify a temporary copy: eval.Open initializes
// store metadata. V2 qualification belongs to the selected variant, independently of siblings.
func verifyQualificationEvidence(t *testing.T, dir string, q compaction.Qualification) error {
	t.Helper()
	if q.Config == "" || q.Config == "offline" || filepath.Base(q.Config) != q.Config || len(q.Models) == 0 || (q.Tier != compaction.Evict && q.Tier != compaction.Summary) {
		return fmt.Errorf("invalid qualification identity")
	}
	if _, err := time.Parse(time.DateOnly, q.Date); err != nil {
		return err
	}
	freeze, err := suite.VerifyFreeze(dir, q.Config)
	if err != nil {
		return err
	}
	hash, err := freeze.Hash()
	if err != nil {
		return err
	}
	if hash != q.Freeze || freeze.CalibrationSHA256 != q.Calibration {
		return fmt.Errorf("qualification freeze or calibration hash mismatch")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	data, err := root.ReadFile(filepath.Join("gate", "report_"+q.Config+".json"))
	if err != nil {
		return err
	}
	if gate.Digest(data) != q.Report {
		return fmt.Errorf("qualification report hash mismatch")
	}
	var committed suite.Report
	if err := json.Unmarshal(data, &committed); err != nil {
		return err
	}
	copied := t.TempDir()
	if err := os.CopyFS(copied, os.DirFS(filepath.Join(dir, "gate", "evidence_"+q.Config))); err != nil {
		return err
	}
	store, err := eval.Open(copied)
	if err != nil {
		return err
	}
	verified, err := suite.StoredReport(dir, q.Config, store, true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(committed, verified) || !qualifiedVariantPassed(verified.Decisions, q.Tier) {
		return fmt.Errorf("qualification requires matching verified PASS report")
	}
	return verifyQualificationModels(root, q, freeze, store)
}

func verifyQualificationModels(root *os.Root, q compaction.Qualification, freeze gate.Freeze, store *eval.Store) error {
	data, err := root.ReadFile(filepath.Join("gate", "looks_"+q.Config+".json"))
	if err != nil {
		return err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return err
	}
	observed := make(map[string]bool)
	for look, id := range ids {
		attempts, err := suite.Overlay(store, id, freeze, look+1)
		if err != nil {
			return err
		}
		for _, attempt := range attempts {
			for _, run := range attempt.Runs {
				record, err := run.Record()
				if err != nil {
					return err
				}
				if err := collectQualificationModels(record, q.Config, observed); err != nil {
					return err
				}
			}
		}
	}
	names := make([]string, 0, len(observed))
	for name := range observed {
		names = append(names, name)
	}
	slices.Sort(names)
	wanted := slices.Clone(q.Models)
	slices.Sort(wanted)
	if !slices.Equal(names, wanted) {
		return fmt.Errorf("qualification models differ from recorded exact identities: %v", names)
	}
	return nil
}

func collectQualificationModels(record *runrecord.RunRecord, config string, observed map[string]bool) error {
	for _, run := range record.Runs {
		for _, call := range run.Calls {
			if call.Completion == nil || call.Completion.Facts == nil || !call.Completion.Facts.ViewBearing {
				continue
			}
			fact := call.Completion.Facts
			if fact.ProviderConfig != config || fact.Model == "" {
				return fmt.Errorf("qualification call configuration mismatch")
			}
			observed[model.NormalizeProviderModel(config, fact.Model)] = true
		}
	}
	return nil
}

// qualifiedVariantPassed reads only the requested tier's retained terminal evidence.
func qualifiedVariantPassed(decisions []gate.Decision, tier compaction.Tier) bool {
	if len(decisions) == 0 {
		return false
	}
	variant := "receipt_only"
	if tier == compaction.Summary {
		variant = "hybrid"
	} else if tier != compaction.Evict {
		return false
	}
	for _, decision := range decisions[len(decisions)-1].Variants {
		if decision.Variant == variant {
			return decision.Status == gate.Pass
		}
	}
	return false
}

func TestQualificationUsesIndividualVariantPass(t *testing.T) {
	for _, sibling := range []gate.Status{gate.Fail, gate.Incomplete, gate.Continue, gate.CalibrationIneligible} {
		decisions := []gate.Decision{{Status: gate.Complete, Variants: []gate.VariantDecision{
			{Variant: "receipt_only", Status: gate.Pass},
			{Variant: "hybrid", Status: sibling},
		}}}
		require.True(t, qualifiedVariantPassed(decisions, compaction.Evict))
		require.False(t, qualifiedVariantPassed(decisions, compaction.Summary))
	}
	require.False(t, qualifiedVariantPassed(nil, compaction.Evict))
	require.False(t, qualifiedVariantPassed([]gate.Decision{{Status: gate.Pass}}, compaction.Summary))
}
