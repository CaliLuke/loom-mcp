// Package registry provides the internal tool registry service implementation.
//
// This file owns distributed provider liveness. Catalog membership is the
// authoritative source of which toolsets participate in health tracking, and
// shared health records are scoped to the current registration epoch so a
// same-name re-registration cannot inherit stale health from a prior provider.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/CaliLuke/loom/pulse/pool"
	"github.com/CaliLuke/loom/pulse/rmap"
	"uuid"
)

type (
	// HealthTracker tracks provider health status for toolsets.
	// It manages ping/pong health checks to detect when providers become unavailable,
	// enabling fast failure instead of timeouts when all providers are unhealthy.
	//
	// The tracker uses two Pulse replicated maps:
	// 1. A catalog map that stores registered toolsets (for cross-node coordination)
	// 2. A health map that stores registration-scoped pong records for each toolset
	//
	// All nodes subscribe to the catalog map. When a toolset is registered/unregistered,
	// all nodes see the change and start/stop their distributed ticker participation.
	// The distributed ticker ensures only one node sends pings at a time, with automatic
	// failover if that node crashes.
	HealthTracker interface {
		// Health returns the current health state for a toolset.
		//
		// Contract:
		//   - Health is derived from the last recorded Pong timestamp and the
		//     configured staleness threshold.
		//   - If the toolset has never ponged (or no entry exists), Health reports
		//     Healthy=false with LastPong unset.
		Health(toolset string) (ToolsetHealth, error)

		// RecordPong records a pong response for a toolset when the pong matches
		// the current catalog registration epoch.
		RecordPong(ctx context.Context, toolset string, pingID string) error

		// IsHealthy returns whether a toolset has healthy providers.
		// A toolset is healthy if a pong was received within the staleness threshold.
		IsHealthy(toolset string) bool

		// StartPingLoop ensures this node participates in health tracking for a
		// catalog-registered toolset. Cross-node participation is derived from the
		// shared catalog, not from a second membership index.
		StartPingLoop(ctx context.Context, toolset string) error

		// StopPingLoop stops local health tracking participation for an
		// unregistered toolset and clears its shared health state. Other nodes stop
		// via catalog change propagation.
		StopPingLoop(ctx context.Context, toolset string)

		// Close stops all ping loops and releases resources.
		Close() error
	}

	// ToolsetHealth reports derived provider health for a toolset.
	ToolsetHealth struct {
		// Healthy reports whether a provider pong was received within the configured threshold.
		Healthy bool
		// LastPong is the timestamp of the last recorded pong when available.
		LastPong time.Time
		// Age is the duration since LastPong when available.
		Age time.Duration
		// StalenessThreshold is the configured maximum acceptable pong age.
		StalenessThreshold time.Duration
	}

	// HealthTrackerOption configures optional settings for the health tracker.
	HealthTrackerOption func(*healthTrackerOptions)

	healthTrackerOptions struct {
		pingInterval        time.Duration
		missedPingThreshold int
		logger              telemetry.Logger
	}

	healthTracker struct {
		streamManager       StreamManager
		catalog             *toolsetCatalog
		healthMap           *rmap.Map // stores registration-scoped health records
		catalogMap          *rmap.Map // stores registered toolsets for cross-node coordination
		poolNode            *pool.Node
		pingInterval        time.Duration
		missedPingThreshold int
		stalenessThreshold  time.Duration
		logger              telemetry.Logger

		mu      sync.RWMutex
		tickers map[string]*pool.Ticker
		cancels map[string]context.CancelFunc
		// tickerTokens records the catalog registration token observed when each
		// local ticker was created. Reconciliation compares it against the current
		// catalog token to detect unregister→re-register cycles that remotely
		// stopped the old shared ticker entry.
		tickerTokens map[string]string
		closed       bool

		stateMu              sync.Mutex
		lastObservedHealthy  map[string]bool
		lastObservedPongNano map[string]int64

		closeOnce sync.Once
		closeCh   chan struct{}
	}

	// healthRecord is the shared liveness state for a toolset registration.
	// RegistrationToken ties the pong to the current catalog entry so same-name
	// re-registration does not inherit stale health from a previous provider.
	healthRecord struct {
		RegistrationToken string `json:"registration_token"`
		LastPongUnixNano  int64  `json:"last_pong_unix_nano"`
	}

	// tickerDetachment pairs a detached local ticker with its loop cancel func
	// so reconciliation can release both outside the tracker lock.
	tickerDetachment struct {
		cancel context.CancelFunc
		ticker *pool.Ticker
	}

	// tickerReconcilePlan captures the local ticker changes computed under the
	// tracker lock during catalog reconciliation. Rotated tickers are closed
	// locally (preserving the recreated shared entry) and started again;
	// stopped tickers are removed cluster-wide.
	tickerReconcilePlan struct {
		start              []string
		rotate             []tickerDetachment
		stop               []tickerDetachment
		forgetObservations []string
	}
)

