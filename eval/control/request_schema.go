package control

import "strconv"

// schema is an in-process closed JSON Schema value; no external references.
type schema map[string]any

const (
	jsonAnyOf      = "anyOf"
	jsonState      = "state"
	jsonTruncated  = "truncated"
	jsonTotal      = "total"
	jsonNextOffset = "next_offset"
	jsonOperation  = "operation"
	jsonMediaType  = "media_type"
	jsonType       = "type"
	jsonSlot       = "slot"

	jsonContentBase64   = "content_base64"
	jsonRequired        = "required"
	jsonSourceCandidate = "source_candidate"
	jsonDraftId         = "draft_id"
	jsonOffset          = "offset"
	jsonDigest          = "digest"
	jsonName            = "name"

	jsonBaseline         = "baseline"
	jsonProperties       = "properties"
	jsonString           = "string"
	jsonNot              = "not"
	jsonCandidate        = "candidate"
	jsonExpectedRevision = "expected_revision"
	jsonEvaluationPlan   = "evaluation_plan"
	jsonConst            = "const"
	jsonJobId            = "job_id"
	jsonLocator          = "locator"
	jsonContent          = "content"
	jsonBoolean          = "boolean"
	jsonDependencies     = "dependencies"
	jsonVersion          = "version"
	jsonBaselineDigest   = "baseline_digest"
	jsonMaxBytes         = "max_bytes"
	jsonKind             = "kind"
)

func requestSchema(op Operation) schema {
	switch op {
	case DiscoverOperation:
		return object(schema{})
	case CreateOperation:
		o := object(schema{jsonBaseline: hexadecimal(64), jsonSourceCandidate: hexadecimal(64)})
		o["oneOf"] = exclusive([]string{jsonBaseline}, []string{jsonSourceCandidate})
		return o
	case GetOperation:
		o := object(schema{jsonDraftId: hexadecimal(32), jsonCandidate: hexadecimal(64)})
		o["oneOf"] = exclusive([]string{jsonDraftId}, []string{jsonCandidate})
		return o
	case ReadOperation:
		o := object(schema{jsonDraftId: hexadecimal(32), jsonExpectedRevision: integer(1), jsonCandidate: hexadecimal(64), "component": nonempty(), jsonOffset: integer(0), jsonMaxBytes: integer(0)}, "component")
		o["oneOf"] = []schema{{jsonRequired: []string{jsonDraftId, jsonExpectedRevision}, jsonNot: schema{jsonRequired: []string{jsonCandidate}}}, {jsonRequired: []string{jsonCandidate}, jsonNot: schema{jsonAnyOf: []schema{{jsonRequired: []string{jsonDraftId}}, {jsonRequired: []string{jsonExpectedRevision}}}}}}
		return o
	case PreviewCopyOperation, CopyOperation:
		return copyRequestSchema(op)
	case EditOperation:
		return editSchema()
	case ValidateOperation, FreezeOperation:
		return object(schema{jsonDraftId: hexadecimal(32), jsonExpectedRevision: integer(1)}, jsonDraftId, jsonExpectedRevision)
	case SubmitOperation:
		o := object(schema{"request_key": nonempty(), jsonCandidate: hexadecimal(64), "task_id": nonempty(), jsonKind: enumeration("probe", "evaluation"), jsonEvaluationPlan: nonempty()}, "request_key", jsonCandidate, "task_id")
		o["if"] = schema{jsonProperties: schema{jsonKind: schema{jsonConst: "evaluation"}}, jsonRequired: []string{jsonKind}}
		o["then"] = schema{jsonRequired: []string{jsonEvaluationPlan}}
		o["else"] = schema{jsonNot: schema{jsonRequired: []string{jsonEvaluationPlan}}}
		return o
	case StatusOperation, CancelOperation:
		return object(schema{jsonJobId: nonempty()}, jsonJobId)
	case ListEvidenceOperation:
		return object(schema{jsonJobId: nonempty(), jsonOffset: integer(0), "limit": integer(0)}, jsonJobId)
	case ReadEvidenceOperation:
		return object(schema{jsonJobId: nonempty(), jsonLocator: hexadecimal(64), jsonOffset: integer(0), jsonMaxBytes: integer(0)}, jsonJobId, jsonLocator)
	case CompareOperation:
		return comparisonRequestSchema()
	default:
		return object(schema{})
	}
}

