package evidence_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CaliLuke/loom-mcp/v2/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func ExampleCreate() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "local-evidence-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	store, err := evidence.Create(ctx, filepath.Join(dir, "capture"))
	if err != nil {
		panic(err)
	}
	rt := runtime.New(
		runtime.WithEngine(engineinmem.New()),
		runtime.WithRunStore(store),
		runtime.WithRunEventStore(store),
	)
	provider, err := model.NewClient(publicEvidenceProvider{})
	if err != nil {
		panic(err)
	}
	if err := rt.RegisterModel("model", provider); err != nil {
		panic(err)
	}
	if err := rt.RegisterAgent(ctx, runtime.AgentRegistration{
		ID: "root", Planner: publicEvidencePlanner{},
		Workflow:         engine.WorkflowDefinition{Name: "root.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "root.plan", ResumeActivityName: "root.resume", ExecuteToolActivity: "root.tool",
	}); err != nil {
		panic(err)
	}
	_, err = rt.MustClient("root").OneShotRun(ctx, []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "hello"}}}})
	if err != nil {
		panic(err)
	}
	// Stop all runs before closing the caller-owned store.
	if err := store.Close(); err != nil {
		panic(err)
	}
	snapshot, err := evidence.Read(ctx, filepath.Join(dir, "capture"))
	if err != nil {
		panic(err)
	}
	fmt.Println("roots:", len(snapshot.Records))
	// Output: roots: 1
}
