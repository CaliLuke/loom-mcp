package eval

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// ShowOptions controls trajectory rendering and optional raw JSON exports.
type ShowOptions struct {
	Render trajectory.RenderOptions
	Full   bool
	Export bool
}

const reportEntailed = "entailed"

// Show renders one stored attempt and its session, degrading unavailable evidence.
func Show(w io.Writer, e *Experiment, a *StoredAttempt, opts ShowOptions) error {
	if e == nil || a == nil {
		return fmt.Errorf("eval: show requires an experiment and attempt")
	}
	var out strings.Builder
	reportHeader(&out, e.Header)
	fmt.Fprintf(&out, "Attempt #%d %s verdict %s duration %s tokens %d", a.Index, a.AttemptKey, a.Verdict, a.Duration, a.Tokens)
	if a.JudgeTokens != 0 {
		fmt.Fprintf(&out, " judge tokens %d", a.JudgeTokens)
	}
	fmt.Fprintf(&out, "\nTask: %s\nReference: %s\n", a.Task, a.Reference)
	showPrepared(&out, a)
	showErrors(&out, a)
	out.WriteString("Checks:\n")
	for _, check := range a.Checks {
		if check.Passed {
			fmt.Fprintf(&out, "  ok %s\n", check.Name)
		} else {
			fmt.Fprintf(&out, "  FAIL %s: %s\n", check.Name, check.Diagnostic)
		}
	}
	out.WriteString("Scores:\n")
	for _, score := range a.Scores {
		fmt.Fprintf(&out, "  %s %g", score.Name, score.Value)
		if score.Threshold != nil {
			fmt.Fprintf(&out, " threshold %g", *score.Threshold)
			if score.Value < *score.Threshold {
				out.WriteString(" BELOW")
			}
		}
		out.WriteByte('\n')
	}
	showClaims(&out, a)
	showSession(&out, a, opts)
	_, err := io.WriteString(w, out.String())
	return err
}

// Summary renders stored verdict groups failing first, followed by failing refs.
func Summary(w io.Writer, e *Experiment) error {
	if e == nil {
		return fmt.Errorf("eval: summary requires an experiment")
	}
	var out strings.Builder
	reportHeader(&out, e.Header)
	groups := summaryGroups(e)
	for _, g := range groups {
		if g.Probes > 0 {
			fmt.Fprintf(&out, "%s/%s [%s] quality not_assessed (%d probes, %d unassessed, %d error, %d not run)\n", g.Scenario, g.Item, g.Variant, g.Probes, g.Unassessed, g.Errored, g.NotRun)
			continue
		}
		fmt.Fprintf(&out, "%s/%s [%s] %d/%d passed (%d failed, %d error, %d pending, %d not run)\n", g.Scenario, g.Item, g.Variant, g.Passed, g.Ran, g.Failed, g.Errored, g.Pending, g.NotRun)
	}
	out.WriteString("Failing attempts:\n")
	for _, g := range groups {
		for _, a := range g.attempts {
			if reportFailure(a) {
				fmt.Fprintf(&out, "  %s/#%d %s %s: %s\n", e.Header.ID, a.Index, a.AttemptKey, a.Verdict, attemptReason(a))
			}
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func reportHeader(out *strings.Builder, h ExperimentHeader) {
	fmt.Fprintf(out, "Experiment %s %q %s", h.ID, h.Label, h.Status)
	if h.Stop != "" {
		fmt.Fprintf(out, " stop=%s", h.Stop)
	}
	fmt.Fprintf(out, " started %s rev %s", h.Started.Format(time.RFC3339), h.Source.Revision)
	if h.Source.Modified {
		out.WriteString(" (modified)")
	}
	out.WriteByte('\n')
	if h.ExecutionKind == ExecutionProbe {
		out.WriteString("Quality: not_assessed\n")
	}
	if h.Judge != nil {
		calibrated := "yes"
		if !h.Judge.Calibrated {
			calibrated = "NO (judgments marked uncalibrated)"
		}
		hash := strings.TrimPrefix(h.Judge.PromptHash, "sha256:")
		fmt.Fprintf(out, "Judge %s prompt sha256:%s calibrated %s\n", h.Judge.ModelID, hash[:min(12, len(hash))], calibrated)
	}
}

func showErrors(out *strings.Builder, a *StoredAttempt) {
	if a.HarnessError == "" && a.Error == "" && a.StorageError == "" {
		return
	}
	out.WriteString("Errors:\n")
	for _, field := range []struct{ name, value string }{{"harness_error", a.HarnessError}, {"error", a.Error}, {"storage_error", a.StorageError}} {
		if field.value != "" {
			fmt.Fprintf(out, "  %s: %s\n", field.name, field.value)
		}
	}
}

func showClaims(out *strings.Builder, a *StoredAttempt) {
	out.WriteString("Claims:\n")
	claimed := make(map[string]bool, len(a.Claims))
	for _, claim := range a.Claims {
		claimed[claim.ID] = true
		fmt.Fprintf(out, "  %s: %s\n", claim.ID, claim.Text)
		found := false
		for _, j := range a.Judgments {
			if j.ClaimID == claim.ID {
				showJudgment(out, claim, j)
				found = true
			}
		}
		if !found {
			showJudgment(out, claim, StoredJudgment{ClaimID: claim.ID, Subject: claim.Subject, Informational: claim.Informational})
		}
	}
	orphans := false
	for _, j := range a.Judgments {
		if claimed[j.ClaimID] {
			continue
		}
		if !orphans {
			out.WriteString("Orphan judgments:\n")
			orphans = true
		}
		showJudgment(out, StoredClaim{}, j)
	}
}

func showJudgment(out *strings.Builder, claim StoredClaim, j StoredJudgment) {
	label, reason := j.Label, j.Rationale
	if label == "" {
		label = "unjudged"
	}
	if j.Error != "" {
		label, reason = "error", j.Error
	}
	subject := claimSubject(j.Subject)
	if j.Subject == "" {
		subject = claimSubject(claim.Subject)
	}
	fmt.Fprintf(out, "    [%s] %s (%s", label, j.ClaimID, subject)
	if j.Step != nil {
		fmt.Fprintf(out, " step r%ds%d %s %s", j.Step.Run, j.Step.Index, j.Step.Tool, j.Step.CallID)
	}
	out.WriteByte(')')
	if claim.Informational || j.Informational {
		out.WriteString(" informational")
	}
	if j.Uncalibrated {
		out.WriteString(" uncalibrated")
	}
	if j.Truncated {
		out.WriteString(" truncated")
	}
	fmt.Fprintf(out, ": %s\n", reason)
}

func showSession(out *strings.Builder, a *StoredAttempt, opts ShowOptions) {
	for i, run := range a.Runs {
		fmt.Fprintf(out, "Run %d/%d %s session %s", i+1, len(a.Runs), run.RunID, run.SessionID)
		if run.TraceID != "" {
			fmt.Fprintf(out, " trace %s", run.TraceID)
		}
		out.WriteByte('\n')
	}
	session, err := a.Session()
	if err == nil && len(a.Runs) == 0 && a.StorageError != "" {
		err = fmt.Errorf("%s", a.StorageError)
	}
	var rendered string
	if err == nil {
		render := opts.Render
		if opts.Full {
			render.Limits = trajectory.Limits{}
		} else if render.Limits == (trajectory.Limits{}) {
			render.Limits = trajectory.DefaultLimits()
		}
		rendered, err = renderReportSession(session, render)
	}
	if err != nil {
		fmt.Fprintf(out, "trajectory unavailable: %s\n", err)
		return
	}
	out.WriteString(rendered)
}

// renderReportSession contains the renderer's documented fail-fast panic boundary.
func renderReportSession(session trajectory.Session, opts trajectory.RenderOptions) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("render: %v", recovered)
		}
	}()
	return session.Render(opts), nil
}

