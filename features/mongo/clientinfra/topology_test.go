package clientinfra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// standaloneHello mirrors the reply of a standalone mongo:7 mongod.
func standaloneHello() bson.M {
	return bson.M{
		"isWritablePrimary": true,
		"maxWireVersion":    int32(21),
		"minWireVersion":    int32(0),
		"ok":                float64(1),
	}
}

// replicaSetHello mirrors the reply of a mongo:7 primary in replica set rs0.
func replicaSetHello() bson.M {
	return bson.M{
		"isWritablePrimary": true,
		"setName":           "rs0",
		"setVersion":        int32(1),
		"primary":           "127.0.0.1:27017",
		"maxWireVersion":    int32(21),
		"ok":                float64(1),
	}
}

// uninitiatedReplicaSetHello mirrors the reply of a mongo:7 started with
// --replSet before rs.initiate() has run. It reports no set name at all.
func uninitiatedReplicaSetHello() bson.M {
	return bson.M{
		"isWritablePrimary": false,
		"secondary":         false,
		"info":              "Does not have a valid replica set config",
		"isreplicaset":      true,
		"maxWireVersion":    int32(21),
		"ok":                float64(1),
	}
}

// shardedHello mirrors the reply of a mongos router.
func shardedHello() bson.M {
	return bson.M{
		"isWritablePrimary": true,
		"msg":               "isdbgrid",
		"maxWireVersion":    int32(21),
		"ok":                float64(1),
	}
}

func TestClassifyDeployment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hello bson.M
		want  deploymentTopology
	}{
		{name: "standalone", hello: standaloneHello(), want: topologyStandalone},
		{name: "replica set", hello: replicaSetHello(), want: topologyReplicaSet},
		{name: "uninitiated replica set", hello: uninitiatedReplicaSetHello(), want: topologyUninitiatedReplicaSet},
		{name: "sharded", hello: shardedHello(), want: topologySharded},
		{name: "empty reply", hello: bson.M{}, want: topologyStandalone},
		{name: "blank set name", hello: bson.M{"setName": ""}, want: topologyStandalone},
		{name: "initiated member is not reported uninitiated", hello: bson.M{"setName": "rs0", "isreplicaset": false}, want: topologyReplicaSet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, classifyDeployment(tt.hello))
		})
	}
}

func TestVerifyTransactionSupport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		hello       bson.M
		wantErr     bool
		errFragment string
	}{
		{name: "replica set", hello: replicaSetHello()},
		{name: "sharded", hello: shardedHello()},
		{
			name:        "standalone is rejected",
			hello:       standaloneHello(),
			wantErr:     true,
			errFragment: "mongo deployment is a standalone",
		},
		{
			name:        "uninitiated replica set is rejected as itself",
			hello:       uninitiatedReplicaSetHello(),
			wantErr:     true,
			errFragment: "mongo deployment is an uninitiated replica set member",
		},
		{
			name:        "replica set below 4.0 is rejected",
			hello:       bson.M{"setName": "rs0", "maxWireVersion": int32(6)},
			wantErr:     true,
			errFragment: "requires MongoDB 4.0 or later",
		},
		{
			name:        "sharded below 4.2 is rejected",
			hello:       bson.M{"msg": "isdbgrid", "maxWireVersion": int32(7)},
			wantErr:     true,
			errFragment: "requires MongoDB 4.2 or later",
		},
		{
			name:  "replica set at 4.0 is accepted",
			hello: bson.M{"setName": "rs0", "maxWireVersion": int32(7)},
		},
		{
			name:  "sharded at 4.2 is accepted",
			hello: bson.M{"msg": "isdbgrid", "maxWireVersion": int32(8)},
		},
		{
			name:  "missing wire version is not rejected on version grounds",
			hello: bson.M{"setName": "rs0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := verifyTransactionSupport(tt.hello)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errFragment)
		})
	}
}

// TestVerifyTransactionSupportErrorsCarryTheRemedy pins the operator-facing half
// of the guard: every rejection must say how to fix the deployment.
func TestVerifyTransactionSupportErrorsCarryTheRemedy(t *testing.T) {
	t.Parallel()

	for name, hello := range map[string]bson.M{
		"standalone":              standaloneHello(),
		"uninitiated replica set": uninitiatedReplicaSetHello(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := verifyTransactionSupport(hello)
			require.Error(t, err)
			// The remedy is a command the operator can paste. It names the
			// member explicitly, because a bare rs.initiate() records the
			// machine hostname and the driver then substitutes it for the
			// connection string's seed.
			assert.Contains(t, err.Error(), initiateCommand)
		})
	}
}

// stubCommandRunner replays a scripted reply per command name so the handshake
// can be driven without a live server.
type stubCommandRunner struct {
	replies   map[string]bson.M
	failures  map[string]error
	calls     []string
	deadlines []time.Time
}

