package cert

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A4 §3: the sign-off rows 9.1 and 9.2 are decided over the whole run.
// Finalize evaluates them after every other case (and again whenever an
// attestation changes a result); Attest records a human attestation on a
// finished run and recomputes everything that depends on it.

// DeviationGroup is one distinct reason or warning and the cases showing it.
type DeviationGroup struct {
	Text  string   `json:"text"`
	Cases []string `json:"cases"`
}

// Deviations is the drafted deviations section (case 9.2).
type Deviations struct {
	NotApplicable []DeviationGroup `json:"not_applicable"`
	Warnings      []DeviationGroup `json:"warnings"`
}

// Empty reports whether the draft has nothing to say.
func (d *Deviations) Empty() bool { return len(d.NotApplicable) == 0 && len(d.Warnings) == 0 }

// stepPrefix is how the runner prefixes a step's warning ("step 4 (checks): ").
var stepPrefix = regexp.MustCompile(`^step \d+ \([a-z_]+\): `)

// DraftDeviations groups every N/A reason and every warning of the run's
// ordinary cases. Notes about attestations are the runner talking about
// itself, not the venue, and are left out.
func DraftDeviations(res *RunResult) *Deviations {
	d := &Deviations{NotApplicable: []DeviationGroup{}, Warnings: []DeviationGroup{}}
	add := func(groups *[]DeviationGroup, text, id string) {
		for i := range *groups {
			if (*groups)[i].Text == text {
				g := &(*groups)[i]
				if g.Cases[len(g.Cases)-1] != id {
					g.Cases = append(g.Cases, id)
				}
				return
			}
		}
		*groups = append(*groups, DeviationGroup{Text: text, Cases: []string{id}})
	}
	for _, c := range res.Cases {
		if c.Review != "" {
			continue
		}
		if c.Status == StatusNA && c.Reason != "" {
			add(&d.NotApplicable, c.Reason, c.ID)
		}
		for _, w := range c.Warnings {
			if strings.HasPrefix(w, "attestation ") {
				continue
			}
			add(&d.Warnings, stepPrefix.ReplaceAllString(w, ""), c.ID)
		}
	}
	return d
}

// DeviationsMarkdown renders the draft for deviations.md.
func DeviationsMarkdown(res *RunResult) string {
	d := res.Deviations
	if d == nil {
		return ""
	}
	var b strings.Builder
	caseID := "9.2"
	status := ""
	for _, c := range res.Cases {
		if c.Review == ReviewDeviations {
			caseID, status = c.ID, c.Status
		}
	}
	fmt.Fprintf(&b, "# Venue deviations — draft (case %s)\n\n", caseID)
	fmt.Fprintf(&b, "Suite %s (%s), target %s, session %s, run %s.\n\n", res.Suite, res.FixVersion, res.Target, res.Session, res.RunID)
	fmt.Fprintf(&b, "Drafted by OrderEcho from this run's N/A reasons and warnings. A human must review it, edit it into the venue's deviations, and attest case %s. Case %s is currently **%s**.\n\n", caseID, caseID, status)
	section := func(title string, groups []DeviationGroup) {
		n := 0
		for _, g := range groups {
			n += len(g.Cases)
		}
		fmt.Fprintf(&b, "## %s (%d distinct, %d case(s))\n\n", title, len(groups), n)
		if len(groups) == 0 {
			b.WriteString("None.\n\n")
			return
		}
		for _, g := range groups {
			fmt.Fprintf(&b, "- %s — case(s) %s\n", g.Text, strings.Join(g.Cases, ", "))
		}
		b.WriteString("\n")
	}
	section("Not applicable", d.NotApplicable)
	section("Warnings", d.Warnings)
	return b.String()
}

