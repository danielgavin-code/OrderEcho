// Package reporttest builds a fixed, sealed certification run directory for
// tests: the same bytes every time, so the report rendered from it can be
// compared byte for byte with a golden file.
package reporttest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/checks"
)

// RunID is the fixture's run id.
const RunID = "20261001-120000.000"

var checkNames = []string{"cum_qty_monotonic", "working_quantities", "terminal_quantities", "fill_quantities_sum", "avg_px",
	"exec_ids_unique", "order_id_constant", "nothing_after_terminal", "version_rules", "requests_answered", "framing_intact"}

func passChecks() []checks.Result {
	var out []checks.Result
	for _, n := range checkNames {
		out = append(out, checks.Result{Name: n, Status: checks.PASS, Explanation: n + " holds for every report of the chain", Rule: n})
	}
	return out
}

const fix41 = `20261001-12:00:01.100 OUT  seq=2    35=D  8=FIX.4.2|9=140|35=D|49=AGENT|56=ORDERECHO|34=2|52=20261001-12:00:01.100|11=OE-20261001-120000.000-1|21=1|55=AAPL|54=1|60=20261001-12:00:01.099|38=100|40=1|10=101|
20261001-12:00:01.600 IN   seq=2    35=8  8=FIX.4.2|9=250|35=8|49=ORDERECHO|56=AGENT|34=2|52=20261001-12:00:01.600|37=O-1|11=OE-20261001-120000.000-1|17=E-1|20=0|150=0|39=0|55=AAPL|54=1|38=100|40=1|32=0|31=0|151=100|14=0|6=0|10=102|
20261001-12:00:02.100 IN   seq=3    35=8  8=FIX.4.2|9=260|35=8|49=ORDERECHO|56=AGENT|34=3|52=20261001-12:00:02.100|37=O-1|11=OE-20261001-120000.000-1|17=E-2|20=0|150=2|39=2|55=AAPL|54=1|38=100|40=1|32=100|31=227.50|151=0|14=100|6=227.5000|10=103|
`

const fix78 = `20261001-12:00:05.000 OUT  seq=5    35=D  8=FIX.4.2|9=90|35=D|49=AGENT|56=ORDERECHO|34=5|52=20261001-12:00:05.000|11=OE-20261001-120000.000-R1|55=AAPL|10=104|  # injected: deliberately incomplete
20261001-12:00:05.200 IN   seq=5    35=j  8=FIX.4.2|9=110|35=j|49=ORDERECHO|56=AGENT|34=5|52=20261001-12:00:05.200|45=5|372=D|380=5|58=Conditionally required field missing|43=Y|10=105|
`

// Options vary the fixture.
type Options struct {
	// PendingManual leaves case 1.1 unattested (PENDING): the run is then
	// INCOMPLETE and 1.1 is attestable.
	PendingManual bool
}

