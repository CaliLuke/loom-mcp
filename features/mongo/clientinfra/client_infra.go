// Package clientinfra provides shared Mongo client setup helpers.
package clientinfra

import (
	"context"
	"errors"
	"reflect"
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

// ResolveCollectionName applies the package default when the configured collection is empty.
func ResolveCollectionName(collection string, defaultCollection string) string {
	if collection == "" {
		return defaultCollection
	}
	return collection
}

// ValidateCollections checks that test or production collection adapters are present.
func ValidateCollections(message string, collections ...any) error {
	for _, collection := range collections {
		if isNil(collection) {
			return errors.New(message)
		}
	}
	return nil
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

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	kind := rv.Kind()
	if kind == reflect.Chan ||
		kind == reflect.Func ||
		kind == reflect.Interface ||
		kind == reflect.Map ||
		kind == reflect.Pointer ||
		kind == reflect.Slice {
		return rv.IsNil()
	}
	return false
}