func summaryGroups(e *Experiment) []reportGroup {
	groups := reportGroups(e.Attempts)
	result := make([]reportGroup, 0, len(groups))
	for _, failing := range []bool{true, false} {
		for _, g := range groups {
			if (g.Passed+g.Unassessed != g.Attempts) == failing {
				result = append(result, g)
			}
		}
	}
	return result
}

func attemptReason(a *StoredAttempt) string {
	for _, check := range a.Checks {
		if !check.Passed {
			return check.Name + ": " + check.Diagnostic
		}
	}
	unjudged := 0
	for _, claim := range a.Claims {
		if claim.Informational {
			continue
		}
		found := false
		for _, j := range a.Judgments {
			if j.ClaimID != claim.ID || j.Informational {
				continue
			}
			found = true
			if j.Label != reportEntailed && j.Error == "" && j.Label != "" {
				return claim.ID + ": " + j.Rationale
			}
		}
		if !found {
			unjudged++
		}
	}
	for _, message := range []string{a.HarnessError, a.Error, a.StorageError} {
		if message != "" {
			return message
		}
	}
	for _, j := range a.Judgments {
		if !j.Informational && j.Error != "" {
			return j.Error
		}
	}
	if a.Verdict == VerdictNotRun {
		return string(a.NotRun)
	}
	return fmt.Sprintf("%d gating claims unjudged", unjudged)
}

func claimSubject(subject string) string {
	if subject == "" {
		return "answer"
	}
	return subject
}

func reportVersion(h ExperimentHeader) int {
	if h.Version == 2 {
		return 2
	}
	return 1
}

func reportFailure(a *StoredAttempt) bool {
	return a.Verdict != VerdictPassed && a.Verdict != VerdictUnassessed
}

func showPrepared(out *strings.Builder, a *StoredAttempt) {
	if a.Probe != nil {
		fmt.Fprintf(out, "Probe execution %s; delivery %s; evidence %s; quality %s\n", a.Probe.Execution, a.Probe.Delivery, a.Probe.Evidence, a.Probe.Quality)
	}
	if a.Prepared != nil {
		fmt.Fprintf(out, "Prepared candidate %s surface %s/%s environment %s/%s\n", a.Prepared.CandidateDigest, a.Prepared.SurfaceID, a.Prepared.SurfaceVersion, a.Prepared.Environment.Mode, a.Prepared.Environment.SnapshotID)
	}
	for _, artifact := range a.Artifacts {
		fmt.Fprintf(out, "Artifact %s %s %d bytes sha256:%s", artifact.Name, artifact.MediaType, artifact.Size, artifact.Digest)
		if artifact.Provisional {
			out.WriteString(" provisional")
		}
		out.WriteByte('\n')
	}
}