// Build writes the fixture run into dir (dir is the run directory).
func Build(dir string, o Options) (*cert.RunResult, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	res := &cert.RunResult{
		Suite: "order-entry-fix42", SuiteFile: "certs/order_entry_fix42.yaml", Title: "Order Entry Certification — FIX 4.2 US Equities",
		Target: "orderecho-emulator", TargetFile: "certs/targets/emulator.yaml", Session: "emu42", FixVersion: "FIX.4.2",
		Version: "0.5.0", Build: "a5", RunID: RunID, Start: "2026-10-01T12:00:00.000Z", End: "2026-10-01T12:00:09.000Z",
		SenderCompID: "AGENT", TargetCompID: "ORDERECHO", Address: "127.0.0.1:9878", ControlAPI: "http://127.0.0.1:8090",
		Counterparty:    "OrderEcho FIX emulator 0.8.0 (cook8), control API http://127.0.0.1:8090",
		Sections:        []cert.Section{{ID: "ENV", Name: "Environment & Connectivity"}, {ID: "ORD", Name: "Order Entry"}, {ID: "REJ", Name: "Reject Scenarios"}, {ID: "SGN", Name: "Final Certification Sign-Off"}},
		RequiredInSuite: []string{"1.1", "4.1", "7.8", "9.1", "9.2"},
		Integrity:       cert.NewIntegrity(),
	}
	att := &cert.Attestation{Status: "pass", By: "Dana Ops", Note: "cert host and docs received 2026-09-30", At: "2026-10-01T12:00:08.000Z", File: "attest.yaml"}
	c11 := &cert.CaseResult{ID: "1.1", Section: "ENV", Title: "Cert environment details received", Task: "Obtain cert host, port and documentation package",
		Required: true, Level: "basic", Mode: cert.ModeManual, Attestable: true, Steps: []cert.StepResult{}, Orders: []string{}}
	if o.PendingManual {
		c11.Status, c11.Reason = cert.StatusPending, "needs a human attestation: Confirm the cert host, port and the venue's documentation package were received."
		c11.Steps = append(c11.Steps, cert.StepResult{Type: "manual", Status: cert.StatusPending, Detail: "Confirm the cert host, port and the venue's documentation package were received.", TS: "2026-10-01T12:00:00.100Z"})
	} else {
		c11.Status, c11.Reason, c11.Attestation = cert.StatusPass, "attested pass by Dana Ops: cert host and docs received 2026-09-30", att
		c11.Steps = append(c11.Steps, cert.StepResult{Type: "attestation", Status: cert.StatusPass, Detail: c11.Reason, TS: "2026-10-01T12:00:00.100Z"})
	}
	c41 := &cert.CaseResult{ID: "4.1", Section: "ORD", Title: "New Order Single — Market Buy", Task: "Submit a market buy order and confirm acknowledgement",
		Required: true, Level: "basic", Mode: cert.ModeAuto, Status: cert.StatusPass, Start: "2026-10-01T12:00:01.000Z", End: "2026-10-01T12:00:02.500Z",
		Steps: []cert.StepResult{
			{Type: "send", Status: cert.StatusPass, Detail: "D 11=OE-20261001-120000.000-1 buy 100 AAPL mkt", TS: "2026-10-01T12:00:01.100Z"},
			{Type: "expect", Status: cert.StatusPass, Detail: "matched 35=8 seq=2 11=OE-20261001-120000.000-1 150=0 39=0", TS: "2026-10-01T12:00:01.600Z"},
			{Type: "expect", Status: cert.StatusPass, Detail: "matched 35=8 seq=3 11=OE-20261001-120000.000-1 150=2 39=2 32=100 31=227.50", TS: "2026-10-01T12:00:02.100Z"},
			{Type: "checks", Status: cert.StatusPass, Detail: "timeline: 1 chain(s), all 11 checks PASS", TS: "2026-10-01T12:00:02.200Z"},
		},
		Orders: []string{"OE-20261001-120000.000-1"},
		Checks: []cert.CheckRecord{{Ref: "_1", ClOrdID: "OE-20261001-120000.000-1", Verdict: checks.PASS, Checks: passChecks()}}}
	c45 := &cert.CaseResult{ID: "4.5", Section: "ORD", Title: "NOS — GTC limit", Task: "Submit a GTC limit order", Required: false, Level: "basic",
		Mode: cert.ModeAuto, Status: cert.StatusNA, Reason: "target orderecho-emulator: Emulator supports TimeInForce Day only", Steps: []cert.StepResult{}, Orders: []string{}}
	c78 := &cert.CaseResult{ID: "7.8", Section: "REJ", Title: "Business Message Reject", Task: "Send a message missing a conditionally required field; expect 35=j <script>alert(1)</script>",
		Required: true, Level: "intermediate", Mode: cert.ModeAuto, Status: cert.StatusPass, Start: "2026-10-01T12:00:04.900Z", End: "2026-10-01T12:00:05.300Z",
		Warnings: []string{"step 3 (assert_received): recommended tag(s) absent: 379"},
		Steps: []cert.StepResult{
			{Type: "send", Status: cert.StatusPass, Detail: "raw seq=5 35=D|11=OE-20261001-120000.000-R1|55=AAPL", TS: "2026-10-01T12:00:05.000Z"},
			{Type: "expect", Status: cert.StatusPass, Detail: "matched 35=j seq=5 45=5 380=5", TS: "2026-10-01T12:00:05.200Z"},
			{Type: "assert_received", Status: cert.StepWarn, Detail: "35=j has 45, 380; recommended tag(s) absent: 379", TS: "2026-10-01T12:00:05.210Z"},
		},
		Orders: []string{"OE-20261001-120000.000-R1"}}
	c91 := &cert.CaseResult{ID: "9.1", Section: "SGN", Title: "All required tests passed", Task: "Confirm all Required tests have status = Pass before submitting for certification",
		Required: true, Level: "basic", Mode: cert.ModeAuto, Review: cert.ReviewRequiredCases, Orders: []string{}}
	c92 := &cert.CaseResult{ID: "9.2", Section: "SGN", Title: "Venue deviations documented", Task: "Document any exchange-specific behavior deviations from FIX 4.2 spec",
		Required: true, Level: "basic", Mode: cert.ModeAssisted, Review: cert.ReviewDeviations, Attestable: true, Orders: []string{},
		Attestation: &cert.Attestation{Status: "pass", By: "Dana Ops", Note: "draft reviewed, sent to the venue", At: "2026-10-01T12:00:08.500Z", File: "attest.yaml"}}
	res.Cases = []*cert.CaseResult{c11, c41, c45, c78, c91, c92}
	res.Deviations = cert.DraftDeviations(res)
	c92.Status, c92.Reason = cert.StatusPass, "attested pass by Dana Ops: draft reviewed, sent to the venue"
	c92.Steps = []cert.StepResult{{Type: "review", Status: cert.StatusPass, Detail: "deviations drafted: 1 N/A reason(s), 1 distinct warning(s) (deviations.md)", TS: "2026-10-01T12:00:09.000Z"},
		{Type: "attestation", Status: cert.StatusPass, Detail: c92.Reason, TS: "2026-10-01T12:00:09.000Z"}}
	if o.PendingManual {
		c91.Status, c91.Reason = cert.StatusPending, "1 of 4 other required case(s) are not PASS or N/A yet: 1.1 PENDING"
	} else {
		c91.Status, c91.Reason = cert.StatusPass, "all 4 other required case(s) are PASS or N/A"
	}
	c91.Steps = []cert.StepResult{{Type: "review", Status: c91.Status, Detail: c91.Reason, TS: "2026-10-01T12:00:09.000Z"}}
	cert.Tally(res)
	res.Exit = cert.ExitCode(res, 0)

	files := map[string]string{"4.1/fix.log": fix41, "7.8/fix.log": fix78,
		"4.1/evidence.jsonl": `{"kind":"event","detail":"cert case start: 4.1"}` + "\n" + `{"kind":"event","detail":"cert case end: 4.1 PASS"}` + "\n",
		"7.8/evidence.jsonl": `{"kind":"event","detail":"cert case start: 7.8"}` + "\n"}
	for _, p := range []string{"4.1/evidence.jsonl", "4.1/fix.log", "7.8/evidence.jsonl", "7.8/fix.log"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(files[p]), 0o644); err != nil {
			return nil, err
		}
		sum, err := cert.FileSHA256(full)
		if err != nil {
			return nil, err
		}
		res.Integrity.Files[p] = sum
	}
	if err := cert.WriteResults(dir, res); err != nil {
		return nil, fmt.Errorf("fixture: %v", err)
	}
	return res, nil
}
