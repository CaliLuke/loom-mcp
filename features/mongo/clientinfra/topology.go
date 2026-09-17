package clientinfra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// commandRunner is the slice of *mongodriver.Database the deployment probe uses.
// Taking the interface keeps the hello and isMaster handshake testable without a
// live server.
type commandRunner interface {
	RunCommand(ctx context.Context, runCommand any, opts ...options.Lister[options.RunCmdOptions]) *mongodriver.SingleResult
}

// deploymentTopology names the Mongo deployment shapes that decide whether
// multi-document transactions are available.
type deploymentTopology string

const (
	// topologyStandalone is a single mongod that is not a replica set member.
	// It supports neither multi-document transactions nor session-scoped
	// retryable writes.
	topologyStandalone deploymentTopology = "standalone"
	// topologyUninitiatedReplicaSet is a mongod started with --replSet whose
	// replica set configuration has never been initiated. It reports no set
	// name yet, so it is not a working replica set member.
	topologyUninitiatedReplicaSet deploymentTopology = "uninitiated replica set member"
	// topologyReplicaSet is a mongod that belongs to an initiated replica set.
	topologyReplicaSet deploymentTopology = "replica set"
	// topologySharded is a mongos router fronting a sharded cluster.
	topologySharded deploymentTopology = "sharded cluster"
)

const (
	// Multi-document transactions arrived with replica sets in MongoDB 4.0
	// (max wire version 7) and with sharded clusters in MongoDB 4.2 (max wire
	// version 8).
	minReplicaSetTransactionWireVersion = 7
	minShardedTransactionWireVersion    = 8

	// shardedRouterHelloMessage is the msg value a mongos reports.
	shardedRouterHelloMessage = "isdbgrid"

	adminDatabase = "admin"

	// helloCommand is the modern handshake command; legacyHelloCommand is the
	// predecessor a server that rejects hello may still answer.
	helloCommand       = "hello"
	legacyHelloCommand = "isMaster"

	// commandNotFoundCode and commandNotFoundName are how a server reports that
	// it does not implement a command. hello was backported to 4.0.21, 4.2.10
	// and 4.4.2, so only older patch releases and Mongo-compatible services
	// still need the isMaster fallback.
	commandNotFoundCode = 59
	commandNotFoundName = "CommandNotFound"

	// defaultTopologyProbeTimeout bounds the probe when the caller passes a
	// non-positive timeout. Without it the probe would wait out the client's
	// server-selection timeout once per command.
	defaultTopologyProbeTimeout = 10 * time.Second

	// initiateCommand names the member explicitly. A bare rs.initiate() records
	// the machine hostname, which the driver then substitutes for the seed in the
	// connection string.
	initiateCommand = `rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:27017"}]})`
	// standaloneRemedy names the fix for a mongod that is not a replica set member.
	standaloneRemedy = "run " + initiateCommand
	// ghostRemedy names the fixes for a member reporting no set configuration.
	// SDAM gives that shape to a member that was never initiated and to one
	// removed from an existing set, so it must not prescribe rs.initiate() alone.
	ghostRemedy = "run " + initiateCommand + ", or re-add this member to its configuration"
	// mismatchRemedy names the fixes for a connection string whose replica set no
	// reachable server belongs to. A wrong set name empties the topology exactly
	// as a standalone does, so the name has to be questioned before the
	// deployment is.
	mismatchRemedy = "check the replicaSet parameter against the set the deployment reports, drop it if this deployment is a standalone, or run " + initiateCommand + " if the set was never created"
	// rsGhostServerType is how the driver's topology description labels a member
	// that reports no set configuration.
	rsGhostServerType = "RSGhost"
	// emptyServerList is how that description renders a topology the driver
	// emptied because no server matched the configured set name.
	emptyServerList = "Servers: []"
)

// RequireTransactionSupport asks the server how it is deployed and rejects
// deployments that cannot run multi-document transactions. Constructors call it
// so an unsupported deployment fails at startup with an actionable error rather
// than at the first transactional write.
//
// A non-positive timeout falls back to defaultTopologyProbeTimeout.
func RequireTransactionSupport(timeout time.Duration, client *mongodriver.Client) error {
	if client == nil {
		return errors.New("mongo client is required")
	}
	return requireTransactionSupport(timeout, client.Database(adminDatabase))
}

func requireTransactionSupport(timeout time.Duration, admin commandRunner) error {
	ctx, cancel := WithTimeout(context.Background(), ResolveTimeout(timeout, defaultTopologyProbeTimeout), true)
	defer cancel()

	hello, err := runHello(ctx, admin)
	if err != nil {
		if hint := probeFailureHint(err); hint != "" {
			return fmt.Errorf("inspect mongo deployment: %w; %s", err, hint)
		}
		return fmt.Errorf("inspect mongo deployment: %w", err)
	}
	return verifyTransactionSupport(hello)
}

