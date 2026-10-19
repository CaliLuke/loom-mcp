package control

const (
	jsonComponents    = "components"
	jsonReplace       = "replace"
	jsonPlanDigest    = "plan_digest"
	jsonOrigins       = "origins"
	jsonSelected      = "selected"
	jsonClosure       = "closure"
	jsonRevision      = "revision"
	jsonSurfaceDigest = "surface_digest"
)

func copyRequestSchema(op Operation) schema {
	selected := array(nonempty())
	selected["minItems"] = 1
	selected["uniqueItems"] = true
	props := schema{jsonDraftId: hexadecimal(32), jsonExpectedRevision: integer(1), jsonSourceCandidate: hexadecimal(64), jsonComponents: selected}
	required := []string{jsonDraftId, jsonExpectedRevision, jsonSourceCandidate, jsonComponents}
	if op == CopyOperation {
		replacements := array(nonempty())
		replacements["uniqueItems"] = true
		props[jsonReplace] = replacements
		props[jsonPlanDigest] = hexadecimal(64)
		required = append(required, jsonReplace, jsonPlanDigest)
	}
	return object(props, required...)
}

func copyPlanSchema() schema {
	return object(schema{
		jsonDigest: hexadecimal(64), jsonVersion: schema{jsonConst: 1},
		jsonDraftId: hexadecimal(32), jsonRevision: integer(1), "destination_digest": hexadecimal(64),
		jsonSurfaceDigest: hexadecimal(64), jsonBaselineDigest: hexadecimal(64), jsonSourceCandidate: hexadecimal(64),
		jsonSelected: array(nonempty()), jsonClosure: array(componentRefSchema()), jsonReplace: array(nonempty()),
	}, jsonDigest, jsonVersion, jsonDraftId, jsonRevision, "destination_digest", jsonSurfaceDigest, jsonBaselineDigest, jsonSourceCandidate, jsonSelected, jsonClosure, jsonReplace)
}

func componentOriginSchema() schema {
	return object(schema{jsonName: text(), jsonSourceCandidate: hexadecimal(64), "source_digest": hexadecimal(64)}, jsonName, jsonSourceCandidate, "source_digest")
}

func copyResultSchema() schema {
	return object(schema{jsonCandidate: resultSchema(GetOperation), jsonPlanDigest: hexadecimal(64), jsonSelected: array(nonempty()), jsonClosure: array(nonempty()), jsonOrigins: array(componentOriginSchema())}, jsonCandidate, jsonPlanDigest, jsonSelected, jsonClosure, jsonOrigins)
}

func candidateManifestSchema() schema {
	origins := array(componentOriginSchema())
	origins["minItems"] = 1
	out := object(schema{jsonVersion: schema{jsonType: "integer", "enum": []int{1, 2}}, "surface_id": text(), "surface_version": text(), jsonSurfaceDigest: hexadecimal(64), jsonBaselineDigest: hexadecimal(64), "bindings": array(bindingSchema()), jsonComponents: array(componentRefSchema()), jsonOrigins: origins}, jsonVersion, "surface_id", "surface_version", jsonSurfaceDigest, "bindings", jsonComponents)
	out["oneOf"] = []schema{
		{jsonProperties: schema{jsonVersion: schema{jsonConst: 1}}, jsonNot: schema{jsonRequired: []string{jsonOrigins}}},
		{jsonProperties: schema{jsonVersion: schema{jsonConst: 2}}, jsonRequired: []string{jsonOrigins}},
	}
	return out
}
