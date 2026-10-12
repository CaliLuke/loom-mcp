package recall

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const (
	// Description contains all model-facing discovery and paging guidance.
	Description = `Choose read/list/find for hidden evidence. IDs: placeholders/pins/index/receipts/summaries: tool use=result/arguments; message=original; ck-=summary. Only shown run/rN: labels.
read: id, part result|arguments, byte offset (0 first); follow next_offset. UTF-8-snapped pages: max(512, ProtectRecentToolTokens) bytes, including document Text/Chunks. Offset 0 lists attachments by original 1-based index. Attachment: part attachment, index, offset 0; index forbidden otherwise. Must fit all observed models, upload byte caps and ProtectRecentToolTokens (image/URI:1500; byte document:ceil(bytes/2)). recall_unsupported_part reports format, bytes, reason, limit, limit_unit (bytes|tokens).
list: rc- handle or inclusive from/through, optional run. Handles: receipts=calls/assistant text, earlier=prior records, users=original first lines. Ranges list visible messages. Continue cursor with same selector.
find: nonempty case-sensitive UTF-8 literal; hidden evidence newest first, current then earlier runs; max 8 MB/10 matches. Read match id/run/part/offset. Keep text with cursor across compaction; restart for new evidence or recall_invalid_input.
Opaque handles/cursors resolve within the session checkpoint chain. Errors: removed=recall_unavailable; unknown=recall_not_found; denied=recall_unauthorized; malformed=recall_invalid_input.
Start:20 reads,20 list/find. One read/attachment; rejections spend allowance; unknown kind spends discovery. Compaction refills ceil(initial/2), cap 3*initial. recall_budget_exhausted gives allowance, other_remaining, refills_at: next compaction. Listings spend no reads.`
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
      "allOf": [{
        "if": {"properties": {"part": {"const": "attachment"}}},
        "then": {"required": ["index"], "properties": {"offset": {"const": 0}}},
        "else": {"not": {"required": ["index"]}}
      }],
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
            "arguments",
            "attachment"
          ]
        },
        "index": {"type":"integer","minimum":1},
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
		Result: tools.TypeSpec{Name: "RecallResult", Schema: []byte(`{"type":"object","additionalProperties":true,"properties":{"limit_unit":{"enum":["bytes","tokens"]}},"if":{"required":["code","reason"],"properties":{"code":{"const":"recall_unsupported_part"},"reason":{"const":"too_large"}}},"then":{"required":["limit_unit"]}}`), Codec: tools.AnyJSONCodec},
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
