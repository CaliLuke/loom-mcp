package control_test

import (
	"bytes"
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/controlcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlRequestFiniteEditParity(t *testing.T) {
	for _, field := range []string{"name", "dependencies", "slot", "media_type"} {
		for _, length := range []int{256, 257} {
			for _, route := range []string{"Go", "native", "CLI"} {
				t.Run(field+"/"+strconv.Itoa(length)+"/"+route, func(t *testing.T) {
					cfg := finiteConfig(t, field, length)
					s := service(t, cfg)
					d := draft(t, s, cfg)
					before, err := cfg.Candidates.Draft(t.Context(), d.DraftID)
					require.NoError(t, err)
					text := "data"
					q := control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "target", Slot: "content", MediaType: "text/plain", Content: &text}
					value := strings.Repeat("n", length)
					switch field {
					case "name":
						q.Name = value
					case "dependencies":
						q.Dependencies = []string{value}
					case "slot":
						q.Slot = value
					case "media_type":
						q.MediaType = "text/" + strings.Repeat("n", length-5)
					}
					err = parityRequest(t, cfg, s, route, control.EditOperation, q)
					if length == 257 {
						code(t, err, control.InvalidInput)
						assert.Equal(t, control.NewError(control.InvalidInput, "", "", "").Error(), err.Error())
					} else {
						require.NoError(t, err)
					}
					after, err := cfg.Candidates.Draft(t.Context(), d.DraftID)
					require.NoError(t, err)
					if length == 257 {
						assert.Equal(t, before, after)
					} else {
						assert.Equal(t, d.Revision+1, after.Revision)
					}
				})
			}
		}
	}
}

func TestControlRequestFiniteComponentAndTaskParity(t *testing.T) {
	for _, length := range []int{256, 257} {
		for _, route := range []string{"Go", "native", "CLI"} {
			t.Run(route+strconv.Itoa(length), func(t *testing.T) {
				cfg := finiteConfig(t, "dependencies", length)
				s := service(t, cfg)
				d := draft(t, s, cfg)
				q := control.ReadCandidateRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Component: strings.Repeat("n", length)}
				err := parityRequest(t, cfg, s, route, control.ReadOperation, q)
				if length == 257 {
					code(t, err, control.InvalidInput)
					assert.Equal(t, control.NewError(control.InvalidInput, "", "", "").Error(), err.Error())
				} else {
					require.NoError(t, err)
				}
				err = parityRequest(t, cfg, s, route, control.SubmitOperation, control.SubmitRequest{Candidate: cfg.Candidates.Discover().BaselineDigest, TaskID: strings.Repeat("t", 257), RequestKey: "key"})
				code(t, err, control.InvalidInput)
				assert.Equal(t, control.NewError(control.InvalidInput, "", "", "").Error(), err.Error())
			})
		}
	}
}

func TestControlRequestOptionalAndUnicodeParity(t *testing.T) {
	for _, route := range []string{"Go", "native", "CLI"} {
		for _, binary := range []bool{false, true} {
			t.Run(route+"/base64="+strconv.FormatBool(binary), func(t *testing.T) {
				cfg := config(t)
				s := service(t, cfg)
				d := draft(t, s, cfg)
				empty := ""
				q := control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: strings.Repeat("é", 256), Slot: "content", MediaType: "text/plain"}
				if binary {
					q.ContentBase64 = &empty
				} else {
					q.Content = &empty
				}
				err := parityRequest(t, cfg, s, route, control.EditOperation, q)
				require.NoError(t, err)
				after, err := cfg.Candidates.Draft(t.Context(), d.DraftID)
				require.NoError(t, err)
				assert.Equal(t, d.Revision+1, after.Revision)
				q.ExpectedRevision++
				q.Name = "missing-content"
				q.Content = nil
				q.ContentBase64 = nil
				err = parityRequest(t, cfg, s, route, control.EditOperation, q)
				code(t, err, control.InvalidInput)
				assert.Equal(t, control.NewError(control.InvalidInput, "", "", "").Error(), err.Error())
			})
		}
	}
}

// finiteConfig advertises long valid slot/media identifiers and retains long
// component names through the lower-level candidate API. Control must still
// apply its own published finite request bounds before mutation or lookup.
func finiteConfig(t *testing.T, field string, length int) control.Config {
	t.Helper()
	cfg := config(t)
	surface := cfg.Candidates.Discover().Surface
	slot := "content"
	media := "text/plain"
	if field == "slot" {
		slot = strings.Repeat("n", length)
		surface.Slots[0].Name = slot
	}
	if field == "media_type" {
		media = "text/" + strings.Repeat("n", length-5)
		surface.Slots[0].MediaTypes = []string{media}
	}
	components := []candidate.Component{{Name: "main.md", Slot: slot, MediaType: media, Data: []byte("baseline")}}
	if field == "dependencies" {
		components = append(components, candidate.Component{Name: strings.Repeat("n", length), Slot: slot, MediaType: media, Data: []byte("source")})
	}
	store, err := candidate.Open(t.Context(), t.TempDir(), surface, components)
	require.NoError(t, err)
	cfg.Candidates = store
	return cfg
}

func parityRequest(t *testing.T, cfg control.Config, s *control.Service, route string, op control.Operation, q any) error {
	t.Helper()
	if route == "Go" {
		switch input := q.(type) {
		case control.EditRequest:
			_, err := s.Edit(t.Context(), input)
			return err
		case control.ReadCandidateRequest:
			_, err := s.ReadCandidate(t.Context(), input)
			return err
		case control.SubmitRequest:
			_, err := s.Submit(t.Context(), input)
			return err
		}
	}
	raw, err := json.Marshal(q, jsonv1.OmitEmptyWithLegacySemantics(true))
	require.NoError(t, err)
	if route == "native" {
		reg := native(t, s)
		out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: tools.Ident("experiment." + string(op)), ToolCallID: "bounds", Payload: rawjson.Message(raw)})
		if err != nil {
			return err
		}
		if out.ToolResult.Error == nil {
			return nil
		}
		var failure control.Error
		require.NoError(t, json.Unmarshal([]byte(out.ToolResult.Error.Message), &failure))
		return &failure
	}
	extensions, err := controlcli.Extensions(func(_ context.Context, store *eval.Store) (*control.Service, error) {
		cfg.Experiments = store
		return control.New(cfg)
	})
	require.NoError(t, err)
	var output, diagnostic bytes.Buffer
	exit := evalcli.Main(t.Context(), []string{"experiment", string(op), "--request", string(raw)}, evalcli.Config{StoreDir: t.TempDir(), Extensions: extensions, Stdout: &output, Stderr: &diagnostic})
	if exit == 0 {
		assert.NotEmpty(t, output.String())
		return nil
	}
	var failure control.Error
	require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &failure))
	return &failure
}
