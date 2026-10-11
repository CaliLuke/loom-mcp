# Runtime Contracts

Use this file for current loom-mcp runtime behavior in this repo. Prefer it over stale external notes.

## Typed Tool Identity

- `tools.TypedTool[P, R]` contains only `Name tools.Ident`,
  `Payload tools.JSONCodec[P]`, and `Result tools.JSONCodec[R]`.
  Schemas and runtime metadata remain in `tools.ToolSpec`.
- Both `tools.Ident` and `tools.TypedTool[P, R]` implement
  `tools.Named` through `Ident() tools.Ident`.
- Generated typed specs and helper packages expose one descriptor per tool,
  preserving exact codec pointer/value types. Descriptor allocation protects
  constants, types, and derived Spec/Inject/Decode/With identifiers; descriptors
  receive suffixes on collision while existing names stay stable. Registry
  specs have untyped codecs and use `tools.Ident` for Named-only APIs without
  typed descriptors.

## Tool Stubs

- `agenttest` always installs a first runtime BeforeTool interceptor. Activity
  tools are strict by default; `Stub(h, tool)` keeps real registration, payload
  preparation, AfterTool and result materialization. Generated executors may be
  omitted. `Runtime.ToolDispatchMode(name)` exposes the resolved mode; unknown,
  duplicate, inline and agent-as-tool stub declarations are rejected.
- Return/Fail/Handle append responses; FIFO becomes first-available matching
  selection with When. ArgsEqual(tool, want) uses the descriptor payload codec
  and canonical JSON; captured want-encoding errors surface only at matching.
  Matchers are never invoked at declaration. Times defaults to one; Always is
  exempt from unused cleanup. Bounds is checked before Run and at Close for
  bounded Return declarations. After respects cancellation.
- StubError records strict/exhausted/unmatched/decode failures. The model sees
  normal runtime tool errors and the run continues; after completion Run returns
  populated evidence plus joined per-run StubErrors. Close reports them all.
  RawStub result-decoder rejection is a StubError in both call records. Later
  runtime result-encoding rejection matches real execution and is not a
  StubError. RunSession
  retains completed evidence before stopping on a harness error.
- Passthrough/PassthroughAll allow real execution of unstubbed activity tools;
  explicit stubs take precedence. RawStub uses rawjson.Message and decodes raw
  results through the registered codec. Calls/ToolCalls are arrival-ordered
  snapshots with copied raw bytes and shallow typed values; real passthrough
  outcomes are read from the trajectory. Parallel identical calls race for FIFO
  responses. Matchers run under the table lock and cannot call harness/stub
  methods; Handle callbacks run outside it.

## Expectations

- `expect` has sealed subjects built with `Calls`, `Trajectory` and `Session`.
  Trajectories include children immediately after their parent tool call;
  `TopLevel` excludes descendants. Session members remain in caller order.
  Listing a child separately from a parent that contains it counts the child
  twice, including calls, events, completion checks and budgets.
- `Call` and `Failed` use typed tool predicates and greedy ordered subsequence
  matching; `Decoded` uses an explicit argument codec. Nil `Call` predicates
  skip decoding. `Exact` matches one-to-one and rejects nested `Forbid` at
  construction. `Forbid` inspects every selected call. Codec errors are quoted.
- Model-choice `Calls` subjects cannot supply execution or run evidence:
  result, error, bounds, await, completion and budget checks fail with
  `not applicable to a model-call subject`.
- `Completed` requires verified successful status on every selected run;
  unverified status fails with `evidence unverified: status`. `Await` preserves
  the containing step's verification flag, examines only the requested planner
  kind and fails with `evidence unverified: await_<kind>` before decoding or
  invoking predicates on unverified evidence. `EventsUnverified` never silently
  passes these checks; record-derived calls and totals remain usable.
- `AllAttemptsSuccessful` checks earlier failures as well as later successes,
  and fails with `no call to <tool>` if the tool was never called. `Bounds`
  inspects recorded bounds; `Failed` distinguishes a matching successful call
  with `call to <tool> did not fail`.
- `MaxModelCalls` counts adopted calls; `MaxTokens` counts all recorded attempts,
  including input, output and both cache token fields, over selected runs.
- `ExpectedTools` matches recursive object subsets, exact arrays and exact
  decimal numbers using v2 JSON decoding without float64 coercion. Malformed
  actual arguments are non-matches reported in a failed check's diagnostic;
  valid matches still contribute to `tool_correctness` (matching / made).
  With no calls the score is 1 if nothing was expected, otherwise 0.
- Passed checks have no diagnostic; failed checks have a non-empty diagnostic.
  `Evaluate` gives checks unique names, and `Require` fails once with all failed
  diagnostics in declaration order. Requiring no expectations fails.

## Eval Scenarios

- `eval.Items` and `eval.JSONL` own immutable datasets and defer errors to Err;
  empty datasets report ErrEmptyDataset. Items returns deep copies. Hash is
  sha256:<hex> of canonical version-1 JSONL, or empty for an invalid dataset.
- JSONL starts with `{"format":"loom-mcp.eval.items","version":1}`. Decode
  rejects unknown/duplicate members (including raw data/args), invalid UTF-8,
  blank lines, repeated headers, unsupported versions, non-objects, multiple
  values per line, CRLF endings, leading/trailing line whitespace and lines over
  16 MiB excluding LF. Exactly 16 MiB is accepted; a final LF is optional.
  Header-only files report ErrEmptyDataset at line 2. DecodeError carries
  source/line and unwraps the cause. Validate prefixes every diagnostic line
  with its scenario location without losing typed errors or causes.
  Encode sorts objects, preserves number tokens and ends
  each line with LF.
- Item requires exactly one Input or Turns with no empty turn. IDs/tags match
  `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`; duplicate IDs/tags are invalid. Persona is
  reserved; ExpectedOutcome requires Turns. ExpectedTools is a pointer: absent
  differs from an explicit empty tools list. All strings are valid UTF-8.
- Scenario requires Agent unless Drive is set, non-negative Timeout, a valid
  dataset and an assertion source. Validate joins every problem with scenario
  and item locations. Claim IDs match `[a-z0-9_]{1,64}`, are unique and have
  non-empty UTF-8 text. Claims are declarations only; no eval/judge import.
- Execute resolves item Task/Reference before defaults, then runs one session.
  Drive replaces RunSession; Assert receives Attempt.Subject and the last
  trajectory's Final. Checks order runs_succeeded, Expect, ExpectedTools, Assert.
  Scores include tool_correctness; duplicate check/score names receive #n.
  Scenario claims precede Assert claims; duplicate claim IDs are errors.
- RunErr is a quality failure in runs_succeeded, independent of event trust.
  Harness/Drive errors preserve session evidence, empty checks/scores and skip
  Assert. Callback errors/panics (with stack), timeout and malformed results go
  to Outcome.Err. Execute's error return covers invalid arguments/nil harness.
  ErrNoAssertions requires a user check or claim beyond the built-in health
  check. Passed checks have no diagnostic; failed checks have one. Names are
  non-empty and score values finite.
- Timeout covers Drive and Assert cooperatively; callbacks/predicates must be
  concurrency-safe. Runner selection, repeats, variants, storage and judging
  remain separate from single-attempt Execute.

## Eval Runner

- eval.Plan validates all config and selector intersections, then enumerates
  variant/scenario/item/repeat keys. eval.Run creates one fresh harness per attempt
  and closes it once. HarnessFunc forwards options; NewHarnessFunc applies base
  options before variant options and closes failed or panicking setup.
- A nil variant Policy preserves earlier WithDefaultRunOptions policy defaults.
  Non-nil policies still override defaults and are cloned per run.
- Variants carry labels, per-run cloned Policy and agenttest Options for models
  and context runtime settings. WithDefaultRunOptions runs before caller options
  on every harness run path; reserved RunID validation includes defaults.
- Selection is OR within each kind and AND across kinds, with bare or qualified
  item IDs. All scenarios are validated. Zero repeats/concurrency means one.
  Longest timeouts start first (zero longest); records keep declaration order.
- Verdict precedence is error, failed, pending, passed. Unjudged gating claims
  remain pending. HarnessErr records factory/setup/panic/reuse/Close failures.
  Groups counts verdicts without gating. OnAttempt is serial, once per record,
  including not-run records last in declaration order. A panic disables further
  callbacks, stops dispatch and returns the panic with its stack after workers
  drain. The stop reason is callback_panic unless already stopped.
- MaxTokens uses >= and counts all completed calls in returned records, including
  descendants and abandoned attempts; positive TotalTokens wins over the sum of
  token components. Decide calls inside Assert and unreturned Drive runs are not
  counted. Running attempts finish after a token stop, allowing Concurrency-1
  additional attempts beyond the crossing attempt; skipped keys may vary.
- Attempts recheck cancellation before constructing a harness; canceled
  dispatches remain not_run. The first stop reason wins for Report.Stop and
  skipped records; later cancellation joins existing errors, preserving the
  ErrLiveModelDisabled match.
- Cancellation and disabled live factories return partial reports with an error;
  token stops and recorded failures do not. Run never enables live models. Every
  planned key retains a record, with nil Outcome iff NotRun is set. Runs exports
  each completed session run and descendants with session/trace IDs; exports stay
  in memory. AttemptKey identifies attempts because run/session IDs may repeat.

## Eval Judge

- `eval/judge` owns labels, ordered claim schemas, deterministic forced-tool
  requests, strict response decoding, calibration and caches. It imports neither
  eval nor trajectory; subject rendering and result mapping belong to T17.
- Inputs require a subject and unique claim IDs matching `[a-z0-9_]{1,64}` and
  non-empty UTF-8 claim text. Schemas are capped at 1 MiB. Complete canonical
  requests, including corrections, are capped at MaxInputBytes (default 512 KiB).
  Initial validation/size failures make zero calls, including calibration.
- Complete uses ModelID, MaxOutputTokens > 0, temperature zero, Thinking nil and
  forced record_judgments. Codex rejects MaxTokens and cannot serve as the judge.
  Properties and judgments retain input order. Only entailed is a passing label;
  the other labels are contradicted, not_addressed and indeterminate.
