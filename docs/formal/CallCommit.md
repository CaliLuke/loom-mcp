# CallCommit implementation proof

The model, invariants, fault configurations and assumptions are documented in
[ContextCompaction.md](ContextCompaction.md#callcommittla). K15 leaves the TLA
module and configurations unchanged.

One model-calling activity kind is modelled. Summariser attempts use the same
commit rule: every send follows its durable start; canonical compaction records
adopt all started keys from the successful activity attempt. Checkpoint Calls
and transcript entries cannot adopt calls. Store faults are abstracted as crashes.

Go proof: `TestSummaryCallCommitCrashWindows`, `TestSummaryZombieAttemptAbandoned`,
`TestSummaryAdoptionRequiresRecordEvent`, `TestWorkflowSummaryAdoption`,
`TestSummarizeActivityRecordingFaults` and `TestSummarizeActivityFailurePolicy`.
The summary size rule is not modelled; K15 tests it in
`TestSummarySizeUsesRecoveryProjection`, `TestSummaryStoredWholeThenBounded` and
`TestSummarizeActivityRejectedResponseRecorded`.

Targeted K15 checks use CallCommitTwoSteps plus the zombie witness and the
send-before-start, adopt-by-entry, per-attempt-sequence and intercepted-record
fault configurations. Each runs separately with `-workers 4` and `-Xmx4g`.
