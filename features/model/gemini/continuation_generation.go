package gemini

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type continuationGeneration struct {
	response model.Response
	hasUsage bool
	index    int32
	seen     map[[sha256.Size]byte]struct{}
}

func hasContinuation(response *genai.GenerateContentResponse) bool {
	if response == nil {
		return false
	}
	for _, candidate := range response.Candidates {
		if candidate != nil && candidate.FinishReason == genai.FinishReasonContinuation {
			return true
		}
	}
	return false
}

func geminiGenerationError(err error) error {
	if overflow := geminiContextOverflow(err); overflow != nil {
		return overflow
	}
	if isRateLimited(err) {
		return fmt.Errorf("%w: %w", model.ErrRateLimited, err)
	}
	return fmt.Errorf("gemini generate_content: %w", err)
}

// completeGeneration treats one bounded token chain as a single logical call.
// Request configuration disables SDK automatic continuation, preserving one hop
// per GenerateContent call. Only the opaque token changes between requests;
// the SDK transport retry policy is separate.
func (c *Client) completeGeneration(ctx context.Context, modelID string, class model.ModelClass, contents []*genai.Content, config *genai.GenerateContentConfig) (*model.Response, error) {
	resp, err := c.models.GenerateContent(ctx, modelID, contents, config)
	if err != nil {
		return nil, geminiGenerationError(err)
	}
	if c.maxGenerationRequests == 0 || !hasContinuation(resp) {
		return translateResponse(modelID, class, resp)
	}
	state := continuationGeneration{response: model.Response{RequestInputUsage: model.RequestInputUsage{Present: true}}, seen: make(map[[sha256.Size]byte]struct{})}
	for request := 1; ; request++ {
		if err := state.observeUsage(modelID, class, resp); err != nil {
			return nil, state.reject(model.OutputValidationUsage, errors.Join(ErrContinuationUnsupported, err))
		}
		candidate, err := state.candidate(resp, request)
		if err != nil {
			return nil, state.reject(model.OutputValidationResponseShape, err)
		}
		if candidate.FinishReason == genai.FinishReasonMaxTokens {
			return &model.Response{Usage: state.response.Usage, RequestInputUsage: state.response.RequestInputUsage, StopReason: string(candidate.FinishReason), OutputLimited: true}, nil
		}
		if candidate.FinishReason != genai.FinishReasonContinuation && candidate.FinishReason != genai.FinishReasonStop {
			return nil, state.reject(model.OutputValidationResponseShape, ErrContinuationUnsupported)
		}
		if err := state.appendCandidate(candidate); err != nil {
			return nil, state.reject(model.OutputValidationOutputBounds, errors.Join(ErrContinuationUnsupported, err))
		}
		if candidate.FinishReason == genai.FinishReasonStop {
			return &state.response, nil
		}
		next, err := state.nextConfig(ctx, config, candidate.ContinuationToken, request, c.maxGenerationRequests)
		if err != nil {
			return nil, state.reject(model.OutputValidationResponseShape, err)
		}
		resp, err = c.models.GenerateContent(ctx, modelID, contents, next)
		if err != nil {
			return nil, state.reject(model.OutputValidationResponseShape, geminiGenerationError(err))
		}
	}
}

// candidate requires a single stable lineage throughout the logical generation.
func (s *continuationGeneration) candidate(response *genai.GenerateContentResponse, request int) (*genai.Candidate, error) {
	if response == nil || response.PromptFeedback != nil || len(response.Candidates) != 1 || response.Candidates[0] == nil {
		return nil, ErrContinuationUnsupported
	}
	candidate := response.Candidates[0]
	if request == 1 {
		s.index = candidate.Index
	}
	if candidate.Index != s.index {
		return nil, ErrContinuationUnsupported
	}
	return candidate, nil
}

// nextConfig admits a fresh token without mutating the original generation config.
func (s *continuationGeneration) nextConfig(ctx context.Context, config *genai.GenerateContentConfig, token []byte, request, limit int) (*genai.GenerateContentConfig, error) {
	digest := sha256.Sum256(token)
	_, repeated := s.seen[digest]
	if len(token) == 0 || repeated || request >= limit {
		return nil, ErrContinuationUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.seen[digest] = struct{}{}
	next := *config
	next.ContinuationToken = token
	return &next, nil
}

// appendCandidate accumulates canonical parts privately. CloneResponse applies
// the same byte/depth/collection limits as terminal validation before another
// hop can run, without accepting incomplete output or executing tool calls.
func (s *continuationGeneration) appendCandidate(candidate *genai.Candidate) error {
	message, calls, err := translateCandidateParts(candidate, true)
	if err != nil {
		return err
	}
	if len(message.Parts) > 0 {
		if len(s.response.Content) == 0 {
			s.response.Content = []model.Message{message}
		} else {
			s.response.Content[0].Parts = append(s.response.Content[0].Parts, message.Parts...)
		}
	}
	s.response.ToolCalls = append(s.response.ToolCalls, calls...)
	s.response.StopReason = string(candidate.FinishReason)
	owned, err := model.CloneResponse(&s.response)
	if err != nil {
		return err
	}
	s.response = *owned
	return nil
}

func (s *continuationGeneration) reject(kind model.OutputValidationKind, cause error) error {
	var usage *model.TokenUsage
	if s.hasUsage {
		usage = &s.response.Usage
	}
	rejection, err := model.RestoreOutputValidationError(kind, cause, model.ResponseEvidence{Present: true, RequestInputUsage: s.response.RequestInputUsage}, usage)
	if err != nil {
		return fmt.Errorf("gemini: construct continuation rejection: %w", err)
	}
	return rejection
}
