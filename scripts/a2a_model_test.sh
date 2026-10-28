#!/usr/bin/env bash
set -euo pipefail

jar="${1:-}"
if [[ ! -f "${jar}" ]]; then
  echo 'usage: make a2a-model-test TLA2TOOLS_JAR=/absolute/path/to/tla2tools.jar' >&2
  exit 1
fi
jar="$(cd "$(dirname "${jar}")" && pwd)/$(basename "${jar}")"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
for module in A2AConcurrency A2AClarification A2AProtocol; do
  cp "${root}/docs/formal/${module}.tla" "${root}/docs/formal/${module}"*.cfg "${work}/"
done
cd "${work}"

run() {
  local config="$1" result=0
  java -Xmx1g -XX:+UseParallelGC -cp "${jar}" tlc2.TLC -workers 4 \
    -metadir "${work}/states-${config}" -config "${config}.cfg" \
    "${module}.tla" >"${config}.log" 2>&1 || result=$?
  echo "${result}"
}
fail() {
  cat "$2.log" >&2
  echo "$2: $1" >&2
  exit 1
}
expect_pass() {
  local config="${module}$1" floor="$2" result states
  result="$(run "${config}")"
  [[ "${result}" == 0 ]] || fail 'expected no violation' "${config}"
  states="$(grep 'distinct states found' "${config}.log" | tail -1 | sed -E 's/.* ([0-9]+) distinct states found.*/\1/')"
  [[ "${states}" -ge "${floor}" ]] || fail "explored ${states} states, fewer than ${floor}" "${config}"
  echo "${config}: passed (${states} distinct states)"
  grep "states generated" "${config}.log" | tail -1
  grep "depth of the complete state graph" "${config}.log"
}
expect_violation() {
  local config="${module}$1" invariant="$2" result
  result="$(run "${config}")"
  if [[ "${result}" != 12 ]] || ! grep -q "Invariant ${invariant} is violated" "${config}.log"; then
    fail "expected a ${invariant} counterexample" "${config}"
  fi
  echo "${config}: expected ${invariant} counterexample"
}

module=A2AConcurrency
expect_pass ZeroHorizon 31000
expect_pass CertifiedHorizon 717000
expect_violation UnsafeZeroHorizonRetry ZeroHorizonSingleClaim
expect_violation UnsafeCrossStoreSuppression NoFalseCanceled
expect_violation UnsafeTimeoutNotFound NoFalseCanceled
expect_violation UnsafeResetSuppression SuppressionIrreversible
expect_violation UnsafeCloseLosesReceipt NoLostReceipt
expect_violation UnsafeAppendAfterClose InputCloseFenced
expect_violation UnsafeDuplicateReceipt ReceiptUnique
expect_violation UnsafeDuplicateExecution AtMostOneExecution
expect_violation UnsafeLateAfterRetention AtMostOneExecution
expect_violation UnsafeStaleProjection TerminalPublicImmutable
expect_violation UnsafeStaleAdmission AdmissionFenced
expect_violation UnsafeRejectClosesRetry NoFalseFailed
expect_violation UnsafeResetRejection RejectionSealed
expect_violation UnsafePositiveHorizonSeal RejectionSealed
for witness in DelayedAcceptance SuppressionRecovery ReceiptRecovery CompletionWins LostBeforeIO RetryDedup StaleClaim StaleClaimLostBeforeIO PositiveHorizonLateFirstAcceptance StartRejected; do
  expect_violation "Witness${witness}" "Witness${witness}"
done

module=A2AClarification
expect_pass "" 1053000
expect_pass CrashRecovery 47000
expect_violation UnsafeCompetingAcceptance OneAcceptedOwner
expect_violation UnsafePlannerLabelOnly AcceptedMatchesOccurrence
expect_violation UnsafeStaleAsConflict DecisionSound
expect_violation UnsafeRetryRedecides ReceiptImmutable
expect_violation UnsafeChangedBodyRebinds BindingImmutable
expect_violation UnsafeReleaseOther ReservationAccounted
expect_violation UnsafeRetainReservation ProjectionComplete
expect_violation UnsafeOmitPublicAnswer ProjectionComplete
expect_violation UnsafeRejectionErasesOwner OwnershipPreserved
expect_violation UnsafeProjectDecision PublicFromReceipt
expect_violation UnsafeDuplicatePublicAnswer PublicAnswerOnce
expect_violation UnsafeResumeDecision ResumeFromReceipt
expect_violation UnsafeResumeRejection ResumeFromReceipt
expect_violation UnsafeDuplicateResume ResumeOnce
expect_violation UnsafeAppendAfterClose InputCloseFenced
for witness in TwoOccurrences HostConflictCleanup StaleRetry ConflictRetry CleanupAfterNewReservation ChangedBodyRetry DecisionCrashRecovery ReceiptCrashRecovery CloseWins AnswerPending; do
  expect_violation "Witness${witness}" "Witness${witness}"
done

module=A2AProtocol
expect_pass "" 388000
expect_pass LeaseReplacement 3713000
expect_pass Combined 12950000
expect_violation UnsafeReceiptReopensTerminal TerminalAbsorbing
expect_violation UnsafeReceiptHidesAwait NewerAwaitPreserved
expect_violation UnsafeRebaseTerminal TerminalAbsorbing
expect_violation UnsafeRebaseAwait NewerAwaitPreserved
expect_violation UnsafeWorkBeforeStart WorkHasExecution
expect_violation UnsafeStaleLeaseCommit CommitAuthorized
for witness in LateAfterCompleted LateAfterCanceled ReplayAfterNewAwait StaleCandidateRebuilt DelayedStartThenAnswer FreshReceiptWorking; do
  expect_violation "Witness${witness}" "Witness${witness}"
done