- JSON v2 rejects duplicate/unknown members; keys must exactly match the claims,
  labels must be valid and rationales non-empty UTF-8. Three corrections maximum
  follow the first call. Raw corrections echo tool calls with error results;
  malformed JSON is carried as text (base64 for invalid UTF-8). Validated output
  rejections get a user correction naming Kind without hidden output. Other
  provider errors return immediately; exhausted corrections wrap ErrInvalidOutput.
- Input.Truncated announces renderer elision in the prompt and Result. The judge
  never truncates. Whitespace subjects yield not_addressed and NoCall without
  calibration. Usage/Latency count judgment calls plus corrections, excluding
  calibration. Errors can return partial results.
- Calibration is singleflight per Judge under the initiating context and a
  two-minute default CalibrationTimeout. Four built-ins precede user Examples;
  each gets one call without correction. All labels must match. PromptHash hashes
  the versioned prompt contract; CalibrationKey adds model, output cap and examples.
- Cache completed passes and failures until Recalibrate; infrastructure failures
  are not cached and keep Result.Calibrated false without blocking judging. A Judge
  retains its outcome; retry with a new Judge. Default cache is process-wide;
  NewMemoryCache isolates suites and NewDirCache atomically replaces <hex>.json.
  Corrupt files miss. The CLI can use the store's _calibration directory. Judgment
  results are never cached.
- Live:true calls agenttest.LiveModelEnabled at construction; disabled wraps
  ErrLiveModelDisabled, invalid values error, and neither makes a model call.
  Live:false leaves scripted/undeclared clients available without an env check.

## Eval Experiment Store

- eval.Open creates a store with its own .gitignore. DefaultStoreDir finds
  .loom-evals beside the nearest ancestor go.mod. UUIDv7 directories contain an
  atomic experiment.json and append-only attempts.jsonl with a version-1 header.
- Begin validates the plan, label and parent. It stages and syncs both headers
  before publishing the UUID directory with a rename and root fsync. Readers ignore
  hidden staging directories left by failures. Writer.OnAttempt appends and syncs
  one line per index. Finish checks every planned key, appends missing records,
  and commits complete or stopped status. A crash leaves running status.
- Write errors stay in Writer.Err. WithOnError fires once, outside the writer
  lock. Validation errors do not poison the writer. Size overflow omits exports
  and records storage_error. The default threshold is 128 MiB, on writes only.
- Load rejects unknown and duplicate members, unsupported schemas and invalid
  exports. A torn final line sets TruncatedTail and preserves earlier attempts.
  StoredRun.Record and Trajectory need no harness. Store Final, EventsUnverified
  and TraceID beside Export.Marshal bytes. DecodeMessage preserves number tokens.
- Version 1 reserves StoredClaim subject/tool, StoredJudgment subject/step
  (run, index, tool, call_id), and attempt judge_usage/judge_tokens. Empty optional
  fields and zero judge usage/tokens are omitted. Decode retains these fields;
  judging integration populates them. wireClaim uses explicit field mapping so
  adding fields to Claim does not break the store's build.
- Fingerprints use recorded Started.ModelID, Class, Prompts, Request.System and
  Request.Tools. Context hashes cover each root run's context, recovery,
  compaction and retained-tool configuration. Finish stores sorted set unions per
  variant. Policy keeps Go field names. Opaque variant Options are not stored.
- List sorts by descending Started and ID. Resolve accepts a full ID, unique
  prefix of at least eight characters, or last. Last requires an overlapping
  scenario/item pair and excludes the supplied ID. Both skip running summaries.
  There is no last pointer file. Complete includes fully recorded stopped runs.
- RerunSelection includes failed, error and not_run groups, excluding pending.
  It returns the accepted variant/item cross-product superset for all repeats.
- Full prompts, tool results, server-only bytes and rejected responses stay local
  without redaction. New directories use 0700 and new files use 0600. Open preserves
  existing directory permissions, which the caller owns. Nothing is uploaded or
  deleted. Git identity detection disables optional locks and never refreshes the
  index. Redaction would invalidate exported request digests.

## Eval Show and Compare

- Show uses default trajectory limits unless Full is set. Render panics and
  missing exports become diagnostics. ShowJSON embeds trajectory.Marshal bytes;
  Export additionally includes raw exports. Summary orders non-passed groups
  first and uses full experiment IDs. JSON formats are loom-mcp.eval.show,
  loom-mcp.eval.summary and loom-mcp.eval.compare, all version 1.
- Compare selects one variant per side and pools all repeats by scenario/item.
  CompareExperiments aligns shared variants, or sole variants even with different
  IDs. CompareVariants is required within one experiment. No shared variants is
  an error. Parent overlays replace whole variant/scenario/item groups; the ID
  must match candidate.Header.Parent. Warnings use candidate header provenance.
- Compare only the intersection; list other pairs separately. JSON groups follow
  candidate declaration order. Text orders regressed, improved, not comparable,
  collapsed unchanged count, totals. Totals cover the intersection. More than two
  variants use one column per variant against the first.
- ran excludes not_run; pending never passes. Integer cross-products compare pass
  fractions. Zero ran on either side is not comparable. Checks use passed/total;
  claims use entailed/total judged units by claim/subject/declared tool selector.
  Legacy empty subjects mean answer. Samples use the first non-passed candidate
  by repeat, or baseline for improvement. Score medians retain fractions; absent
  scores are null. Ran-only token/duration medians floor an even midpoint.
- Modal tool sequences flatten children depth-first immediately after their
  parent tool. Ties use lowest repeat; share excludes unavailable trajectories,
  counted in tools_unavailable. LCS ties delete first. No tool delta is inferred
  when either side has no available sequence.
- Stable warning codes are dataset_changed, no_agent_change, judge_changed,
  judge_uncalibrated, judgments_truncated, incomplete, variant_unmatched.
  no_agent_change includes models and policy as well as hashes/context/prompts.
  Calibration and truncation mark groups and claim rows without suppressing status;
  text truncation fractions pool both sides. Keep report examples snippet-synced.

## Planner Streaming

- `PlannerContext.ModelClient(id)` returns a runtime-decorated client.
- A planner can drain the decorated stream itself with `Recv()` and `Finalize`,
  or pass it to `planner.ConsumeStream`. The stream ownership marker prevents
  duplicate planner events in the helper path.
- `planner.ConsumeStream` also recognizes runtime presentation events when a
  validated stream is not decorated, so both supported paths use the same
  durable acceptance boundary.
- `planner.ConsumeStreamWithObserver` calls a fallible observer for every
  accepted chunk variant. It keeps the same event ownership and presentation
  behavior as `ConsumeStream`.
- The observer error is the exact primary error supplied to `Finalize`.
  `StreamConsumptionError` exposes that primary error and the finalization
  result separately. Both remain available through `errors.Is` and `errors.As`.
- `ConsumeStreamWithObserver` returns the canonical `Response` only after
  literal EOF. The response remains available when finalization reports a
  cleanup failure after EOF.
- Drain to the literal `io.EOF`; wrapped EOF is a provider failure.
- Call `ValidatedStreamer.Finalize(primaryErr)` after draining or aborting.
  `Close` is cleanup-only and does not prove successful completion.
- `Response()` becomes available only after the consumer observes literal EOF;
  internal provider draining does not satisfy this boundary. The first
  `Finalize` call owns the authoritative joined result.
- Context cancellation observed before terminal acceptance rejects the stream.
  Tracing, rate limiting, and other lifecycle observers commit only from
  `Finalize`, after cleanup errors are known.
- Final tool calls and typed completions are withheld until terminal validation
  accepts the entire stream.
- Each runtime model stream starts one provisional presentation. Live text and
  thinking share its presentation ID. Stream finalization stages valid content;
  the planner activity emits exactly one accepted or discarded outcome.
- Acceptance follows planner success and one atomic canonical response write for
  all ready presentations in the activity. The committed assistant-turn payload
  carries their presentation IDs plus the authoritative response messages. If
  the planner or write fails, discard every presentation and fail the attempt;
  never retain an accepted client projection from an activity that can retry.
- Canonical presentation commits support the model output boundary rather than
  the smaller ordinary-hook payload limit. Bound the aggregate activity commit
  before staging a presentation so oversized batches never become accepted.
- When a final planner response has the same visible text as an accepted model
  presentation, the runtime owns streamed-result suppression. A missing planner
  `Streamed` flag cannot create a duplicate canonical assistant turn.
- Runtime model tool-argument fragments stay private until validation produces
  a complete tool call. They are not provisional presentation events.

## Codex Subscription Provider

- Applications supply a context-aware credential source. The provider resolves
  it once for each logical request.
- The provider does not find, refresh, or store credentials. It does not read
  environment variables or Codex CLI state.
- Requests use fixed ChatGPT Codex endpoints. Tests and desktop applications can
  inject HTTP and WebSocket transports.
- Automatic transport selection uses WebSocket first. It can retry once with SSE
  only before canonical output reaches the caller.
- Codex tool schemas retain exact numeric tokens in SSE, WebSocket, and Lite
  requests. Send `strict: false` explicitly to preserve the authored schema;
  validated clients enforce its exact bounds before accepting tool arguments.
- A raw stream exposes its response only after literal `io.EOF`. `Close` is
  cleanup-only.
- Provider errors contain safe codes, status, request IDs, and retryability. They
  never contain credentials or response bodies.
- The provider does not implement `model.TokenCounter`. Exact provider-native
  counting is not available.

## Planner Activity Retries

- The runtime schedules one logical `PlanStart` or `PlanResume` turn, but the
  engine may invoke its Go method multiple times when an activity attempt fails.
- Generated registrations allow up to three activity attempts by default.
- A late failure can repeat a completed model call or planner-owned side effect;
  planners must be retry-safe and use stable idempotency keys for non-idempotent
  external effects.
- Planner errors fail the current activity attempt. The run fails only after the
  configured activity retries are exhausted or the error is non-retryable.
- `PlanResult.RetryHint` is successful planner output used to recover from tool
  failures. It is separate from engine activity retries.
- `AwaitClarification.Question` must contain non-whitespace text. Start and
  resume planner activities reject blank questions before committing model
  presentations or storing planner-result content, discard staged presentations,
  and return a non-retryable `*engine.ApplicationError` with type
  `runtime.PlannerContractErrorType` (`planner_contract`). Valid question text
  is preserved.