const (
	// DefaultPingInterval is the default interval between health check pings.
	DefaultPingInterval = 10 * time.Second
	// DefaultMissedPingThreshold is the default number of consecutive missed pings
	// before marking a toolset as unhealthy.
	DefaultMissedPingThreshold = 3

	healthKeyPrefix = "registry:health:"

	healthTrackerIOTimeout = 5 * time.Second
)

// WithPingInterval sets the interval between health check pings.
func WithPingInterval(d time.Duration) HealthTrackerOption {
	return func(o *healthTrackerOptions) {
		o.pingInterval = d
	}
}

// WithMissedPingThreshold sets the number of consecutive missed pings
// before marking a toolset as unhealthy.
func WithMissedPingThreshold(n int) HealthTrackerOption {
	return func(o *healthTrackerOptions) {
		o.missedPingThreshold = n
	}
}

// WithHealthLogger sets the logger used for health-transition and ping errors.
func WithHealthLogger(l telemetry.Logger) HealthTrackerOption {
	return func(o *healthTrackerOptions) {
		o.logger = l
	}
}

// NewHealthTracker creates a new distributed health tracker.
//
// The tracker derives toolset participation from the shared catalog map, stores
// registration-scoped health in the shared health map, and uses a Pulse pool
// ticker so only one node in the cluster publishes pings at a time.
func NewHealthTracker(streamManager StreamManager, healthMap, catalogMap *rmap.Map, node *pool.Node, opts ...HealthTrackerOption) (HealthTracker, error) {
	if err := validateHealthTrackerDeps(streamManager, healthMap, catalogMap, node); err != nil {
		return nil, err
	}
	options := resolveHealthTrackerOptions(opts)
	catalogEvents := catalogMap.Subscribe()
	if catalogEvents == nil {
		return nil, fmt.Errorf("subscribe to catalog map: map is already stopped")
	}
	h := &healthTracker{
		streamManager:        streamManager,
		catalog:              newToolsetCatalogWithLogger(catalogMap, options.logger),
		healthMap:            healthMap,
		catalogMap:           catalogMap,
		poolNode:             node,
		pingInterval:         options.pingInterval,
		missedPingThreshold:  options.missedPingThreshold,
		stalenessThreshold:   healthTrackerStalenessThreshold(options),
		logger:               options.logger,
		tickers:              make(map[string]*pool.Ticker),
		cancels:              make(map[string]context.CancelFunc),
		tickerTokens:         make(map[string]string),
		lastObservedHealthy:  make(map[string]bool),
		lastObservedPongNano: make(map[string]int64),
		closeCh:              make(chan struct{}),
	}

	// Start watching for catalog changes from other nodes.
	go h.watchCatalogChanges(catalogEvents)

	// Sync with existing catalog entries.
	h.syncExistingToolsets()

	return h, nil
}

