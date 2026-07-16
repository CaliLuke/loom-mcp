// Package middleware provides reusable model.Client middlewares such as
// adaptive rate limiting.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom/pulse/rmap"
)

type (
	// AdaptiveRateLimiter applies an AIMD-style adaptive token bucket on top of a
	// model.Client. It estimates the token cost of each request, blocks callers
	// until capacity is available, and adjusts its effective tokens-per-minute
	// budget in response to rate limiting signals from the provider.
	//
	// The limiter is process-local and designed to sit at the provider client
	// boundary. Callers construct a single instance per process and wrap the
	// underlying model.Client with Middleware before passing it to planners or
	// runtimes.
	AdaptiveRateLimiter struct {
		mu sync.Mutex

		limiter *rate.Limiter

		currentTPM float64
		minTPM     float64
		maxTPM     float64

		recoveryRate float64

		onBackoff func(newTPM float64)
		onProbe   func(newTPM float64)
	}

	limitedClient struct {
		next    model.Client
		limiter *AdaptiveRateLimiter
	}

	tokenCountingLimitedClient struct {
		*limitedClient
		counter model.TokenCounter
	}

	limitedStreamer struct {
		inner    model.Streamer
		limiter  *AdaptiveRateLimiter
		observed sync.Once
	}

	// clusterMap is the subset of rmap.Map used by the cluster-aware limiter.
	clusterMap interface {
		Get(key string) (string, bool)
		SetIfNotExists(ctx context.Context, key, value string) (bool, error)
		TestAndSet(ctx context.Context, key, test, value string) (string, error)
		Subscribe() <-chan rmap.EventKind
		Unsubscribe(ch <-chan rmap.EventKind)
	}

	rmapClusterMap struct {
		m *rmap.Map
	}
)

// ErrRequestTooLarge is returned when a single request's estimated input-token
// cost exceeds the limiter's maximum tokens-per-minute capacity. Such a request
// can never be admitted no matter how long it waits, so the limiter fails fast
// with this error instead of blocking. Callers should raise the limiter's max
// TPM or reduce the request size.
var ErrRequestTooLarge = errors.New("model middleware: request exceeds rate limiter capacity")

// NewAdaptiveRateLimiter constructs an AdaptiveRateLimiter with a
// tokens-per-minute budget. When m and key are set, it coordinates capacity
// across processes using a Pulse replicated map; otherwise it operates as a
// process-local limiter.
func NewAdaptiveRateLimiter(ctx context.Context, m *rmap.Map, key string, initialTPM, maxTPM float64) *AdaptiveRateLimiter {
	var cm clusterMap
	if m != nil {
		cm = &rmapClusterMap{m: m}
	}
	return newClusterAdaptiveRateLimiter(ctx, cm, key, initialTPM, maxTPM)
}

// newAdaptiveRateLimiter constructs an AdaptiveRateLimiter configured with an
// initial tokens-per-minute budget and an upper bound. The limiter uses a
// simple AIMD strategy and is used internally by the cluster-aware
// constructor.
//
// initialTPM and maxTPM are expressed in tokens per minute. When maxTPM is
// zero or less than initialTPM, it is clamped to initialTPM.
//
// The bucket burst is pinned at maxTPM for the lifetime of the limiter;
// backoff and probing adjust only the refill rate. This guarantees that any
// request estimated at or below maxTPM tokens can always be admitted by
// waiting for refill, even after repeated backoffs shrink the effective
// budget. Requests estimated above maxTPM fail fast with ErrRequestTooLarge.
func newAdaptiveRateLimiter(initialTPM, maxTPM float64) *AdaptiveRateLimiter {
	if initialTPM <= 0 {
		// Default to a conservative budget when callers do not provide one.
		initialTPM = 60000
	}
	if maxTPM <= 0 || maxTPM < initialTPM {
		maxTPM = initialTPM
	}
	minTPM := initialTPM * 0.1
	if minTPM < 1 {
		minTPM = 1
	}
	recoveryRate := initialTPM * 0.05
	if recoveryRate < 1 {
		recoveryRate = 1
	}
	// Pin the burst at the maximum budget so shrinking the effective TPM never
	// makes in-range requests permanently unadmittable; only the refill rate
	// adapts (see backoff, probe, and replaceTPM).
	burst := int(maxTPM)
	if burst < 1 {
		burst = 1
	}
	lim := rate.NewLimiter(rate.Limit(initialTPM/60.0), burst)

	return &AdaptiveRateLimiter{
		limiter:      lim,
		currentTPM:   initialTPM,
		minTPM:       minTPM,
		maxTPM:       maxTPM,
		recoveryRate: recoveryRate,
	}
}