## Cancellation Classification

- The in-memory engine uses `StartWorkflow`'s context to accept the request and retain context values. After acceptance, the run has an independent lifetime; cancel it through the workflow handle or `CancelByID`. Child runs started with the parent workflow context remain tied to parent cancellation.
- A positive in-memory `WorkflowStartRequest.RunTimeout` sets a fixed execution deadline; zero leaves the run without one. Timeout cancellation is cooperative, and a timed-out run fails with `context.DeadlineExceeded`. Agent runtime starts continue to omit this fixed timeout so external waits can pause their active-time budget.
- For Temporal `WorkflowContext.Await`, use the same workflow's `Context()` or
  derive from it only to add values. Active native Go cancellation/deadline
  contexts and foreign workflow scopes are rejected. To cancel while waiting,
  create a scope with `WithCancel()` and call the returned workflow context's
  `Await` with its own `Context()`. The adapter returns the engine cancellation
  marker and preserves SDK inspection of the original cause.
- The runtime classifies the complete error graph. Every leaf must be
  `context.Canceled` or the engine cancellation marker for a canceled result.
- The classifier has fixed limits for depth, visits, and children. Cycles,
  typed nil errors, panics, and oversized graphs are failures. Each decision
  uses one bounded inspection, including stable Temporal marker discovery.
- A mixed cancellation and failure graph fails the run. The returned error
  keeps the cancellation leaf and each independent failure leaf.
- The Temporal adapter makes a mixed graph non-retryable. A stable application
  error type restores cancellation evidence after Temporal failure conversion.
  This applies to activities, children, top-level waits, and restart queries.
- The Temporal adapter replaces a rejected graph with a static, non-retryable
  failure. The failure converter does not traverse the rejected graph.
- All completion paths replace invalid graphs before interceptors, hook
  classification, logging, or serialization can inspect them.
- `context.DeadlineExceeded` is a timeout failure. It is not a cancellation.
- A canceled tool activity or agent child stops the owning run. The runtime
  does not create an ordinary failed tool result for this condition.
- Telemetry suppresses context termination only when the operation context is
  also done. Mixed gRPC or Go error graphs remain visible as errors.

## Agent-As-Tool

- Agent-as-tool runs as a real child workflow, not an inline local shortcut.
- Temporal parent closure requests child cancellation; it must not terminate a
  child run as a generic failure.
- Parent and child runs are linked with a `ChildRunLinked` event.
- Parent tool results carry a `RunLink` to the child run.
- Runtime execution goes through `ExecuteAgentChildWithRoute`.
- `AgentToolConfig.Route` is required; there is no fallback to ad hoc local lookup.
- Consumer-side prompt rendering is optional and payload-only. Provider-side context belongs in the provider planner/runtime, not the consumer.
- Generated helper packages expose `NewRegistration(...)`; runtime internals build the underlying registration with `runtime.NewAgentToolsetRegistration(...)`.

## Stored Tool Outputs and Run Completion

- Workflow protocol 3 removes `RunOutput.ToolEvents`. `ToolEventsRef` addresses
  one stored list in `RootRunID`; `ToolEventCount` supplies `ChildrenCount` for
  agent-as-tool parents. `ToolSummary` preserves child error and telemetry rules.
- `runtime.LoadRunToolEvents(ctx, store, rootRunID, ref)` restores full result and
  server-data bytes outside workflows. Missing content fails with wrapped store
  errors. Authorize root-scope access before exposing server-only content.
- Every planner input builder omits only the bytes whose corresponding refs are
  set. `PlanResumeActivity` restores them from `Runtime.RunStore` in the root
  scope before invoking the planner. Unreferenced entries remain inline.
- Every RunOutput-producing runtime path schedules `Runtime.FinishRunActivity`
  once in a detached workflow scope. It stores the omitted-bytes event list and
  loads referenced completion results with `LoadToolResult` and the generated
  codec. Final tool-result envelopes are bounded to 1,000,000 encoded bytes.
- `RegisterAgent` registers `<agentID>.finish` on the workflow queue. Manual
  registrations use `FinishRunActivityName`, `Engine.RegisterFinishRunActivity`,
  and `api.FinishRunInput`/`FinishRunOutput`; custom workflow contexts implement
  `ExecuteFinishRunActivity`. Workers and clients share the run store.
- Typed workflow state, transcript history, inline/child/provided result storage,
  hooks, run logs, and memory projection remain separate later work.

## Streams

- Streams are session-owned.
- `stream.StreamProfile` controls visibility by audience.
- Child runs are linked, not flattened, by default.
- Use profile selection to shape chat, debug, or metrics views instead of changing core runtime behavior.

## Prompt Runtime

- `Runtime.PromptRegistry` stores baseline prompt specs.
- `runtime.WithPromptStore(...)` adds scoped overrides.
- Planners should render prompts through `RenderPrompt(...)` so provenance flows into model requests.
- Mongo prompt overrides persist a canonical SHA-256 label-scope fingerprint.
  Resolution uses at most one session query plus one global query against the
  fingerprint index, ordered by label specificity and creation time; it does
  not scan same-specificity candidates in Go. Mongo client startup backfills
  missing `scope_fingerprint` values before creating the compound index and
  fails construction on migration errors.
- The Mongo adapter accepts at most 15 labels per stored or requested scope so
  the exact matching-subset fingerprint set remains bounded.

## Registration Lifecycle

- `Runtime.Seal(ctx)` closes agent, toolset, and model registration before
  activating staged workers. The first submitted run closes the same registry.
- `RegisterModel` returns `ErrRegistrationClosed` after that boundary. Loom does
  not support post-seal model-client hot-swapping; replace the runtime instead.
- Registration remains closed even when the first `Seal` activation attempt
  fails. A later successful `Seal` retry is idempotent.

## Registry-Routed Provider Lifecycle

- `runtime/toolregistry/provider.Serve` owns one provider incarnation from
  registration through lease renewal, drain, local settlement, acknowledgement
  drain, and exact-token release. Applications supply typed registry callbacks;
  they do not reproduce lifecycle ordering.
- Admission revision plus canonical generated schema and wire-protocol version
  derive one deterministic registration token. The token fences calls, claims,
  output deltas, overload reports, terminal settlement, and consumer reads.
- The registry persists one admitted or rejected decision before provider
  execution for each stable run/tool-call identity. Exact replay returns that
  decision and result stream. After admission, lost routing certainty is
  terminal `outcome_unknown`, not a safe planner retry. The typed tool error kind
  survives activity serialization and forces tool-free finalization, bypassing
  configured terminal tool plans. Runs with a required completion tool fail
  without further planning or execution. Warning text alone is not a classifier.
- Providers claim before handler execution. Pulse redelivery observes claimed
  or terminal state and cannot repeat handler work. Stale generations settle
  queued calls as `stale_registration`.
- Queue publication checks unread backlog and appends atomically. Trimming is
  garbage collection, never admission control. One absolute execution deadline
  precedes one registry-owned absolute result-retention deadline.

## Model Clients

- Runtime model clients implement `runtime/agent/model.Client`.
- `PlannerContext.ModelClient(id)` attaches `model.CallInfo` before each
  `Complete`/`Stream` invocation reaches middleware or the registered client.
  Identity includes agent, run, root run scope, session, turn, registered model
  ID, sequence, and engine activity attempt. `model.CallInfoFrom(ctx)` reads it.
- Each invocation allocates `Seq` through
  `Runtime.RunStore.NextSeq(ctx, RootRunID, RunID)`. Values start at 1 per run
  within its root scope and are never reused across parallel calls or retries;
  allocation order is not completion order. Allocation failure fails unsent.
  Workers must share the store. The identity wrapper itself does not write records;
  the innermost planner client recorder does.
- `Attempt` comes from `engine.ActivityAttemptFromContext`, not run resume
  metadata: Temporal reports its current attempt and in-memory execution uses
  1. Custom adapters set `engine.WithActivityAttempt`; absence reads as zero.
- `Runtime.ModelClient(id)` bypasses planner decoration and neither attaches
  `CallInfo` nor allocates a sequence.
- `model.ContextInfo.ContextLimits(ctx, req)` returns limits, the resolved model
  ID, and whether its window is known. `ContextLimits` includes ContextWindow,
  MaxOutputTokens, DefaultMaxTokens and ImageTokens (zero means 1,500).
  `model.WithCapabilities` preserves exactly the optional ContextInfo and
  TokenCounter method sets through NewClient and adaptive rate limiting.
- Anthropic optionally accepts ModelsClient via Options.Models; NewFromAPIKey
  wires it. Gemini optionally discovers Models.Get. Lookup runs in activities,
  never workflows, with a caller-bounded two-second deadline. Raw successes
  cache per requested model for the client's lifetime. Caller cancellation/expiry
  failures are never cached; other failures retain fallback for 60 seconds.
  After lookup, API input limits map per request to ContextWindow = input limit
  + contextmeasure.OutputReserve(req, limits), preserving the input allowance
  after reserve subtraction. The quarter-window fallback solves to min(max
  output, input limit/3), keeping the returned reserve stable. Shared table
  windows stay unchanged. Table IDs must be dependency SDK constants or adapter non-test
  IDs; verify every figure from an official provider page. Bedrock, Codex and
  Gemini fallback tables are currently empty; Ollama/gateway require overrides.
- Options.ContextLimits / WithContextLimits overrides positive fields by resolved
  model, falling back to registered client ID. Zero retains adapter values.
  Copy the configuration at construction; reject negatives and max output above
  the supplied window. The innermost recorder queries the registered client
  boundary, so overrides apply even when outer planner wrappers drop capabilities.
- `runtime/agent/contextmeasure` supplies pure RequestFacts and RequestOutcome
  types, separate text/fixed/non-text measurement, metadata/cache-independent
  view hashes, rational bounded calibration and ceiling estimates. Binary image
  and document uploads never count as text; odd document byte costs round up.
  Output reserve prefers request MaxTokens, then DefaultMaxTokens, then the
  smaller of MaxOutputTokens and a quarter window. Estimates saturate at MaxInt.
