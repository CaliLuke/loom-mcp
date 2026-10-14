package gate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Margins fixes the four release criteria.
type Margins struct {
	Success     float64 `json:"success"`
	Unsupported float64 `json:"unsupported"`
	Evidence    float64 `json:"evidence"`
	Recall      float64 `json:"recall"`
}

// Bootstrap records the immutable resample count and seed.
type Bootstrap struct {
	Resamples int    `json:"resamples"`
	Seed      uint64 `json:"seed"`
}

// FrozenItem binds an item ID to its materialized SHA-256.
type FrozenItem struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// Pool binds raw dataset bytes and ordered item identities to a partition.
type Pool struct {
	Scenario    string       `json:"scenario"`
	Partition   string       `json:"partition"`
	DatasetHash string       `json:"dataset_hash"`
	Items       []FrozenItem `json:"items"`
}

// Freeze is the immutable experiment contract. Hash the serialized value before
// running variants; keep that trusted hash separate from the mutable input.
// Scenario weights are equal within GatedScenarios. Policies are exact JSON.
type Freeze struct {
	Policy             *Policy                      `json:"policy,omitempty"`
	Selections         []CandidateSelection         `json:"selections,omitempty"`
	Roles              map[string]RoleIdentity      `json:"roles,omitempty"`
	Profiles           map[string]CapabilityProfile `json:"profiles,omitempty"`
	EffectivePolicies  map[string]json.RawMessage   `json:"effective_policies,omitempty"`
	SourceSHA256       string                       `json:"source_sha256,omitempty"`
	SourceBundleSHA256 string                       `json:"source_bundle_sha256,omitempty"`
	ValidationSHA256   string                       `json:"validation_sha256,omitempty"`
	InventorySHA256    string                       `json:"inventory_sha256,omitempty"`

	Format              string                     `json:"format"`
	Version             int                        `json:"version"`
	Configuration       string                     `json:"configuration"`
	Margins             Margins                    `json:"margins"`
	Alpha               float64                    `json:"alpha"`
	Looks               []int                      `json:"looks"`
	Repeats             int                        `json:"repeats"`
	Bootstrap           Bootstrap                  `json:"bootstrap"`
	Guard               float64                    `json:"guard"`
	GatedScenarios      []string                   `json:"gated_scenarios"`
	ReportedScenarios   []string                   `json:"reported_scenarios"`
	ScenarioContexts    map[string]json.RawMessage `json:"scenario_contexts"`
	Policies            map[string]json.RawMessage `json:"policies"`
	Pools               []Pool                     `json:"pools"`
	MaterializerVersion string                     `json:"materializer_version"`
	CalibrationSHA256   string                     `json:"calibration_sha256"`
	SimulatorVersion    string                     `json:"simulator_version"`
	GitRevision         string                     `json:"git_revision"`
	Dirty               bool                       `json:"dirty"`
	Created             time.Time                  `json:"created"`
}

// FreezeInputs supplies current bytes for offline verification before look/report.
type FreezeInputs struct {
	SourceSHA256       string                       `json:"source_sha256"`
	SourceBundleSHA256 string                       `json:"source_bundle_sha256"`
	ValidationSHA256   string                       `json:"validation_sha256"`
	InventorySHA256    string                       `json:"inventory_sha256"`
	CalibrationSHA256  string                       `json:"calibration_sha256"`
	Roles              map[string]RoleIdentity      `json:"roles"`
	Profiles           map[string]CapabilityProfile `json:"profiles"`
	EffectivePolicies  map[string]json.RawMessage   `json:"effective_policies"`

	Pools               map[string][]byte `json:"pools"`
	Materialized        map[string][]byte `json:"materialized"`
	MaterializerVersion string            `json:"materializer_version"`
}

// FreezeFormat identifies the versioned freeze document.
const FreezeFormat = "loom-mcp.eval.gate.freeze"

// DefaultMargins returns the binding K19 margins.
func DefaultMargins() Margins {
	return Margins{-.05, .02, -.05, .80}
}

// Digest returns a lowercase SHA-256 content address.
func Digest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// CanGenerateItems refuses pool regeneration after a freeze exists.
func CanGenerateItems(f *Freeze) error {
	if f != nil {
		return fmt.Errorf("gate: pools are frozen")
	}
	return nil
}

// Hash returns the deterministic JSON hash of the full freeze contract.
func (f Freeze) Hash() (string, error) {
	data, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return Digest(data), nil
}

// Verify rejects contract, dataset and materializer drift against a trusted hash.
func (f Freeze) Verify(expected string, in FreezeInputs) error {
	hash, err := f.Hash()
	if err != nil {
		return err
	}
	if expected == "" || hash != expected {
		return fmt.Errorf("gate: freeze hash mismatch")
	}
	if err := f.verifyV2(in); err != nil {
		return err
	}
	if in.MaterializerVersion != f.MaterializerVersion {
		return fmt.Errorf("gate: materializer version mismatch")
	}
	for _, p := range f.Pools {
		key := p.Scenario + "/" + p.Partition
		data, ok := in.Pools[key]
		if !ok || Digest(data) != p.DatasetHash {
			return fmt.Errorf("gate: pool mismatch: %s", key)
		}
		for _, item := range p.Items {
			data, ok := in.Materialized[item.ID]
			if !ok || Digest(data) != item.SHA256 {
				return fmt.Errorf("gate: materialized item mismatch: %s", item.ID)
			}
		}
	}
	return nil
}

// Label returns the required experiment label after hashing the freeze.
func (f Freeze) Label(look int) (string, error) {
	if look < 1 || look > 3 {
		return "", fmt.Errorf("gate: invalid look")
	}
	hash, err := f.Hash()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("gate/%s/%s/look%d", f.Configuration, hash[:8], look), nil
}

// VerifyLabel rejects experiments that do not belong to this frozen look.
func (f Freeze) VerifyLabel(label string, look int) error {
	want, err := f.Label(look)
	if err != nil {
		return err
	}
	if label != want {
		return fmt.Errorf("gate: experiment freeze label mismatch")
	}
	return nil
}