// Middleware returns a model.Client middleware that enforces the adaptive
// tokens-per-minute limit for both Complete and Stream calls.
func (l *AdaptiveRateLimiter) Middleware() func(model.Client) model.Client {
	return func(next model.Client) model.Client {
		if next == nil {
			return nil
		}
		client := &limitedClient{
			next:    next,
			limiter: l,
		}
		if counter, ok := next.(model.TokenCounter); ok {
			return &tokenCountingLimitedClient{
				limitedClient: client,
				counter:       counter,
			}
		}
		return client
	}
}

// Complete enforces the limiter before delegating to the underlying client.
func (c *limitedClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if err := c.limiter.wait(ctx, req); err != nil {
		return nil, err
	}
	resp, err := c.next.Complete(ctx, req)
	c.limiter.observe(err)
	return resp, err
}

// Stream enforces the limiter before delegating to the underlying client. A
// successful setup is not treated as a successful request: the returned
// streamer adjusts the limiter only after Recv reports its terminal outcome.
func (c *limitedClient) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	if err := c.limiter.wait(ctx, req); err != nil {
		return nil, err
	}
	stream, err := c.next.Stream(ctx, req)
	if err != nil {
		c.limiter.observe(err)
		return nil, err
	}
	return &limitedStreamer{
		inner:   stream,
		limiter: c.limiter,
	}, nil
}

// CountTokens preserves a wrapped provider's exact token-counting capability.
func (c *tokenCountingLimitedClient) CountTokens(ctx context.Context, req *model.Request) (model.TokenCount, error) {
	return c.counter.CountTokens(ctx, req)
}

// Recv delegates to the provider stream and observes its first terminal
// outcome. EOF is the only successful terminal outcome; rate-limit failures
// back off, while other failures leave the adaptive budget unchanged.
func (s *limitedStreamer) Recv() (model.Chunk, error) {
	chunk, err := s.inner.Recv()
	if err == nil {
		return chunk, nil
	}

	s.observed.Do(func() {
		if errors.Is(err, model.ErrRateLimited) {
			s.limiter.observe(err)
			return
		}
		if errors.Is(err, io.EOF) {
			s.limiter.observe(nil)
			return
		}
		s.limiter.observe(err)
	})
	return chunk, err
}

// Close delegates resource cleanup without changing the adaptive budget. An
// early close is not evidence that the provider completed the request.
func (s *limitedStreamer) Close() error {
	return s.inner.Close()
}

// Metadata delegates provider metadata without copying or transforming it.
func (s *limitedStreamer) Metadata() map[string]any {
	return s.inner.Metadata()
}

func (l *AdaptiveRateLimiter) wait(ctx context.Context, req *model.Request) error {
	count, err := model.TokenEstimator{}.CountTokens(ctx, req)
	if err != nil {
		return err
	}
	// The burst is fixed at construction time, so a request that exceeds it can
	// never be admitted. Fail fast with an actionable diagnostic instead of the
	// raw x/time/rate "exceeds limiter's burst" error.
	if burst := l.limiter.Burst(); count.InputTokens > burst {
		return fmt.Errorf(
			"%w: estimated %d input tokens exceeds the maximum burst capacity of %d tokens (max TPM); raise the limiter's max TPM or reduce the request size",
			ErrRequestTooLarge, count.InputTokens, burst,
		)
	}
	return l.limiter.WaitN(ctx, count.InputTokens)
}

func (l *AdaptiveRateLimiter) observe(err error) {
	if err == nil {
		l.probe()
		return
	}
	if errors.Is(err, model.ErrRateLimited) {
		l.backoff()
	}
}

func (l *AdaptiveRateLimiter) backoff() {
	l.mu.Lock()

	newTPM := l.currentTPM * 0.5
	if newTPM < l.minTPM {
		newTPM = l.minTPM
	}
	if newTPM == l.currentTPM {
		l.mu.Unlock()
		return
	}
	l.currentTPM = newTPM
	// Adjust only the refill rate; the burst stays pinned at maxTPM so large
	// in-range requests remain admittable after backoff.
	l.limiter.SetLimit(rate.Limit(newTPM / 60.0))

	cb := l.onBackoff

	l.mu.Unlock()

	if cb != nil {
		cb(newTPM)
	}
}

func (l *AdaptiveRateLimiter) probe() {
	l.mu.Lock()

	newTPM := l.currentTPM + l.recoveryRate
	if newTPM > l.maxTPM {
		newTPM = l.maxTPM
	}
	if newTPM == l.currentTPM {
		l.mu.Unlock()
		return
	}
	l.currentTPM = newTPM
	l.limiter.SetLimit(rate.Limit(newTPM / 60.0))

	cb := l.onProbe

	l.mu.Unlock()

	if cb != nil {
		cb(newTPM)
	}
}

