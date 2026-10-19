package control

func resultSchema(op Operation) schema {
	switch op {
	case DiscoverOperation:
		return object(schema{"surface": discoverySchema(), "tasks": array(object(schema{"id": nonempty(), "modes": array(enumeration("probe", "evaluation")), "evaluation_plans": array(nonempty())}, "id", "modes")), "operations": array(enumeration("discover", "create_candidate", "get_candidate", "read_candidate", "edit_candidate", "preview_copy", "copy_candidate", "validate_candidate", "freeze_candidate", "submit_trial", "get_job", "cancel_job", "list_evidence", "read_evidence", "compare_trials"))}, "operations")
	case CreateOperation, GetOperation, EditOperation:
		return object(schema{jsonDraftId: hexadecimal(32), "revision": integer(1), jsonCandidate: hexadecimal(64), "components": array(componentRefSchema())}, "components")
	case PreviewCopyOperation:
		return copyPlanSchema()
	case CopyOperation:
		return copyResultSchema()
	case ReadOperation:
		return object(schema{"data": text(), jsonMediaType: text(), jsonDigest: hexadecimal(64), jsonOffset: integer(0), jsonTotal: integer(0), jsonNextOffset: integer(0), jsonTruncated: schema{jsonType: jsonBoolean}}, jsonMediaType, jsonDigest, jsonOffset, jsonTotal, jsonTruncated)
	case ValidateOperation:
		return object(schema{"valid": schema{jsonConst: true}}, "valid")
	case FreezeOperation:
		return object(schema{jsonCandidate: hexadecimal(64), "manifest": manifestSchema()}, jsonCandidate, "manifest")
	case SubmitOperation, StatusOperation, CancelOperation:
		return object(schema{"id": nonempty(), "state": enumeration("queued", "running", "cancel_requested", "succeeded", "failed", "canceled", "interrupted"), "evidence_available": schema{jsonType: jsonBoolean}, "failure": errorSchema()}, "id", "state", "evidence_available")
	case ListEvidenceOperation:
		return object(schema{"entries": array(entrySchema()), jsonTotal: integer(0), jsonNextOffset: integer(0), jsonTruncated: schema{jsonType: jsonBoolean}}, "entries", jsonTotal, jsonTruncated)
	case ReadEvidenceOperation:
		return object(schema{"entry": entrySchema(), "data": text(), jsonOffset: integer(0), jsonTotal: integer(0), jsonNextOffset: integer(0), jsonTruncated: schema{jsonType: jsonBoolean}}, "entry", jsonOffset, jsonTruncated)
	case CompareOperation:
		return object(schema{"status": enumeration("comparable", "not_comparable"), jsonBaseline: comparisonSideSchema(), jsonCandidate: comparisonSideSchema(), "warning_codes": array(text())}, "status", jsonBaseline, jsonCandidate)
	default:
		return object(schema{})
	}
}
func componentRefSchema() schema {
	return object(schema{jsonName: text(), jsonSlot: text(), jsonMediaType: text(), jsonDigest: hexadecimal(64), "size": integer(0), jsonDependencies: array(text())}, jsonName, jsonSlot, jsonMediaType, jsonDigest, "size")
}
func bindingSchema() schema {
	return object(schema{"id": text(), jsonVersion: text()}, "id", jsonVersion)
}
func manifestSchema() schema {
	return candidateManifestSchema()
}
func discoverySchema() schema {
	slot := object(schema{jsonName: text(), "activation": enumeration("always_on", "on_demand", "child_context", "inline"), "operations": array(enumeration("put", "delete")), "media_types": array(text()), jsonMaxBytes: integer(1), "max_components": integer(1), "binding": bindingSchema(), "capabilities": array(text())}, jsonName, "activation", "operations", "media_types", jsonMaxBytes, "max_components", "binding")
	surface := object(schema{"id": text(), jsonVersion: text(), "max_total_bytes": integer(1), "slots": array(slot)}, "id", jsonVersion, "max_total_bytes", "slots")
	return object(schema{"surface": surface, jsonBaselineDigest: hexadecimal(64), jsonBaseline: array(componentRefSchema())}, "surface", jsonBaselineDigest, jsonBaseline)
}
func errorSchema() schema {
	return object(schema{"code": enumeration("invalid_input", "revision_conflict", "frozen", "not_found", "unsupported", "request_conflict", "executor_unavailable", "evidence_not_ready", "operation_failed"), "message": text(), "recovery": text(), "field": text()}, "code", "message", "recovery")
}
func entrySchema() schema {
	key := object(schema{"scenario": text(), "item": text(), "variant": text(), "repeat": integer(1)}, "scenario", "item", "variant", "repeat")
	provenance := object(schema{"experiment_id": text(), "attempt": key, "run_id": text(), "step": integer(0), "call_seq": integer(0), "call_attempt": integer(0), "candidate_digest": text(), jsonBaselineDigest: text(), "note": text()})
	return object(schema{jsonLocator: hexadecimal(64), jsonKind: enumeration("task_source", "baseline_component", "candidate_component", "component_diff", "run_view", "call_view", "deliverable", "decision_note", "evaluator_reference", "rendered"), "label": text(), jsonMediaType: text(), "state": enumeration("available", "missing", "withheld", "provisional"), "size": integer(0), jsonDigest: hexadecimal(64), "source_digest": hexadecimal(64), "renderer_version": text(), "source_locator": hexadecimal(64), "links": array(text()), "provenance": provenance, "incomplete": schema{jsonType: jsonBoolean}, "code": text()}, jsonLocator, jsonKind, "label", "state")
}
func comparisonSideSchema() schema {
	quality := object(schema{"passed": integer(0), "failed": integer(0), "errored": integer(0), "pending": integer(0), "not_run": integer(0)}, "passed", "failed", "errored", "pending", "not_run")
	return object(schema{"attempts": integer(0), "ran": integer(0), "median_tokens": integer(0), "median_duration_nanos": integer(0), "quality": quality}, "attempts", "ran", "median_tokens", "median_duration_nanos")
}