func validateHealthTrackerDeps(streamManager StreamManager, healthMap, catalogMap *rmap.Map, node *pool.Node) error {
	switch {
	case streamManager == nil:
		return fmt.Errorf("stream manager is required")
	case healthMap == nil:
		return fmt.Errorf("health map is required for distributed health tracking")
	case catalogMap == nil:
		return fmt.Errorf("catalog map is required for cross-node coordination")
	case node == nil:
		return fmt.Errorf("pool node is required for distributed tickers")
	default:
		return nil
	}
}

func resolveHealthTrackerOptions(opts []HealthTrackerOption) *healthTrackerOptions {
	options := &healthTrackerOptions{
		pingInterval:        DefaultPingInterval,
		missedPingThreshold: DefaultMissedPingThreshold,
		logger:              telemetry.NewNoopLogger(),
	}
	for _, opt := range opts {
		opt(options)
	}
	if options.logger == nil {
		options.logger = telemetry.NewNoopLogger()
	}
	return options
}

func healthTrackerStalenessThreshold(options *healthTrackerOptions) time.Duration {
	return time.Duration(options.missedPingThreshold+1) * options.pingInterval
}

// RecordPong implements HealthTracker.
func (h *healthTracker) RecordPong(ctx context.Context, toolset string, pingID string) error {
	registrationToken, err := h.registrationToken(ctx, toolset)
	if err != nil {
		if errors.Is(err, errToolsetNotFound) {
			return nil
		}
		return fmt.Errorf("resolve registration token: %w", err)
	}
	if !pingBelongsToRegistration(pingID, registrationToken) {
		return nil
	}

	key := healthKey(toolset)
	record := healthRecord{
		RegistrationToken: registrationToken,
		LastPongUnixNano:  time.Now().UnixNano(),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal health record: %w", err)
	}
	_, err = h.healthMap.SetAndWait(ctx, key, string(payload))
	if err != nil {
		return fmt.Errorf("record pong: %w", err)
	}
	return nil
}

// Health implements HealthTracker.
func (h *healthTracker) Health(toolset string) (ToolsetHealth, error) {
	registrationToken, err := h.registrationToken(context.Background(), toolset)
	if err != nil {
		if errors.Is(err, errToolsetNotFound) {
			return ToolsetHealth{
				Healthy:            false,
				StalenessThreshold: h.stalenessThreshold,
			}, nil
		}
		return ToolsetHealth{}, fmt.Errorf("resolve registration token: %w", err)
	}

	key := healthKey(toolset)
	val, ok := h.healthMap.Get(key)
	if !ok {
		return ToolsetHealth{
			Healthy:            false,
			StalenessThreshold: h.stalenessThreshold,
		}, nil
	}
	record, err := parseHealthRecord(val)
	if err != nil {
		return ToolsetHealth{}, fmt.Errorf("parse last pong timestamp for %q: %w", toolset, err)
	}
	if record.RegistrationToken != registrationToken {
		return ToolsetHealth{
			Healthy:            false,
			StalenessThreshold: h.stalenessThreshold,
		}, nil
	}
	lastPong := time.Unix(0, record.LastPongUnixNano)
	age := time.Since(lastPong)
	healthy := age <= h.stalenessThreshold
	return ToolsetHealth{
		Healthy:            healthy,
		LastPong:           lastPong,
		Age:                age,
		StalenessThreshold: h.stalenessThreshold,
	}, nil
}

// IsHealthy implements HealthTracker.
func (h *healthTracker) IsHealthy(toolset string) bool {
	hh, err := h.Health(toolset)
	if err != nil {
		return false
	}
	return hh.Healthy
}

// StartPingLoop implements HealthTracker.
func (h *healthTracker) StartPingLoop(ctx context.Context, toolset string) error {
	registrationToken, err := h.registrationToken(ctx, toolset)
	if err != nil {
		if !errors.Is(err, errToolsetNotFound) {
			return fmt.Errorf("resolve registration token: %w", err)
		}
		registrationToken = ""
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return fmt.Errorf("health tracker is closed")
	}
	// (Re)start local ticker.
	//
	// In production we observed that a node can keep a stale *pool.Ticker in-memory
	// even after the shared ticker-map entry has been deleted remotely (e.g., by a
	// different node). In that case, the local ticker stops but the health tracker
	// still thinks it is running and will not recreate it, causing pings to stop.
	//
	// We solve this by explicitly closing the local ticker instance (without
	// deleting the shared entry) and recreating it on every StartPingLoop.
	cancel, ticker := h.detachLocalTickerLocked(toolset)
	h.mu.Unlock()

	cancelAndCloseLocalTicker(cancel, ticker)
	return h.startLocalTicker(ctx, toolset, registrationToken)
}

