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
// skipped by cancellation, token limits, disabled live models or an OnAttempt
// panic; the first stop reason wins. Run returns a partial report and an error
// for caller cancellation, disabled live use or an OnAttempt panic (with its
// stack); token limits and recorded failures are not Run errors. Run never
// enables live models. OnAttempt is serial, at most once per record, with
// skipped records delivered last in declaration order; a panic disables later
// callbacks. Groups reports counts without a pass threshold.
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
// Open creates a self-gitignored experiment store. Begin validates the plan,
// stages both headers, and atomically publishes the experiment directory.
// Readers ignore hidden staging directories. Writer.OnAttempt streams complete
// attempt lines. Finish fills missing records and atomically writes the terminal summary.
// Write failures are sticky and WithOnError reports the first failure once.
// Oversized records omit exports and retain a StorageError diagnostic.
// Schema version 1 reserves StoredClaim subjects and tool selectors, judgment
// subjects and step locators, and separate judge usage and tokens. These optional
// fields survive decoding; the current runner does not populate them.
//
// Load rejects invalid schemas and exports but retains complete lines before a
// torn final line. StoredRun.Record and Trajectory restore evidence after harness
// closure, including final messages, event verification and trace IDs. Fingerprints
// summarize recorded model/prompt identities and system, tool and context hashes.
// List, Resolve, Last and Find locate experiments and attempts. RerunSelection
// selects failed, error and not-run groups as a variant/item cross-product superset.
// Full prompts and server-only content remain local without redaction. Directories
// created by the store use mode 0700 and new files use mode 0600. Callers own
// existing directory permissions, which Open preserves. The store never deletes
// experiments.
//
// Claim is a declaration only. Judging belongs to a separate layer.
// Execute may run concurrently on one harness; callbacks and expectation predicates
// must be concurrency-safe and cooperate with cancellation.
package eval