- GuardTokens uses GuardBudget (Tokens plus adapter ImageTokens) and a rational
  trigger fraction. Above the strict threshold it attempts one count, trusts
  Exact only, and adds replayed thinking text/redacted bytes at the rate.
  GuardCount.GuardOn requires a positive known budget and exact or calibrated
  measurement. Zero triggerAt disables counting while retaining the estimate
  and calibrated GuardOn; only the estimation rate defaults to 1/3.
  K12 carries durable Completion.Facts parallel to CallKeys and sorted Models
  across activity boundaries. Every agent Observes adopted facts and sends Models under protocol 9; only
  threshold transformations remain opt-in.
- Planner call recording sits inside model interceptors, capturing the decorated
  request against the committed pre-history-policy view (loaded IndexHead, no
  Pending). Recording is required in planner contexts. Missing CallInfo or
  Attempt < 1 fails unsent. Direct Runtime.ModelClient calls and interceptor
  short-circuits are not recorded. Started precedes send; response Put precedes
  completion. The callrecord package owns deterministic JSON v2 bodies and exact
  request verification, including Meta/cache points and precision-preserving
  hash fallback for messages whose ordinary JSON round trip is unstable.
- Stream recording sums usage chunks and writes synchronously in Finalize or
  Close. Every activity exit marks open streams stream_abandoned. Terminal store
  failures propagate and can leave started evidence.
- Every terminal recorder call has typed callrecord.Completion.Facts:
  contextmeasure.RequestFacts with snake_case JSON tags, Call equal to the
  stored key, resolved Model,
  limits, reserve, view participation and text/fixed/non-text pre-send measures.
  Measure after BuildRequest; preserve facts through unary, stream setup failure,
  Finalize, Close and abandonment. WithResult adds input+cache reads+writes and
  classifies ok/context_overflow/error. Started records have no facts.
  ViewTextBytes/ViewNonText are omitzero matching-view measurements; old records
  decode as zero. CountLatency records optional pre-send token counting. Canonical
  measurement matching ignores Meta/cache; K8 request rebuilding still uses full
  encodings. RunRecord exposes facts through Completion and preserves them in
  Export/FromExport, retaining strict unknown-member decoding.
- Protocol 6 planner activity output carries all started CallKeys, the first
  RequestGrowth, and ProducingCall (last completed call that emitted planner
  events). All output keys are adopted independently of ProducingCall. Step is
  one-based and monotonic for every planner schedule, recovery/finalize included;
  TurnID remains the conversational turn. Successful initial adoption shares its
  hook activity with the execution-phase event, with the record first; recovery
  and resume adoption retain a separate activity (~97 ms median per resumed step
  over 20 Temporal dev-server runs); the approved real-Temporal budget is 2s. Initial
  planning keeps two activities, resumed planning increases from one to two, and
  sealing adds one per run. Each validated output commits a
  planner_step with committed ViewRef and deterministic planner_step:<run>:<step>
  key. Assistant entries, including terminal responses, carry the producing key.
  The workflow publishes context_request_growth once per run. Presentation
  commits carry positional CallKeys, nil for standalone unrecorded helper streams.
- Provider packages under `features/model/*` implement raw `model.Provider`.
  Runtime helpers construct the provider and wrap it with `model.NewClient`.
  Direct callers must do the same before registration.
- `bedrock.New(awsClient, opts)` no longer takes a ledger source. Bedrock
  encodes only the planner's `model.Request.Messages`, including when
  `RunID` is set. It does not query the workflow or prepend ledger messages.
- `Provider.Stream` returns raw `model.Streamer`; `Client.Stream` returns
  `model.ValidatedStreamer`. This difference prevents provider adapters from
  satisfying the consumer-facing client interface accidentally.
- A raw provider stream returns its provider-owned terminal `Response` only
  after literal EOF. The validated boundary reconciles it with observed chunks;
  there is no independent stream metadata channel.
- The validated client owns bounded request/response copies and checks the exact
  tool catalog, tool choice, structured output, generated completion decoder,
  token usage, output limits, and terminal stream protocol.
- `model.TokenUsage.InputTokens` excludes cached input. `CacheReadTokens` and
  `CacheWriteTokens` are disjoint counts, and non-zero `TotalTokens` equals the
  sum of input, output, cache-read, and cache-write tokens. Provider adapters
  normalize native usage before the validated client and runtime aggregate it.
- Runtime model interceptors capture that immutable request contract before
  provider work and revalidate any short-circuit or replacement response, so an
  interceptor cannot broaden the request while replacing output.
- GenAI message tracing captures the effective request after cache, tool policy,
  and interceptor replacement. It does not capture rejected output. Internal
  `runtime.tool_unavailable` payloads use only the effective tool catalog.
- Model recovery keeps a bounded set of rejected calls and selects the exact
  error returned by the planner. It accounts all billed unary rejections, even
  when the planner catches one or model calls finish out of order. Durable token
  totals use checked addition. Stage streamed text and thinking until terminal
  validation and provider cleanup both succeed. Rejected or cleanup-failed
  presentation content must not enter the canonical run log.
- Tool-call recovery must carry a non-nil, bounded, unique copy of the rejected
  request catalog. It cannot widen an active policy. Recovery-budget exhaustion
  enters the tool-free failure finalizer on both start and resume paths. A
  finalizer rejection ends the run without recursive recovery. The model and
  recovery boundaries share a limit of 256 tool definitions per request.
- Runtime model streams own planner event publication. `planner.ConsumeStream`
  detects this ownership through the stream marker and only builds its summary;
  it must not publish the same text, thinking, tool delta, or usage twice.
- Runtime presentation delivery is a best-effort client projection. Sink
  failures or ended sessions cannot fail, retry, or reclassify model work.
  Canonical stream and bus projections use detached payloads so one consumer
  cannot mutate another projection or the planner transcript.
- Provider changes must extend the shared conformance matrix for applicable
  complete/streaming, multimodal, tool-call, structured-output, typed-thinking,
  token-counting, cancellation, normalized-error, and name-codec behavior.
- `model.ProviderError` kinds are `auth`, `invalid_request`, `context_overflow`,
  `rate_limited`, `unavailable`, and `unknown`. `context_overflow` is not retryable
  with the same request. Anthropic and Bedrock map documented prompt-too-long
  errors and `model_context_window_exceeded`; OpenAI and Codex map the
  `context_length_exceeded` code, including streamed failure/error events;
  Gemini/Vertex map HTTP 400 `INVALID_ARGUMENT` token-overflow messages.
  Recorded-body conformance covers Complete and supported Stream paths.
  Bedrock context stops no longer set `OutputLimited` or enter `OutputBounds`
  shorter-answer/tools-disabled recovery. Overflow returns ContextOverflow for
  bounded workflow recovery; other !Retryable ProviderErrors become non-retryable
  engine application errors of type provider_error.<kind>. Ollama truncates to `num_ctx`, so no chat overflow is mapped; the
  gateway has no provider wire contract. Other error classifications stay intact.

- Setup-time and receive-time provider throttling must preserve
  `model.ErrRateLimited` in the error chain. Adaptive rate limiting probes only
  after a stream reaches `io.EOF`; a successful stream setup is not completion.
- Gemini and Vertex support exact `model.TokenCounter` but their `Stream`
  method returns `model.ErrStreamingUnsupported`; streaming is an explicit
  provider capability despite being part of the shared client interface.
- OpenAI Responses projects tool and structured-output schemas into the
  provider strict-schema subset, rejects structured output combined with
  tools, and round-trips canonical tool names through its request-local codec.
- Canonical tool IDs may exceed provider name limits. Provider codecs must use
  deterministic byte-bounded names with a stable hash suffix, retain the
  request-scoped reverse mapping, and fail fast on collisions.
- Tool-use correlation IDs have a separate request-scoped codec. Anthropic and
  Bedrock must reserve every provider-safe replay ID before encoding, allocate
  unsafe IDs from unoccupied synthetic `tN` values, and use the same mapping for
  matching tool-use and tool-result blocks.
- Anthropic explicit cache checkpoints mark the nearest preceding cacheable
  block in the applicable system or conversation history. This search crosses
  an encoded message boundary for a leading or checkpoint-only marker.
  Encoding fails when that history has no cacheable block. It never silently
  drops the marker.
- Model middleware must not invent optional interfaces. Adaptive rate limiting
  implements `model.TokenCounter` only when the wrapped provider implements it;
  providers without exact counting remain non-`TokenCounter` after wrapping.
  `NewAdaptiveRateLimiter` keeps estimated-input-only admission.
  `NewOutputReservationAdaptiveRateLimiter` requires an exact provider count
  and positive `MaxTokens`, then reserves their overflow-safe sum under a
  version-isolated Pulse key.
- The Ollama adapter uses the local `/api/chat` endpoint for text, image,
  streaming, function-tool, native thinking, and structured-output requests. It
  maps `model.Request.Thinking` to Ollama's top-level `think` flag and surfaces
  `message.thinking` as typed `model.ThinkingPart` / `model.ThinkingChunk`, never
  as assistant text or structured-output content. Some Gemma 4 variants,
  including MLX builds, also require the model-level `<|think|>` control token at
  the start of the system prompt to activate thinking; keep that prompt concern
  separate from the adapter's response-side typed thinking contract. It should
  keep tool calls as `model.ToolCall` values so the runtime, not the provider
  adapter, owns tool execution and unknown-tool recovery.

## Policy Decisions

- `policy.Decision.AllowedTools` is authoritative. Nil or empty denies every
  ordinary tool at planning and execution. Only an absent policy engine leaves
  all candidates available, subject to per-run overrides.
- Basic policy retry hints only narrow the already-filtered candidates. A hinted
  tool cannot bypass allow/block filters or the requested candidate set.
- `basic.Options.DisableRetryHints` disables hint handling with or without
  configured filters; default hint handling remains enabled.

## Tool Execution Contracts

- Runtime-owned tool specs and codecs are the schema source of truth.
- Use generated `tool_specs.Specs` and codecs for payload/result schema and encoding needs.
- Do not introspect `docs.json` at runtime.
- Tool results and retry hints should stay structured; avoid best-effort coercion when contracts fail.
- Every tool-result shape, bounds, retry-hint and pause contract violation
  returns a root non-retryable `*engine.ApplicationError` with type
  `runtime.ToolResultContractErrorType` (`tool_result_contract`). Keep the root
  visible to engine adapters on activity, provided/external, child agent-as-tool,
  inline, timeout and synthesized paths; a generic error wrapper hides the type
  from Temporal's outbound converter.
