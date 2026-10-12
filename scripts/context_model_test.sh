#!/usr/bin/env bash
set -euo pipefail

jar="${1:-}"
if [[ ! -f "${jar}" ]]; then
  echo 'usage: make context-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar' >&2
  exit 1
fi
jar="$(cd "$(dirname "${jar}")" && pwd)/$(basename "${jar}")"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
modules=(ContextCompaction SessionContinuation CallCommit RequestRecord
  ContinuationCarry RecallDiscovery OverflowGuard StepPayload)
# Keep TLC traces outside the checkout, including expected counterexamples.
for module in "${modules[@]}"; do
  cp "${root}/docs/formal/${module}.tla" "${root}/docs/formal/${module}"*.cfg "${work}/"
done
cd "${work}"

# run MODULE CONFIG prints TLC's exit code. Deadlock checking stays on: every
# module names its legitimate end states with an explicit stuttering action,
# so any other state without a successor is a modelling error.
run() {
  local module="$1" config="$2"
  local result=0
  java -Xmx10g -XX:+UseParallelGC -cp "${jar}" tlc2.TLC -workers auto \
    -metadir "${work}/states-${config}" -config "${config}.cfg" "${module}.tla" >"${config}.log" 2>&1 || result=$?
  echo "${result}"
}

fail() {
  cat "$2.log" >&2
  echo "$2: $1" >&2
  exit 1
}

# expect_pass MODULE CONFIG MIN_STATES checks a configuration that must pass,
# and that it still explores at least MIN_STATES distinct states, so a change
# that collapses the state space cannot pass silently.
expect_pass() {
  local module="$1" config="$2" floor="$3"
  local result states
  result="$(run "${module}" "${config}")"
  [[ "${result}" == 0 ]] || fail "expected no violation" "${config}"
  states="$(grep 'distinct states found' "${config}.log" | tail -1 | sed -E 's/.* ([0-9]+) distinct states found.*/\1/')"
  [[ "${states}" -ge "${floor}" ]] || fail "explored ${states} distinct states, fewer than ${floor}" "${config}"
  echo "${config}: passed (${states} distinct states)"
}

# Each fault configuration breaks one design rule; TLC must find the
# counterexample for the invariant that rule protects. A witness
# configuration states a scenario as a negated invariant, so the
# counterexample proves the scenario is reachable.
expect_violation() {
  local module="$1" config="$2" invariant="$3"
  local result
  result="$(run "${module}" "${config}")"
  if [[ "${result}" != 12 ]] || ! grep -q "Invariant ${invariant} is violated" "${config}.log"; then
    fail "expected a ${invariant} counterexample" "${config}"
  fi
  echo "${config}: reproduced the expected ${invariant} counterexample"
}

# expect_liveness_violation checks a liveness fault: TLC must report that a
# temporal property fails (exit code 13), so the property is not vacuous.
expect_liveness_violation() {
  local module="$1" config="$2"
  local result
  result="$(run "${module}" "${config}")"
  if [[ "${result}" != 13 ]] || ! grep -q "Temporal properties were violated" "${config}.log"; then
    fail "expected a liveness counterexample" "${config}"
  fi
  echo "${config}: reproduced the expected liveness counterexample"
}

# expect_action_violation is expect_violation for an action property, which
# TLC reports with exit code 13.
expect_action_violation() {
  local module="$1" config="$2" property="$3"
  local result
  result="$(run "${module}" "${config}")"
  if [[ "${result}" != 13 ]] || ! grep -q "Action property ${property} is violated" "${config}.log"; then
    fail "expected a ${property} counterexample" "${config}"
  fi
  echo "${config}: reproduced the expected ${property} counterexample"
}

