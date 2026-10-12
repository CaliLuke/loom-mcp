// Package selvedge implements the engine contract on a Selvedge App backed by
// SQLite or PostgreSQL. NewWorker owns an unstarted App: register handlers, seal
// registration to start workers, and Close to shut them down. An optional started
// Client remains caller-owned and keeps existing handles usable across worker
// replacement. NewClient accepts only that caller-owned client.
//
// Workflow and run IDs are the same durable instance ID. Closed IDs cannot be
// reused. Workflow queues, start/child run timeouts and retry policies, memo,
// search attributes and termination are unsupported. Status reports running,
// completed, failed or canceled, never pending, paused or timed out. Activities
// must be registered locally before workflow code invokes them.
//
// Queries replay local workflow definitions, so root-run and tool-await queries
// require worker mode. ProvideToolResults must run in a worker process. Workflow
// Go contexts carry values only; use WithCancel for workflow scope cancellation.
//
// Registration activity options are recorded with nonzero per-call overrides.
// StartToCloseTimeout defaults to two minutes. Retry policies default to unlimited
// attempts, a one-second initial interval, coefficient two, and no jitter.
// ActivityDefaults fills omitted queue-wait and heartbeat limits by class.
// Activity contexts expose the claim attempt, resolved heartbeat timeout and a
// progress recorder. Heartbeat details are not retained. Claim attempts can exceed
// the failure retry limit after graceful worker replacement.
//
// ExportHistory reads portable histories from an initialized client. ReplayHistory
// needs registered workflows only: no sealing, workers, activities or hook effects.
// Configure backend observability through Selvedge App options.
package selvedge