// Finalize decides the review cases from the other cases' results and
// recomputes the counts. It is idempotent.
func Finalize(res *RunResult) {
	now := ts(time.Now())
	hasDeviations := false
	for _, c := range res.Cases {
		if c.Review == ReviewDeviations {
			hasDeviations = true
		}
	}
	if hasDeviations {
		res.Deviations = DraftDeviations(res)
	}
	for _, c := range res.Cases {
		if c.Review != ReviewDeviations {
			continue
		}
		d := res.Deviations
		draft := fmt.Sprintf("deviations drafted: %d N/A reason(s), %d distinct warning(s) (deviations.md)", len(d.NotApplicable), len(d.Warnings))
		c.Steps = []StepResult{{Type: "review", Status: StatusPass, Detail: draft, TS: now}}
		if c.Attestation != nil {
			applyAttestation(c, *c.Attestation, now)
		} else {
			c.Status = StatusPending
			c.Reason = draft + "; needs a human to review the draft and attest this case"
		}
		c.End = now
	}
	// Then the required-cases review, which may depend on the deviations case.
	for _, c := range res.Cases {
		if c.Review != ReviewRequiredCases {
			continue
		}
		byID := map[string]*CaseResult{}
		for _, o := range res.Cases {
			byID[o.ID] = o
		}
		var failing, waiting []string
		checked := 0
		for _, id := range res.RequiredInSuite {
			o := byID[id]
			if o != nil && o.Review == ReviewRequiredCases {
				continue
			}
			checked++
			switch {
			case o == nil:
				waiting = append(waiting, id+" not run in this run")
			case o.Status == StatusPass || o.Status == StatusNA:
			case o.Status == StatusFail || o.Status == StatusError:
				failing = append(failing, id+" "+o.Status)
			default:
				waiting = append(waiting, id+" "+o.Status)
			}
		}
		notOK := append(append([]string{}, failing...), waiting...)
		switch {
		case len(notOK) == 0:
			c.Status = StatusPass
			c.Reason = fmt.Sprintf("all %d other required case(s) are PASS or N/A", checked)
		case len(failing) > 0:
			c.Status = StatusFail
			c.Reason = fmt.Sprintf("%d of %d other required case(s) are not PASS or N/A: %s", len(notOK), checked, strings.Join(notOK, ", "))
		default:
			c.Status = StatusPending
			c.Reason = fmt.Sprintf("%d of %d other required case(s) are not PASS or N/A yet: %s", len(notOK), checked, strings.Join(notOK, ", "))
		}
		stepStatus := c.Status
		c.Steps = []StepResult{{Type: "review", Status: stepStatus, Detail: c.Reason, TS: now}}
		if c.Start == "" {
			c.Start = now
		}
		c.End = now
	}
	Tally(res)
}

// AttestError is a refused attestation, with what to do instead.
type AttestError struct {
	Code   string // not_found | not_attestable | invalid
	Detail string
	Hint   string
}

func (e *AttestError) Error() string { return e.Detail }

// Attest records a human attestation on case id of a finished run, then
// re-decides the review cases and the counts. Only cases a human decides
// (Attestable) accept one; a later attestation replaces an earlier one.
func Attest(res *RunResult, id string, a Attestation) error {
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	if a.Status != "pass" && a.Status != "fail" && a.Status != "na" {
		return &AttestError{Code: "invalid", Detail: fmt.Sprintf("status must be pass, fail or na, got %q", a.Status),
			Hint: "use status pass, fail or na"}
	}
	if strings.TrimSpace(a.By) == "" {
		return &AttestError{Code: "invalid", Detail: "'by' (who attests) is required", Hint: "set by to the name of the human who confirmed it"}
	}
	var c *CaseResult
	var ids []string
	for _, x := range res.Cases {
		if x.ID == id {
			c = x
		}
		if x.Attestable {
			ids = append(ids, x.ID)
		}
	}
	if c == nil {
		return &AttestError{Code: "not_found", Detail: fmt.Sprintf("run %s has no case %q", res.RunID, id),
			Hint: "attestable cases in this run: " + strings.Join(ids, ", ")}
	}
	if !c.Attestable {
		why := "it is decided by code"
		switch {
		case c.Status == StatusNA && strings.HasPrefix(c.Reason, "target "):
			why = "the target marks it N/A (" + c.Reason + ")"
		case c.Mode == ModeAssisted:
			why = "its control steps ran against the counterparty's control API, so the run decided it"
		case c.Review == ReviewRequiredCases:
			why = "it is decided by code from the other required cases (attest those instead)"
		case c.Status == StatusNotRun:
			why = "it was not run"
		}
		return &AttestError{Code: "not_attestable", Detail: fmt.Sprintf("case %s (%s, %s) takes no attestation: %s", c.ID, c.Mode, c.Status, why),
			Hint: "attestable cases in this run: " + strings.Join(ids, ", ")}
	}
	now := ts(time.Now())
	if a.At == "" {
		a.At = now
	}
	if c.Review == "" {
		// Drop an earlier attestation step; the new one replaces it.
		var kept []StepResult
		for _, s := range c.Steps {
			if s.Type != "attestation" {
				kept = append(kept, s)
			}
		}
		c.Steps = kept
		applyAttestation(c, a, now)
	} else {
		c.Attestation = &a
	}
	Finalize(res)
	return nil
}
