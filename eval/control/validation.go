package control

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// authorizeRequest applies the host grant before the same finite schema used by
// native and CLI callers. No adapter dispatch is involved in this Go boundary.
func (s *Service) authorizeRequest(ctx context.Context, operation Operation, request any) error {
	if err := s.authorize(ctx, operation); err != nil {
		return err
	}
	// Legacy omitempty semantics preserve a nonnil pointer to explicit empty
	// text/base64 while retaining json/v2's strict UTF-8 and exact-number encoding.
	data, err := json.Marshal(request, jsonv1.OmitEmptyWithLegacySemantics(true))
	if err != nil {
		return invalid("")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return invalid("")
	}
	document, err := json.Marshal(s.nativeRequestSchema(operation))
	if err != nil {
		return NewError(OperationFailed, "", "", "")
	}
	if err := tools.ValidateJSONSchema(document, value); err != nil {
		return invalid("")
	}
	return nil
}
