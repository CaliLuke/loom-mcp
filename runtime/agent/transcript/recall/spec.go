package recall

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const (
	// Description contains all model-facing discovery and paging guidance.
	Description = `Retrieve hidden conversation evidence. Choose one: read, list or find.
Use IDs shown in placeholders, pins, index lines, receipts and summaries. Tool use IDs address results or original arguments; message IDs address original text; ck- IDs address full summaries. Use run labels or rN:-qualified IDs only when shown.
read: give id, part (result or arguments), and byte offset (start at 0). Follow next_offset until complete. Offsets snap to UTF-8 boundaries. Pages use max(512, ProtectRecentToolTokens) bytes. Images/documents report recall_unsupported_part with format and bytes until supported.
list: give a collapsed rc- handle, or inclusive from/through message range and optional run. Receipt handles list calls and assistant text; earlier handles list earlier-run records; users handles list original user first lines. Ranges list every model-visible message. Follow cursor with the same selector.
find: nonempty literal, case-sensitive UTF-8 text. Scans hidden content newest first, current then earlier runs, up to 8 MB/10 matches. Read using each match's id/run/part/offset. Continue with cursor and same text across compaction; omit cursor for newly hidden content or after recall_invalid_input.
Handles/cursors are opaque and re-resolved within the checkpoint chain; they grant no access outside the session. Removed content returns recall_unavailable; unknown IDs/handles recall_not_found; denied access recall_unauthorized; malformed input recall_invalid_input.
Discovery (list/find) and reads each start with 20 calls. Rejected/malformed calls spend their allowance; an unrecognized kind spends discovery. Compaction refills both by half their initial allowance rounded up, capped at three times initial. recall_budget_exhausted gives allowance, other_remaining and refills_at: next compaction. Listings never spend reads.`
	inputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "read": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "id",
        "part",
        "offset"
      ],
      "properties": {
        "id": {
          "type": "string",
          "minLength": 1
        },
        "run": {
          "type": "string"
        },
        "part": {
          "enum": [
            "result",
            "arguments"
          ]
        },
        "offset": {
          "type": "integer",
          "minimum": 0
        }
      }
    },
    "list": {
      "oneOf": [
        {
          "type": "object",
          "additionalProperties": false,
          "required": [
            "handle"
          ],
          "properties": {
            "handle": {
              "type": "string",
              "minLength": 1
            },
            "cursor": {
              "type": "string"
            }
          }
        },
        {
          "type": "object",
          "additionalProperties": false,
          "required": [
            "from",
            "through"
          ],
          "properties": {
            "from": {
              "type": "integer",
              "minimum": 1
            },
            "through": {
              "type": "integer",
              "minimum": 1
            },
            "run": {
              "type": "string"
            },
            "cursor": {
              "type": "string"
            }
          }
        }
      ]
    },
    "find": {
      "type": "object",
      "additionalProperties": false,
      "required": [
        "text"
      ],
      "properties": {
        "text": {
          "type": "string",
          "minLength": 1
        },
        "cursor": {
          "type": "string"
        }
      }
    }
  },
  "oneOf": [
    {
      "required": [
        "read"
      ]
    },
    {
      "required": [
        "list"
      ]
    },
    {
      "required": [
        "find"
      ]
    }
  ]
}`
)

// Spec returns an owned tool spec without registering or advertising it.
func Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: tools.RecallContext, Service: "loom-mcp", Toolset: tools.RecallContext.Toolset(), Description: Description,
		Payload: tools.TypeSpec{Name: "RecallInput", Schema: []byte(inputSchema), Codec: tools.JSONCodec[any]{
			FromJSON: func(data []byte) (any, error) {
				return ParseInput(data)
			},
			ToJSON: func(value any) ([]byte, error) {
				return json.Marshal(value)
			},
		}},
		Result: tools.TypeSpec{Name: "RecallResult", Schema: []byte(`{"type":"object","additionalProperties":true}`), Codec: tools.AnyJSONCodec},
	}
}

// ValidateToolName rejects identifiers and provider names that expose the
// reserved lookup name in rendered receipts. Registration invokes this for
// application tools when compaction is enabled; the runtime-owned spec is exempt.
func ValidateToolName(identifier tools.Ident, providerName string) error {
	if strings.Contains(identifier.String(), tools.RecallContext.Tool()) || strings.Contains(providerName, tools.RecallContext.Tool()) {
		return fmt.Errorf("tool %q (provider name %q) contains reserved name %q", identifier, providerName, tools.RecallContext.Tool())
	}
	return nil
}