// StopPingLoop implements HealthTracker.
func (h *healthTracker) StopPingLoop(ctx context.Context, toolset string) {
	// Clean up health state.
	healthK := healthKey(toolset)
	if _, err := h.healthMap.Delete(ctx, healthK); err != nil {
		h.logger.Error(ctx, "delete toolset health failed", "component", "tool-registry-health", "toolset", toolset, "key", healthK, "err", err)
	}

	// Stop local ticker (other nodes will do the same via watchRegistryChanges).
	h.stopLocalTicker(toolset)

	h.forgetHealthObservations([]string{toolset})
}

// Close implements HealthTracker.
func (h *healthTracker) Close() error {
	h.closeOnce.Do(func() {
		close(h.closeCh)

		h.mu.Lock()
		h.closed = true
		cancels, tickers := h.detachAllLocalTickersLocked()
		h.tickers = make(map[string]*pool.Ticker)
		h.cancels = make(map[string]context.CancelFunc)
		h.tickerTokens = make(map[string]string)
		h.mu.Unlock()

		for _, cancel := range cancels {
			cancel()
		}
		for _, ticker := range tickers {
			// Close stops the ticker locally without deleting the shared ticker-map
			// entry, preserving distributed ownership during node shutdown/restart.
			ticker.Close()
		}
	})
	return nil
}

// watchCatalogChanges reacts to catalog map changes from other nodes.
// The events channel must be obtained via catalogMap.Subscribe() before
// calling this method to avoid missing events that arrive between tracker
// construction and goroutine startup.
func (h *healthTracker) watchCatalogChanges(events <-chan rmap.EventKind) {
	defer h.catalogMap.Unsubscribe(events)

	for {
		select {
		case <-h.closeCh:
			return
		case _, ok := <-events:
			if !ok {
				// The catalog map was stopped and closed the subscriber channel.
				// Exit instead of busy-spinning on a closed channel.
				return
			}
			h.syncWithCatalog()
		}
	}
}

// syncExistingToolsets syncs with toolsets that were registered before this node started.
func (h *healthTracker) syncExistingToolsets() {
	h.syncWithCatalog()
}

// syncWithCatalog ensures local tickers match the catalog state.
func (h *healthTracker) syncWithCatalog() {
	if h.isClosed() {
		return
	}

	registered, tokens := h.catalogTickerState()
	h.streamManager.RemoveStreamsNotInCatalog(registered)
	h.reconcileCatalogTickers(registered, tokens)
}

// catalogTickerState snapshots catalog membership and the current registration
// token per toolset. Entries deleted between Keys() and the read are dropped;
// undecodable entries stay registered but carry no token so reconciliation
// neither stops nor churns their tickers.
func (h *healthTracker) catalogTickerState() (map[string]bool, map[string]string) {
	registered := make(map[string]bool)
	tokens := make(map[string]string)
	for _, key := range h.catalogMap.Keys() {
		toolset := toolsetFromCatalogKey(key)
		if toolset == "" {
			continue
		}
		token, err := h.registrationToken(context.Background(), toolset)
		if err != nil {
			if errors.Is(err, errToolsetNotFound) {
				continue
			}
			h.logger.Error(
				context.Background(),
				"resolve catalog registration token failed",
				"event", "resolve_catalog_registration_token_failed",
				"component", "tool-registry-health",
				"toolset", toolset,
				"err", err,
			)
			registered[toolset] = true
			continue
		}
		registered[toolset] = true
		tokens[toolset] = token
	}
	return registered, tokens
}