m=ContextCompaction
expect_pass ${m} ${m} 82500000
expect_pass ${m} ${m}TwoChanges 15200000
expect_pass ${m} ${m}LargeSummary 60000
expect_pass ${m} ${m}Liveness 7700000
expect_liveness_violation ${m} ${m}LivenessUnsafeUnboundedRecovery
expect_violation ${m} ${m}WitnessShrinkThenRecover WitnessShrinkThenRecover
expect_violation ${m} ${m}UnsafeReferenceBeforeRecord RecordBeforeReference
expect_violation ${m} ${m}UnsafeCutInsideUnit ViewWellFormed
expect_violation ${m} ${m}UnsafeUnrecordedLimit ReplayDeterministic
expect_violation ${m} ${m}UnsafeUnrecordedSize ReplayDeterministic
expect_violation ${m} ${m}UnsafeRecoveryWithoutNewView RecoveryShrinks
expect_violation ${m} ${m}UnsafeRecoveryIgnoresTarget RecoveryShrinks
expect_violation ${m} ${m}UnsafeCompactDuringRecovery RecoveryShrinks
expect_violation ${m} ${m}UnsafeSummaryAtOtherCut NothingSilentlyDropped
expect_violation ${m} ${m}UnsafeEvictDropsSummary NothingSilentlyDropped
expect_violation ${m} ${m}UnsafeUnboundedReceipt CheckpointBounded
expect_violation ${m} ${m}UnsafeRenderStoredSummary CheckpointBounded
expect_violation ${m} ${m}UnsafeAccessByRendering NothingSilentlyDropped
expect_violation ${m} ${m}UnsafeReadServerOnly ServerOnlyNeverExposed
expect_violation ${m} ${m}UnsafeViewRendersServer ServerOnlyNeverExposed
expect_violation ${m} ${m}UnsafeRecoveryKeepsBudget RecoveryReplays
expect_violation ${m} ${m}UnsafeRecoveryReadsAdapter RecoveryReplays

m=SessionContinuation
expect_pass ${m} ${m} 68700000
expect_pass ${m} ${m}Children 3340000
expect_pass ${m} ${m}Liveness 50000
expect_violation ${m} ${m}WitnessUnsuccessful WitnessUnsuccessful
expect_violation ${m} ${m}WitnessAlreadyAfterAdvance WitnessAlreadyAfterAdvance
expect_violation ${m} ${m}WitnessResummarizedCopy WitnessResummarizedCopy
expect_violation ${m} ${m}UnsafeNonIdempotentPublish NoFalseSupersede
expect_violation ${m} ${m}UnsafePublishBeforeSeal PublishedImpliesSealed
expect_violation ${m} ${m}UnsafePublishFailed PublishedImpliesSealed
expect_violation ${m} ${m}UnsafeUnconditionalHead OneSuccessor
expect_violation ${m} ${m}UnsafeChildPublishes ChildNeverPublishes
expect_violation ${m} ${m}UnsafeOrderByLocalId OrderIsChronological
expect_violation ${m} ${m}UnsafeBareCitesCurrentRun CitationsResolve
expect_violation ${m} ${m}UnsafeRawPreviousSummary CitationsResolve
expect_violation ${m} ${m}UnsafeAlreadyWithoutCompare NoSilentBodyMismatch
expect_violation ${m} ${m}UnsafeAdvanceTwoWrites OneSuccessor

m=CallCommit
expect_pass ${m} ${m} 9200000
expect_pass ${m} ${m}TwoSteps 239000
expect_pass ${m} ${m}Liveness 21900
expect_liveness_violation ${m} ${m}LivenessUnsafeRetrySameAttempt
expect_violation ${m} ${m}WitnessZombieAfterAdoption WitnessZombieAfterAdoption
expect_violation ${m} ${m}UnsafeSendBeforeStarted SentImpliesRecorded
expect_violation ${m} ${m}UnsafeAdoptByEntry ClassificationCorrect
expect_violation ${m} ${m}UnsafeIncompleteAsAbandoned ClassificationCorrect
expect_violation ${m} ${m}UnsafeSeqPerAttempt KeysUnique
expect_violation ${m} ${m}UnsafeIndexFirstStored IndexMatchesAdoption
expect_violation ${m} ${m}UnsafePresentationWithoutKey PresentationsClassified
expect_violation ${m} ${m}UnsafeOverflowCommitsPresentation NoPresentationFromOverflow
expect_violation ${m} ${m}UnsafeInterceptedRecordEvent ClassificationCorrect

