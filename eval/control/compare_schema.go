package control

const (
	jsonBaselineJobID   = "baseline_job_id"
	jsonCandidateJobID  = "candidate_job_id"
	jsonBaselineJobIDs  = "baseline_job_ids"
	jsonCandidateJobIDs = "candidate_job_ids"
)

// comparisonRequestSchema keeps scalar and grouped selection presence exclusive.
func comparisonRequestSchema() schema {
	ids := func() schema {
		s := array(nonempty())
		s["minItems"] = 1
		s["uniqueItems"] = true
		return s
	}
	o := object(schema{jsonBaselineJobID: nonempty(), jsonCandidateJobID: nonempty(), jsonBaselineJobIDs: ids(), jsonCandidateJobIDs: ids()})
	o["oneOf"] = []schema{
		{jsonRequired: []string{jsonBaselineJobID, jsonCandidateJobID}, jsonNot: schema{jsonAnyOf: []schema{{jsonRequired: []string{jsonBaselineJobIDs}}, {jsonRequired: []string{jsonCandidateJobIDs}}}}},
		{jsonRequired: []string{jsonBaselineJobIDs, jsonCandidateJobIDs}, jsonNot: schema{jsonAnyOf: []schema{{jsonRequired: []string{jsonBaselineJobID}}, {jsonRequired: []string{jsonCandidateJobID}}}}},
	}
	return o
}

func comparisonResultSchema() schema {
	member := object(schema{jsonJobId: nonempty(), jsonState: enumeration("succeeded", "failed", "canceled", "interrupted")}, jsonJobId, jsonState)
	group := object(schema{"baseline_jobs": array(member), "candidate_jobs": array(member), jsonBaseline: comparisonSideSchema(), jsonCandidate: comparisonSideSchema()}, "baseline_jobs", "candidate_jobs", jsonBaseline, jsonCandidate)
	return object(schema{"status": enumeration("comparable", "not_comparable"), jsonBaseline: comparisonSideSchema(), jsonCandidate: comparisonSideSchema(), "warning_codes": array(text()), "groups": array(group), "only_baseline": array(member), "only_candidate": array(member)}, "status", jsonBaseline, jsonCandidate)
}
