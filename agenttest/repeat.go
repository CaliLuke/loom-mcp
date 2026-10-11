package agenttest

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
)

// RepeatReport summarizes sequential attempts. Indices are one-based.
type RepeatReport struct {
	Attempts int
	Passed   int
	Failed   []AttemptReport
}

// AttemptReport contains one failed attempt's ordered diagnostics.
type AttemptReport struct {
	Index       int
	Diagnostics []string
}

type attemptTB struct {
	testing.TB
	mu           sync.Mutex
	ctx          context.Context
	index, total int
	failed       bool
	skipped      bool
	diagnostics  []string
	cleanups     []func()
}

// Repeat runs all n attempts sequentially, accepting at least minPass successes.
// Each attempt isolates failures, Fatal, panics and cleanup from the parent.
// A skipped attempt with no failure skips the parent and stops further attempts.
func Repeat(t testing.TB, n, minPass int, fn func(testing.TB)) RepeatReport {
	t.Helper()
	if minPass < 1 || minPass > n {
		t.Fatalf("agenttest: Repeat: require 1 <= minPass <= n (n=%d, minPass=%d)", n, minPass)
		return RepeatReport{}
	}
	report := RepeatReport{}
	for index := 1; index <= n; index++ {
		at := runAttempt(t, index, n, fn)
		report.Attempts++
		if at.skipped && !at.failed {
			t.Skipf("agenttest: Repeat: attempt %d skipped: %v", index, at.diagnostics)
			return report
		}
		if at.failed {
			report.Failed = append(report.Failed, AttemptReport{Index: index, Diagnostics: at.diagnostics})
		} else {
			report.Passed++
		}
	}
	if report.Passed < minPass {
		t.Errorf("agenttest: Repeat: %d/%d passed, need %d", report.Passed, n, minPass)
		for _, attempt := range report.Failed {
			for _, diagnostic := range attempt.Diagnostics {
				t.Errorf("attempt %d/%d: %s", attempt.Index, n, diagnostic)
			}
		}
	} else {
		for _, attempt := range report.Failed {
			for _, diagnostic := range attempt.Diagnostics {
				t.Logf("attempt %d/%d: %s", attempt.Index, n, diagnostic)
			}
		}
	}
	return report
}

func runAttempt(t testing.TB, index, total int, fn func(testing.TB)) *attemptTB {
	ctx, cancel := context.WithCancel(t.Context())
	at := &attemptTB{TB: t, ctx: ctx, index: index, total: total}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			cancel()
			at.runCleanups()
		}()
		defer at.recoverPanic()
		fn(at)
	}()
	<-done
	return at
}

func (t *attemptTB) Error(args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failed = true
	t.diagnostics = append(t.diagnostics, fmt.Sprint(args...))
}
func (t *attemptTB) Errorf(format string, args ...any) {
	t.Error(fmt.Sprintf(format, args...))
}
func (t *attemptTB) Fail() {
	t.Error("attempt failed")
}
func (t *attemptTB) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed
}
func (t *attemptTB) FailNow() {
	t.Fail()
	runtime.Goexit()
}
func (t *attemptTB) Fatal(args ...any) {
	t.Error(args...)
	runtime.Goexit()
}
func (t *attemptTB) Fatalf(format string, args ...any) {
	t.Fatal(fmt.Sprintf(format, args...))
}
func (t *attemptTB) Skip(args ...any) {
	t.mu.Lock()
	t.skipped = true
	t.diagnostics = append(t.diagnostics, fmt.Sprint(args...))
	t.mu.Unlock()
	runtime.Goexit()
}
func (t *attemptTB) SkipNow() {
	t.Skip("skipped")
}
func (t *attemptTB) Skipf(format string, args ...any) {
	t.Skip(fmt.Sprintf(format, args...))
}
func (t *attemptTB) Cleanup(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, fn)
}
func (t *attemptTB) Context() context.Context {
	return t.ctx
}
func (t *attemptTB) Name() string {
	return fmt.Sprintf("%s/attempt-%d", t.TB.Name(), t.index)
}
func (t *attemptTB) Log(args ...any) {
	t.TB.Logf("attempt %d/%d: %s", t.index, t.total, fmt.Sprint(args...))
}
func (t *attemptTB) Logf(format string, args ...any) {
	t.Log(fmt.Sprintf(format, args...))
}

// recoverPanic must be deferred directly so recover sees the panicking frame.
func (t *attemptTB) recoverPanic() {
	if value := recover(); value != nil {
		t.Errorf("panic: %v\n%s", value, debug.Stack())
	}
}

// runCleanups chains deferred cleanup calls so a Fatal, panic or Goexit inside
// one cleanup still runs the remaining cleanups, including newly added ones.
func (t *attemptTB) runCleanups() {
	t.mu.Lock()
	if len(t.cleanups) == 0 {
		t.mu.Unlock()
		return
	}
	last := len(t.cleanups) - 1
	fn := t.cleanups[last]
	t.cleanups = t.cleanups[:last]
	t.mu.Unlock()
	defer t.runCleanups()
	defer t.recoverPanic()
	fn()
}