- Pause questions and `RetryHint.Reason == planner.RetryReasonMissingFields`
  clarification questions must contain non-whitespace text. Preserve valid text.
  Compatibility: missing-fields hints without a question now fail under every
  OnMissingFields policy (finalize, resume, await_clarification and default),
  before policy handling. Valid runs are unchanged; this validation and error
  classification correction does not require a workflow protocol bump.
- `ProvideToolResults` accepts only the first remaining tool-channel await.
  The read-only `tool_awaits` query exposes this head, including call payloads.
  Other IDs return a root non-retryable `tool_result_contract` error with a
  `ToolAwaitNotReadyError` cause, detectable with `errors.As`. Unknown and consumed
  IDs return the same error. Retry only after the query advertises the ID.
  Rejections leave the run paused without content writes or signals.
  Tool uses and results remain paired at consumption in queue order (RB3).
  Preflight validates call identities and decodes results, then runs the same
  materializer-before-contract pipeline as the activity. Client runtimes must
  register the worker's specs, codecs, and materializers. For provided results,
  materializers must be deterministic and free of I/O and side effects.
  Preflight discards enrichment. Activities repeat it and own artifact writes.
  Custom engines must implement `engine.ToolAwaitQuerier`. These query and
  preflight changes add no workflow commands. Protocol remains 13.
- Generated validation errors use fixed framework text. Do not preserve raw Goa
  validation messages because they can contain submitted values. Bound generated
  retry hints after all field-contract enrichment and before durable storage.
- Retry-and-reflect guidance also uses fixed framework text. Do not persist the
  raw service error in its tool error or retry hint because the error can contain
  submitted values.
- Retry-and-reflect failure tracking is process-local and limited to 4096
  run/tool keys. Each failure refreshes its key. When full, the interceptor
  discards the least-recently-failed keys, which restart at attempt one. Do not
  depend on workflow-completion callbacks to clear activity-worker state.
- `runtime.tool_unavailable` is not a policy candidate. Rewrite every direct
  model call to it with the exact effective request catalog at the model
  boundary. Preserve that narrower catalog at the activity boundary when the
  policy allows more tools. Never retain or echo a model-supplied
  `available_tools` value. Preserve this internal recovery call across per-run
  tool and tag filters.
- Generated `Idempotent()` tags are metadata only. Until an explicit runtime
  replay contract exists, do not claim exactly-once execution or automatic
  duplicate suppression.
- Tool confirmation is runtime-owned. Design-time `Confirmation(...)` records
  `tools.ConfirmationSpec` on generated tool specs; runtime
  `WithToolConfirmation(...)` can require or override confirmation for specific
  tools. Confirmed calls emit `AwaitConfirmation`, resume through
  `ProvideConfirmation`, record a `ToolAuthorization` hook/stream event, execute
  only when approved, and return schema-compliant denied results when rejected.
  After the entire await queue is satisfied, the workflow schedules one logical
  planner resume and acts on that result, including its tools or final answer.
  This also holds for planner awaits mixed with tool calls and tool pauses.
  Resume activities remain retryable.
- Tool-produced artifacts use `artifact.Content` on `planner.ToolResult.Artifacts`;
  runtime persistence converts them to workflow-safe `artifact.Ref` values on
  planner outputs, API tool events, hook payloads, and memory records.
- Model-facing artifact access is design-owned through `Toolset("artifacts",
  FromArtifacts(MaxArtifactBytes(...), MaxArtifacts(...)))`. Generated
  registration must use `runtime.NewArtifactToolsetRegistration(...)` and the
  application runtime must provide `runtime.WithArtifactStore(...)`.
- Model-facing memory access is design-owned through `Toolset("memory",
  FromMemory(MemoryMaxResults(...)))`. Generated registration must use
  `runtime.NewMemoryToolsetRegistration(...)`; `scope:"current_run"` falls back
  to `runtime.WithMemoryStore(...)`, while `scope:"indexed"` requires
  `runtime.WithMemorySearcher(...)` and otherwise returns an
  `unsupported_operation` retry hint.
- Long-term memory is a separate `memory.Service` contract with entry-shaped
  `PutEntry`, `IngestRun`, `IngestEvents`, and `Search` operations. Generated
  `FromMemory(MemoryLongTerm(), ...)` registrations pass
  `rt.MemoryService` and `rt.MemoryScopeResolver`, expose `search_memory`, and
  accept only query/filter payload fields from the model. Visibility and scope
  are design/runtime-owned and must not be controlled by tool payloads. Tool
  results use model-facing hits and must not expose raw scope, source
  references, or metadata from stored entries.
- Planner-input memory preload is opt-in through generated
  `RunPolicy.PreloadMemory`. It fills `planner.PlanInput.PreloadedMemory` and
  `planner.PlanResumeInput.PreloadedMemory` with bounded snippets without
  changing default transcript/history behavior.
- Long-term preload is opt-in through generated
  `RunPolicy.PreloadLongTermMemory`. It searches with the latest
  history-filtered user text and fills `PreloadedMemoryEntries`, leaving raw
  transcript event preload unchanged. Planners that want prompt text should use
  `memory.FormatEntriesForPrompt(...)` rather than inventing ad hoc formatting.

## Interceptors

- Interceptors are opt-in typed interfaces: run, tool, model, and event.
- Runtime-level interceptors execute before agent-scoped interceptors.
- Generated `RunPolicy(func(){ Interceptors("id") })` stores interceptor IDs;
  application code supplies implementations with `runtime.WithNamedInterceptors(...)`.
- `PlannerContext.ModelClient(id)` applies model interceptors after cache and
  tool-policy decorators and before tracing. Raw clients passed directly to
  `planner.ConsumeStream` are not wrapped by runtime model interceptors.
- `BeforeModel` may return a response to short-circuit non-streaming
  completions. Streaming calls do not synthesize a `model.Streamer` from that
  response; returning one from `BeforeModel` on `Stream` fails the call.
- Record events (planner_step, run_sealed, child_run_linked and reserved
  context_compacted, context_compaction_skipped, context_overflow) and canonical
  presentation commits bypass event interceptors. Hook publication appends first,
  then publishes best-effort projections. SealRecord appends directly without
  projections.
- Ordinary event interceptors run in `runtime.publish_hook` before `appendHookRunEvent`,
  stream publication, and hook-bus publication. Dropped events must be absent
  from all three surfaces.
- Interceptor errors short-circuit the active path.

## Transcript Views And Child Validation

- Protocol 7 planner activity inputs carry ViewRef for history: IndexHead
  plus one step's Pending. Caller seed history is committed before the first
  planner invocation and never sent in Pending. Entries are immutable; a later
  HideBookkeeping entry changes rendering without changing earlier hashes.
- The view loader stores deterministic pending segments before returning a head,
  verifies the chain/content and incrementally caches segments, ToolOutputs and
  TypedInputs in workers, bounded to 256 runs per runtime with LRU eviction.
  WithViewCacheRuns(n) changes the bound and panics for n <= 0. Evicted runs
  reconstruct from IndexHead; eviction removes only the map/list reference so
  active loads remain safe. Canonical JSON message equality is the observable
  contract, including the in-memory engine. History policy, recovery correction
  and resume-only ValidateBedrock operate on loaded messages.
- PlanResumeInput.ToolOutputs has refs, bounded preview and metadata, with empty
  Result/ServerData/Payload. PlanResumeInput.Content loads full canonical content
  in RootRunID. TypedInputs carries ID/PayloadRef and empty Payload; finalize
  supplies no typed inputs. Graph predicates load results through ContentLoader.
- Each workflow result batch makes at most one RecordContent activity call for
  content, metadata, large structural parameters and reminders. A stored call
  supplies original payload content unless an adapted payload has its own ref.
- PreChildValidator runs in ChildDispatchActivity before child admission/start.
  A view load with Pending returns IndexHead on success or structured rejection;
  the workflow adopts it before following dispatches/planner steps, preserving
  the warm cache and held call references. Unloaded/empty Pending returns no head.
  AgentToolValidationInput.View is a lazy ViewLoader with LoadMessages(ctx),
  replacing Messages. No parent message content returns to the workflow. Custom
  engines must implement the typed register/execute child-dispatch methods.
- The run-loop ledger and ledger_messages query are removed. Content signals
  contain refs only. Over-1-MB planner envelopes return PlanActivityInputBudgetError
  with Bytes, Limit and RunID, matching errors.As. Replay fixture is protocol 7 with real recorded facts and a receipt checkpoint;
  unchanged protocol 5 and 6 fixtures verify mismatch rejection before effects. Structural templates
  remain version 5.
- `make test-payload-growth` runs full non-short payload/growth acceptance. Fixed per-run
  ToolArgs, Labels and tool catalogs remain inline; K7 accepts the measured
  fixed-part overruns documented in runtime.md. `make itest` invokes the target;
  `make test` covers typed P2 and 1,000-step growth. Omitted result bodies load
  on explicit ContentLoader requests or bounded small-result hook publication.
  FinishRunInput.ToolEvents retains bounded per-call metadata and grows with count.

- ExecuteWorkflow allocates the single live workflowPlanInput before initial hooks
  and threads it through planning, recovery, tool turns, finalization and terminal
  cleanup. Every validated adoption advances its head before further planning.
  Before run_completed, a detached context executes <agent>.seal with RootRunID,
  RunID, ProtocolVersion, IndexHead, Pending, fixed run_sealed:<run> EventKey and workflow Timestamp.
  SealRecord validates lineage and stores pending segments, then directly appends
  typed run_sealed{IndexHead, ProtocolVersion}. It skips interception/projections and fails closed.
  Retried input writes identical segments and one event, even after a lost append
  acknowledgement. EventKey is deterministic; ID remains the store-owned cursor.
  Custom engines add RegisterSealRecordActivity and ExecuteSealRecordActivity
  (breaking). External termination has no seal; latest planner_step is its readable
  head. K12 uses protocol 7; the history fixture includes seal and compaction commands.

