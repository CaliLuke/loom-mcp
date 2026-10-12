// Package summaryeval evaluates recorded compaction cuts independently of agent
// execution. Read loads a T13 experiment or raw runrecord export; Cuts extracts
// threshold summaries, rejected attempts and optional receipt-only evictions.
// Verify replays recorded responses in an isolated runtime and checks request
// digests, rejection kind and checkpoint identity before any variant runs.
//
// Run uses the production SummarizeContextActivity for each cut, variant and
// repeat. It preserves the recorded previous summary, evidence and cut budget;
// planners, tools and recall never run. Prompt templates use Previous, Evicted,
// Instructions and Budget. Cut-moving and chained variants are outside this API.
//
// Private versioned replays and summary caches live under _compaction in the eval
// store. Judgments use eval/judge, share the old side across variants, and are not
// cached. Aggregate excludes unverified baselines and incomplete comparisons.
// Callers supply gated models and optional application facts through Config.
package summaryeval