func editSchema() schema {
	deps := array(nonempty())
	deps["uniqueItems"] = true
	o := object(schema{jsonDraftId: hexadecimal(32), jsonExpectedRevision: integer(1), jsonOperation: enumeration("put", "delete"), jsonName: nonempty(), jsonSlot: nonempty(), jsonMediaType: nonempty(), jsonContent: text(), jsonContentBase64: text(), jsonDependencies: deps}, jsonDraftId, jsonExpectedRevision, jsonOperation, jsonName)
	o["oneOf"] = []schema{
		{jsonProperties: schema{jsonOperation: schema{jsonConst: "put"}}, jsonRequired: []string{jsonSlot, jsonMediaType}, "oneOf": exclusive([]string{jsonContent}, []string{jsonContentBase64})},
		{jsonProperties: schema{jsonOperation: schema{jsonConst: "delete"}}, jsonNot: schema{jsonAnyOf: []schema{{jsonRequired: []string{jsonSlot}}, {jsonRequired: []string{jsonMediaType}}, {jsonRequired: []string{jsonContent}}, {jsonRequired: []string{jsonContentBase64}}, {jsonRequired: []string{jsonDependencies}}}}},
	}
	return o
}

func object(properties schema, required ...string) schema {
	return schema{jsonType: "object", jsonProperties: properties, jsonRequired: required, "additionalProperties": false}
}

func text() schema {
	return schema{jsonType: jsonString}
}

func nonempty() schema {
	return schema{jsonType: jsonString, "minLength": 1, "maxLength": 256}
}

func integer(min int) schema {
	return schema{jsonType: "integer", "minimum": min}
}

func array(items schema) schema {
	return schema{jsonType: "array", "items": items}
}

func enumeration(values ...string) schema {
	return schema{jsonType: jsonString, "enum": values}
}

func hexadecimal(n int) schema {
	return schema{jsonType: jsonString, "pattern": "^[a-f0-9]{" + strconv.Itoa(n) + "}$"}
}

func exclusive(a, b []string) []schema {
	return []schema{{jsonRequired: a, jsonNot: schema{jsonRequired: b}}, {jsonRequired: b, jsonNot: schema{jsonRequired: a}}}
}

// nativeRequestSchema advertises the same finite byte boundary as candidate edits.
func (s *Service) nativeRequestSchema(op Operation) schema {
	out := requestSchema(op)
	if op == CompareOperation {
		props := out[jsonProperties].(schema)
		props[jsonBaselineJobIDs].(schema)["maxItems"] = s.cfg.Limits.MaxPageEntries
		props[jsonCandidateJobIDs].(schema)["maxItems"] = s.cfg.Limits.MaxPageEntries
	}
	if op == PreviewCopyOperation || op == CopyOperation {
		props := out[jsonProperties].(schema)
		props[jsonComponents].(schema)["maxItems"] = s.cfg.Limits.MaxPageEntries
		if op == CopyOperation {
			props[jsonReplace].(schema)["maxItems"] = s.cfg.Limits.MaxPageEntries
		}
	}
	if op == EditOperation {
		props := out[jsonProperties].(schema)
		maxBytes := s.cfg.Candidates.Discover().Surface.MaxTotalBytes
		props[jsonContent].(schema)["maxLength"] = maxBytes
		props[jsonContentBase64].(schema)["maxLength"] = maxBytes/3*4 + 8
	}
	return out
}
