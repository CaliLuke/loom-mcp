package summaryeval

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	rtcompaction "github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

type recordedClient struct {
	cut      Cut
	calls    []runrecord.Call
	next     int
	mismatch Verification
}

var earlierCitation = regexp.MustCompile(`\[r([0-9]+) message `)

// Verify replays captured responses without consulting a registered model.
func Verify(ctx context.Context, c Cut) (Verification, error) {
	in, err := Reconstruct(c)
	if errors.Is(err, errViewMismatch) {
		return Verification{Status: verificationUnverifiable, Reason: reasonViewMismatch}, nil
	}
	if err != nil {
		return Verification{}, err
	}
	calls, reason := verificationEvidence(c)
	if reason != "" {
		return Verification{Status: verificationUnverifiable, Reason: reason}, nil
	}
	client := &recordedClient{cut: c, calls: calls}
	result, err := Replay(ctx, c, in, client)
	if client.mismatch.Status != "" {
		return client.mismatch, nil
	}
	if err != nil {
		return Verification{}, err
	}
	if client.next != len(calls) {
		return Verification{Status: verificationMismatch, FirstCall: calls[client.next].Record.Key.Seq, Field: "call_count"}, nil
	}
	if result.Kind != c.Old.Kind {
		return Verification{Status: verificationMismatch, FirstCall: calls[0].Record.Key.Seq, Field: "kind"}, nil
	}
	if c.Origin == originSummary && result.Checkpoint != c.Record {
		return Verification{Status: verificationMismatch, FirstCall: calls[0].Record.Key.Seq, Field: "checkpoint"}, nil
	}
	if string(contentHash(rtcompaction.Canonical(in.ContextSettings))) != in.ContextFingerprint {
		return Verification{Status: verificationMismatch, FirstCall: calls[0].Record.Key.Seq, Field: "context_settings"}, nil
	}
	return Verification{Status: verificationVerified}, nil
}

func (c *recordedClient) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	if c.next >= len(c.calls) {
		c.mismatch = Verification{Status: verificationMismatch, FirstCall: c.calls[len(c.calls)-1].Record.Key.Seq + 1, Field: "call_count"}
		return nil, fmt.Errorf("recorded calls exhausted")
	}
	call := c.calls[c.next]
	c.next++
	digest, err := callrecord.RequestDigest(req)
	if err != nil {
		return nil, err
	}
	if digest != call.Started.Request.Request {
		c.mismatch = Verification{Status: verificationMismatch, FirstCall: call.Record.Key.Seq, Field: "request_digest"}
		return nil, callrecord.ErrRequestMismatch
	}
	return callrecord.DecodeResponse(c.cut.record.Content[*call.Completion.Response])
}

func (c *recordedClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, fmt.Errorf("summary replay is unary")
}

func (c *recordedClient) ContextLimits(_ context.Context, _ *model.Request) (model.ContextLimits, string, bool) {
	call := c.calls[min(c.next, len(c.calls)-1)]
	window := call.Completion.Facts.ContextWindow
	return model.ContextLimits{ContextWindow: window}, call.Started.Model, window > 0
}

func verificationEvidence(c Cut) ([]runrecord.Call, string) {
	previous := ""
	if c.Prev != nil {
		cp, ok := findCheckpoint(c.run, *c.Prev)
		if ok && cp.Summary != nil {
			previous = string(c.record.Content[*cp.Summary])
		}
	}
	if c.ChainPos > 1 && earlierCitation.MatchString(c.Old.Text+previous) {
		return nil, "continued_run_citations"
	}
	calls := summaryCalls(c)
	if len(calls) == 0 {
		return nil, "no_calls"
	}
	for _, call := range calls {
		if call.Completion == nil || call.Completion.Error != nil || call.Completion.Response == nil || call.Completion.Facts == nil {
			return nil, "recorded_provider_error"
		}
	}
	return calls, ""
}
