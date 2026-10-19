# Frozen progressive-discovery comparison

This fixture compares the existing generated assistant search with SchemaRouter
v0.17.0's stock MCP importer and deterministic `retrieve` API. It does not change
the rankers, generated code, runtime, or repository dependencies.

Run correctness checks from the assistant fixture:

```sh
go test ./discoverybench -count=1
python3 -m unittest discover -s discoverybench -p '*_test.py'
```

Refresh measurements explicitly, with `uv` available and no concurrent builds,
tests, or benchmarks:

```sh
./discoverybench/reproduce.sh
```

The script creates an isolated Python 3.13.9 environment under
`/tmp/loom-discoverybench-env` (override `LOOM_DISCOVERY_BENCH_ENV`), installs the
exact committed dependency lock, and replaces `results/`. It never changes the
catalog, cases, or ranking configuration. The competitor source is pinned to
commit `f7a332220eb16d2264d85f2a87c56dc0c5c020f8`, the dereferenced v0.17.0 tag.
Python dependencies are hashed; the VCS source is pinned by that exact commit.
No model, embedding API, or executable competitor binding is used.

`testdata/catalog.json` is the actual generated MCP tools/list response with
search disabled. The server result observer preserves raw int64/uint64 bounds
before generic client decoding. The cases file defines task-required schema
pointers and alternative acceptable routes. Corpus hashes are asserted before
measurement. Changes require a new contract, not a result-driven adjustment.

Each case runs at K=1/3/5, with 20 warmups and 100 sequential timing samples. Baseline
uses the generated local registration's Execute callback, including validation,
search and response serialization. SchemaRouter timing includes analysis,
retrieval and typed result construction, but excludes model_dump/artifact
serialization. These durations cover different wrapper work. They do not prove
ranker speedup or end-to-end application performance.

`scoring.py` resolves field paths against returned input/output schemas, compares
structural constraints and required membership, and ignores only description,
title, $comment and examples annotations. It never awards fields solely because
the tool was retrieved. Alternative routes are scored separately; their partial
fields are never combined. Unsupported fields are outside supported recall.
Each distinct result payload and its ordered warmup/timed sample references are
retained. Metrics weight all 100 timed observations; variants and ranges remain
visible. The first run exposed nondeterministic generated phrase-match scores
and stopped on a recorder equality assumption. Its log is preserved; the
corrected recorder leaves ranking unchanged.

Schema bytes use lossless compact UTF-8 JSON with sorted keys; full retrieval
payload and native payload byte counts are also retained.

`results/capture_source_hashes.json` preserves measurement-time source identity;
`results/source_hashes.json` records final source identity. The retained run differs
only by callback formatting and the provenance-copy command added after capture.
A fresh reproduction writes both from its current source.

`results/imported_catalog.json` exposes the stock importer's projection, including
loss of MCP discovery categories/tags/keywords. `baseline.json` retains the
baseline-only metadata filter check and validated generated invocation outcomes.
The boundary proof is direct generated registration Execute; it does not prove
Runtime.ExecuteToolActivity result materialization. The native-discovery
experiment records that separate runtime limitation.

Recompute metrics without executing either retriever:

```sh
python3 discoverybench/summarize.py discoverybench/results
```

The decision report is `docs/wips/progressive_discovery_cross_benchmark.md` at the
repository root. Negative retrieval outcomes and importer losses are evidence,
not reasons to tune the frozen experiment.
