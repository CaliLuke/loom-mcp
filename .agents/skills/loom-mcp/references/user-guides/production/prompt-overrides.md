# Prompt Overrides with Mongo Store

Production prompt management uses:

- baseline specs in `runtime.PromptRegistry`
- scoped overrides persisted via `features/prompt/mongo`

## Wiring

--- CODE ---
import (
    promptmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo"
    clientmongo "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo/clients/mongo"
    "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

promptClient, err := clientmongo.New(clientmongo.Options{
    Client:     mongoClient,
    Database:   "aura",
    Collection: "prompt_overrides", // optional (default is prompt_overrides)
})
if err != nil {
    panic(err)
}

promptStore, err := promptmongo.NewStore(promptClient)
if err != nil {
    panic(err)
}

rt := runtime.New(
    runtime.WithEngine(temporalEng),
    runtime.WithPromptStore(promptStore),
)
--- END CODE ---

## Override Resolution and Rollout

Precedence order:

1. matching session scopes before non-session scopes
2. within either dimension, the matching scope with the most labels
3. for equal specificity, the newest override
4. global scope
5. baseline spec when no override matches

The Mongo adapter stores an exact, versioned SHA-256 `scope_fingerprint` for
the sorted label map. A resolve computes the exact fingerprints of matching
label subsets, performs at most one indexed session lookup plus one indexed
global lookup, and asks MongoDB for the highest label count/newest record. It
does not decode and scan all overrides of a specificity in application code.

Mongo scopes are limited to 15 labels so the candidate fingerprint set remains
bounded. Label keys and values may contain dots, dollar signs, separators, or
other arbitrary text because they are length-delimited before hashing.

This is a persisted-schema change. Client construction scans only documents
missing `scope_fingerprint`, derives it from `scope_labels`, then creates the
compound index: `prompt_id`, `scope_session`, `scope_fingerprint`,
`scope_label_count`, then `created_at`. Migration, decode, update, oversized
legacy scope, and index errors fail construction. After a successful startup,
normal resolution is index-only and never falls back to broad candidate scans.

Recommended rollout:

- register baseline specs first
- roll out broad overrides (`org`) then narrow (`facility`, `session`)
- monitor effective versions via `prompt_rendered` events and `model.Request.PromptRefs`
- roll back by writing a newer override at the same scope
