# Qualified context defaults

Planner agents record a default context policy with attachment protection. The
shipped qualification table is empty: no provider receives automatic threshold
compaction until its exact configuration and model have qualification evidence.
Overflow recovery continues independently of qualification.

An application that has qualified its own configuration can register that result:

```go
rt := runtime.New(runtime.WithContextQualification(runtime.Qualification{
    Config: "anthropic",
    Models: []string{"the-exact-model-id"},
    Tier: compaction.Evict,
}))
```

This is self-certification, not a built-in attestation. Configuration names are
`anthropic`, `bedrock-anthropic` for Claude on Bedrock, `bedrock` for other Bedrock
models, `openai`, `gemini`, `ollama`, and `codex`. Unknown clients and Gateway have
no configuration identity unless an application supplies the optional
`model.ProviderConfigurationInfo` capability. Matching is exact; only Bedrock
routing prefixes (`us.`, `eu.`, `apac.`, `global.`) are removed. A model family name
never stands in for a dated model ID.

The first adopted view-bearing call fixes each model's qualification and support.
A later model may narrow the allowed tier or turn automatic compaction off.
Changing registration, the qualification table, or provider capabilities cannot
change replay. An `evict` qualification permits receipt-only compaction. A
`summary` qualification also permits summaries when the agent has a registered
`ContextModel` in `RunPolicy.ContextDefault`; without one, it remains receipt-only.

Use `runtime.WithoutContextDefault(agentID)` or
`RunPolicy.ContextDefault = &runtime.ContextDefault{Disabled: true}` to disable
this default. Explicit `WithContextCompaction` wins over disablement and preserves
its existing behavior, including failure on unknown limits. Per-run
`ContextOverrides.Threshold` explicitly enables or disables thresholds. A false
`KeepNonTextResident` override requires explicit threshold enablement. Setting
`Threshold=true` clears default provenance but retains an inherited residency
guard; also set `KeepNonTextResident=false` to waive it. Setting `Threshold=false`
clears both forms of threshold enablement.

Original images and byte/URI documents stay resident when the recorded model
support cannot recall them or their aggregate non-text estimate exceeds the
current recall allowance. Exact formats, upload sizes, and URI schemes are
checked through the existing RecordContent activity, including newly pending
entries and originals already evicted. Matching recorded evidence may be reused;
a new attachment or model requires a fresh check. Missing or malformed durable
content is an error. No descriptors or provider support arrays are added to the
bounded planner envelope. Before the first model observation, support-dependent
residency checks wait for recorded facts; absent facts do not mean zero support.

The sticky `context_nontext_resident` diagnostic is emitted once before the next
affected planner schedule. Its reason is `provider` for empty effective support,
or `too_large` for format, scheme, byte-size, or token-budget incompatibility.
It names the deterministic recorded model witness. Application hooks may drop or
rewrite this ordinary diagnostic. Canonical records and exports derive sticky
residency from a protected `context_residency_adopted` decision, adopted model
facts and compatibility evidence independently of diagnostic delivery. The
protected decision binds the actual preparation step and original coverage; a
failed planner still retains residency when its final record seals. Merely
sealing a terminal first step does not create a new residency decision. Overflow recovery can still
remove attachments. Explicit compaction waives this protection unless
`ContextCompaction.KeepNonTextResident` is true.

Successful qualified default runs can publish session continuation only when all
observed steps remained qualified and the run never became resident. Otherwise
the previous head remains. A store without continuation support starts default
runs fresh with `store_unsupported`; explicit continuation still requires the
store capability. An agent declaring a name containing `recall_context` registers
successfully but disables its default with one warning. Explicit registration
retains its reserved-name validation.

Protocol 19 records these settings, call facts, compatibility evidence and
adoptions. RunRecord exports verify this evidence using their included content.
Drain protocol 18 workflows before deploying; old records remain readable.
Temporal and Selvedge each have ten genuine v19 replay histories. The structural
template version remains 5. Every generated planner config exposes optional
ContextModel. Retired policy APIs are removed; see the
[migration guide](runtime.md#migration-from-removed-history-apis).
