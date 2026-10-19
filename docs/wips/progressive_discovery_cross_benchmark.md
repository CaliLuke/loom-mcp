# Generated progressive discovery and typed retrieval

## Decision

Retain the current generated discovery path for this fixture. The frozen
comparison does not justify adding SchemaRouter as a framework dependency or
replacing the generated ranker. At K=3, generated discovery retrieved all eleven
supported cases; the stock SchemaRouter MCP path retrieved ten. Both retained
the required schema fields whenever they retrieved the expected tool.

This is evidence from fifteen deterministic fixture tools and twelve authored
queries, not autonomous-agent task success or broad production superiority.
The benchmark also found an existing generated ranking instability, recorded
below as follow-up work without changing either retriever.

## Frozen inputs and reproduction

The [benchmark fixture](../../integration_tests/fixtures/assistant/discoverybench/README.md)
contains the runners, dependency lock, configuration, raw observations and scoring
tests. The original scope is [issue #310](https://github.com/CaliLuke/loom-mcp/issues/310).

The actual root assistant MCP `tools/list` result contains fifteen tools. Its
SHA-256 is `5533903d33ecbb42f6523b353ba9bfb8c60c0a705ee499c9b28223736a66a94b`.
The twelve-case artifact hash is
`a248254ef490c931f2284648a1c97f5a796b6b368f752c1081247ccbd020a62b`.
The catalog observer captures the generated server result before generic client
decoding, preserving integer bounds such as `18446744073709551615` exactly.
No descriptions, schema fields, discovery keywords, queries or expectations
were changed after observing rankings.

Baseline: the existing generated local registration's `Execute` callback for
`search_tools`, with generated defaults, no pins, `include_schemas=true` and
request `max_results` set to 1, 3 or 5. The category-filter capability check is
separate from the cross-arm aggregates.

Competitor: SchemaRouter v0.17.0, annotated tag resolved to commit
`f7a332220eb16d2264d85f2a87c56dc0c5c020f8`; stock
[`tool_from_mcp`](https://github.com/JDeun/SchemaRouter/blob/f7a332220eb16d2264d85f2a87c56dc0c5c020f8/src/schemarouter/adapters/mcp.py#L519)
imports the same catalog as one server-scoped ToolSpec, named `assistant-mcp`.
A default `SchemaRouter()` receives it through `add_tool` and runs `retrieve`
at the same K. The default keyword analyzer and candidate index remain enabled;
structural retrieval, embedding/semantic services and execution bindings are
absent. No custom enrichment or tuning compensates for importer loss.

Run from `integration_tests/fixtures/assistant`:

```sh
./discoverybench/reproduce.sh
```

The isolated environment pins CPython 3.13.9 and the competitor's exact commit,
with hashed transitive dependencies. It does not change repository dependencies.
Correctness tests run without installing the competitor or refreshing timings.
Recompute scores from retained observations with:

```sh
python3 discoverybench/summarize.py discoverybench/results
```

The sampled baseline used Go 1.27.2, on macOS 27.0, arm64, Mac17,2 / Apple M5,
24 GiB RAM. The repository base was `5875a032be9c77660ee29c0d41ff9fd5ff2ae5f1`
with the current reviewed batch still uncommitted. Exact fixture/source hashes,
dirty-tree inventory and installed package versions are retained in
`results/source_hashes.json` and `results/environment.txt`. The original
capture-time hashes remain in `results/capture_source_hashes.json`: after capture,
four callback bodies were expanded to the required multi-line style and the
reproduction script gained the capture-hash copy. These nonbehavioral changes
do not alter the measured retrieval path. Hardware evidence was
captured with `sysctl -n hw.model machdep.cpu.brand_string hw.memsize` in
`results/hardware.txt`. Both agents and the coordinator paused builds/tests
while the arms ran sequentially.

## Recall and exposed fields

Each table cell is supported-tool recall over eleven cases, with an OR hit for
each ambiguous case. The unsupported banking request is excluded. The partially
unsupported summary request counts only its supported summary fields; it never
adds `exchange_rate` to the supported denominator. Every case/K has 100 measured
observations after 20 warmups. All timed observations contribute, including
variants; warmups are retained separately.

| K | Generated | SchemaRouter |
| --- | ---: | ---: |
| 1 | 10/11, 90.9% | 8/11, 72.7% |
| 3 | 11/11, 100% | 10/11, 90.9% |
| 5 | 11/11, 100% | 10/11, 90.9% |

Input and output field recall follow the same percentages in this run because
all expected field contracts survived for every retrieved expected tool. This
was checked independently by resolving the frozen JSON Schema pointers and
comparing types, bounds, enums, defaults and required membership along their
ancestor paths. Only description, title, `$comment` and examples annotations
are ignored. Tests prove that a missing field, changed bound or changed nested
required membership becomes a miss even when the tool is present. Raw results
contain separate input/output hit counts for every acceptable route; partial
fields from different routes are never combined.

| Case | Generated hits at K=1/3/5 | SchemaRouter hits at K=1/3/5 |
| --- | --- | --- |
| exact-summary | yes / yes / yes | yes / yes / yes |
| sentiment-synonym | yes / yes / yes | yes / yes / yes |
| keyword-synonym | yes / yes / yes | yes / yes / yes |
| document-lookup | yes / yes / yes | no / no / no |
| records-lookup | yes / yes / yes | no / yes / yes |
| projected | yes / yes / yes | yes / yes / yes |
| field-bounded | yes / yes / yes | yes / yes / yes |
| field-design | yes / yes / yes | yes / yes / yes |
| ambiguous-lookup | no / yes / yes | no / yes / yes |
| ambiguous-search | yes / yes / yes | yes / yes / yes |
| unsupported-field, supported part | yes / yes / yes | yes / yes / yes |

The frozen acceptable routes for `lookup` were `search`, `search_records` or
`projected_lookup_tool`. Both arms instead returned `projected_bounded_lookup_tool`
at K=1. That remains a miss under the authored contract; the acceptable set was
not broadened after seeing the result. At K=3/5, `projected_lookup_tool` was the
accepted retrieved route; neither arm retrieved either search route there.

For `search query limit`, generated K=1 returned `search` in 86/100 observations
and `search_records` in 14/100. SchemaRouter returned `search_records` in all
100. Both routes are acceptable, so OR recall stays 100%; the individual-route
frequencies remain visible. At K=3 and K=5, both search routes appear in every
observation for both arms. Generated order still varies: records first in 8/100
K=3 observations and 12/100 K=5 observations.

## Importer and negative behavior

All fifteen full input schemas and all fifteen full output schemas survived the
stock import exactly, including large integers and nested contracts. The
imported parameter/output-field projections are retained in
`results/imported_catalog.json` and `results/summary.json`.

The importer does not retain the MCP `_meta` discovery category, tags or
keywords, nor icons. This is an observed projection difference between the
stock paths, not equivalent internal metadata or a reason to add handcrafted
competitor hints. Generated `lookup` with category `records` returned only
`search_records`; that baseline-only capability check is excluded from recall
aggregates. The import loss is consistent with the document-lookup miss, but
this experiment does not isolate it as the sole cause.

For the unsupported request `send invoice payment to bank account`, generated
search returned no suggestions in all 300 timed observations. SchemaRouter
returned exactly K suggestions in all 300: `report_progress` first, followed
by unrelated catalog tools at larger K. Thus nonempty-shortlist false suggestion
frequency was 0% versus 100% for this one negative intent. It is a retrieval
observation, not a claim that SchemaRouter executed an unauthorized action.

Both arms returned `summarize_text` for `summarize text and return exchange_rate`.
Neither exposed the unsupported output field in any returned schema. Retrieval
of the supported part does not prove the entire requested task can be fulfilled.

The shared generated invocation proof is deliberately separate from retrieval.
Both arms found `analyze_sentiment` for its exact-name discovery check. Direct
execution through the generated compact registration produced:

| Invocation | Outcome | Service effects |
| --- | --- | ---: |
| `call_tool` → analyze_sentiment, valid text | positive sentiment | 1 |
| Hidden direct analyze_sentiment | rejected | 0 |
| Proxy unknown send_invoice | rejected | 0 |
| Proxy summarize_text without required text | rejected | 0 |
| Proxy execute_code with language rust | rejected before service | 0 |

No handwritten dispatcher or validation engine replaces generated decoding and
middleware. This proof ends at generated registration `Execute`; it does not
claim end-to-end `Runtime.ExecuteToolActivity` result materialization. The
separate native-discovery experiment found that structured results currently
fail at that runtime encoding boundary. This benchmark does not repair it.

## Payload cost and timing

Schema bytes sum compact UTF-8 `{"inputSchema":...,"outputSchema":...}` envelopes
for actual returned candidates, without deduplication. Whole-payload bytes use
the same lossless serializer for both arms; native payload bytes are also retained.
Means below include all twelve cases and all timed observations at each K.

| K | Arm | Mean shortlist | Mean schema bytes | Mean full payload bytes |
| --- | --- | ---: | ---: | ---: |
| 1 | Generated | 0.92 | 685.6 | 1,244.6 |
| 1 | SchemaRouter | 1.00 | 787.3 | 3,735.0 |
| 3 | Generated | 1.58 | 1,221.3 | 2,072.3 |
| 3 | SchemaRouter | 3.00 | 2,168.9 | 9,121.7 |
| 5 | Generated | 1.58 | 1,221.3 | 2,072.3 |
| 5 | SchemaRouter | 5.00 | 3,545.2 | 13,623.2 |

Actual shortlist size matters: generated exact-match narrowing and rejection
usually return fewer than K; this SchemaRouter configuration fills K. The
payloads also have different content: generated invocation guidance versus
rich typed candidate fields, scores and provenance. The byte differences do
not establish equivalent-information compression or provider token savings.

Each cell below is the range across twelve per-case median/p95 durations, in
microseconds, with 100 sequential samples per case/K and nearest-rank p95.
Individual samples and per-case statistics remain in the artifacts.

| K | Arm | Per-case median range, µs | Per-case p95 range, µs |
| --- | --- | ---: | ---: |
| 1 | Generated | 88.7–150.0 | 264.0–413.0 |
| 1 | SchemaRouter | 739.3–1,146.6 | 798.4–2,869.8 |
| 3 | Generated | 99.2–162.8 | 257.4–427.0 |
| 3 | SchemaRouter | 2,217.4–3,873.6 | 2,373.9–8,563.3 |
| 5 | Generated | 100.6–167.7 | 267.4–468.8 |
| 5 | SchemaRouter | 3,674.0–4,636.5 | 4,147.4–14,077.3 |

These are in-process retrieval API durations with different wrapper work.
Generated timing includes registration dispatch, validation, search and response
serialization. Competitor timing includes query analysis, search and typed
result construction, but excludes later `model_dump` and artifact serialization.
Neither includes network transport. Registration setup was 0.035 ms; competitor
import/registration setup was 2.502 ms, both excluded from samples. Language,
wrapper and output differences prevent a transport-normalized speedup claim.
There are no timing pass thresholds.

## Existing instability and limitations

The first run stopped because the observation harness incorrectly assumed every
repeated result was identical. Its failure log is retained. The approved
correction stores each distinct lossless payload with ordered warmup/timed
sample references and weights every timed variant in scoring. No query,
expectation, K, ranking setting or sample count changed.

The source explains the observed generated order variation:
`toolInputParameterText` iterates a Go properties map, then `toolSearchRank`
applies a phrase substring match to that joined text before token-overlap
scoring. `search query limit` can span adjacent property descriptions/names
depending on map iteration order. This can change scores; the stable-sort tie
breaker itself is deterministic. The owning generated functions are in
`integration_tests/fixtures/assistant/gen/mcp_assistant/adapter_server.go`.
A separate generator-owned follow-up should make this text construction
order-independent and prove stable scoring. No ranker correction is included here.

This catalog is small, authored and local. Results do not establish recall on
larger third-party catalogs, provider behavior, model-driven tool choice,
authorization policy quality, session-state benefits, or production latency.
The benchmark supports retaining current discovery and investigating the
specific observed failures. It supplies a reproducible comparison rather than
a new runtime dependency or an adoption claim.