func (s *stubCommandRunner) RunCommand(ctx context.Context, runCommand any, _ ...options.Lister[options.RunCmdOptions]) *mongodriver.SingleResult {
	command := runCommand.(bson.D)[0].Key
	s.calls = append(s.calls, command)
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Time{}
	}
	s.deadlines = append(s.deadlines, deadline)
	if err, ok := s.failures[command]; ok {
		return mongodriver.NewSingleResultFromDocument(bson.M{}, err, nil)
	}
	return mongodriver.NewSingleResultFromDocument(s.replies[command], nil, nil)
}

func TestRunHello(t *testing.T) {
	t.Parallel()

	commandNotFound := mongodriver.CommandError{
		Code:    commandNotFoundCode,
		Name:    commandNotFoundName,
		Message: "no such command: 'hello'",
	}
	// A wrong password reaches the caller as a CommandError as well, which is why
	// matching the type alone retried failures the fallback cannot repair.
	authFailure := mongodriver.CommandError{
		Code:    18,
		Name:    "AuthenticationFailed",
		Message: `connection() error occurred during connection handshake: auth error: unable to authenticate using mechanism "SCRAM-SHA-256": (AuthenticationFailed) Authentication failed.`,
	}
	networkFailure := errors.New("server selection error: context deadline exceeded")

	tests := []struct {
		name        string
		runner      *stubCommandRunner
		wantCalls   []string
		wantSetName string
		wantErr     string
		wantAlsoErr string
	}{
		{
			name:        "hello answers",
			runner:      &stubCommandRunner{replies: map[string]bson.M{helloCommand: replicaSetHello()}},
			wantCalls:   []string{helloCommand},
			wantSetName: "rs0",
		},
		{
			name: "server rejects hello so isMaster answers",
			runner: &stubCommandRunner{
				replies:  map[string]bson.M{legacyHelloCommand: replicaSetHello()},
				failures: map[string]error{helloCommand: commandNotFound},
			},
			wantCalls:   []string{helloCommand, legacyHelloCommand},
			wantSetName: "rs0",
		},
		{
			name: "an authentication failure is not retried",
			runner: &stubCommandRunner{
				failures: map[string]error{helloCommand: authFailure, legacyHelloCommand: authFailure},
			},
			wantCalls: []string{helloCommand},
			wantErr:   "Authentication failed",
		},
		{
			name: "a rejection for another reason is not retried",
			runner: &stubCommandRunner{
				replies:  map[string]bson.M{legacyHelloCommand: replicaSetHello()},
				failures: map[string]error{helloCommand: mongodriver.CommandError{Code: 13, Name: "Unauthorized", Message: "not authorized on admin"}},
			},
			wantCalls: []string{helloCommand},
			wantErr:   "not authorized on admin",
		},
		{
			name: "a server selection failure is not retried",
			runner: &stubCommandRunner{
				failures: map[string]error{helloCommand: networkFailure, legacyHelloCommand: networkFailure},
			},
			wantCalls: []string{helloCommand},
			wantErr:   "server selection error",
		},
		{
			name: "both commands failing reports both",
			runner: &stubCommandRunner{
				failures: map[string]error{helloCommand: commandNotFound, legacyHelloCommand: mongodriver.CommandError{Code: commandNotFoundCode, Name: commandNotFoundName, Message: "no such command: 'isMaster'"}},
			},
			wantCalls: []string{helloCommand, legacyHelloCommand},
			wantErr:   "no such command: 'isMaster'",
			// Both failures must survive: the hello error is usually the one
			// that explains the deployment.
			wantAlsoErr: "no such command: 'hello'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hello, err := runHello(context.Background(), tt.runner)
			assert.Equal(t, tt.wantCalls, tt.runner.calls)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantSetName, hello["setName"])
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			if tt.wantAlsoErr != "" {
				assert.Contains(t, err.Error(), tt.wantAlsoErr)
			}
		})
	}
}

// TestRunHelloReportsAnAuthFailureOnlyOnce pins the doubling regression. The
// driver reports a wrong password as a CommandError, so gating the fallback on
// the type alone retried it and printed the identical sentence twice.
func TestRunHelloReportsAnAuthFailureOnlyOnce(t *testing.T) {
	t.Parallel()

	failure := mongodriver.CommandError{
		Code:    18,
		Name:    "AuthenticationFailed",
		Message: "connection() error occurred during connection handshake: auth error: (AuthenticationFailed) Authentication failed.",
	}
	runner := &stubCommandRunner{failures: map[string]error{helloCommand: failure, legacyHelloCommand: failure}}

	_, err := runHello(context.Background(), runner)

	require.Error(t, err)
	assert.Equal(t, []string{helloCommand}, runner.calls)
	assert.Equal(t, 1, strings.Count(err.Error(), "Authentication failed"))
}