func (h *healthTracker) reconcileCatalogTickers(registered map[string]bool, tokens map[string]string) {
	plan, ok := h.planCatalogTickerChanges(registered, tokens)
	if !ok {
		return
	}

	for _, detached := range plan.rotate {
		cancelAndCloseLocalTicker(detached.cancel, detached.ticker)
	}
	for _, detached := range plan.stop {
		cancelAndStopLocalTicker(detached.cancel, detached.ticker)
	}
	h.forgetHealthObservations(plan.forgetObservations)
	for _, toolset := range plan.start {
		if err := h.startLocalTicker(context.Background(), toolset, tokens[toolset]); err != nil {
			h.logger.Error(context.Background(), "start ticker failed", "event", "start_ticker_failed", "toolset", toolset, "err", err)
		}
	}
}

// planCatalogTickerChanges computes, under the tracker lock, which local
// tickers to start, rotate, or stop for the given catalog snapshot. It returns
// false when the tracker is closed.
func (h *healthTracker) planCatalogTickerChanges(registered map[string]bool, tokens map[string]string) (tickerReconcilePlan, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return tickerReconcilePlan{}, false
	}

	var plan tickerReconcilePlan
	for toolset := range registered {
		if _, ok := h.tickers[toolset]; !ok {
			plan.start = append(plan.start, toolset)
			continue
		}
		token, ok := tokens[toolset]
		if !ok || token == h.tickerTokens[toolset] {
			continue
		}
		// The registration epoch rotated since this local ticker was created
		// (unregister→re-register). The unregister remotely deleted the shared
		// ticker entry and permanently stopped this local ticker, so detach it
		// (Close, not Stop, to preserve the recreated shared entry) and join the
		// new registration with a fresh ticker.
		cancel, ticker := h.detachLocalTickerLocked(toolset)
		plan.rotate = append(plan.rotate, tickerDetachment{cancel: cancel, ticker: ticker})
		plan.start = append(plan.start, toolset)
	}

	for toolset := range h.tickers {
		if !registered[toolset] {
			cancel, ticker := h.detachLocalTickerLocked(toolset)
			plan.stop = append(plan.stop, tickerDetachment{cancel: cancel, ticker: ticker})
			plan.forgetObservations = append(plan.forgetObservations, toolset)
		}
	}
	return plan, true
}

func (h *healthTracker) forgetHealthObservations(toolsets []string) {
	if len(toolsets) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, toolset := range toolsets {
		delete(h.lastObservedHealthy, toolset)
		delete(h.lastObservedPongNano, toolset)
	}
}

func (h *healthTracker) isClosed() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.closed
}

// startLocalTicker creates this node's distributed ticker participant and
// launches the long-lived ping loop for the toolset when the tracker is open.
// registrationToken is the catalog registration token observed by the caller;
// it is recorded so reconciliation can detect epoch rotation later.
func (h *healthTracker) startLocalTicker(ctx context.Context, toolset string, registrationToken string) error {
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return fmt.Errorf("health tracker is closed")
	}
	if _, ok := h.tickers[toolset]; ok {
		h.mu.RUnlock()
		return nil
	}
	h.mu.RUnlock()

	// Use a fresh context for the ping loop that's only cancelled when we explicitly stop.
	// This ensures the loop survives even if the caller ctx (e.g., an RPC request context)
	// is canceled as soon as the request completes.
	loopCtx, cancel := context.WithCancel(context.Background())

	// Create a distributed ticker - only one node in the pool will receive ticks.
	tickerName := fmt.Sprintf("registry:ping:%s", toolset)
	tickerCtx, stopTickerCreate := boundedHealthTrackerContext(ctx)
	ticker, err := h.poolNode.NewTicker(tickerCtx, tickerName, h.pingInterval)
	stopTickerCreate()
	if err != nil {
		cancel()
		return fmt.Errorf("create distributed ticker: %w", err)
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		cancelAndCloseLocalTicker(cancel, ticker)
		return fmt.Errorf("health tracker is closed")
	}
	if _, ok := h.tickers[toolset]; ok {
		h.mu.Unlock()
		cancelAndCloseLocalTicker(cancel, ticker)
		return nil
	}
	h.tickers[toolset] = ticker
	h.cancels[toolset] = cancel
	h.tickerTokens[toolset] = registrationToken
	h.mu.Unlock()

	go h.runPingLoop(loopCtx, toolset, ticker)

	return nil
}

