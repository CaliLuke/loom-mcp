// Package agenttest provides deterministic model scripts and an isolated,
// in-memory agent runtime harness for iteration under go test.
//
// New uses the test context, registers application components, installs scripted
// model overrides, and reports leaks at test cleanup. NewHarness exposes the same
// construction for callers that manage Close themselves. The harness owns its
// engine, run/content stores, session store and hook bus. WithRuntimeOptions may
// configure other runtime policies, memory and interceptors, but cannot replace
// those owned components or the tracer.
//
// Run accepts User text or arbitrary Input messages and returns the final output,
// sealed RunRecord, causal Trajectory and answered awaits. An agent failure is a
// Result.RunErr, not a harness error. Setup errors, unanswered awaits, timeouts
// and evidence reconstruction failures use the error return. Stub dispatch failures
// return a populated Result and joined harness error after normal run completion.
// The default run timeout is 30 seconds, including external waits. Cancellation drains the run;
// Close joins script diagnostics and any remaining lifecycle leaks.
//
// WithResponder supplies first-match AwaitRules for confirmation, clarification,
// questions, typed input and external tools. Callbacks execute outside hook
// publication and must be safe for concurrent runs and return promptly. Answer
// emits the standard answers/question_id/selected_ids JSON shape; QuestionsFunc
// supports application-owned result schemas. Result.Awaits retains typed signal
// answers and their observed prompts. If any registered BeforeEvent interceptor
// can alter events, observed trajectory evidence is marked unverified.
//
// RunSession starts ordered runs in a shared new session. Earlier turns are
// visible only through the agent's configured memory policy: the harness does no
// transcript carry-over. WithTracerProvider opens agenttest.run spans and wires
// runtime planner/model tracing to the supplied provider without modifying the
// global provider. TraceID is omitted when no provider is supplied. The caller
// owns provider shutdown and export configuration.
//
// Application-registered models are preserved unless a scripted override uses
// the same ID. No live model is selected by default. Live-model gating and
// Temporal execution are outside this harness.
//
// Activity tools are strict by default: declare Stub responses or opt into
// Passthrough / PassthroughAll. Stubs preserve registration, payload preparation,
// AfterTool and result materialization. Close reports every dispatch failure and
// unused finite response. RawStub supports registered tools without descriptors.
// Inline and agent-as-tool stubbing remain a runtime follow-up.
package agenttest
