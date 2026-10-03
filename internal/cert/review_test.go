package cert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A4 §3: 9.1 is auto (PASS iff every required case is PASS or N/A, listing
// the ones that aren't) and 9.2 is assisted (the runner drafts the
// deviations; an attestation still decides it).

const reviewCases = `  - { id: "m1", section: ORD, title: manual, task: t, required: true, level: basic, mode: manual, steps: [ { manual: { prompt: "confirm it" } } ] }
  - { id: "n1", section: ORD, title: n/a one, task: t, required: true, level: basic, mode: auto, steps: [ { session: testreq } ] }
  - { id: "n2", section: ORD, title: n/a two, task: t, required: false, level: basic, mode: auto, steps: [ { session: testreq } ] }
  - { id: "9.1", section: ORD, title: all required passed, task: t, required: true, level: basic, mode: auto, steps: [ { review: required_cases } ] }
  - { id: "9.2", section: ORD, title: deviations, task: t, required: true, level: basic, mode: assisted, steps: [ { review: deviations } ] }
  - { id: "o1", section: ORD, title: optional manual, task: t, required: false, level: basic, mode: manual, steps: [ { manual: { prompt: "optional" } } ] }
`

func reviewTarget() *Target {
	return &Target{Name: "venue", Vars: map[string]string{}, Sessions: map[string]string{},
		NotApplicable: map[string]string{"n1": "no TIF other than Day", "n2": "no TIF other than Day"}}
}

func byID(r *RunResult) map[string]*CaseResult {
	m := map[string]*CaseResult{}
	for _, c := range r.Cases {
		m[c.ID] = c
	}
	return m
}

func TestReviewCasesValidation(t *testing.T) {
	bad := map[string]string{
		`  - { id: "r1", section: ORD, title: x, task: y, required: true, level: basic, mode: manual, steps: [ { review: required_cases } ] }`:                     `review: required_cases belongs in a auto case, not manual`,
		`  - { id: "r2", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { review: deviations } ] }`:                           `review: deviations belongs in a assisted case, not auto`,
		`  - { id: "r3", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { review: required_cases }, { session: testreq } ] }`: `a review step must be the case's only step`,
		`  - { id: "r4", section: ORD, title: x, task: y, required: true, level: basic, mode: auto, steps: [ { review: everything } ] }`:                           `review must be 'required_cases' or 'deviations'`,
	}
	for body, want := range bad {
		p := filepath.Join(t.TempDir(), "s.yaml")
		os.WriteFile(p, []byte(head+body+"\n"), 0o644)
		if _, err := LoadSuite(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	// The shipped suite: 9.1 auto/required_cases, 9.2 assisted/deviations.
	s, err := LoadSuite("../../certs/order_entry_fix42.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Case("9.1"); c.Mode != ModeAuto || c.ReviewOf() != ReviewRequiredCases {
		t.Fatalf("9.1 %s %q", c.Mode, c.ReviewOf())
	}
	if c := s.Case("9.2"); c.Mode != ModeAssisted || c.ReviewOf() != ReviewDeviations {
		t.Fatalf("9.2 %s %q", c.Mode, c.ReviewOf())
	}
}

func TestRequiredCasesReviewAndDeviationsDraft(t *testing.T) {
	s := suiteFrom(t, head+reviewCases)
	f := newFake(t, "FIX.4.2")
	var progress []string
	r := runWith(f, s, Options{Target: reviewTarget(), Progress: func(done, total int, c *CaseResult) {
		progress = append(progress, c.ID)
	}})
	st := byID(r)
	// Without attestations: m1 and 9.2 wait for a human, so 9.1 waits too.
	if st["m1"].Status != StatusPending || st["9.2"].Status != StatusPending {
		t.Fatalf("m1 %s, 9.2 %s", st["m1"].Status, st["9.2"].Status)
	}
	if c := st["9.1"]; c.Status != StatusPending || c.Reason != "2 of 3 other required case(s) are not PASS or N/A yet: m1 PENDING, 9.2 PENDING" {
		t.Fatalf("9.1 %s %q", c.Status, c.Reason)
	}
	if ExitCode(r, 0) != ExitIncomplete {
		t.Fatalf("exit %d", ExitCode(r, 0))
	}
	// Reviews are decided (and reported) after every other case; results
	// stay in suite order.
	if strings.Join(progress, ",") != "m1,n1,n2,o1,9.1,9.2" {
		t.Fatalf("progress %v", progress)
	}
	var order []string
	for _, c := range r.Cases {
		order = append(order, c.ID)
	}
	if strings.Join(order, ",") != "m1,n1,n2,9.1,9.2,o1" {
		t.Fatalf("order %v", order)
	}
	// 9.2's draft groups the N/A reasons (and warnings), case lists included.
	d := r.Deviations
	if d == nil || len(d.NotApplicable) != 1 || strings.Join(d.NotApplicable[0].Cases, ",") != "n1,n2" ||
		d.NotApplicable[0].Text != "target venue: no TIF other than Day" {
		t.Fatalf("deviations %+v", d)
	}
	if !st["9.2"].Attestable || st["9.1"].Attestable || !st["m1"].Attestable || st["n1"].Attestable {
		t.Fatal("attestable flags")
	}
	if !strings.Contains(st["9.2"].Reason, "deviations drafted: 1 N/A reason(s), 0 distinct warning(s)") ||
		!strings.Contains(st["9.2"].Reason, "needs a human") {
		t.Fatalf("9.2 reason %q", st["9.2"].Reason)
	}
	dir := t.TempDir()
	if err := WriteResults(dir, r); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "deviations.md"))
	if err != nil || !strings.Contains(string(md), "- target venue: no TIF other than Day — case(s) n1, n2") ||
		!strings.Contains(string(md), "Case 9.2 is currently **PENDING**") {
		t.Fatalf("deviations.md %v:\n%s", err, md)
	}

	// With both attested (the file may also name 9.1: ignored, noted), 9.1 passes.
	att := map[string]Attestation{
		"m1":  {Status: "pass", By: "Dan", Note: "checked"},
		"9.2": {Status: "pass", By: "Dan", Note: "reviewed the draft"},
		"9.1": {Status: "fail", By: "Dan", Note: "should be ignored"},
	}
	r2 := runWith(newFake(t, "FIX.4.2"), s, Options{Target: reviewTarget(), Attest: att})
	st = byID(r2)
	if st["9.2"].Status != StatusPass || st["9.2"].Reason != "attested pass by Dan: reviewed the draft" {
		t.Fatalf("9.2 %s %q", st["9.2"].Status, st["9.2"].Reason)
	}
	if c := st["9.1"]; c.Status != StatusPass || c.Reason != "all 3 other required case(s) are PASS or N/A" ||
		len(c.Warnings) != 1 || !strings.HasPrefix(c.Warnings[0], "attestation ignored") {
		t.Fatalf("9.1 %s %q %v", c.Status, c.Reason, c.Warnings)
	}
	// The ignored-attestation note is about the runner, not the venue.
	if len(r2.Deviations.Warnings) != 0 {
		t.Fatalf("warnings %+v", r2.Deviations.Warnings)
	}
	if ExitCode(r2, 0) != ExitOK {
		t.Fatalf("exit %d", ExitCode(r2, 0))
	}

	// A required FAIL makes 9.1 FAIL (and names it); a partial run can't pass 9.1.
	r3 := runWith(newFake(t, "FIX.4.2"), s, Options{Target: reviewTarget(), Attest: map[string]Attestation{
		"m1": {Status: "fail", By: "Dan", Note: "not done"}, "9.2": {Status: "pass", By: "Dan", Note: "ok"}}})
	if c := byID(r3)["9.1"]; c.Status != StatusFail || !strings.Contains(c.Reason, "m1 FAIL") {
		t.Fatalf("9.1 %s %q", c.Status, c.Reason)
	}
	r4 := runWith(newFake(t, "FIX.4.2"), s, Options{Target: reviewTarget(), Attest: att,
		Select: func(c *Case) bool { return c.ID == "9.1" || c.ID == "9.2" }})
	if c := byID(r4)["9.1"]; c.Status != StatusPending || !strings.Contains(c.Reason, "m1 not run in this run") || !strings.Contains(c.Reason, "n1 not run in this run") {
		t.Fatalf("9.1 %s %q", c.Status, c.Reason)
	}
}

func TestDeviationsGroupWarnings(t *testing.T) {
	res := &RunResult{Cases: []*CaseResult{
		{ID: "7.8", Status: StatusPass, Warnings: []string{"step 3 (assert_received): tag 379 missing (recommended)"}},
		{ID: "7.9", Status: StatusPass, Warnings: []string{"step 5 (assert_received): tag 379 missing (recommended)", "attestation recorded but not used: x"}},
		{ID: "5.1", Status: StatusNA, Reason: "target emu: never sends Pending New"},
		{ID: "9.2", Review: ReviewDeviations, Attestable: true},
	}}
	Finalize(res)
	d := res.Deviations
	if len(d.Warnings) != 1 || d.Warnings[0].Text != "tag 379 missing (recommended)" || strings.Join(d.Warnings[0].Cases, ",") != "7.8,7.9" {
		t.Fatalf("%+v", d.Warnings)
	}
	if len(d.NotApplicable) != 1 || d.NotApplicable[0].Cases[0] != "5.1" {
		t.Fatalf("%+v", d.NotApplicable)
	}
	if res.Cases[3].Status != StatusPending || res.Counts[StatusPending] != 1 {
		t.Fatalf("%+v %v", res.Cases[3], res.Counts)
	}
}

func TestAttestAfterTheRun(t *testing.T) {
	s := suiteFrom(t, head+reviewCases+
		`  - { id: "a1", section: ORD, title: auto, task: t, required: false, level: basic, mode: auto, steps: [ { session: testreq } ] }
`)
	r := runWith(newFake(t, "FIX.4.2"), s, Options{Target: reviewTarget()})
	if ExitCode(r, 0) != ExitIncomplete {
		t.Fatalf("exit %d", ExitCode(r, 0))
	}
	a := Attestation{Status: "pass", By: "Dan", Note: "confirmed on the phone"}
	for id, want := range map[string]string{
		"a1":   "takes no attestation: it is decided by code",
		"n1":   "the target marks it N/A",
		"9.1":  "decided by code from the other required cases",
		"zz":   `has no case "zz"`,
		"9.2x": `has no case "9.2x"`,
	} {
		err := Attest(r, id, a)
		ae, ok := err.(*AttestError)
		if !ok || !strings.Contains(ae.Detail, want) || !strings.Contains(ae.Hint, "attestable cases in this run: m1, 9.2, o1") {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err := Attest(r, "m1", Attestation{Status: "maybe", By: "Dan"}); err == nil {
		t.Fatal("bad status accepted")
	}
	if err := Attest(r, "m1", Attestation{Status: "pass"}); err == nil {
		t.Fatal("missing by accepted")
	}
	if err := Attest(r, "m1", a); err != nil {
		t.Fatal(err)
	}
	st := byID(r)
	if st["m1"].Status != StatusPass || st["m1"].Attestation.At == "" || st["9.1"].Status != StatusPending ||
		!strings.Contains(st["9.1"].Reason, "9.2 PENDING") || strings.Contains(st["9.1"].Reason, "m1") {
		t.Fatalf("m1 %s, 9.1 %s %q", st["m1"].Status, st["9.1"].Status, st["9.1"].Reason)
	}
	if err := Attest(r, "9.2", Attestation{Status: "PASS", By: "Dan", Note: "draft reviewed"}); err != nil {
		t.Fatal(err)
	}
	st = byID(r)
	if st["9.2"].Status != StatusPass || st["9.1"].Status != StatusPass || r.Counts[StatusPending] != 1 || ExitCode(r, 0) != ExitOK {
		t.Fatalf("9.2 %s 9.1 %s counts %v exit %d", st["9.2"].Status, st["9.1"].Status, r.Counts, ExitCode(r, 0))
	}
	// A later attestation replaces the earlier one (one attestation step).
	if err := Attest(r, "m1", Attestation{Status: "fail", By: "Ops", Note: "not after all"}); err != nil {
		t.Fatal(err)
	}
	st = byID(r)
	n := 0
	for _, s := range st["m1"].Steps {
		if s.Type == "attestation" {
			n++
		}
	}
	if st["m1"].Status != StatusFail || n != 1 || st["9.1"].Status != StatusFail || ExitCode(r, 0) != ExitFail {
		t.Fatalf("m1 %s (%d attestation steps), 9.1 %s, exit %d", st["m1"].Status, n, st["9.1"].Status, ExitCode(r, 0))
	}
	// Run-level errors and a logout timeout survive a results.json round trip.
	r.RunError, r.LogoutTimeout = "service restarted", true
	if ExitCode(r, 0) != ExitRunnerError {
		t.Fatal("run error")
	}
}