// stopLocalTicker stops the distributed ticker for a toolset on this node.
func (h *healthTracker) stopLocalTicker(toolset string) {
	h.mu.Lock()
	cancel, ticker := h.detachLocalTickerLocked(toolset)
	h.mu.Unlock()

	cancelAndStopLocalTicker(cancel, ticker)
}

func (h *healthTracker) detachLocalTickerLocked(toolset string) (context.CancelFunc, *pool.Ticker) {
	var cancel context.CancelFunc
	if found, ok := h.cancels[toolset]; ok {
		cancel = found
		delete(h.cancels, toolset)
	}
	var ticker *pool.Ticker
	if found, ok := h.tickers[toolset]; ok {
		ticker = found
		delete(h.tickers, toolset)
	}
	delete(h.tickerTokens, toolset)
	return cancel, ticker
}

func (h *healthTracker) detachAllLocalTickersLocked() ([]context.CancelFunc, []*pool.Ticker) {
	cancels := make([]context.CancelFunc, 0, len(h.cancels))
	for _, cancel := range h.cancels {
		cancels = append(cancels, cancel)
	}
	tickers := make([]*pool.Ticker, 0, len(h.tickers))
	for _, ticker := range h.tickers {
		tickers = append(tickers, ticker)
	}
	return cancels, tickers
}

func cancelAndCloseLocalTicker(cancel context.CancelFunc, ticker *pool.Ticker) {
	if cancel != nil {
		cancel()
	}
	if ticker != nil {
		// Close stops the ticker locally without deleting the shared ticker-map
		// entry, preserving distributed ownership during node shutdown/restart.
		ticker.Close()
	}
}

func cancelAndStopLocalTicker(cancel context.CancelFunc, ticker *pool.Ticker) {
	if cancel != nil {
		cancel()
	}
	if ticker != nil {
		ticker.Stop()
	}
}

func boundedHealthTrackerContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, healthTrackerIOTimeout)
}

// healthKey returns the shared health-map key for a toolset.
func healthKey(toolset string) string {
	return healthKeyPrefix + toolset
}

// toolsetFromCatalogKey extracts the toolset name from a catalog key.
func toolsetFromCatalogKey(key string) string {
	if !strings.HasPrefix(key, toolsetCatalogKeyPrefix) {
		return ""
	}
	return strings.TrimPrefix(key, toolsetCatalogKeyPrefix)
}

// registrationToken resolves the current catalog-backed liveness epoch for a
// toolset. The catalog owns this opaque token so re-registration rotates epoch
// identity even when the human-readable registration timestamp collides.
func (h *healthTracker) registrationToken(ctx context.Context, toolset string) (string, error) {
	return h.catalog.RegistrationToken(ctx, toolset)
}

// runPingLoop emits periodic pings for the distributed ticker winner and logs
// state transitions before each ping publish.
func (h *healthTracker) runPingLoop(ctx context.Context, toolset string, ticker *pool.Ticker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.observeHealth(ctx, toolset)
			h.sendPing(ctx, toolset)
		}
	}
}

