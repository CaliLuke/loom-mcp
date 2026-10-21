package engine

import "github.com/CaliLuke/loom/execution"

type (
	// ActivityRegistrar uses the shared Loom execution contract.
	ActivityRegistrar = execution.ActivityRegistrar

	// ActivityExecutor uses the shared Loom execution contract.
	ActivityExecutor = execution.ActivityExecutor

	// ActivityDefinition uses the shared Loom execution contract.
	ActivityDefinition = execution.ActivityDefinition

	// ActivityCall uses the shared Loom execution contract.
	ActivityCall = execution.ActivityCall

	// ActivityFuture uses the shared Loom execution contract.
	ActivityFuture = execution.ActivityFuture

	// Activity uses the shared Loom execution contract.
	Activity[I, O any] = execution.Activity[I, O]

	// Future uses the shared Loom execution contract.
	Future[T any] = execution.Future[T]

	// ActivityOptions uses the shared Loom execution contract.
	ActivityOptions = execution.ActivityOptions

	// RetryPolicy uses the shared Loom execution contract.
	RetryPolicy = execution.RetryPolicy
)
