# Stateful native discovery evaluation (#363)

Decision: do not adopt stateful native discovery as a production default from
this experiment. The prototype preserves the tested execution checks and reduces
request schema overhead for repeated summarization. It does not improve retrieval
with the same ranker, and some discovered sets cost more than the compact proxy.
A pre-existing generated-result materialization mismatch independently prevents
claiming complete runtime activity success. No production API, generator, ranking,
workflow protocol, or authorization behavior changed.

This evaluates the retained-tool idea in
[ADK commit 90afda4](https://github.com/google/adk-go/commit/90afda4db998aabc288162d05a8d69379345f96c).
It does not import ADK, copy its ranker, or claim equivalence with its durable state.
The implementation is a test-only state holder around actual Loom generated
registration, execution, routing, measurement, and compaction boundaries.

## Frozen inputs and execution boundaries

The fixture is
[`stateful_evaluation`](../../integration_tests/fixtures/assistant/progressive_discovery/stateful_evaluation/).
It consumes #310's unchanged 15-tool
[`catalog.json`](../../integration_tests/fixtures/assistant/discoverybench/testdata/catalog.json)
and 12-case
[`cases.json`](../../integration_tests/fixtures/assistant/discoverybench/testdata/cases.json).

| Input | SHA-256 |
| --- | --- |
| Catalog | `5533903d33ecbb42f6523b353ba9bfb8c60c0a705ee499c9b28223736a66a94b` |
| Cases | `a248254ef490c931f2284648a1c97f5a796b6b368f752c1081247ccbd020a62b` |
| Experiment config | `d62b5496bcd63565417c25826068609a70a260bd46ca3dcf331bc9dac824f44e` |
| Turn traces | `9aaf9e5f01d75f6c7894b4bd8de31f11e4c92d01f558f898cad430376bc33518` |

The executable hashes guard the inputs before measurement. K=5, schemas enabled,
five retained tools, 8192 retained descriptor bytes, oldest-discovery eviction,
idempotent rediscovery, and pin `search` were fixed before recording. Pins are
outside retained-state bounds and inside model request costs. A single oversized
descriptor is rejected without evicting prior state. The state key is agent plus
session; a catalog generation change discards that key's retained descriptors.

Both arms use
[`NewAssistantAssistantMcpLocalToolsetRegistration`](../../integration_tests/fixtures/assistant/gen/mcp_assistant/local_provider.go)
and the same generated search implementation and current host interceptor.
The compact registration exposes `search_tools`, `call_tool`, and pin `search`.
A second registration marks all catalog tools AlwaysVisible to obtain their actual
native ToolSpecs. The experiment projects that registration to `search_tools`, the
pin, and retained names, omitting `call_tool`. No alternate dispatcher or schema
validator is introduced.

Each scripted call constructs a runtime and registers the projected ToolSpecs.
A missing native name goes through actual `Runtime.ExecuteToolActivity` with an
empty ToolsetName and is rejected by routing. Exposed calls use the unchanged
generated registration.Execute callback, including strict decoding, proxy target
validation, interceptor authorization, and the real deterministic assistant service.
A successful callback is labeled `generated_executor`, never runtime activity
success. Retained results explicitly contain zero runtime activity successes.

One existing source distinction is asserted exactly: `report_progress` has
`{"type":"object"}` in the frozen tools/list input schema, while its generated
ToolSpec has `{"type":"object","properties":{},"additionalProperties":false}`.
The other input schemas and all output schemas match. Neither source is rewritten:
actual ToolSpecs determine model-request bytes; discovery/catalog descriptors
determine retained-state bytes. Raw schema numbers remain JSON bytes, including
integer bounds that cannot be represented exactly by float64.

## Recorded outcomes

The retained
[`results.json`](../../integration_tests/fixtures/assistant/progressive_discovery/stateful_evaluation/testdata/results.json)
contains all case results, per-turn observations, pressure measurements, four
warmups, and 400 timed samples with their returned payloads. Recording took place
on 2026-10-19 at 19:47:15 UTC, Go 1.27.2, Darwin arm64, GOMAXPROCS=10, in a window
with the other agents' builds and tests paused.

| Outcome | Compact proxy | Native retained tools |
| --- | ---: | ---: |
| Required tool and field OR-route recall, cases with a route | 11/11 | 11/11 |
| Scripted supported-task success | 10/10 | 10/10 |
| Successful generated executor calls in corpus | 11 | 11 |
| Unsupported request with no route | No candidate/call | No candidate/call |
| Unsupported `exchange_rate` field | Not advertised; whole task unsupported | Not advertised; whole task unsupported |
| Initial model-facing tool bytes | 1893 | 1333 |
| Tool bytes after exact summarize discovery | 1893 | 1507 |
| One discovery plus five repeat-call request exposures | 11358 | 8868 |
| Each summarize name-plus-argument envelope | 68 | 35 |
| Exact summarize discovery result bytes | 703 | 703 |

The eleven route-bearing cases include one unsupported-field case: execution of its
supported summarize route succeeds, but this is not counted as successful fulfillment
of the complete unsupported request. Both ambiguous cases use their frozen OR routes;
all route-specific hits and chosen first returned acceptable routes remain in JSON.
These are scripted choices and service results, not model-driven tool selection.

For the six-turn repeated-summary trace, native exposure saves 2490 measured tool
bytes (21.9%). There is one discovery and five invocations in each arm. No repeated
search is silently charged to the baseline. Native model tool overhead instead
reaches 2295 bytes for the projected-tools query and 2068 for the design query,
versus the constant 1893-byte compact registration. The native set reached 4267
model tool bytes in the frozen traces. This is a workload-dependent tradeoff.

`FixedBytes` on a trace row is the projected current-request overhead before that
operation. Administrative deny/generation/stale-injection rows remain state
observations and add no cumulative request exposure. A skipped call also adds no
request exposure. Only executed discovery/invocation rows increase
`CumulativeFixedBytes`. `InvocationFixedBytes` in quality rows likewise describes
the projected surface, even when no candidate is invoked. These costs exclude
actual provider envelopes, network framing, system prompts, and output schemas;
they use the framework's model ToolDefinition contract.

The native ordinal-name traversal reached 5546 retained descriptor bytes and nine
evictions, all under the five-tool bound; the frozen real catalog did not hit the
8192-byte limit or contain a rejected oversized descriptor. Byte eviction and
whole-descriptor oversized rejection are proven separately with synthetic 5000-byte
and 8193-byte descriptions in `TestDiscoveryStateContract`. Those synthetic values
are excluded from the recorded quality and cost results. Count eviction and
idempotent rediscovery preserve the documented oldest-discovery order.

## Visibility, authorization, and invalidation

The executable traces cover exact, semantic, ambiguous, repeat, rediscovery,
hidden-call, denied, revoked, stale-generation, session/agent isolation, count,
and ordinal byte-bound scenarios. The real generated interceptor checks current
host authority on every invocation. Denial and revocation produce no summarize
service effect, even after discovery. Generated discovery can disclose a denied
tool descriptor; this experiment introduces no metadata confidentiality guarantee.

A fresh compact proxy can call a known hidden summarize tool through its validated
`call_tool` contract. A fresh native surface rejects the same undiscovered direct
name through runtime routing. This interface difference is preserved explicitly.
Other sessions and agents cannot use the native retained state. Generation change
removes a deliberately injected old-schema snapshot; the old direct name rejects,
rediscovery restores the current tool, and missing required text rejects through
the current generated decoder before the service runs.

The state is an in-memory experimental object. Runtime reconstruction, process
restart, persistence, concurrent mutation, replay, and continue-as-new are not
production designs established by these tests. The generated search payload still
contains compact `call_tool` instructions; this is another reason the scripted
results cannot stand in for an autonomous model experiment with a native surface.

## Compaction and estimated admission

The equal pressure history starts with the 15-byte user text `historical task`,
then eight closed tool-use/result pairs with 4096 ASCII result bytes each, followed
by a 128-byte user message. This leading framing is required by canonical transcript
grammar and was frozen before recording. Exact stored encodings, model parts, and
index metadata are built by the harness and checked through canonical validation.

The test uses actual
[`contextmeasure.Measure`](../../runtime/agent/contextmeasure/measure.go),
[`compaction.Observe`/`Decide`](../../runtime/agent/compaction/decide.go), and
[`checkpoint.Project`](../../runtime/agent/transcript/checkpoint/view.go).
Settings are Clear only, trigger 4/5, protected recent tool tokens 512, minimum
saving 100; usable budgets are 3000 and 6000 with output reserve 1000. The estimator
is one token per three text bytes, rounded up. There is no measured provider token
count, actual provider admission, or calibrated guard claim.

| Exposure | Before text bytes | After text bytes | Fixed tool bytes, before and after | Estimated tokens before → after |
| --- | ---: | ---: | ---: | ---: |
| Compact, all phases | 35244 | 6817 | 1893 | 11748 → 2273 |
| Native, initial | 34684 | 6257 | 1333 | 11562 → 2086 |
| Native, discovered/repeat | 34858 | 6431 | 1507 | 11620 → 2144 |

Actual rendered history shrinks from 33351 to 4924 view text bytes in both arms.
All configurations reject before compaction and accept afterward under both fixed
estimated budgets. The experiment did not observe an after-compaction budget
rejection. Discovery adds 174 fixed bytes and 58 estimated tokens to the native
post-compaction request; compaction neither removes this schema overhead nor
changes retained discovery state. No input or budget was tuned to manufacture a
crossing. The equal pressure history isolates this effect; separately retained
discovery payload and invocation-envelope bytes are not claimed as total prompt
savings.

## Local latency observations

Each operation has one warmup and 100 sequential samples. The interval measures
the generated executor only; JSON report encoding and fresh runtime registration
are outside it. Setup measures runtime construction, projected registration, and
RegisterToolset; it excludes fixture adapter/service construction. Medians are the
midpoint of sorted samples 49 and 50, rounded down to whole nanoseconds; p95 is
nearest-rank sample 94 (all indexes zero-based).

| Arm / operation | Execution median / p95, µs | Setup median / p95, µs |
| --- | ---: | ---: |
| Compact search | 202.916 / 436.875 | 11.500 / 47.500 |
| Compact summarize | 7.583 / 19.708 | 10.520 / 22.958 |
| Native search | 205.708 / 428.208 | 11.375 / 36.708 |
| Native summarize | 2.666 / 7.250 | 6.333 / 13.833 |

These are local observations, with no pass thresholds, randomized arm order,
confidence intervals, network latency, or evidence of production speedup. All raw
samples remain available. The ranker is unchanged and can have schema-map ordering
variation; the harness retains each returned payload rather than assuming all
repeated responses are identical. This particular latency recording observed one
payload variant per operation/arm.

## Existing materialization follow-up

`TestGeneratedLocalRuntimeMaterializationLimitation` reproduces the separate
baseline issue with actual Runtime.ExecuteToolActivity and compact summarize:

```text
encode call_tool tool result: tool call_tool result must be a typed Go value, got jsontext.Value
```

The service effect occurs once before encoding fails. The harness asserts that
fact and does not retry the effect. Generated
[`localPlannerToolResult`](../../codegen/mcp/templates/local_provider.go.tpl)
returns `loom.JSONValue(structuredContent)`; the runtime's
[`EncodeCanonicalToolResult`](../../runtime/agent/runtime/tool_result_encoding.go)
rejects raw JSON at its typed-codec boundary. Existing direct-executor parity does
not establish successful activity materialization. The follow-up belongs to the
generated local provider/result codec contract, requires a real activity-level
regression, and should preserve exactly-once effect handling. No local encoder
shim, suppression, generated edit, or production repair is included here.

Before any later adoption proposal, resolve that owner contract and separately
specify durable session/agent state, replay and continuation, invalidation,
concurrency, and native search instructions. This experiment does not establish
enough model or production evidence to authorize that implementation.

## Reproduction and acceptance proof

From the repository root:

```sh
go test -C integration_tests/fixtures/assistant ./progressive_discovery/stateful_evaluation -count=1
go test -C integration_tests/fixtures/assistant ./progressive_discovery/stateful_evaluation -race -count=10
# Only with other workloads paused; deliberately replaces the retained run:
LOOM_RECORD_STATEFUL_DISCOVERY=1 go test -C integration_tests/fixtures/assistant ./progressive_discovery/stateful_evaluation -run '^TestRecordEvaluation$' -count=1
```

Targeted lint, from `integration_tests/fixtures/assistant`:

```sh
golangci-lint run --config ../../../.golangci.yml --allow-serial-runners --timeout=5m ./progressive_discovery/stateful_evaluation/...
```

`TestDiscoveryStateContract` proves deterministic bounds, order, rediscovery and
invalidation. `TestNativeDiscoveryExecutionBoundary` proves routing, trusted
execution/authority, required-field rejection and service effects.
`TestFrozenCorpusUsesActualGeneratedSchemas`, `TestFrozenDiscoveryTraces`, and
`TestCompactionPreservesDiscoveredSchemaOverhead` execute the unchanged cases,
traces, and pressure contract. `TestRecordedEvaluationProvenance` verifies input
identities and sample counts. The opt-in recorder is skipped in normal checks.

Acceptance results: normal targeted tests pass; targeted lint reports zero issues;
the coordinated final recording passes (0.815s), and the final targeted race suite
passes ten repetitions (10.629s). Declaration-only ordering changes after recording
were formatted and rechecked with the normal suite; they do not alter the timed
operations. Repository batch gates remain the coordinating agent's responsibility. Earlier red scenarios exposed
missing state implementation, the real runtime materialization limitation,
canonical leading-user framing, and report encoding of unrelated duration fields;
the final pressure report projects measured scalars directly from canonical facts.