// replaceTPM updates the limiter effective budget to the given value,
// clamped to the configured [minTPM, maxTPM] range.
func (l *AdaptiveRateLimiter) replaceTPM(tpm float64) {
	l.mu.Lock()
	if tpm < l.minTPM {
		tpm = l.minTPM
	}
	if tpm > l.maxTPM {
		tpm = l.maxTPM
	}
	if tpm == l.currentTPM {
		l.mu.Unlock()
		return
	}
	l.currentTPM = tpm
	l.limiter.SetLimit(rate.Limit(tpm / 60.0))
	l.mu.Unlock()
}

func (l *AdaptiveRateLimiter) setClusterCallbacks(onBackoff, onProbe func(newTPM float64)) {
	l.mu.Lock()
	l.onBackoff = onBackoff
	l.onProbe = onProbe
	l.mu.Unlock()
}

func (m *rmapClusterMap) Get(key string) (string, bool) {
	return m.m.Get(key)
}

func (m *rmapClusterMap) SetIfNotExists(ctx context.Context, key, value string) (bool, error) {
	return m.m.SetIfNotExists(ctx, key, value)
}

func (m *rmapClusterMap) TestAndSet(ctx context.Context, key, test, value string) (string, error) {
	return m.m.TestAndSet(ctx, key, test, value)
}

func (m *rmapClusterMap) Subscribe() <-chan rmap.EventKind {
	return m.m.Subscribe()
}

func (m *rmapClusterMap) Unsubscribe(ch <-chan rmap.EventKind) {
	m.m.Unsubscribe(ch)
}

func newClusterAdaptiveRateLimiter(ctx context.Context, m clusterMap, key string, initialTPM, maxTPM float64) *AdaptiveRateLimiter {
	l := newAdaptiveRateLimiter(initialTPM, maxTPM)
	if key == "" || m == nil {
		return l
	}
	if !seedClusterRateLimit(ctx, m, key, l.currentTPM) {
		return l
	}
	sharedTPM := loadSharedTPM(m, key, l.currentTPM)
	l.replaceTPM(sharedTPM)
	configureClusterCallbacks(l, m, key)
	watchSharedTPM(ctx, m, key, l)
	return l
}

func seedClusterRateLimit(ctx context.Context, m clusterMap, key string, initialTPM float64) bool {
	if _, ok := m.Get(key); ok {
		return true
	}
	_, err := m.SetIfNotExists(ctx, key, strconv.Itoa(int(initialTPM)))
	return err == nil
}

func loadSharedTPM(m clusterMap, key string, fallback float64) float64 {
	cur, ok := m.Get(key)
	if !ok {
		return fallback
	}
	v, err := strconv.ParseFloat(cur, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func configureClusterCallbacks(l *AdaptiveRateLimiter, m clusterMap, key string) {
	min := l.minTPM
	max := l.maxTPM
	step := l.recoveryRate
	l.setClusterCallbacks(
		func(_ float64) {
			go globalBackoff(context.Background(), m, key, min)
		},
		func(_ float64) {
			go globalProbe(context.Background(), m, key, step, max)
		},
	)
}

func watchSharedTPM(ctx context.Context, m clusterMap, key string, l *AdaptiveRateLimiter) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}

	ch := m.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if ch == nil {
			return
		}
		defer m.Unsubscribe(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
			}
			v, ok := parseSharedTPM(m, key)
			if ok {
				l.replaceTPM(v)
			}
		}
	}()
	return done
}

func parseSharedTPM(m clusterMap, key string) (float64, bool) {
	cur, ok := m.Get(key)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(cur, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

func globalBackoff(ctx context.Context, m clusterMap, key string, floor float64) {
	const maxAttempts = 3

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	for i := 0; i < maxAttempts; i++ {
		curStr, ok := m.Get(key)
		if !ok {
			return
		}
		cur, err := strconv.ParseFloat(curStr, 64)
		if err != nil || cur <= 0 {
			return
		}
		next := cur * 0.5
		if next < floor {
			next = floor
		}
		nextStr := strconv.Itoa(int(next))
		prev, err := m.TestAndSet(ctx, key, curStr, nextStr)
		if err != nil {
			return
		}
		if prev == curStr {
			return
		}
	}
}

func globalProbe(ctx context.Context, m clusterMap, key string, step, ceiling float64) {
	const maxAttempts = 3

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	for i := 0; i < maxAttempts; i++ {
		curStr, ok := m.Get(key)
		if !ok {
			return
		}
		cur, err := strconv.ParseFloat(curStr, 64)
		if err != nil || cur <= 0 {
			return
		}
		if cur >= ceiling {
			return
		}
		next := cur + step
		if next > ceiling {
			next = ceiling
		}
		nextStr := strconv.Itoa(int(next))
		prev, err := m.TestAndSet(ctx, key, curStr, nextStr)
		if err != nil {
			return
		}
		if prev == curStr {
			return
		}
	}
}
