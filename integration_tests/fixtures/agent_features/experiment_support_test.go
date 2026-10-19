package agentfeatures_test

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	controlfixture "github.com/CaliLuke/loom-mcp/v2/eval/control/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

type supportFacts struct {
	Kind      string        `json:"kind"`
	ID        string        `json:"id"`
	Label     string        `json:"label"`
	Audience  string        `json:"audience"`
	Protected string        `json:"protected"`
	Body      string        `json:"body"`
	Nodes     []supportNode `json:"nodes,omitempty"`
	Edges     []supportEdge `json:"edges,omitempty"`
	Draft     bool          `json:"draft"`
}

type supportNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type supportEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

type supportAnalysis struct {
	Facts        supportFacts `json:"facts"`
	Summary      string       `json:"summary"`
	SourceDigest string       `json:"source_digest"`
}

type supportEnvironment struct {
	candidates  *candidate.Store
	experiments *eval.Store
	workspace   *inspect.Workspace
	executor    *controlfixture.DurableExecutor
	controller  *agenttest.Harness
	facts       map[string]supportFacts
	root        string
	directories []string
	calls       atomic.Int64
}

type supportRun struct {
	record  control.JobRecord
	frozen  control.Frozen
	outputs map[string][]byte
}

func TestExperimentSupportConsumers(t *testing.T) {
	for _, kind := range []string{"document", "graph"} {
		for _, style := range []string{"revision", "composition"} {
			t.Run(kind+"/"+style, func(t *testing.T) {
				env, err := newSupportEnvironment(t)
				require.NoError(t, err)
				var selected control.Frozen
				if style == "revision" {
					selected = supportRevision(t, env, "Evidence")
					run := supportExecute(t, env, selected, kind+"-A")
					assertSupportDomain(t, env.facts[kind+"-A"], run)
					assertSupportChild(t, env, run, "section=Evidence\n")
				} else {
					first, second, composed := supportComposition(t, env, kind+"-A")
					assertSupportDomain(t, env.facts[kind+"-A"], first)
					assertSupportDomain(t, env.facts[kind+"-A"], second)
					assertSupportDomain(t, env.facts[kind+"-A"], composed)
					assert.NotEqual(t, first.outputs, composed.outputs)
					assert.NotEqual(t, second.outputs, composed.outputs)
					assertSupportChild(t, env, composed, "section=Audit\n")
					selected = composed.frozen
				}
				reused := supportTaskB(t, env, selected, kind+"-B")
				assertSupportDomain(t, env.facts[kind+"-B"], reused)
				assertSupportChild(t, env, reused, "section=Review\n")
				assertSupportFreshFacts(t, env, kind, reused, selected)
			})
		}
	}
}

func TestExperimentSupportChildInstructions(t *testing.T) {
	env, err := newSupportEnvironment(t)
	require.NoError(t, err)
	first := supportExecute(t, env, supportRevision(t, env, "Evidence"), "document-A")
	second := supportExecute(t, env, supportRevision(t, env, "Audit"), "document-A")
	assertSupportChild(t, env, first, "section=Evidence\n")
	assertSupportChild(t, env, second, "section=Audit\n")
	assert.NotEqual(t, first.outputs["document.md"], second.outputs["document.md"])
}

func TestExperimentSupportUnsupportedComponents(t *testing.T) {
	env, err := newSupportEnvironment(t)
	require.NoError(t, err)
	draft := supportCreate(t, env)
	for _, slot := range []string{"memory", "plugins", "hooks", "permissions"} {
		content := "change host authority"
		_, err := supportCall[control.CandidateInfo](t, env, control.EditOperation, control.EditRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Operation: candidate.Put, Name: slot + ".txt", Slot: slot, MediaType: "text/plain", Content: &content})
		var failure *control.Error
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, control.InvalidInput, failure.Code)
	}
	current := supportMust[control.CandidateInfo](t, env, control.GetOperation, control.GetCandidateRequest{DraftID: draft.DraftID})
	assert.Equal(t, draft, current)
	assert.Zero(t, env.calls.Load())
	assertSupportUnsupportedHost(t, env)
}