m=RequestRecord
expect_pass ${m} ${m} 9300
expect_violation ${m} ${m}UnsafeViewrefPlusAdded RoundTrip
expect_violation ${m} ${m}UnsafeSpanIgnoresOrder RoundTrip
expect_violation ${m} ${m}UnsafeHashFirstOwn RoundTrip
expect_violation ${m} ${m}UnsafeDecodeCurrentView RoundTrip
expect_violation ${m} ${m}UnsafeMatchCanonicalHash RoundTrip

m=ContinuationCarry
expect_pass ${m} ${m} 1360000
expect_violation ${m} ${m}WitnessMinimalNeeded WitnessMinimalNeeded
expect_violation ${m} ${m}WitnessCarriedTwice WitnessCarriedTwice
expect_violation ${m} ${m}UnsafeCopyOriginal CopyIsRendered
expect_violation ${m} ${m}UnsafeCarryReasoning NoReasoningCarried
expect_violation ${m} ${m}UnsafeDropOnEvict EveryPartAccounted
expect_violation ${m} ${m}UnsafeChainRestarts EveryPartAccounted
expect_violation ${m} ${m}UnsafeCopyTruncatesFull RenderableFromCopy
expect_violation ${m} ${m}UnsafeRecarryRestoresFull CarryBounded
expect_violation ${m} ${m}UnsafeInheritWatermark PlaceholdersStick
expect_violation ${m} ${m}UnsafeNoForcedClear CarryBounded

m=RecallDiscovery
expect_pass ${m} ${m} 23700000
expect_pass ${m} ${m}Liveness 622000
expect_liveness_violation ${m} ${m}LivenessUnsafeRefillRoundsDown
expect_violation ${m} ${m}WitnessReadAfterRefill WitnessReadAfterRefill
expect_violation ${m} ${m}UnsafeAssistantTextOmitted Discoverable
expect_violation ${m} ${m}UnsafeCollapseWithoutHandle Discoverable
expect_violation ${m} ${m}UnsafePairByPrevious Discoverable
expect_violation ${m} ${m}UnsafeResolveAnyRecord HandlesConfined
expect_violation ${m} ${m}UnsafeResolveCurrentOnly Discoverable
expect_action_violation ${m} ${m}UnsafeSharedAllowance ListingKeepsReads
expect_violation ${m} ${m}UnsafeCiteTextFromSummary UserTextFromRecord
expect_violation ${m} ${m}UnsafeRecallReadsRecord RecallNeverExposesServerData

m=OverflowGuard
expect_pass ${m} ${m} 1300
expect_pass ${m} ${m}Liveness 1300
expect_liveness_violation ${m} ${m}LivenessUnsafeGuardNeverOff
expect_violation ${m} ${m}WitnessGuardThenProvider WitnessGuardThenProvider
expect_violation ${m} ${m}UnsafeGuardFailsStep FailsOnlyOnProvider
expect_violation ${m} ${m}UnsafeCompareLastRejection FailsOnlyOnProvider
expect_violation ${m} ${m}UnsafeCompareViewOnly RetriesShrink
expect_violation ${m} ${m}UnsafeGuardSpendsOverflowBudget BudgetFailureHonest

m=StepPayload
expect_pass ${m} ${m} 10400
expect_violation ${m} ${m}WitnessCrashRetry WitnessCrashRetry
expect_violation ${m} ${m}UnsafeSeedInPending InputBounded
expect_violation ${m} ${m}UnsafePendingNotFlushed InputBounded
expect_violation ${m} ${m}UnsafeAccumulatedHistory InputBounded
expect_violation ${m} ${m}UnsafeHeadBeforeStore Reconstructs
expect_violation ${m} ${m}UnsafeSegmentPrevWrong Reconstructs
