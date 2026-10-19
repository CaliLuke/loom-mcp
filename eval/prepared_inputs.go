package eval

import (
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// preparedInfo resolves defaults without changing legacy AttemptInfo semantics.
func preparedInfo(a plannedAttempt) PreparedAttemptInfo {
	info := attemptInfo(a)
	return PreparedAttemptInfo{Key: info.Key, Variant: info.Variant, Item: resolvedItem(a.scenario, info.Item)}
}

func validatePreparedLease(lease *PreparedAttempt, agentID agent.Ident) error {
	var problems []error
	if lease.Harness.Register == nil {
		problems = append(problems, errors.New("eval: prepared registration is required"))
	}
	if (lease.Inputs != nil) == (lease.Drive != nil) {
		problems = append(problems, errors.New("eval: prepared lease requires exactly one Inputs or Drive"))
	}
	if lease.Inputs != nil && (len(lease.Inputs) == 0 || agentID == "") {
		problems = append(problems, errors.New("eval: prepared inputs require nonempty turns and Agent"))
	}
	if lease.Drive != nil && lease.Receipt.DriverVersion == "" {
		problems = append(problems, errors.New("eval: prepared Drive requires DriverVersion"))
	}
	if lease.Quiesce == nil || lease.Collect == nil || lease.Release == nil {
		problems = append(problems, errors.New("eval: prepared Quiesce, Collect and Release are required"))
	}
	problems = append(problems, validatePreparedReceipt(lease.Receipt), validateDeliverables(lease.Deliverables))
	return errors.Join(problems...)
}

// ownPreparedInputs clones nested messages before registration can mutate source data.
func ownPreparedInputs(lease *PreparedAttempt) error {
	inputs := make([]agenttest.Input, len(lease.Inputs))
	for i, input := range lease.Inputs {
		if len(input) == 0 {
			return fmt.Errorf("eval: prepared input %d has no messages", i)
		}
		request, err := model.CloneRequest(&model.Request{Messages: input})
		if err != nil {
			return err
		}
		inputs[i] = request.Messages
	}
	if lease.Inputs != nil {
		lease.Inputs = inputs
	}
	return nil
}

// collectedDeliverables distinguishes omitted outputs from retained empty bytes.
func collectedDeliverables(declared []Deliverable, collected []CollectedArtifact) ([]CollectedArtifact, error) {
	wanted := make(map[string]string, len(declared))
	for _, item := range declared {
		wanted[item.Name] = item.MediaType
	}
	seen := make(map[string]bool, len(collected))
	var valid []CollectedArtifact
	var problems []error
	for _, artifact := range collected {
		mediaType, exists := wanted[artifact.Name]
		if !exists || mediaType != artifact.MediaType || seen[artifact.Name] {
			problems = append(problems, fmt.Errorf("invalid or duplicate collected deliverable %q", artifact.Name))
			continue
		}
		seen[artifact.Name] = true
		artifact.Data = slices.Clone(artifact.Data)
		valid = append(valid, artifact)
	}
	for _, item := range declared {
		if !seen[item.Name] {
			problems = append(problems, fmt.Errorf("missing declared deliverable %q", item.Name))
		}
	}
	return valid, errors.Join(problems...)
}
