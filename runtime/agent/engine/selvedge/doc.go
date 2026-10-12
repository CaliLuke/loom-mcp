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
// This initial adapter uses registration StartToCloseTimeout (default two minutes)
// and disables activity retries. Per-call activity options, activity queues,
// schedule-to-start timeouts, heartbeat enforcement, heartbeat recorders/details,
// durable retry policies and replay harness methods are not exposed. Activity
// contexts report the current claim attempt and a zero heartbeat timeout.
// Configure backend observability through Selvedge App options.
package selvedge
