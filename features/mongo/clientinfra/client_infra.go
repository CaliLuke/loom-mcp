package clientinfra

import (
	"context"
	"errors"
	"time"

	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// ValidateMongoOptions checks the shared required Mongo constructor inputs.
func ValidateMongoOptions(client *mongodriver.Client, database string) error {
	if client == nil {
		return errors.New("mongo client is required")
	}
	if database == "" {
		return errors.New("database name is required")
	}
	return nil
}

// ResolveTimeout applies the package default when the configured timeout is not positive.
func ResolveTimeout(timeout time.Duration, defaultTimeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultTimeout
	}
	return timeout
}

// EnsureIndexes runs index initialization with a timeout-bounded background context.
func EnsureIndexes(timeout time.Duration, ensure func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return ensure(ctx)
}

// Ping performs the common primary-read-preference health check.
func Ping(ctx context.Context, client *mongodriver.Client, normalizeNil bool) error {
	if normalizeNil && ctx == nil {
		ctx = context.Background()
	}
	return client.Ping(ctx, readpref.Primary())
}

// WithTimeout wraps the caller context with the configured timeout.
func WithTimeout(ctx context.Context, timeout time.Duration, normalizeNil bool) (context.Context, context.CancelFunc) {
	if normalizeNil && ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