func TestCommandUnsupported(t *testing.T) {
	t.Parallel()

	notFound := mongodriver.CommandError{Code: commandNotFoundCode, Name: commandNotFoundName, Message: "no such command"}
	assert.True(t, commandUnsupported(notFound))
	assert.True(t, commandUnsupported(fmt.Errorf("wrapped: %w", notFound)))
	// Reported by code alone, as a Mongo-compatible service may omit the name.
	assert.True(t, commandUnsupported(mongodriver.CommandError{Code: commandNotFoundCode}))
	assert.True(t, commandUnsupported(mongodriver.CommandError{Name: commandNotFoundName}))

	// Every other CommandError describes a failure the fallback cannot repair.
	assert.False(t, commandUnsupported(mongodriver.CommandError{Code: 18, Name: "AuthenticationFailed"}))
	assert.False(t, commandUnsupported(mongodriver.CommandError{Code: 13, Name: "Unauthorized"}))
	assert.False(t, commandUnsupported(errors.New("connection refused")))
	assert.False(t, commandUnsupported(context.DeadlineExceeded))
}

func TestRequireTransactionSupportRejectsNilClient(t *testing.T) {
	t.Parallel()

	require.EqualError(t, RequireTransactionSupport(0, nil), "mongo client is required")
}

// TestRequireTransactionSupportBoundsTheProbe pins the budget the probe installs.
// A non-positive timeout must become the package default: leaving the context
// without a deadline let hello and then isMaster each wait out the client's full
// server-selection timeout, and an immediately expired one skipped the check.
func TestRequireTransactionSupportBoundsTheProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "zero falls back to the default", timeout: 0, want: defaultTopologyProbeTimeout},
		{name: "negative falls back to the default", timeout: -time.Second, want: defaultTopologyProbeTimeout},
		{name: "a positive timeout is honoured", timeout: 2 * time.Second, want: 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := &stubCommandRunner{replies: map[string]bson.M{helloCommand: replicaSetHello()}}
			start := time.Now()

			require.NoError(t, requireTransactionSupport(tt.timeout, runner))

			require.Len(t, runner.deadlines, 1)
			budget := runner.deadlines[0].Sub(start)
			// start is taken before the probe installs its deadline, so any
			// scheduling delay only ever makes the measured budget longer. The
			// upper bound is loose enough to survive that and still far below
			// the next case's value.
			assert.Greater(t, budget, tt.want-100*time.Millisecond, "probe deadline is earlier than the budget")
			assert.Less(t, budget, tt.want+3*time.Second, "probe deadline is later than the budget")
		})
	}
}

// TestRequireTransactionSupportNamesTheGhostRemedy covers the failure the
// classification never sees: a member reporting no set configuration is never
// selected through a replicaSet connection string, so the probe only times out.
func TestRequireTransactionSupportNamesTheGhostRemedy(t *testing.T) {
	t.Parallel()

	ghost := errors.New(`server selection error: context deadline exceeded, current topology: ` +
		`{ Type: ReplicaSetNoPrimary, Servers: [{ Addr: 127.0.0.1:27017, Type: RSGhost }] }`)
	runner := &stubCommandRunner{failures: map[string]error{helloCommand: ghost}}

	err := requireTransactionSupport(time.Second, runner)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rs.initiate")
	assert.Contains(t, err.Error(), "re-add this member")
}

// TestRequireTransactionSupportNamesTheSetNameMismatch covers the shape the
// production guide's replicaSet= URI produces against a non-member: the driver
// empties the topology, so no server is selected and the classification never
// runs.
func TestRequireTransactionSupportNamesTheSetNameMismatch(t *testing.T) {
	t.Parallel()

	mismatch := errors.New(`server selection error: context deadline exceeded, current topology: ` +
		`{ Type: ReplicaSetNoPrimary, Servers: [] }`)
	runner := &stubCommandRunner{failures: map[string]error{helloCommand: mismatch}}

	err := requireTransactionSupport(time.Second, runner)

	require.Error(t, err)
	// The set name is questioned first: a healthy set reached with the wrong name
	// empties the topology exactly as a standalone does.
	assert.Contains(t, err.Error(), "check the replicaSet parameter")
	assert.NotContains(t, err.Error(), "re-add this member")
}

// TestRequireTransactionSupportWithholdsTheRemedyFromUnrelatedFailures guards the
// other side: a refused connection or a bad hostname must not be handed
// replica-set advice.
func TestRequireTransactionSupportWithholdsTheRemedyFromUnrelatedFailures(t *testing.T) {
	t.Parallel()

	for name, failure := range map[string]error{
		"connection refused": errors.New(`server selection error: context deadline exceeded, current topology: ` +
			`{ Type: Unknown, Servers: [{ Addr: 127.0.0.1:1, Type: Unknown, Last error: dial tcp 127.0.0.1:1: connect: connection refused }] }`),
		"unknown host": errors.New(`server selection error: context deadline exceeded, current topology: ` +
			`{ Type: Unknown, Servers: [{ Addr: nope.invalid:27017, Type: Unknown, Last error: dial tcp: lookup nope.invalid: no such host }] }`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runner := &stubCommandRunner{failures: map[string]error{helloCommand: failure}}

			err := requireTransactionSupport(time.Second, runner)

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "rs.initiate")
		})
	}
}

func TestHelloInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "int32", value: int32(21), want: 21, ok: true},
		{name: "int64", value: int64(21), want: 21, ok: true},
		{name: "float64", value: float64(21), want: 21, ok: true},
		{name: "string", value: "21"},
		{name: "nil", value: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := helloInt(tt.value)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
