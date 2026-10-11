package recall

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Access identifies a run reached through verified transcript evidence.
	Access struct{ SessionID, RunID, TargetRunID, Scope string }
	// RunChain resolves displayed labels and index heads. K13's default (nil)
	// resolves no rN labels; K18 supplies the production continuation chain.
	RunChain interface {
		Resolve(context.Context, string, string) (index.RunCheckpoint, error)
		Head(context.Context, string, string) (runstore.Hash, error)
	}
	// Request is the trusted activity envelope, never caller-supplied tool arguments.
	Request struct {
		RunID, Scope         string
		View                 index.ViewRef
		Head                 runstore.Hash
		PageBytes, ScanBytes int
	}
	// Handler reads only model-eligible evidence reachable from the activity envelope.
	Handler struct {
		Store     runstore.Store
		Sessions  session.Store
		Chain     RunChain
		Authorize func(context.Context, Access) error
	}
	// Response is the model-facing result. Cursor and NextOffset continue bounded pages.
	Response struct {
		Code       Code    `json:"code,omitzero"`
		Text       string  `json:"text,omitzero"`
		NextOffset *int    `json:"next_offset,omitzero"`
		Entries    []Line  `json:"entries,omitzero"`
		Matches    []Match `json:"matches,omitzero"`
		Cursor     string  `json:"cursor,omitzero"`
		Format     string  `json:"format,omitzero"`
		Bytes      int     `json:"bytes,omitzero"`
	}
	// Line is a bounded model-visible receipt or message descriptor.
	Line struct {
		ID    string `json:"id"`
		Run   string `json:"run,omitzero"`
		Kind  string `json:"kind"`
		Text  string `json:"text,omitzero"`
		Bytes int    `json:"bytes,omitzero"`
		Error bool   `json:"error,omitzero"`
	}
	// Match locates literal eligible text by original byte offset.
	Match struct {
		ID      string `json:"id"`
		Run     string `json:"run,omitzero"`
		Offset  int    `json:"offset"`
		Snippet string `json:"snippet"`
	}
	callReader struct {
		handler   Handler
		req       Request
		sessionID string
		entered   map[string]string
		entries   map[string][]index.MessageEntry
	}
	recordNode struct {
		run, scope string
		hash       runstore.Hash
		record     index.CheckpointRecord
	}
)

// Handle resolves a parsed input. Stable rejections are results; infrastructure
// faults are errors so durable engines retry rather than invent missing evidence.
func (h Handler) Handle(ctx context.Context, req Request, in Input) (Response, error) {
	reader := callReader{handler: h, req: req, entered: map[string]string{}, entries: map[string][]index.MessageEntry{}}
	meta, err := h.Sessions.LoadRun(ctx, req.RunID)
	if err != nil {
		return recallFailure(err)
	}
	reader.sessionID = meta.SessionID
	if err := reader.enter(ctx, req.RunID, req.Scope); err != nil {
		return recallFailure(err)
	}
	var response Response
	switch in.Kind {
	case Read:
		if in.Read == nil {
			return Response{Code: CodeInvalidInput}, nil
		}
		response, err = reader.read(ctx, *in.Read)
	case List:
		if in.List == nil {
			return Response{Code: CodeInvalidInput}, nil
		}
		response, err = reader.list(ctx, *in.List)
	case Find:
		if in.Find == nil {
			return Response{Code: CodeInvalidInput}, nil
		}
		response, err = reader.find(ctx, *in.Find)
	default:
		return Response{Code: CodeInvalidInput}, nil
	}
	if err != nil {
		return recallFailure(err)
	}
	return response, nil
}

func recallFailure(err error) (Response, error) {
	var rejection *Error
	if errors.As(err, &rejection) {
		return Response{Code: rejection.Code}, nil
	}
	if errors.Is(err, runstore.ErrNotFound) || errors.Is(err, session.ErrRunNotFound) {
		return Response{Code: CodeUnavailable}, nil
	}
	return Response{}, err
}

func (r *callReader) enter(ctx context.Context, runID, scope string) error {
	if enteredScope, ok := r.entered[runID]; ok {
		if enteredScope != scope {
			return &Error{Code: CodeUnauthorized}
		}
		return nil
	}
	meta, err := r.handler.Sessions.LoadRun(ctx, runID)
	if err != nil {
		return err
	}
	if meta.SessionID == "" || meta.SessionID != r.sessionID {
		return &Error{Code: CodeUnauthorized}
	}
	if r.handler.Authorize != nil {
		if err := r.handler.Authorize(ctx, Access{SessionID: r.sessionID, RunID: r.req.RunID, TargetRunID: runID, Scope: scope}); err != nil {
			return &Error{Code: CodeUnauthorized}
		}
	}
	r.entered[runID] = scope
	return nil
}
