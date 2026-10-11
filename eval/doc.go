// Package eval binds immutable datasets and scenario assertions to agenttest sessions.
//
// Items and JSONL eagerly capture validation errors in a Dataset, allowing datasets
// to be declared as package variables. Validate joins all scenario problems before
// execution. JSONL version 1 starts with a format/version header, rejects unknown
// and duplicate members, and preserves number tokens in item data and tool arguments.
// Hash addresses the canonical JSONL bytes, independently of their source file.
//
// Execute handles one attempt. The default driver runs scripted user inputs in one
// new session; Drive may replace it and Assert may add checks, scores and claims.
// Task and Reference resolve from the item before falling back to scenario defaults.
// Evaluation orders runs_succeeded, scenario expectations, item expected tools, then
// Assert checks. Failed checks describe quality failures. Outcome.Err describes
// infrastructure errors, panics, timeouts and broken assertion contracts. The error
// return is reserved for invalid arguments. The mandatory health check alone does
// not satisfy the requirement for user checks or claims.
//
// Plan validates all scenarios, variants and selectors, then enumerates variant,
// scenario, item and repeat keys in declaration order. Run constructs a fresh
// harness per attempt, starts longest timeouts first (zero is longest), bounds
// concurrency and retains declaration-ordered records. Repeats and Concurrency
// default to one. Selectors combine with OR within a kind and AND across kinds.
//
// NewHarnessFunc forwards base then variant options and closes failed setups.
// Factories must forward supplied options and return fresh harnesses. Prefer
// ScriptedModel, which creates one script per harness, over ScriptedModelFrom
// with a shared script. Variant labels and cloned policy are run defaults;
// context compaction and recovery settings belong in Variant.Options.
//
// Verdict precedence is error, failed, pending, passed. Gating claims remain
// pending until judging; informational claims do not gate. HarnessErr records
// factory/setup/panic/reuse/Close failures. NotRun records identify attempts
// skipped by cancellation, token limits or disabled live models. Run returns a
// partial report and an error for caller cancellation or disabled live use;
// token limits and recorded failures are not Run errors. Run never enables live
// models. OnAttempt is serial, once per record, with skipped records delivered
// last in declaration order. Groups reports counts without a pass threshold.
//
// Tokens come from all completed calls in each returned run record, including
// descendants and abandoned calls. TotalTokens is used when positive, otherwise
// input, output and both cache fields are summed. Decide calls inside Assert and
// runs omitted from a custom Drive's returned session are not counted. A token
// limit stops new attempts at >= MaxTokens; running attempts finish, allowing up
// to Concurrency-1 additional attempts beyond the crossing attempt. Callback
// order and skipped keys are nondeterministic under concurrency; record order is
// stable. Runs exposes runrecord exports for storage; AttemptKey is the identity
// because run and session IDs may repeat across isolated harnesses. Exports are
// retained in memory for the entire experiment.
//
// Claim is a declaration only. Storage and judging belong to separate layers.
// Execute may run concurrently on one harness; callbacks and expectation predicates
// must be concurrency-safe and cooperate with cancellation.
package eval