// probeFailureHint names the deployment problem behind a probe failure that the
// classification never gets to see, and returns an empty string for failures
// that already explain themselves.
//
// A server-selection error carries the topology the driver gave up on. Two of
// its shapes mean the connection string and the deployment disagree, and neither
// ever reaches classifyDeployment because no server is selected:
//
//   - a member reporting no set configuration is labelled RSGhost;
//   - a server whose set name does not match the connection string is dropped
//     from the topology altogether, leaving no servers at all. A standalone
//     reached through a replicaSet= URI lands here.
//
// Keying on those shapes rather than on the timeout keeps an unreachable host or
// a mistyped name from being handed replica-set advice.
func probeFailureHint(err error) string {
	dump := err.Error()
	switch {
	case strings.Contains(dump, rsGhostServerType):
		return ghostRemedy
	case strings.Contains(dump, emptyServerList):
		return mismatchRemedy
	default:
		return ""
	}
}

// classifyDeployment reports the deployment shape described by a hello (or
// isMaster) command reply. A reply whose msg is isdbgrid comes from a mongos, a
// reply carrying setName comes from an initiated replica set member, a reply
// flagged isreplicaset comes from a --replSet mongod awaiting rs.initiate(), and
// anything else is a standalone mongod.
func classifyDeployment(hello bson.M) deploymentTopology {
	if msg, ok := hello["msg"].(string); ok && msg == shardedRouterHelloMessage {
		return topologySharded
	}
	if setName, ok := hello["setName"].(string); ok && setName != "" {
		return topologyReplicaSet
	}
	if uninitiated, ok := hello["isreplicaset"].(bool); ok && uninitiated {
		return topologyUninitiatedReplicaSet
	}
	return topologyStandalone
}

// verifyTransactionSupport returns nil when the deployment described by a hello
// reply can run multi-document transactions, and an error naming the remedy when
// it cannot. The wire version is only enforced when the reply reports one, so a
// reply missing the field is never rejected on version grounds alone.
func verifyTransactionSupport(hello bson.M) error {
	topology := classifyDeployment(hello)
	switch topology {
	case topologyStandalone:
		return fmt.Errorf("mongo deployment is a %s: this store requires a %s or a %s; %s",
			topologyStandalone, topologyReplicaSet, topologySharded, standaloneRemedy)
	case topologyUninitiatedReplicaSet:
		return fmt.Errorf("mongo deployment is an %s: %s",
			topologyUninitiatedReplicaSet, ghostRemedy)
	case topologyReplicaSet, topologySharded:
	}

	minimum := minReplicaSetTransactionWireVersion
	release := "4.0"
	if topology == topologySharded {
		minimum = minShardedTransactionWireVersion
		release = "4.2"
	}
	wireVersion, ok := helloInt(hello["maxWireVersion"])
	if ok && wireVersion < minimum {
		return fmt.Errorf("mongo %s reports max wire version %d: this store requires MongoDB %s or later on this topology",
			topology, wireVersion, release)
	}
	return nil
}

// runHello issues the hello handshake command, falling back to its isMaster
// predecessor when the server rejects hello itself. Both replies carry the
// setName, msg, isreplicaset, and maxWireVersion fields the classification needs.
func runHello(ctx context.Context, admin commandRunner) (bson.M, error) {
	var hello bson.M
	helloErr := admin.RunCommand(ctx, bson.D{{Key: helloCommand, Value: 1}}).Decode(&hello)
	if helloErr == nil {
		return hello, nil
	}
	if !commandUnsupported(helloErr) {
		return nil, helloErr
	}
	var legacy bson.M
	if err := admin.RunCommand(ctx, bson.D{{Key: legacyHelloCommand, Value: 1}}).Decode(&legacy); err != nil {
		return nil, errors.Join(helloErr, err)
	}
	return legacy, nil
}

// commandUnsupported reports whether the server answered and said it does not
// know the command, which is the only failure the isMaster fallback can repair.
//
// The type alone cannot decide this. The driver funnels every error carrying a
// driver.Error into mongo.CommandError, so an authentication failure arrives as
// CommandError{Code: 18} and a handshake failure as one wrapping a connection
// error. Retrying those repeats the same failure and reports it twice, so the
// check keys on the CommandNotFound reply itself.
func commandUnsupported(err error) bool {
	var commandErr mongodriver.CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	return commandErr.Code == commandNotFoundCode || commandErr.Name == commandNotFoundName
}

// helloInt reads a numeric hello field. BSON decodes an integer as int32 or
// int64 and a double as float64, so those are the shapes a reply can carry.
func helloInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
