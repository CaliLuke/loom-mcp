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
// Claim is a declaration only. Judging, experiment selection, repeats and storage
// belong to separate layers. Execute may run concurrently on one harness; callbacks
// and expectation predicates must be concurrency-safe and cooperate with cancellation.
package eval