// sendPing publishes one health ping bound to the current registration epoch.
func (h *healthTracker) sendPing(ctx context.Context, toolset string) {
	registrationToken, err := h.registrationToken(ctx, toolset)
	if err != nil {
		if errors.Is(err, errToolsetNotFound) {
			return
		}
		h.logger.Error(
			context.Background(),
			"resolve ping registration token failed",
			"event", "resolve_ping_registration_token_failed",
			"component", "tool-registry-health",
			"toolset", toolset,
			"err", err,
		)
		return
	}

	pingID := newPingID(registrationToken)
	msg := toolregistry.NewPingMessage(pingID)
	defer h.removeStreamIfRegistrationChanged(ctx, toolset, registrationToken)
	if err := h.streamManager.PublishToolCall(ctx, toolset, msg); err != nil {
		h.logger.Error(
			context.Background(),
			"publish ping failed",
			"event", "publish_ping_failed",
			"component", "tool-registry-health",
			"toolset", toolset,
			"ping_id", pingID,
			"err", err,
		)
	}
}

func (h *healthTracker) removeStreamIfRegistrationChanged(ctx context.Context, toolset string, expectedToken string) {
	registrationToken, err := h.registrationToken(ctx, toolset)
	if err != nil {
		if errors.Is(err, errToolsetNotFound) {
			h.streamManager.RemoveStream(toolset)
			return
		}
		h.logger.Error(
			context.Background(),
			"resolve post-publish registration token failed",
			"event", "resolve_post_publish_registration_token_failed",
			"component", "tool-registry-health",
			"toolset", toolset,
			"err", err,
		)
		return
	}
	if registrationToken != expectedToken {
		h.streamManager.RemoveStream(toolset)
	}
}

// observeHealth samples the current derived health state and forwards it to the
// transition logger.
func (h *healthTracker) observeHealth(ctx context.Context, toolset string) {
	health, err := h.Health(toolset)
	if err != nil {
		h.logger.Error(ctx, "read toolset health failed", "component", "tool-registry-health", "toolset", toolset, "err", err)
		h.noteHealth(ctx, toolset, false, 0, "missing_health_entry")
		return
	}
	if health.LastPong.IsZero() {
		h.noteHealth(ctx, toolset, false, 0, "missing_health_entry")
		return
	}
	h.noteHealth(ctx, toolset, health.Healthy, health.LastPong.UnixNano(), "ok")
}

// parseHealthRecord decodes the shared health-map payload.
func parseHealthRecord(raw string) (healthRecord, error) {
	var record healthRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return healthRecord{}, err
	}
	return record, nil
}

// newPingID returns a ping identifier that carries the active registration
// token so pong handling can reject stale registrations.
func newPingID(registrationToken string) string {
	return registrationToken + "/" + uuid.New().String()
}

// pingBelongsToRegistration reports whether the ponged ping ID belongs to the
// current registration epoch.
func pingBelongsToRegistration(pingID string, registrationToken string) bool {
	return strings.HasPrefix(pingID, registrationToken+"/")
}

// noteHealth logs health transitions while suppressing duplicate observations
// that would otherwise spam the registry logs on every ping tick.
func (h *healthTracker) noteHealth(ctx context.Context, toolset string, healthy bool, lastPongNano int64, reason string) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	prevHealthy, hasPrev := h.lastObservedHealthy[toolset]
	prevPong := h.lastObservedPongNano[toolset]

	h.lastObservedHealthy[toolset] = healthy
	if lastPongNano != 0 {
		h.lastObservedPongNano[toolset] = lastPongNano
	}

	if !hasPrev {
		return
	}
	if prevHealthy == healthy && prevPong == lastPongNano {
		return
	}

	now := time.Now()
	var lastPong time.Time
	if lastPongNano != 0 {
		lastPong = time.Unix(0, lastPongNano)
	} else if prevPong != 0 {
		lastPong = time.Unix(0, prevPong)
	}

	if prevHealthy && !healthy {
		h.logger.Warn(
			ctx,
			"toolset became unhealthy",
			"component", "tool-registry-health",
			"toolset", toolset,
			"reason", reason,
			"staleness_threshold", h.stalenessThreshold.String(),
			"ping_interval", h.pingInterval.String(),
			"missed_ping_threshold", h.missedPingThreshold,
			"last_pong", lastPong.UTC().Format(time.RFC3339Nano),
			"age_since_last_pong", now.Sub(lastPong).String(),
		)
		return
	}
}
