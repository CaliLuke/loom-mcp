# loom-mcp quickstart — model-backed planners

Use the repository's `quickstart/README.md` for the runnable application and
`docs/runtime.md` for provider construction and planner streaming contracts.
This page records only the registration boundary that commonly drifts.

Create the provider client, register it before runtime sealing, and resolve it
from the planner context:

```go
modelClient, err := openai.NewFromAPIKey(
    os.Getenv("OPENAI_API_KEY"),
    "gpt-4o",
)
if err != nil {
    return err
}

rt := runtime.New(runtime.WithStream(&ConsoleSink{}))
if err := rt.RegisterModel("openai", modelClient); err != nil {
    return err
}
```

In `PlanStart` or `PlanResume`:

```go
mc, ok := in.Agent.ModelClient("openai")
if !ok {
    return nil, fmt.Errorf("model client %q is not registered", "openai")
}
resp, err := mc.Complete(ctx, &model.Request{
    Messages: in.Messages,
})
```

Register every model, agent, and toolset before `Runtime.Seal` or the first
submitted run. Post-seal model rotation requires a replacement runtime.
