# Compaction tail-retention coverage audit

Issue: [#368](https://github.com/CaliLuke/loom-mcp/issues/368). Audited baseline:
`5875a032`, 2026-10-19. This change adds a deterministic regression test and this
audit; it does not change compaction policy or runtime behavior.

## Upstream intent and framework fit

[ADK TestTailRetentionE2E at 7fbb88fe](https://github.com/google/adk-go/blob/7fbb88fe7354e0c86a0310d31b5c0926ff833c44/agent/llmagent/llmagent_tail_retention_test.go)
uses eight turns, a token threshold of 400 and two retained events to force at
least two summaries through a recorded real model. It checks that the actual
next summarizer request contains the complete previous summary after transcript
escaping, that summaries supersede each other, and that subsequent agent prompts
contain the current summary plus retained tail. It explicitly excludes semantic
fact retention as a guarantee.

Loom keeps its budget-based view, recorded context-model calls, canonical
checkpoint lineage and durable adoption rules. Copying ADK's thresholds or
provider cassette would add no useful contract coverage.

## Coverage mapping

All test names below are in `runtime/agent/runtime` unless qualified.

| Invariant | Executable coverage |
|---|---|
| Complete prior summary reaches the next actual request | New `TestSummaryRollingSeedAndTailRetention` runs two accepted summary activities through real preparation, request rendering, recording, adoption and view reconstruction. It checks the complete multiline quoted seed. Existing `TestSummaryPromptScopeAndFoldIdentity` covered only within-activity folds; `TestContinuationThreeRunReceiptRecallAndBoundedSummary` checked inherited seed preparation without dispatch. |
| Current summary supersedes prior summary; tail and lineage survive | The new test checks current summary rendering without stacking the first, exact retained tool-call/result messages, both provider grammars, bounded checkpoint bytes, advancing Through/Chain and Prev linkage. `TestCompactedReceiptChain`, `TestCompactedCutAndGrammar`, `TestCheckpointBudgetAndCaches`, `TestViewUpdateAdoption` and `TestTailInheritsHiddenCalls` support safe cuts and reconstruction. |
| Stored summary stays whole; planner rendering stays bounded | `TestSummaryStoredWholeThenBounded`; `checkpoint.TestFromRecordCoverageAndSummary` and `checkpoint.TestChooseTail`. |
| Recorded settings and separate context model | `TestSummaryContextModelStampAndSeal`, `TestSummaryPromptScopeAndFoldIdentity`, `TestSummarizeActivityStoresRecordBeforeReference`; the new test requires both calls to be adopted. |
| Rejected, stale or interrupted work cannot adopt itself | `TestWorkflowSummaryAdoption`, `TestSummaryAdoptionRequiresRecordEvent`, `TestSummaryCallCommitCrashWindows`, `TestSummaryZombieAttemptAbandoned`. |
| Exact unsummarized recall and canonical evidence | `TestSummaryUnsummarisedRangesRecall`, `TestSummaryEligibleEvidenceP4`; `summarize.TestInputEligibleEvidence`. |

The new test scripts only context-model responses. It does not mock summary
preparation, model-request construction, canonical adoption or planner-view
reconstruction. Existing rejection, recovery and recall tests remain the proof
for those dimensions; the new test does not duplicate them or claim semantic
quality from its scripted text.

## Executed proof

Before adding the regression, this bounded matrix passed across all three
packages:

```sh
go test -short ./runtime/agent/runtime ./runtime/agent/transcript/checkpoint ./runtime/agent/transcript/summarize -run 'Test(Summary|Summarize|WorkflowSummary|Compacted|CheckpointBudget|ViewUpdateAdoption|TailInherits|ChooseTail|FromRecordCoverageAndSummary|UnsafeEvictDropsSummaryRejected|InputEligibleEvidence|ChunkPlanningWindowsAndCoverage|ContinuationThreeRunReceiptRecallAndBoundedSummary)' -count=1
```

A Go overlay that dropped the initial previous-summary seed still passed the
selected runtime portion of that matrix. This confirmed a coverage gap, not a
runtime defect. The new test passed on the unchanged production implementation,
then failed each mutation independently:

- Seed loss: `the next actual request must contain the complete prior accepted summary`.
- Tail loss: `accepted summary preserves the exact recent tool-call/result tail`.
  Removing a complete pair leaves tool grammar valid, so the equality assertion
  detects loss that grammar checks alone cannot detect.

Reproduce the mutations from the repository root without editing production
files. The script prints each temporary overlay path; use it in the command below.

```sh
python3 - <<'PY'
import json
import tempfile
from pathlib import Path

root = Path.cwd()
work = Path(tempfile.mkdtemp(prefix="loom-368-mutations-"))
mutations = {
    "seed": (
        "runtime/agent/runtime/summary_fold.go",
        "previous := e.prepared.evidence.Previous",
        'previous := ""',
    ),
    "tail": (
        "runtime/agent/runtime/view_render.go",
        "messages := transcript.AssembleView(c.systemMessages, message, c.tailMessages)",
        "tail := c.tailMessages\n"
        "\tif message != nil {\n"
        "\t\ttail = tail[:len(tail)-2]\n"
        "\t}\n"
        "\tmessages := transcript.AssembleView(c.systemMessages, message, tail)",
    ),
}
for name, (relative, before, after) in mutations.items():
    source = root / relative
    original = source.read_text()
    assert original.count(before) == 1, f"mutation anchor changed: {relative}"
    mutated = work / f"{name}.go"
    mutated.write_text(original.replace(before, after))
    overlay = work / f"{name}.json"
    overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}))
    print(f"{name}: {overlay}")
PY
```

Run each overlay only against this focused test: the tail mutation deliberately
assumes the fixture has at least two recent messages. Both runs must exit nonzero
with the named assertion above, rather than a build failure or panic.

```sh
go test -short -overlay /absolute/path/printed/seed.json ./runtime/agent/runtime -run '^TestSummaryRollingSeedAndTailRetention$' -count=1
go test -short -overlay /absolute/path/printed/tail.json ./runtime/agent/runtime -run '^TestSummaryRollingSeedAndTailRetention$' -count=1
```

Run without an overlay for the passing implementation:

```sh
go test -short ./runtime/agent/runtime -run '^TestSummaryRollingSeedAndTailRetention$' -count=1
go test -short -race ./runtime/agent/runtime -run '^TestSummaryRollingSeedAndTailRetention$' -count=20
```

This is a bounded audit, not a whole-repository mutation test, live-provider
qualification, or statistical recall measurement.
