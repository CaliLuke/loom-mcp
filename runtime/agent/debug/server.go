// Package debug exposes an opt-in local HTTP debug server for agent runs.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

type (
	// Config configures an explicit local debug server.
	Config struct {
		Runtime *agentsruntime.Runtime
		Addr    string
	}

	// Server serves development-only run/debug endpoints.
	Server struct {
		runtime *agentsruntime.Runtime
		addr    string
		server  *http.Server
		handler http.Handler
	}

	errorEnvelope struct {
		Error debugError `json:"error"`
	}

	debugError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	dataEnvelope struct {
		Data any `json:"data"`
	}

	workflowNode struct {
		ID               string `json:"id"`
		ToolName         string `json:"tool_name,omitempty"`
		Status           string `json:"status"`
		Queue            string `json:"queue,omitempty"`
		ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	}

	workflowAwait struct {
		ID         string          `json:"id,omitempty"`
		Type       hooks.EventType `json:"type"`
		ToolName   string          `json:"tool_name,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}

	workflowSnapshot struct {
		RunID      string                  `json:"run_id"`
		EventCount map[hooks.EventType]int `json:"event_counts"`
		Nodes      map[string]workflowNode `json:"nodes"`
		Awaits     []workflowAwait         `json:"awaits"`
		Phases     []string                `json:"phases"`
	}
)

const defaultAddr = "127.0.0.1:0"

// NewServer constructs a disabled-by-default debug server. Call Start to bind.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Runtime == nil {
		return nil, errors.New("debug runtime is required")
	}
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = defaultAddr
	}
	s := &Server{runtime: cfg.Runtime, addr: addr}
	mux := http.NewServeMux()
	mux.HandleFunc("/runs/", s.handleRun)
	s.handler = mux
	return s, nil
}

// Handler returns the HTTP handler without starting a listener.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Addr returns the configured or bound address.
func (s *Server) Addr() string {
	return s.addr
}

// Start binds the debug server. The default address is loopback-only.
func (s *Server) Start() error {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	s.server = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = s.server.Serve(ln)
	}()
	return nil
}

// Shutdown gracefully stops a started server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID, suffix, ok := parseRunPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown debug endpoint")
		return
	}
	switch suffix {
	case "":
		s.writeRunSnapshot(w, r, runID)
	case "events":
		s.writeRunEvents(w, r, runID)
	case "await":
		s.writeAwaitState(w, r, runID)
	case "memory":
		s.writeMemory(w, r, runID)
	case "artifacts":
		s.writeArtifacts(w, r, runID)
	case "workflow":
		s.writeWorkflow(w, r, runID)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown debug endpoint")
	}
}

func parseRunPath(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/runs/")
	if rest == path || rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func (s *Server) writeRunSnapshot(w http.ResponseWriter, r *http.Request, runID string) {
	meta, status, err := s.runMetaAndStatus(r.Context(), runID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeData(w, map[string]any{
		"run":           meta,
		"engine_status": status,
	})
}

func (s *Server) writeRunEvents(w http.ResponseWriter, r *http.Request, runID string) {
	page, err := s.runtime.ListRunEvents(r.Context(), runID, r.URL.Query().Get("cursor"), 200)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeData(w, page)
}

func (s *Server) writeAwaitState(w http.ResponseWriter, r *http.Request, runID string) {
	page, err := s.runtime.ListRunEvents(r.Context(), runID, "", 500)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	var awaits []*runlog.Event
	for _, event := range page.Events {
		if event != nil && isAwaitEvent(event.Type) {
			awaits = append(awaits, event)
		}
	}
	writeData(w, map[string]any{
		"run_id": runID,
		"awaits": awaits,
	})
}

func (s *Server) writeMemory(w http.ResponseWriter, r *http.Request, runID string) {
	if s.runtime.Memory == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "memory store is not configured")
		return
	}
	agentID, err := s.agentID(r.Context(), runID, r)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	snapshot, err := s.runtime.Memory.LoadRun(r.Context(), agentID, runID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeData(w, snapshot)
}

func (s *Server) writeArtifacts(w http.ResponseWriter, r *http.Request, runID string) {
	if s.runtime.ArtifactStore == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "artifact store is not configured")
		return
	}
	agentID, err := s.agentID(r.Context(), runID, r)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	refs, err := s.runtime.ArtifactStore.List(r.Context(), artifact.ListQuery{AgentID: agentID, RunID: runID, Limit: 200})
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeData(w, map[string]any{"artifacts": refs})
}

func (s *Server) writeWorkflow(w http.ResponseWriter, r *http.Request, runID string) {
	page, err := s.runtime.ListRunEvents(r.Context(), runID, "", 500)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeData(w, buildWorkflowSnapshot(runID, page.Events))
}

func (s *Server) runMetaAndStatus(ctx context.Context, runID string) (session.RunMeta, engine.RunStatus, error) {
	meta, err := s.runtime.SessionStore.LoadRun(ctx, runID)
	if err != nil {
		return session.RunMeta{}, "", err
	}
	if s.runtime.Engine == nil {
		return meta, "", nil
	}
	status, _ := s.runtime.Engine.QueryRunStatus(ctx, runID)
	return meta, status, nil
}

func (s *Server) agentID(ctx context.Context, runID string, r *http.Request) (string, error) {
	if agentID := strings.TrimSpace(r.URL.Query().Get("agent_id")); agentID != "" {
		return agentID, nil
	}
	meta, err := s.runtime.SessionStore.LoadRun(ctx, runID)
	if err != nil {
		return "", err
	}
	if meta.AgentID == "" {
		return "", fmt.Errorf("run %q has no agent id", runID)
	}
	return meta.AgentID, nil
}

func isAwaitEvent(t hooks.EventType) bool {
	return t == hooks.AwaitClarification ||
		t == hooks.AwaitQuestions ||
		t == hooks.AwaitExternalTools ||
		t == hooks.AwaitTypedInput ||
		t == hooks.AwaitConfirmation
}

func buildWorkflowSnapshot(runID string, events []*runlog.Event) workflowSnapshot {
	snapshot := workflowSnapshot{
		RunID:      runID,
		EventCount: make(map[hooks.EventType]int),
		Nodes:      make(map[string]workflowNode),
		Awaits:     make([]workflowAwait, 0),
		Phases:     make([]string, 0),
	}
	for _, event := range events {
		applyWorkflowEvent(&snapshot, event)
	}
	return snapshot
}

func applyWorkflowEvent(snapshot *workflowSnapshot, event *runlog.Event) {
	if event == nil {
		return
	}
	snapshot.EventCount[event.Type]++
	payload := eventPayloadMap(event)
	if event.Type == hooks.ToolCallScheduled {
		applyScheduledWorkflowNode(snapshot, payload)
		return
	}
	if event.Type == hooks.ToolResultReceived {
		applyCompletedWorkflowNode(snapshot, payload)
		return
	}
	if event.Type == hooks.RunPhaseChanged {
		if phase := payloadString(payload, "phase", "Phase"); phase != "" {
			snapshot.Phases = append(snapshot.Phases, phase)
		}
		return
	}
	if isAwaitEvent(event.Type) {
		snapshot.Awaits = append(snapshot.Awaits, workflowAwait{
			ID:         payloadString(payload, "id", "ID"),
			Type:       event.Type,
			ToolName:   payloadString(payload, "tool_name", "ToolName"),
			ToolCallID: payloadString(payload, "tool_call_id", "ToolCallID"),
		})
	}
}

func applyScheduledWorkflowNode(snapshot *workflowSnapshot, payload map[string]any) {
	id := payloadString(payload, "tool_call_id", "ToolCallID")
	if id == "" {
		return
	}
	snapshot.Nodes[id] = workflowNode{
		ID:               id,
		ToolName:         payloadString(payload, "tool_name", "ToolName"),
		Status:           "scheduled",
		Queue:            payloadString(payload, "queue", "Queue"),
		ParentToolCallID: payloadString(payload, "parent_tool_call_id", "ParentToolCallID"),
	}
}

func applyCompletedWorkflowNode(snapshot *workflowSnapshot, payload map[string]any) {
	id := payloadString(payload, "tool_call_id", "ToolCallID")
	if id == "" {
		return
	}
	node := snapshot.Nodes[id]
	node.ID = id
	node.ToolName = firstNonEmpty(node.ToolName, payloadString(payload, "tool_name", "ToolName"))
	node.ParentToolCallID = firstNonEmpty(node.ParentToolCallID, payloadString(payload, "parent_tool_call_id", "ParentToolCallID"))
	node.Status = "completed"
	if payload["error"] != nil || payload["Error"] != nil {
		node.Status = "failed"
	}
	snapshot.Nodes[id] = node
}

func eventPayloadMap(event *runlog.Event) map[string]any {
	if event == nil || len(event.Payload) == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload.RawMessage(), &payload); err != nil {
		return map[string]any{}
	}
	return payload
}

func payloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		if s, ok := value.(string); ok {
			return s
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func writeData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dataEnvelope{Data: data})
}

func writeMappedError(w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrRunNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if errors.Is(err, artifact.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", err.Error())
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: debugError{Code: code, Message: message}})
}