- runrecord.Read(ctx, runStore, runLog, rootRunID, runID) walks child_run_linked
  descendants without workflow/history access. It uses the seal or latest step
  head and one incremental renderer per run. planner_step/run_sealed carry explicit
  ProtocolVersion from the workflow RunInput checked by validateWorkflowInput.
  The live plan state retains that validated value across recovery/finalization;
  SealRecordInput forwards it to the seal activity. RunRecord rejects conflicting
  stamps and reports version 0 without a record event, even with positive-Step calls.
  It never infers a version from event kinds or started records. Export preserves
  the recorded value. Record-event CallKeys adopt every
  call of an attempt; MessageEntry.Call identifies only the producing call.
  Final unadopted calls are abandoned; started calls are incomplete. Each includes
  its exact rebuilt request, Activity and Step. Presentations are adopted,
  presented-not-adopted or presented-unrecorded. Full results, typed/clarification
  answers and separate server-only content remain available beyond previews.
  Recorded checkpoint refs resolve index.CheckpointRecord including Calls. K12
  produces receipt-only records; RunRecord rebuilds exact compacted call views.
  Entry template versions remain 5, independent of workflow protocol 7.
- record.Export().Marshal() writes deterministic version-1 JSON with rendered
  transcripts, original call/log bytes and addressed content, server-only bytes
  included. runrecord.FromExport restores identical evidence without a store or
  historical renderer. Exact request numeric tokens above 2^53 are preserved.
  New 1000-step recorder-write growth and real Temporal P1 tests skip -short and are
  wired into test-payload-growth with explicit timeouts. KeepRecentTurns/Compress
  remain comparison cases; K12 adds real compaction to the same recorder-growth proof.

- ToolResultReceivedEvent always carries Refs for the full eligible result and
  server-only sidecar/executed payload. Small bodies remain inline unchanged.
  Above the 1,000,000-byte encoded hook bound, compaction removes Result,
  ResultJSON and ServerData together, preserving refs and the original preview.
  The hook_payload_too_large marker is removed (breaking for marker consumers).
  publishStoredToolContent skips known oversized result loads and stops sidecar
  loading once the current envelope cannot fit; sidecar size is known only after Get.
  A missing preview uses the bounded omission text when bodies exceed the limit.
  RunRecord resolves results only from canonical index/output refs and call evidence,
  never tool_result_received refs. Completed partial-batch results are retained as
  refs and indexed before terminal sealing; inline error sidecars are stored even
  without an eligible body. Export includes every resolved server-only byte.
- K8 growth counts every recorder Put and PutCall body write, including repeated
  writes and addressed content, excluding index/view setup. Over 1000 steps,
  fitted slopes off 1999.743, KeepRecentTurns 2032.677, Compress 2458.549 bytes/step;
  max residuals 700.227, 1103.159, 3799.192 bytes. The complete request is a canonical
  verification digest only and is never stored or copied into export Content;
  rebuild and import recompute it. ChildRunLinked is a canonical record event;
  BeforeEvent cannot hide the run tree. Pure loop-state preparation precedes the
  initial planner_step/execution-phase batch without another activity.
  test-payload-growth sets 20m for runtime growth and 3m for real Temporal P1;
  make itest invokes both. K12 includes real compaction.

## Workflow Composition

- Plain `Workflow` plus `Step` remains source-compatible and generated through
  `planner.NewSequentialWorkflowPlanner(...)`. Sequential step names must be
  unique because they become tool-call IDs.
- Graph helpers (`Parallel`, `Join`, `RequestInput`, `Loop`, `Branch`) generate
  `planner.NewGraphWorkflowPlanner(...)`.
- Both built-in workflow planners honor `PlanResumeInput.Finalize` before
  processing tool failures or scheduling work. They return the configured
  `FinalMessage` on time, tool, and failure caps without more tools or awaits.
- Graph workflow resume state is derived from stable node/tool-call IDs in
  `ToolOutputs`, not from `len(ToolOutputs)`.
- Parallel resume must schedule only unfinished ready nodes. Joins are virtual
  dependency barriers. Loops must be bounded by `MaxIterations`. Failed loop
  iterations permit retries. If every iteration fails, return the last
  iteration's `ToolError`.
- Graph node IDs cannot contain `#`. The planner reserves that separator for
  generated loop iteration IDs.
- Workflow dependencies must be acyclic, and dependency, branch source/target,
  and loop predicate step IDs must resolve to declared graph nodes. Enforce the
  cycle contract in both design validation and runtime planner construction.
- `RequestInput` emits `AwaitTypedInput`; answers resume via
  `Runtime.ProvideTypedInput` and enter `PlanResumeInput.TypedInputs`, not
  `ToolOutputs`.
- The branch default DSL helper is `BranchDefault` to avoid colliding with Loom's
  `Default` helper in dot-imported designs.

## Time Budgets

- `TimeBudget` and `runtime.WithRunTimeBudget(...)` bound active runtime work,
  not total elapsed wall time.
- Clarification, confirmation, typed-input, and external-tool waits pause the
  active budget and resume it when work continues.
- `policy.CapsState` tracks counter budgets only. Its legacy `ExpiresAt` field is
  deprecated and ignored; do not create a second wall-clock deadline through a
  policy decision.
- After the active budget expires, only the time-budget finalizer may plan,
  once, at every resume boundary, including after an await barrier. The grace
  period does not permit normal planning or domain tool execution.
- A terminal result already in hand (a final response or final tool result, with
  no tool calls or await) completes the run even when adoption crosses the
  budget, without another finalizer call. Planner scheduling rejects expired
  deadlines; the finalizer uses the hard deadline including its grace period.
  A finalizer that would start at or after that deadline (Budget + FinalizerGrace)
  is not scheduled; the run fails with context deadline exceeded.
- Engine-specific queue/schedule timeouts are separate from semantic run,
  planner, and tool budgets.

## Runtime Observability

- `Runtime.ModelIDs()` returns a sorted owned snapshot under the read lock; agenttest gates every construction-time model for per-root-run capture and seals registration before returning the harness, rejecting late models and replacements with ErrRegistrationClosed. Model exposes the pre-gate client for Decide. LiveModel/SkipUnlessLive require LOOM_LIVE_MODEL=1; undeclared application clients are not blocked.
- `Runtime.HasEventInterceptors()` detects BeforeEvent middleware across runtime and merged agent registrations, excluding RuntimeInterceptorFuncs with nil BeforeEventFunc; agenttest marks observed trajectory evidence unverified when present.
- `telemetry.NewTracer(provider)` uses the caller's TracerProvider without global changes; agenttest.WithTracerProvider shares it with root agenttest.run spans, while callers own export and shutdown.
- `Trajectory.TraceID` is optional `trace_id` in v1 JSON, set by the harness after Build; Render prints Trace after Agent only when present. RunSession history comes only from agent memory policy, with no harness transcript carry-over.
- `runtime.WithMetrics` emits stable `loom_mcp.runtime.*` run, planner, and
  tool counters/timers; `runtime.WithTracer` emits planner/model spans and the
  canonical `tool.execute` semantic span.
- Planner measurements are activity-attempt-level and may repeat on retry.
  Run/tool measurements are emitted only for newly inserted canonical event
  keys, preventing ordinary hook retry double-counting.
- Keep run/session/turn/tool-call IDs on spans only. Metric dimensions are the
  bounded `agent`, `operation`, `tool`, and `status` values documented in
  `docs/runtime.md`.
- These semantic signals are engine-neutral. Temporal infrastructure spans,
  generated MCP adapter telemetry, SDK transport observation, Pulse streams,
  and the debug server remain separate surfaces.

## Event Authority And Reliability

- Sessionful starts reserve a run ID with a unique admission owner before engine
  submission. `session.Store.ReserveRun` is idempotent only for that owner and
  immutable run identity. `RejectRun` changes only its still-pending record to
  failed; it never removes records or changes a running or terminal run.
- Engines wrap `engine.ErrWorkflowStartRejected` only for proven rejection before
  acceptance. Unknown transport outcomes and existing-execution conflicts must
  not carry that marker. Runtime cleanup preserves the original error and any
  persistence failure. Child admission uses a replay-stable owner and commits its
  parent link atomically before submission; immediate definite rejection settles
  that reservation. Asynchronous Temporal child-start outcomes remain outside
  that classification.

- Hook activity payloads use JSON v2 and
  `encoding/json.FormatDurationAsNano(true)`. Duration-bearing per-run policy
  fields cross the workflow/activity boundary as integer nanoseconds while
  JSON field tags and duration map keys keep their standard behavior.
- Workflow state and its transcript index are the live deterministic execution authority.
- `runlog.Store` is the canonical append-only introspection record. Run-log
  append or metadata-update failure fails the hook activity so the engine can
  retry or stop; it is not best-effort.
- Mongo run-log pagination uses per-run sequences committed in the event's
  atomic insert under a unique index. Writers retry sequence conflicts; never
  allocate a counter separately before inserting an event. ObjectID cursors
  resolve to stored sequence positions, and reads use the primary. Startup
  backfills legacy records in their previous ObjectID order and fails on
  migration errors. Stop old writers for this upgrade; do not support mixed
  writers or claim to recover historical cross-worker append order.
- Stream sinks and the hook bus are observer projections with their own
  delivery contracts. High-volume partial signals may be best-effort.
- `memory.Store` is a derived raw-event projection; `memory.Searcher` indexes
  those events; `memory.Service` stores separate entry-shaped long-term memory.
- The Mongo `memory.Store` adapter appends immutable batch documents in its
  companion events collection. Reads load the legacy single-run document and
  new buckets in `created_at`, `_id` order, then stable-sort the combined events
  by timestamp. Equal timestamps retain legacy-before-new and deterministic
  bucket order. New code must not append back into the legacy document's
  unbounded `events` array.
- Pulse auto-ack subscription is suitable for ephemeral UI fanout only. Durable
  consumers use manual `Delivery` values, make processing idempotent with the
  stable event key, and acknowledge after their durable commit. Decode failures
  are manual deliveries too: dead-letter the raw payload before acknowledging,
  or leave the entry pending.
- Temporal activities start new trace roots linked to the scheduling workflow
  span. Do not model durable workflow-to-activity scheduling as ordinary
  in-process parent/child span nesting.
- `runtime.WithEngine(temporalEngine)` persists live workflow state through
  Temporal history only. It does not change the process-local defaults for
  `runlog.Store` or `session.Store`; production worker and client runtimes must
  explicitly share persistent implementations when introspection and session
  metadata must survive restart. `memory.Store` and `stream.Sink` remain
  separate projections with their own persistence and delivery contracts.
- Temporal replay does not make activity side effects exactly once. Activities
  may retry after an external side effect when successful completion was not
  recorded, so planner/tool effects must be retry-safe.

## Skills

- `runtime/mcp/skills` is the shared discovery and read path for MCP
  `SkillDirectory(...)` resources and model-facing `Toolset(FromSkills(...))`
  tools.
- `SKILL.md` frontmatter supports `id`, `name`, `description`,
  `allowed_tools`, `preload`, and `reload`. Skills without frontmatter remain
  compatible by deriving the ID from the directory name and description from the
  first heading or text line.
- Duplicate skill IDs, invalid frontmatter, unknown preload modes, and unknown
  reload modes are hard discovery errors.
- Generated `FromSkills(..., SkillPreload(...), SkillReload(...))`
  registrations wire `runtime.NewSkillToolsetRegistration(...)`; model-facing
  `list_skills` and `load_skill*` results include parsed metadata.

## MCP Progress And Resource Updates

- `runtime/mcp.ReportProgress` sends standard SDK progress notifications when
  the request includes a progress token.
- `WatchableResource` enables standard SDK subscriptions. The generated
  `SDKServer.ResourceUpdated(ctx, uri)` method sends update notifications and
  rejects unknown URIs.
- Watchable resources require persistent Streamable HTTP sessions. Reject them
  with stateless HTTP.

## Debug Server

- `runtime/agent/debug` is opt-in application code, not a DSL or generated API
  surface.
- `debug.NewServer(debug.Config{Runtime: rt})` defaults to `127.0.0.1:0`.
- Debug responses use `{data:...}` and `{error:{code,message}}` JSON
  envelopes for run snapshots, events, await state, memory, artifacts, and
  workflow counts.
- The debug server must read runtime stores without changing planner, hook,
  stream, MCP, or generated API behavior.

## Where To Verify

- `docs/runtime.md`
- `runtime/agent/runtime/agent_tools.go`
- `runtime/agent/runtime/model_wrapper.go`
- `runtime/agent/stream/stream.go`

## Pure Checkpoint Helpers

- `runtime/agent/transcript/checkpoint` owns FromRecord, repeatable newest-first
  receipt traversal and read-only projection shared with RunRecord. The runtime
  stores receipt-only records and caches input, pins, checkpoint and tail renders.
- Atomic units include assistant batches/results, bookkeeping, reminders,
  typed/finalize attachments and an open-await suffix. CutPoints only yields
  assistant starts after complete preceding call sets; ChooseTail always keeps
  the newest unit, falling back to the latest earlier safe cut when needed.
  RoleOf must resolve stored Message roles successfully.
- Render orders record-authored Users by origin position and original ID.
  CitedUsers selects lines, never wording. Pins use half of KeepUserTokens,
  rechecking the first user then newest first and stopping before a gap;
  image/byte-document/URI parts use MaxPinnedParts and a quarter-token allowance.
  DefaultLimits remains 32 pins, 160 index bytes, two non-text parts.
- ValidateCitations resolves both entry kind and role, rejecting user-role tool
  results. Qualify binds copied bare citations to Summary.Run before rendering
  or re-summarizing. K15/K18 supply integration and prior-run resolution.
- Text bytes never exceed MaxBytes(current RenderBudget). Exact framing takes
  a deficit from receipt first, then summary. MinCheckpointBytes(b) is the
  maximum of 512 bytes and the full user allowance plus largest framing bytes.
  K17 validates this threshold. FramingTokens remains 200.
  Receipt lines cap at 512 bytes and older lines collapse to at most two handles.
  Tiny component budgets omit whole lines/handles, never partial handles.
- Handle(part, recordID) uses rc- plus eight SHA-256 hex digits over part || ID;
  parts are users, receipt, earlier. CheckpointID uses ck- plus eight SHA-256
  hex digits over ID. Copied summary references preserve run and record ID.
- transcript.AssembleView strips pre-tail ThinkingPart, including Redacted,
  and marks the checkpoint. ValidateView applies GrammarBedrock/Anthropic,
  first-user/post-checkpoint-assistant constraints and bidirectional tool
  handshakes. K12 applies both grammars to checkpoint views at load;
  uncompacted views keep the existing resume-only ValidateBedrock. Child
  admission loads compacted parent views with ValidateChildAdmissionView,
  which permits only the trailing unanswered calls of the in-flight assistant
  turn; planner views keep strict validation.

## Engine Conformance

- Custom engines should run `enginetest.RunConformance` with a fresh fixture for
  each scenario. Durable and Retries capabilities gate explicit skips only.
- Fixtures provide backend option inspection and, for durable engines, replacement
  workers on the same backend with re-registration. Keep existing workflow handles
  usable. Completed activities must not execute again after replacement.
- In-memory value preservation checks pointer passing, not a codec round trip.
  Serializing adapters exercise their codecs through the same suite.
- `make test-engine-conformance` runs in-memory and real Temporal dev-server
  conformance with race detection and a five-minute timeout; `make itest` includes
  it. Temporal conformance skips under `-short`. Conformance-only workflow-task
  and sticky schedule-to-start timeouts are one second to bound worker recovery.
- Executable KnownGaps must return a typed enginetest.GapError wrapping an
  observed behavioral difference. Unexpected success fails the suite until the
  obsolete gap is removed. Deadlines, cancellations, expired scenario contexts
  and untyped errors fail the test. Fixture/setup failures are ordinary failures,
  not accepted gap evidence.
- Receiver.Pending counts buffered, unreceived signals without side effects,
  reflects workflow delivery in history order under replay, and is safe inside
  an Await predicate. Conformance awaits Pending() > 0 and then receives the signal.
- Current executable gaps: inmem omits the activity heartbeat timeout and
  ignores scheduling mechanics.

## Engine errors

- `engine.ApplicationError` carries Type, Message, NonRetryable, Details and Cause.
  Engines must not retry a NonRetryable activity failure, and must surface neutral
  application/timeout/cancellation errors to workflow code for errors.As inspection.
- Engine aliases use the leaf `engine/errtypes` implementation to avoid existing
  engine -> api -> hooks and engine -> telemetry -> cancellation import cycles.
- Provider errors use `hooks.ProviderErrorDetails` with unchanged payload fields.
  Temporal converts at all activity, child, timer and workflow completion boundaries.
  It preserves original SDK diagnostic causes and mixed-cancellation protections.
- `engine.ErrCanceled` shares context.Canceled's identity. Exact-leaf classification
  remains bounded. Backend SDK cancellation predicates run only at exact leaves.
- In-memory execution preserves application failures without retries, returns the
  cancellation marker and marks native execution deadlines as engine.TimeoutError.
- `runtime/temporaltrace` is an explicitly Temporal-specific, opt-in exception.
- Shared `application_errors` conformance scenarios verify non-retryability and
  Type/Details preservation across every existing activity case.
- Converted Temporal application wrappers preserve the SDK's exact Error text,
  including nested failures, in workflow/client results and recorded tool payloads.
- Outbound conversion never hoists an engine node out of external joins or reason
  wrappers. Only root engine nodes or adapter-owned synthetic wrappers convert.

## Context Compaction

- WithContextCompaction(agent.Ident, ContextCompaction{TriggerAt, KeepResultTools})
  opts an agent in; registration and workflow start reject HistoryPolicy when
  stamped compaction settings are present, including split client/worker deployments. TriggerAt is rational,
  default 85/100. Zero token settings resolve against the smallest adopted usable
  budget: keep 10% (cap 20k), protect 15% (cap 40k), save 10% (cap 20k).
- Clients overwrite caller settings with owned resolved configuration in RunInput,
  including a snapshot for child starts. Canonical planner_step and run_sealed
  records preserve settings and keep tools for RunRecord. Replay never reads worker
  compaction options. DisableRecall belongs to ContextRecovery in K13.
- Every entry writer records model-facing text bytes, non-text tokens and role;
  result entries of KeepResultTools set Keep. Caller signal measurements are
  overwritten by the runtime storing the content. The writer inventory test covers
  content items, assistant output, resume, clarification, result and structural entries.
  Resume requires equal MessageRefs/MessageSizes lengths; clarification AnswerBytes
  must be present (pointer to zero for an empty answer), otherwise fail before indexing.
- Before every planner schedule, including finalize, pure Decide consumes adopted
  facts and immutable sizes. The first request remains full. Tier 1 clears old
  non-Keep results only above MinSavingTokens; eviction uses closed-unit CutPoints.
  No-progress suppresses attempts until enough estimated growth and records one
  skipped event per suppression episode. Prefix system entries remain resident and
  count in the estimate. Resident budgets shrink with MinB; B < 2900 disables threshold compaction for the run, including
  a known window exhausted by its output reserve (BudgetTooSmall, not unknown limits).
- Rendered checkpoint size is reused only for the same hash, tail and budget;
  otherwise estimate uses MaxBytes. planner_step records the adopted measurement.
  Output-validation recovery outputs adopt facts, never ViewUpdate or threshold transitions.
  planner_step records recovery (rejected output), recovering (correction retry)
  and the actual loader-built ViewRef, including Pinned, supplied by activity CallView. Offline
  reconstruction preserves the last successful view/decision state across rejected
  steps, advances its head, Observes adopted facts and preserves the view on retries.
- Receipt-only records preserve Summary, SummaryRun and CitedUsers and link Prev;
  deterministic JSON v2 Put succeeds before any returned hash is adopted. Receipt
  lines carry Kind, ToolUseID and inclusive Unsummarised ranges. receiptLines owns
  base call/assistant construction; K15 AnnotateReceipts adds ranges and part lines. Only eligible arguments and assistant
  prose enter receipts; hidden calls and server-only content stay out. Seeded and
  resumed message ToolUseParts also supply eligible name/ID/bounded arguments;
  result-only user messages are covered by calls and never create empty user rows.
- Input cache: record hash. Pins: hash and budget. Checkpoint message: hash, budget,
  effective pins. Tail: head, TailFrom, ClearedThrough, PreviewThrough, ForceClearThrough and
  PreviewTokens. Hidden-call state crosses the
  cut. Cache eviction reconstructs byte-identical views. RunRecord shares projection
  rules and verifies each compacted request's canonical digest. Loader pins are
  canonical SelectPins(record users, Budget), not incoming overrides; ViewUpdate
  and CallView record the derived list. Tests prove rendering these recorded pins
  equals loader rendering, warm/cold and after budget shrink.
- context_compacted/context_compaction_skipped are canonical record events, bypass
  BeforeEvent and use <type>:<run>:<step>, with empty call_keys in K12. Diagnostics
  context_unmeasured/context_limits_unknown/context_budget_too_small are normal
  hooks, once per run for every agent. Threshold opt-out still Observes facts,
  sends Models and recovers overflows. Protocol 9 extends facts, views and settings;
  structural templates remain version 5. Drain older workflows before upgrading.
- K12 recorder growth over 1,000 steps: off 2212.230 bytes/step (max residual
  990.001 bytes), keep_recent 2242.624 (1239.443), compress 2669.240 (3937.941),
  real_compaction 2740.337 (542.128), with two evictions after 1,000 seed turns.
  Entry-size metadata moved all four modes; index/checkpoint setup writes are excluded.

- K13 uses workflow protocol 8 (structural templates remain 5). Custom engines
  register/execute RecallActivity and engine conformance includes its typed
  values, errors, attempts and options. A real v8 recorded history exercises
  compaction, a lookup activity and finalization; retain earlier histories.
- WithContextCompaction carries RecallDiscoveryCalls/RecallReadCalls (zero=20).
  compaction.State.Recall charges original call order before timeout grouping;
  committed compaction refills ceil(initial/2), capped at 3*initial. Malformed
  known reads spend reads; list/find and unknown kinds spend discovery.
- The existing ContextRecovery{GuardMargin, DisableRecall} is configured through
  WithContextRecovery(agentID, settings), stamped into RunInput and RunRecord.
  Zero GuardMargin resolves to 1/10. Never put DisableRecall on ContextCompaction.
- Advertise tools.RecallContext only on shortened views (including pending
  eviction intents), then keep it sticky. Omit it on disabled recovery/tool
  turns and finalization. Unadvertised calls become ToolUnavailable.
- recall.Handler derives the session from LoadRun and resolves only reachable
  Prev/From records; WithRecallAuthorizer runs once for every entered run before
  content reads. Never fetch Record/Output/server-only hashes. Parallel reads
  pair ToolUseIDs; users come from original index entries. RunChain defaults to
  no rN resolution until K18.
- Read pages are max(512, ProtectRecentToolTokens) bytes with UTF-8 offsets;
  find scans 8 MB/ten matches with 160-byte snippets. Opaque base32 cursors
  bind selectors and re-resolve on every call. Activity results are stored
  eligible refs with context lookup previews. Multimodal recall remains K22.
- ContextRecalled records one keyed charge/outcome per call; exhaustion emits
  once per allowance episode, reset on refill; disabled emits once at first
  shortened view. RunRecord validates reconstructed charges and stamped settings.
- FromRecord marks lookup receipt items; rendering and generated lookup IDs omit
  recall_context. Compaction-enabled application names/provider names must not
  contain the reserved substring. Compact handle floors preserve discovery;
  B=2,900 gives MinCheckpointBytes 790 <= MaxBytes 1,444.

## Overflow recovery (K14, protocol 9)

- CallRecording runs Check after started persistence and measurement. An exact
  counter can enable the guard before calibration: counting eligibility uses
  the upper clamp rate 1/2, while fallback size stays at its supplied rate.
  GuardReject completes context_budget/pre_send_rejected; NoProgress completes
  context_overflow_no_progress without sending. After any mark, later calls fail
  ErrContextOverflowPending before recording. The activity returns ContextOverflow
  regardless of the planner result and discards all presentations of that attempt.
- overflow.Step owns one guard rejection and two provider recovery retries per
  logical planner step, separate from RemainingRecoveryTurns. Check compares every
  call against that model's smallest provider-rejected Size, at default 1/3 plus
  non-text and reserve; calibration/ordinal cannot alter progress. Guard checks
  take precedence. The guard is advisory: unavailable or unsatisfied recovery
  disables it and sends; only an observed provider rejection fails the step.
- Recover recomputes Budget from failed RequestFacts, clears without threshold
  protection/saving gates, evicts closed units preserving the previous summary,
  halves newest previews evenly down to 256 bytes, then forces placeholders,
  Keep included. A trailing user unit joins the latest legal cut. Checkpoint
  cost is always MaxBytes for recovery. Known+calibrated target is strictly below
  B*TriggerAt; otherwise halve recorded view text bytes. No append, threshold
  decision or summary intervenes before the immediate retry.
- If B-derived MaxBytes < MinCheckpointBytes, no recovery or budget enlargement
  is attempted. ContextOverflowError wraps the provider cause and typed checkpoint
  byte shortfall, emits context_budget_too_small and consumes no retry. Other
  failures distinguish ErrContextOverflowNoProgress, ErrContextBudgetExceeded
  and ErrContextOverflowBudget, carry call keys/facts and fail without activity
  retry. Minimal errors name the retained unit and estimated fixed overhead.
- context_overflow is a canonical record event, replacing planner_step. It adopts
  all CallKeys/head/actual view/settings/facts, source provider|guard|no_progress,
  outcome recovered|guard_off|failed:<reason> and the pure recovery intent. A
  transformation adds context_compacted{reason:overflow,tier:clear|evict|preview|
  placeholder}. RunRecord verifies each intent from durable call facts and rejects
  presentations from overflow attempts. Monotone preview/force watermarks and
  preview budget qualify projection caches and produce identical cold/warm bytes.
- RunInput stamps ContextSettings for every agent, including Threshold and
  GuardMargin. Recall activates on shortened views even without threshold options;
  ContextRecovery.DisableRecall and per-episode refill rules still apply.
  HistoryPolicy receives the recovered loaded view. Compress planners that build
  unrelated requests fail not_view_bearing at the observed provider overflow.
- Protocol 13 records overflow recovery, a confirmation-gated tool turn with
  a clarification question/answer pair, one resume, lookup and finalization in
  workflow_protocol_v13.json. The separate workflow_protocol_v13_budget.json
  records terminal adoption across the active budget without a finalizer call;
  v5-v12 fixtures reject before effects. Structural templates stay version 5.
  Anthropic/Bedrock Claude recognize older input-length/max_tokens overflow text;
  OpenAI recognizes nested response.failed/error.code mid-stream and Anthropic
  handles status-200 SSE error events. Tests cite documented recorded-body shapes.


## K15 summary workflow (protocol 13)

- ContextCompaction.ContextModel explicitly selects a registered client, Class and
  image/document capabilities; nil disables summaries and logs once per run.
  Seal rejects unknown model IDs. Summary.Instructions and Summary.Prompt are
  immutable strings. Owned model/configuration snapshots propagate through run
  inputs and child inputs into recorded ContextSettings.Summary and RunRecord.
- The pure summarize package reads complete eligible content and previous summary,
  qualifies citations, frames quoted data and uses the shared checkpoint.LookupLine
  and recall.NewCitationResolver. Pos 0 is current run; unknown rN fails before K18.
  Never read Record, Output, injected fields or ServerData for summary evidence.
- Plan at most eight folds with recorded limits and the uncalibrated table rate,
  10% margin and Out=min(MaxSummaryTokens,floor(window/5)). Each result caps at
  half the usable summariser budget. Half-open planner ranges become inclusive
  result receipt ranges; other text retains separate message/part metadata.
  Unsupported non-text parts retain placeholders and part receipts.
- CallInfo wraps standard request/response validation, then the existing recorder,
  then the raw registered client. Record Activity summarize, planner Step, engine
  Attempt, fresh Seq, hash-segment messages and nil View. Raw rejected responses
  remain in durable call records. Only OutputValidationError permits K15 readback;
  provider errors keep their retry policy even with a recorded partial response.
- Retry transient rate-limit/5xx/timeout twice, with fresh Seq. Exhaustion returns
  summariser_error; overflow returns summariser_overflow once. Store/infrastructure
  failures fail the activity. Put the whole summary and checkpoint before returning
  its hash. Checkpoint Calls contains successful folds and never adopts anything.
- Size validation uses checkpoint.SummaryBytes and a read-only candidate overlay
  with reselected pins. Compare against the old checkpoint and evicted view using
  shared ResultText, including recovery previews and forced placeholders. Eligible
  reads are memoized within the attempt.
- Engine adapters register/execute SummarizeContextActivity. Before threshold
  eviction, synchronously fold at From=TailFrom and To=cut when enabled. No summary
  runs during correction/overflow recovery, finalization or after active-budget
  expiry. Finalizers retain the committed view with tier-1 clearing and receipt-only
  eviction available. The default summary timeout covers 24 planner-call timeouts,
  capped by the active deadline. Reserve each admitted schedule's step before
  preparation can publish summary/compaction events, even if the planner deadline
  check fails afterward. Finalizers must use a fresh event identity. Preserve RB1
  terminal completion and RB2/RB3 single-resume/recorded-clarification behavior.
- Canonical context_compacted tier summary adopts all started keys and the view
  before planning; rejected/unknown/stale outputs use context_compaction_skipped.
  Apply only at the exact original cut. Rejections fall back to receipt-only at
  the same cut; stale leaves the current view unchanged. Existing record events
  bypass interceptors. Telemetry includes model, usage, chunks, unsummarised bytes,
  loose_refs and PromptRef. Summary disablement rebuilds from skipped records;
  the third non-stale rejection adds one summaries_disabled record keyed :disabled
  after its ordinary rejection identity to avoid event-key deduplication.
- Agent registration installs the embedded <agent>.context.summary baseline or
  Summary.Prompt replacement. Resolve scoped overrides once per activity; render
  Previous/Evicted (quoted), Instructions and integer Budget per fold. Preserve
  PromptRef on every call and checkpoint. Legacy Compress stays unrecorded.
- Three real v13 fixtures cover recovery/confirmation/clarification/lookup,
  terminal adoption across the budget, and summary adoption before resume.
  v12 and older histories reject before effects. Structural templates stay at 5.
