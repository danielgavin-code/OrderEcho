package cert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// Case statuses.
const (
	StatusPass    = "PASS"
	StatusFail    = "FAIL"
	StatusBlocked = "BLOCKED"
	StatusNA      = "N/A"
	StatusPending = "PENDING"
	StatusError   = "ERROR"
	StatusNotRun  = "NOT_RUN" // skipped by --stop-on-fail
)

// Step statuses (plus PASS/FAIL/ERROR/BLOCKED).
const (
	StepWarn    = "WARN"
	StepSkipped = "SKIPPED"
)

// StepResult is one executed step.
type StepResult struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	TS     string `json:"ts"`
}

// CheckRecord is one chain's check results, for results.json.
type CheckRecord struct {
	Ref     string          `json:"ref"`
	ClOrdID string          `json:"cl_ord_id"`
	Verdict string          `json:"verdict"`
	Checks  []checks.Result `json:"checks"`
}

// CaseResult is one case's outcome.
type CaseResult struct {
	ID          string        `json:"id"`
	Section     string        `json:"section"`
	Title       string        `json:"title"`
	Task        string        `json:"task"`
	Required    bool          `json:"required"`
	Level       string        `json:"level"`
	Mode        string        `json:"mode"`
	Status      string        `json:"status"`
	Reason      string        `json:"reason"`
	Warnings    []string      `json:"warnings,omitempty"`
	Steps       []StepResult  `json:"steps"`
	Orders      []string      `json:"orders"`
	Checks      []CheckRecord `json:"checks,omitempty"`
	Attestation *Attestation  `json:"attestation,omitempty"`
	// Attestable: a human attestation decides this case (manual, assisted
	// without an executable control step, the deviations review). Auto
	// cases, assisted cases whose control steps ran, and target N/A cases
	// are decided by the run.
	Attestable bool   `json:"attestable"`
	Review     string `json:"review,omitempty"` // required_cases | deviations
	Start      string `json:"start,omitempty"`
	End        string `json:"end,omitempty"`
	Ran        bool   `json:"-"` // talked to the session (has an evidence slice)
	startOff   Offsets
	endOff     Offsets
}

// RunResult is a whole run.
type RunResult struct {
	Suite      string         `json:"suite"`
	SuiteFile  string         `json:"suite_file"`
	Title      string         `json:"title"`
	Target     string         `json:"target"`
	TargetFile string         `json:"target_file"`
	Session    string         `json:"session"`
	FixVersion string         `json:"fix_version"`
	Version    string         `json:"version"`
	Build      string         `json:"build"`
	RunID      string         `json:"run_id"`
	Start      string         `json:"start"`
	End        string         `json:"end"`
	Counts     map[string]int `json:"counts"`
	Required   map[string]int `json:"required_counts"`
	Exit       int            `json:"exit_code"`
	SessionErr string         `json:"session_error,omitempty"`
	// A5: who the run talked to, for the report header.
	SenderCompID string    `json:"sender_comp_id,omitempty"`
	TargetCompID string    `json:"target_comp_id,omitempty"`
	Address      string    `json:"address,omitempty"`
	ControlAPI   string    `json:"control_api,omitempty"`
	Counterparty string    `json:"counterparty,omitempty"` // e.g. the emulator's name and version
	Sections     []Section `json:"sections,omitempty"`
	// Integrity: SHA-256 of every per-case file, written at run end (A5).
	Integrity *Integrity    `json:"integrity,omitempty"`
	Cases     []*CaseResult `json:"cases"`
	// RequiredInSuite lists every required case of the suite, run or not,
	// so the required-cases review (9.1) can name the ones this run left out.
	RequiredInSuite []string `json:"required_in_suite,omitempty"`
	// Deviations is the drafted deviations section (9.2), when the run
	// includes a deviations review.
	Deviations *Deviations `json:"deviations,omitempty"`
	// RunError is set when the run itself was cut short (the service
	// stopped or restarted mid-run); the run then counts as ERROR.
	RunError string `json:"run_error,omitempty"`
	// NeverLoggedOn is set when the session could not be established at all.
	NeverLoggedOn bool `json:"never_logged_on,omitempty"`
	SessionDead   bool `json:"session_dead,omitempty"`
	// LogoutTimeout: our final Logout was never answered (exit 4).
	LogoutTimeout bool `json:"logout_timeout,omitempty"`
}

// Options configure a run.
type Options struct {
	Suite       *Suite
	Target      *Target
	Attest      map[string]Attestation
	Driver      Driver
	Clock       clock.Clock
	Sleep       func(time.Duration) // default time.Sleep; tests advance a FakeClock
	CLIVars     map[string]string
	CaseTimeout time.Duration
	StopOnFail  bool
	Select      func(*Case) bool
	Progress    func(done, total int, r *CaseResult)
	// OnCaseStart is told which case is about to run (for live status).
	OnCaseStart func(c *Case)
	// Abort, when it returns a reason, stops the run: every case not yet
	// started becomes NOT_RUN with that reason.
	Abort   func() string
	HTTP    *http.Client
	RunID   string
	Version string
	Build   string
	// Counterparty identifies the venue when known (the emulator's version).
	Counterparty string
	// ConnectAtStart logs on before the first case (with a reset); the CLI
	// sets it. Unit tests may connect themselves.
	ConnectAtStart bool
	ResetAtStart   bool
}

type runner struct {
	opt      Options
	vars     map[string]string
	info     SessionInfo
	res      *RunResult
	everOn   bool
	failures int // consecutive connect failures
}

const (
	defaultWithin    = 10 * time.Second
	defaultNoneFor   = 3 * time.Second
	pollInterval     = 20 * time.Millisecond
	maxConnectErrors = 2
)

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Run executes the selected cases in suite order.
func Run(opt Options) *RunResult {
	if opt.Clock == nil {
		opt.Clock = clock.SystemClock{}
	}
	if opt.Sleep == nil {
		opt.Sleep = time.Sleep
	}
	if opt.CaseTimeout <= 0 {
		opt.CaseTimeout = 60 * time.Second
	}
	if opt.HTTP == nil {
		opt.HTTP = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	}
	if opt.Target == nil {
		opt.Target = &Target{Name: "none", Sessions: map[string]string{}, Vars: map[string]string{}, NotApplicable: map[string]string{}}
	}
	r := &runner{opt: opt, info: opt.Driver.Info()}
	hb := r.info.HeartbeatSec
	builtins := map[string]string{
		"fix_version":    r.info.FixVersion,
		"sender_comp_id": r.info.SenderCompID,
		"target_comp_id": r.info.TargetCompID,
		"session":        r.info.SessionID,
		"heartbeat_sec":  strconv.Itoa(hb),
		// Long enough to see a heartbeat each way: HeartBtInt plus the
		// TestRequest grace plus slack.
		"hb_window": fmt.Sprintf("%ds", hb+hb/5+5),
	}
	r.vars = MergeVars(opt.Suite.Vars, opt.Target.Vars, opt.CLIVars, builtins)
	r.res = &RunResult{Suite: opt.Suite.Name, SuiteFile: opt.Suite.File, Title: opt.Suite.Title, Target: opt.Target.Name,
		TargetFile: opt.Target.File, Session: r.info.SessionID, FixVersion: r.info.FixVersion, Version: opt.Version,
		Build: opt.Build, RunID: opt.RunID, Start: ts(opt.Clock.Now()), Counts: map[string]int{}, Required: map[string]int{},
		SenderCompID: r.info.SenderCompID, TargetCompID: r.info.TargetCompID, Address: r.info.Address,
		ControlAPI: opt.Target.ControlAPI, Counterparty: opt.Counterparty, Sections: opt.Suite.Sections,
		Integrity: NewIntegrity()}

	var selected []*Case
	for _, c := range opt.Suite.Cases {
		if opt.Select == nil || opt.Select(c) {
			selected = append(selected, c)
		}
	}
	if opt.ConnectAtStart {
		if err := opt.Driver.Connect(opt.ResetAtStart); err != nil {
			r.res.SessionErr = err.Error()
			r.res.NeverLoggedOn = true
		} else {
			r.everOn = true
		}
	} else if opt.Driver.Connected() {
		r.everOn = true
	}
	for _, c := range opt.Suite.Cases {
		if c.Required {
			r.res.RequiredInSuite = append(r.res.RequiredInSuite, c.ID)
		}
	}
	stop, aborted := false, ""
	done := 0
	var reviews []*CaseResult
	for _, c := range selected {
		var cr *CaseResult
		if aborted == "" && opt.Abort != nil {
			aborted = opt.Abort()
		}
		switch {
		case aborted != "":
			cr = r.newResult(c)
			cr.Status, cr.Reason = StatusNotRun, "not run: "+aborted
		case stop:
			cr = r.newResult(c)
			cr.Status, cr.Reason = StatusNotRun, "not run: --stop-on-fail after an earlier failure"
		case c.ReviewOf() != "":
			// Reviews judge the other cases, so they are decided at the end.
			cr = r.reviewCase(c)
			if cr.Status == "" {
				r.res.Cases = append(r.res.Cases, cr)
				reviews = append(reviews, cr)
				continue
			}
		default:
			if opt.OnCaseStart != nil {
				opt.OnCaseStart(c)
			}
			cr = r.runCase(c)
		}
		r.res.Cases = append(r.res.Cases, cr)
		done++
		if opt.Progress != nil {
			opt.Progress(done, len(selected), cr)
		}
		if opt.StopOnFail && (cr.Status == StatusFail || cr.Status == StatusError) {
			stop = true
		}
	}
	Finalize(r.res)
	for _, cr := range reviews {
		done++
		if opt.Progress != nil {
			opt.Progress(done, len(selected), cr)
		}
	}
	// A session that went down and cannot come back is the session failing,
	// not a runner problem.
	if r.everOn && r.res.SessionErr != "" && !opt.Driver.Connected() && !r.res.SessionDead {
		if err := opt.Driver.Connect(false); err != nil {
			r.res.SessionDead = true
			r.res.SessionErr += "; reconnect failed: " + err.Error()
		}
	}
	Tally(r.res)
	r.res.End = ts(opt.Clock.Now())
	return r.res
}

// Tally recomputes the counts by status.
func Tally(res *RunResult) {
	res.Counts, res.Required = map[string]int{}, map[string]int{}
	for _, cr := range res.Cases {
		res.Counts[cr.Status]++
		if cr.Required {
			res.Required[cr.Status]++
		}
	}
}

func (r *runner) newResult(c *Case) *CaseResult {
	return &CaseResult{ID: c.ID, Section: c.Section, Title: c.Title, Task: c.Task, Required: c.Required,
		Level: c.Level, Mode: c.Mode, Steps: []StepResult{}, Orders: []string{}}
}

// ------------------------------------------------------------ one case

func (r *runner) runCase(c *Case) *CaseResult {
	cr := r.newResult(c)
	now := r.opt.Clock.Now()
	cr.Start = ts(now)
	defer func() { cr.End = ts(r.opt.Clock.Now()) }()

	if why, ok := r.opt.Target.NotApplicable[c.ID]; ok {
		cr.Status, cr.Reason = StatusNA, "target "+r.opt.Target.Name+": "+why
		if a, ok := r.opt.Attest[c.ID]; ok {
			cr.Warnings = append(cr.Warnings, "attestation ignored: the target marks this case N/A")
			cr.Attestation = &a
		}
		return cr
	}
	if c.Mode == ModeManual {
		var prompts []string
		for _, s := range c.Steps {
			prompts = append(prompts, s.Manual.Prompt)
		}
		cr.Attestable = true
		if a, ok := r.opt.Attest[c.ID]; ok {
			r.applyAttestation(cr, a)
			return cr
		}
		cr.Status, cr.Reason = StatusPending, "needs a human attestation: "+strings.Join(prompts, " / ")
		for _, p := range prompts {
			cr.Steps = append(cr.Steps, StepResult{Type: "manual", Status: StatusPending, Detail: p, TS: ts(now)})
		}
		return cr
	}
	if c.Mode == ModeAssisted {
		var blocked []string
		for _, s := range c.Steps {
			if s.Control != nil && (r.opt.Target.ControlAPI == "" || s.Control.Path == "") {
				blocked = append(blocked, s.Control.Description)
			}
		}
		if len(blocked) > 0 {
			cr.Attestable = true
			if a, ok := r.opt.Attest[c.ID]; ok {
				r.applyAttestation(cr, a)
				return cr
			}
			cr.Status = StatusBlocked
			cr.Reason = "BLOCKED: needs counterparty action: " + strings.Join(blocked, "; ")
			for _, d := range blocked {
				cr.Steps = append(cr.Steps, StepResult{Type: "control", Status: StatusBlocked, Detail: d, TS: ts(now)})
			}
			return cr
		}
		if a, ok := r.opt.Attest[c.ID]; ok {
			cr.Attestation = &a
			cr.Warnings = append(cr.Warnings, "attestation recorded but not used: the control steps ran against the control API")
		}
	}
	return r.execute(c, cr)
}

// reviewCase starts a review case: N/A by target is final at once; any
// other review is decided by Finalize once every other case has run.
func (r *runner) reviewCase(c *Case) *CaseResult {
	cr := r.newResult(c)
	cr.Review = c.ReviewOf()
	if why, ok := r.opt.Target.NotApplicable[c.ID]; ok {
		cr.Review = ""
		cr.Status, cr.Reason = StatusNA, "target "+r.opt.Target.Name+": "+why
		if a, ok := r.opt.Attest[c.ID]; ok {
			cr.Warnings = append(cr.Warnings, "attestation ignored: the target marks this case N/A")
			cr.Attestation = &a
		}
		return cr
	}
	cr.Start = ts(r.opt.Clock.Now())
	a, attested := r.opt.Attest[c.ID]
	switch cr.Review {
	case ReviewDeviations:
		cr.Attestable = true
		if attested {
			cr.Attestation = &a
		}
	case ReviewRequiredCases:
		if attested {
			cr.Warnings = append(cr.Warnings, "attestation ignored: this case is decided by code from the other cases' results")
			cr.Attestation = &a
		}
	}
	return cr
}

func (r *runner) applyAttestation(cr *CaseResult, a Attestation) {
	applyAttestation(cr, a, ts(r.opt.Clock.Now()))
}

func applyAttestation(cr *CaseResult, a Attestation, now string) {
	cr.Attestation = &a
	switch a.Status {
	case "pass":
		cr.Status = StatusPass
	case "fail":
		cr.Status = StatusFail
	default:
		cr.Status = StatusNA
	}
	cr.Reason = fmt.Sprintf("attested %s by %s", a.Status, a.By)
	if a.Note != "" {
		cr.Reason += ": " + a.Note
	}
	cr.Steps = append(cr.Steps, StepResult{Type: "attestation", Status: cr.Status, Detail: cr.Reason, TS: now})
}

// ref is a named order or raw message within a case.
type ref struct {
	name  string
	root  string // managed order root ClOrdID
	raw   bool
	rawID string // 11 of a raw message, if any
	seq   int    // MsgSeqNum of a raw message
}

type caseRun struct {
	r          *runner
	c          *Case
	cr         *CaseResult
	refs       map[string]*ref
	order      []string // ref names in creation order
	last       string
	cursor     map[string]int // per direction
	startIdx   int            // history index at case start
	deadline   time.Time
	down       bool // a step took the session down
	lastTestID string
	firstInSeq int
}

type stepErr struct {
	status string // FAIL or ERROR
	msg    string
}

func (e *stepErr) Error() string { return e.msg }

func fail(format string, args ...any) *stepErr {
	return &stepErr{status: StatusFail, msg: fmt.Sprintf(format, args...)}
}

func infra(format string, args ...any) *stepErr {
	return &stepErr{status: StatusError, msg: fmt.Sprintf(format, args...)}
}

func (r *runner) execute(c *Case, cr *CaseResult) *CaseResult {
	d := r.opt.Driver
	if !d.Connected() {
		if r.res.SessionDead {
			cr.Status, cr.Reason = StatusError, "session unavailable: "+r.res.SessionErr
			return cr
		}
		if reason := d.Dropped(); reason != "" {
			r.res.SessionErr = reason
		}
		if err := d.Connect(false); err != nil {
			r.failures++
			r.res.SessionErr = err.Error()
			if r.failures >= maxConnectErrors {
				r.res.SessionDead = true
			}
			cr.Status, cr.Reason = StatusError, "session unavailable: "+err.Error()
			return cr
		}
		r.failures = 0
		r.everOn = true
	}
	cr.Ran = true
	cr.startOff = d.Offsets()
	d.Mark("cert case start", fmt.Sprintf("%s %s", c.ID, c.Title))
	h := d.History()
	run := &caseRun{r: r, c: c, cr: cr, refs: map[string]*ref{}, cursor: map[string]int{"in": h.Len(), "out": h.Len()}, startIdx: h.Len(),
		deadline: r.opt.Clock.Now().Add(r.opt.CaseTimeout), firstInSeq: d.NextIn()}

	cr.Status = StatusPass
	for i, st := range c.Steps {
		res, err := run.step(st)
		res.TS = ts(r.opt.Clock.Now())
		cr.Steps = append(cr.Steps, res)
		if res.Status == StepWarn {
			cr.Warnings = append(cr.Warnings, fmt.Sprintf("step %d (%s): %s", i+1, st.Type, res.Detail))
		}
		if err != nil {
			cr.Status = err.status
			cr.Reason = fmt.Sprintf("step %d (%s): %s", i+1, st.Type, err.msg)
			break
		}
	}
	run.cleanup()
	for _, name := range run.order {
		if rf := run.refs[name]; rf.root != "" {
			cr.Orders = append(cr.Orders, rf.root)
		} else if rf.rawID != "" {
			cr.Orders = append(cr.Orders, rf.rawID)
		}
	}
	d.Mark("cert case end", fmt.Sprintf("%s %s %s", c.ID, cr.Status, cr.Reason))
	cr.endOff = d.Offsets()
	return cr
}

// cleanup cancels the case's orders that are still working, so the next
// case starts from a quiet book. Best effort; never changes the result.
func (run *caseRun) cleanup() {
	d := run.r.opt.Driver
	if !d.Connected() {
		return
	}
	var pending []string
	for _, name := range run.order {
		rf := run.refs[name]
		if rf.root == "" {
			continue
		}
		if info, ok := d.Order(rf.root); ok && !info.Terminal {
			if _, err := d.Cancel(rf.root, false); err == nil {
				pending = append(pending, rf.root)
			}
		}
	}
	if len(pending) == 0 {
		return
	}
	end := run.r.opt.Clock.Now().Add(5 * time.Second)
	for run.r.opt.Clock.Now().Before(end) {
		done := true
		for _, root := range pending {
			if info, ok := d.Order(root); ok && !info.Terminal {
				done = false
			}
		}
		if done {
			break
		}
		run.r.opt.Sleep(pollInterval)
	}
	run.cr.Steps = append(run.cr.Steps, StepResult{Type: "cleanup", Status: StatusPass,
		Detail: "canceled working order(s) " + strings.Join(pending, ", "), TS: ts(run.r.opt.Clock.Now())})
}

// ------------------------------------------------------------ variables

func (run *caseRun) resolver(name string) (string, bool) {
	d := run.r.opt.Driver
	switch name {
	case "uid":
		return d.NewID(), true
	case "transact_time":
		return codec.FormatTime(run.r.opt.Clock.Now()), true
	case "last_testreq_id":
		return run.lastTestID, run.lastTestID != ""
	case "case_first_in_seq":
		return strconv.Itoa(run.firstInSeq), true
	}
	if strings.HasPrefix(name, "order.") {
		parts := strings.Split(name, ".")
		if len(parts) != 3 {
			return "", false
		}
		rf, ok := run.refs[parts[1]]
		if !ok {
			return "", false
		}
		if rf.raw {
			switch parts[2] {
			case "root", "cl_ord_id":
				return rf.rawID, rf.rawID != ""
			case "seq":
				return strconv.Itoa(rf.seq), true
			}
			return "", false
		}
		info, ok := d.Order(rf.root)
		if !ok {
			return "", false
		}
		switch parts[2] {
		case "root":
			return info.Root, true
		case "cl_ord_id":
			return info.Current, true
		case "order_id":
			return info.OrderID, info.OrderID != ""
		case "state":
			return info.State, true
		case "seq":
			if n := len(info.Seqs); n > 0 {
				return strconv.Itoa(info.Seqs[n-1]), true
			}
		}
		return "", false
	}
	return "", false
}

func (run *caseRun) sub(s string) (string, *stepErr) {
	if s == "" {
		return "", nil
	}
	out, err := Substitute(s, run.r.vars, run.resolver)
	if err != nil {
		return "", infra("%v in %q", err, s)
	}
	return out, nil
}

func (run *caseRun) subAll(dst ...*string) *stepErr {
	for _, p := range dst {
		v, err := run.sub(*p)
		if err != nil {
			return err
		}
		*p = v
	}
	return nil
}

// ------------------------------------------------------------ steps

func (run *caseRun) step(st *Step) (StepResult, *stepErr) {
	res := StepResult{Type: st.Type}
	var detail string
	var err *stepErr
	switch st.Type {
	case "send":
		detail, err = run.send(*st.Send)
	case "expect":
		detail, err = run.expect(*st.Expect)
		if err == nil && strings.HasPrefix(detail, "optional:") {
			res.Status, res.Detail = StepSkipped, detail
			return res, nil
		}
	case "session":
		detail, err = run.session(*st.Session)
	case "control":
		detail, err = run.control(*st.Control)
	case "assert_sent", "assert_received":
		dir := "out"
		if st.Type == "assert_received" {
			dir = "in"
		}
		var warn string
		detail, warn, err = run.assert(dir, *st.Assert)
		if err == nil && warn != "" {
			res.Status, res.Detail = StepWarn, detail+"; "+warn
			return res, nil
		}
	case "checks":
		var warn string
		detail, warn, err = run.checks(st.Checks)
		if err == nil && warn != "" {
			res.Status, res.Detail = StepWarn, detail+"; "+warn
			return res, nil
		}
	default:
		err = infra("step type %q cannot run in a %s case", st.Type, run.c.Mode)
	}
	if err != nil {
		res.Status, res.Detail = err.status, err.msg
		return res, err
	}
	res.Status, res.Detail = StatusPass, detail
	return res, nil
}

func (run *caseRun) addRef(rf *ref) {
	name := rf.name
	if name == "" {
		name = fmt.Sprintf("_%d", len(run.order)+1)
		rf.name = name
	}
	if _, exists := run.refs[name]; !exists {
		run.order = append(run.order, name)
	}
	run.refs[name] = rf
	run.last = name
}

func (run *caseRun) send(s SendStep) (string, *stepErr) {
	d := run.r.opt.Driver
	if err := run.subAll(&s.Side, &s.OrdType, &s.Symbol, &s.Qty, &s.Price, &s.TIF, &s.Account); err != nil {
		return "", err
	}
	if !d.Connected() && !run.down {
		return "", run.dropped()
	}
	if len(s.Raw) > 0 {
		var fields []codec.Field
		rawID := ""
		for _, p := range s.Raw {
			v, err := run.sub(p[1])
			if err != nil {
				return "", err
			}
			tag, _ := strconv.Atoi(p[0])
			if tag == 11 {
				rawID = v
			}
			fields = append(fields, codec.F(tag, v))
		}
		seq, err := d.SendRaw(fields)
		if err != nil {
			return "", infra("SendRaw: %v", err)
		}
		run.addRef(&ref{name: s.Ref, raw: true, rawID: rawID, seq: seq})
		var pairs []string
		for _, f := range fields {
			pairs = append(pairs, fmt.Sprintf("%d=%s", f.Tag, f.Value))
		}
		return fmt.Sprintf("raw seq=%d %s", seq, strings.Join(pairs, "|")), nil
	}
	switch s.Msg {
	case "D":
		spec := order.Spec{Symbol: s.Symbol, Qty: s.Qty, Side: s.Side, OrdType: s.OrdType, Price: s.Price, TIF: s.TIF, Account: s.Account}
		root, err := d.NewOrder(spec)
		if err != nil {
			return "", infra("NewOrderSingle: %v", err)
		}
		run.addRef(&ref{name: s.Ref, root: root})
		return fmt.Sprintf("D 11=%s %s %s %s %s %s %s", root, s.Side, s.Qty, s.Symbol, s.OrdType, s.Price, s.TIF), nil
	case "F", "G":
		rf := run.refs[s.Order]
		if rf == nil || rf.root == "" {
			return "", infra("order %q is not a managed order in this case", s.Order)
		}
		var id string
		var err error
		if s.Msg == "F" {
			id, err = d.Cancel(rf.root, s.AllowTerminal)
		} else {
			id, err = d.Replace(rf.root, s.Qty, s.Price)
		}
		if err != nil {
			return "", infra("%s: %v", s.Msg, err)
		}
		run.last = s.Order
		if s.Ref != "" && s.Ref != s.Order {
			run.addRef(&ref{name: s.Ref, root: rf.root})
		}
		return fmt.Sprintf("%s 11=%s for order %s", s.Msg, id, rf.root), nil
	}
	return "", infra("send: nothing to send")
}

// dropped turns an unexpected disconnect into an ERROR.
func (run *caseRun) dropped() *stepErr {
	reason := run.r.opt.Driver.Dropped()
	if reason == "" {
		reason = "not connected"
	}
	run.r.res.SessionErr = reason
	return infra("session dropped: %s", reason)
}

// ------------------------------------------------------------ matching

func (run *caseRun) ids(name string) (map[string]bool, map[int]bool, bool) {
	rf, ok := run.refs[name]
	if !ok {
		return nil, nil, false
	}
	ids, seqs := map[string]bool{}, map[int]bool{}
	if rf.raw {
		if rf.rawID != "" {
			ids[rf.rawID] = true
		}
		seqs[rf.seq] = true
		return ids, seqs, true
	}
	info, ok := run.r.opt.Driver.Order(rf.root)
	if !ok {
		ids[rf.root] = true
		return ids, seqs, true
	}
	for _, id := range info.ClOrdIDs {
		ids[id] = true
	}
	for _, s := range info.Seqs {
		seqs[s] = true
	}
	return ids, seqs, true
}

func belongs(m *codec.Message, ids map[string]bool, seqs map[int]bool) bool {
	switch m.MsgType() {
	case "8", "9":
		return ids[m.Value(11)] || ids[m.Value(41)]
	case "3", "j":
		ref, err := strconv.Atoi(m.Value(45))
		return err == nil && seqs[ref]
	}
	return true
}

func matches(mt Matcher, version string, m *codec.Message) bool {
	msgs := mt.Msg
	if len(msgs) == 0 && (len(mt.ExecType) > 0 || len(mt.OrdStatus) > 0) {
		msgs = []string{"8"}
	}
	if len(msgs) > 0 && !contains(msgs, m.MsgType()) {
		return false
	}
	if len(mt.ExecType) > 0 {
		ok := false
		for _, name := range mt.ExecType {
			if ExecTypeMatches(name, version, m) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if len(mt.OrdStatus) > 0 {
		ok := false
		for _, name := range mt.OrdStatus {
			if v, _ := OrdStatusValue(name); v == m.Value(39) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if mt.Side != "" && m.Value(54) != sideNames[mt.Side] {
		return false
	}
	for tag, allowed := range mt.Tags {
		v, has := m.Get(tag)
		if !has {
			return false
		}
		if !contains(allowed, "*") && !contains(allowed, v) {
			return false
		}
	}
	for _, tag := range mt.Present {
		if !m.Has(tag) {
			return false
		}
	}
	for _, tag := range mt.Absent {
		if m.Has(tag) {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// acceptsReject reports whether a 39=8 ExecutionReport could satisfy e.
func acceptsReject(e ExpectStep) bool {
	ok := func(m Matcher) bool {
		for _, n := range m.OrdStatus {
			if n == "REJECTED" {
				return true
			}
		}
		for _, n := range m.ExecType {
			if n == "REJECTED" {
				return true
			}
		}
		return len(m.OrdStatus) == 0 && len(m.ExecType) == 0 && (len(m.Msg) == 0 || contains(m.Msg, "8"))
	}
	if len(e.AnyOf) > 0 {
		for _, a := range e.AnyOf {
			if ok(a) {
				return true
			}
		}
		return false
	}
	return ok(e.Matcher)
}

func (mt Matcher) empty() bool {
	return len(mt.Msg) == 0 && len(mt.ExecType) == 0 && len(mt.OrdStatus) == 0 && mt.Side == "" &&
		len(mt.Tags) == 0 && len(mt.Present) == 0 && len(mt.Absent) == 0
}

func (mt Matcher) orderScoped() bool {
	if len(mt.ExecType) > 0 || len(mt.OrdStatus) > 0 {
		return true
	}
	for _, m := range mt.Msg {
		if m == "8" || m == "9" || m == "3" || m == "j" {
			return true
		}
	}
	return false
}

func (mt Matcher) String() string {
	var b []string
	if len(mt.Msg) > 0 {
		b = append(b, "35="+strings.Join(mt.Msg, "|"))
	}
	if len(mt.ExecType) > 0 {
		b = append(b, "exec_type="+strings.Join(mt.ExecType, "|"))
	}
	if len(mt.OrdStatus) > 0 {
		b = append(b, "ord_status="+strings.Join(mt.OrdStatus, "|"))
	}
	if mt.Side != "" {
		b = append(b, "side="+mt.Side)
	}
	var tags []int
	for t := range mt.Tags {
		tags = append(tags, t)
	}
	sort.Ints(tags)
	for _, t := range tags {
		b = append(b, fmt.Sprintf("%d=%s", t, strings.Join(mt.Tags[t], "|")))
	}
	if len(mt.Present) > 0 {
		b = append(b, fmt.Sprintf("present %v", mt.Present))
	}
	if len(mt.Absent) > 0 {
		b = append(b, fmt.Sprintf("absent %v", mt.Absent))
	}
	return strings.Join(b, " ")
}

func describe(m *codec.Message) string {
	s := fmt.Sprintf("35=%s seq=%s", m.MsgType(), m.Value(34))
	for _, tag := range []int{11, 41, 37, 150, 39, 32, 14, 151, 6, 102, 103, 45, 371, 372, 373, 380, 112, 7, 16, 36, 123, 141, 43, 58} {
		if v, ok := m.Get(tag); ok {
			s += fmt.Sprintf(" %d=%s", tag, v)
		}
	}
	return s
}

func (e ExpectStep) String() string {
	parts := []string{}
	if !e.Matcher.empty() {
		parts = append(parts, e.Matcher.String())
	}
	if len(e.AnyOf) > 0 {
		var alts []string
		for _, a := range e.AnyOf {
			alts = append(alts, "("+a.String()+")")
		}
		parts = append(parts, "any of "+strings.Join(alts, " or "))
	}
	who := "from the counterparty"
	if e.From == "us" {
		who = "sent by us"
	}
	return strings.Join(parts, " ") + " " + who
}

func (run *caseRun) expect(e ExpectStep) (string, *stepErr) {
	d := run.r.opt.Driver
	version := run.r.info.FixVersion
	dir := "in"
	if e.From == "us" {
		dir = "out"
	}
	for i := range e.Matcher.Tags {
		for j, v := range e.Matcher.Tags[i] {
			s, err := run.sub(v)
			if err != nil {
				return "", err
			}
			e.Matcher.Tags[i][j] = s
		}
	}
	for _, alt := range e.AnyOf {
		for i := range alt.Tags {
			for j, v := range alt.Tags[i] {
				s, err := run.sub(v)
				if err != nil {
					return "", err
				}
				alt.Tags[i][j] = s
			}
		}
	}
	within := defaultWithin
	if e.None {
		within = defaultNoneFor
	}
	if e.Within != "" {
		w, serr := run.sub(e.Within)
		if serr != nil {
			return "", serr
		}
		dd, err := ParseDuration(w)
		if err != nil {
			return "", infra("within: %v", err)
		}
		within = dd
	}
	orderName := e.Order
	scoped := e.Matcher.orderScoped()
	for _, a := range e.AnyOf {
		scoped = scoped || a.orderScoped()
	}
	if orderName == "" && scoped && dir == "in" {
		orderName = run.last
	}
	var ids map[string]bool
	var seqs map[int]bool
	candidate := func(m *codec.Message) bool {
		if orderName != "" && !belongs(m, ids, seqs) {
			return false
		}
		if !e.Matcher.empty() && !matches(e.Matcher, version, m) {
			return false
		}
		if len(e.AnyOf) > 0 {
			for _, alt := range e.AnyOf {
				if matches(alt, version, m) {
					return true
				}
			}
			return false
		}
		return true
	}
	start := run.r.opt.Clock.Now()
	deadline := start.Add(within)
	if deadline.After(run.deadline) {
		deadline = run.deadline
	}
	var lastSeen *codec.Message
	for {
		if orderName != "" {
			ids, seqs, _ = run.ids(orderName) // ClOrdIDs/seqs grow as requests go out
		}
		for _, en := range d.History().Since(run.startIdx) {
			if en.Dir == dir && orderName != "" && en.Msg.MsgType() != "0" && belongs(en.Msg, ids, seqs) {
				lastSeen = en.Msg
			}
		}
		for _, en := range d.History().Since(run.cursor[dir]) {
			if en.Dir != dir {
				continue
			}
			if candidate(en.Msg) {
				if e.None {
					return "", fail("unexpected %s", describe(en.Msg))
				}
				run.cursor[dir] = en.Index + 1
				return "matched " + describe(en.Msg), nil
			}
			// Fail fast: the order we are waiting on was rejected, and a
			// reject is not what this step expects. (Without an order
			// scope, a reject says nothing about the awaited message.)
			if !e.None && orderName != "" && dir == "in" && en.Msg.MsgType() == "8" &&
				en.Msg.Value(39) == "8" && belongs(en.Msg, ids, seqs) && !acceptsReject(e) {
				if e.Optional {
					return fmt.Sprintf("optional: the order was rejected instead: %s", describe(en.Msg)), nil
				}
				return "", fail("the counterparty rejected the order while waiting for %s: %s", strings.TrimSpace(e.String()), describe(en.Msg))
			}
		}
		now := run.r.opt.Clock.Now()
		if !now.Before(deadline) {
			if e.None {
				return fmt.Sprintf("no %s within %s, as required", strings.TrimSpace(e.String()), within), nil
			}
			if !now.Before(run.deadline) {
				return "", fail("case timeout after %s waiting for %s", run.r.opt.CaseTimeout, strings.TrimSpace(e.String()))
			}
			seen := "nothing relevant received"
			if lastSeen != nil {
				seen = "last relevant message: " + describe(lastSeen)
			}
			if e.Optional {
				return fmt.Sprintf("optional: no %s within %s (%s)", strings.TrimSpace(e.String()), within, seen), nil
			}
			return "", fail("timed out after %s waiting for %s; %s", within, strings.TrimSpace(e.String()), seen)
		}
		if !d.Connected() && !run.down {
			return "", run.dropped()
		}
		run.r.opt.Sleep(pollInterval)
	}
}

// ------------------------------------------------------------ session

func (run *caseRun) session(s SessionStep) (string, *stepErr) {
	d := run.r.opt.Driver
	needLive := s.Action != "reconnect" && s.Action != "wait"
	if needLive && !d.Connected() {
		if run.down {
			return "", fail("session is down (an earlier step took it down); %s needs a live session", s.Action)
		}
		return "", run.dropped()
	}
	switch s.Action {
	case "testreq":
		id, err := d.TestRequest()
		if err != nil {
			return "", infra("TestRequest: %v", err)
		}
		run.lastTestID = id
		if !s.RequireAnswer {
			return "sent TestRequest 112=" + id + " (no answer required)", nil
		}
		within := s.Within
		if within == "" {
			within = "10s"
		}
		// Peek: the Heartbeat stays available to a later expect step.
		saved := run.cursor["in"]
		detail, err2 := run.expect(ExpectStep{Matcher: Matcher{Msg: []string{"0"}, Tags: map[int][]string{112: {id}}}, From: "counterparty", Within: within})
		run.cursor["in"] = saved
		if err2 != nil {
			return "", err2
		}
		return "TestRequest 112=" + id + " answered: " + detail, nil
	case "logout":
		run.down = true
		if err := d.Logout(); err != nil {
			return "", fail("%v", err)
		}
		return "clean Logout exchanged", nil
	case "reconnect":
		if d.Connected() {
			return "", infra("reconnect: already connected (log out or disconnect first)")
		}
		if err := d.Connect(s.Reset); err != nil {
			return "", fail("%v", err)
		}
		run.down = false
		run.r.everOn = true
		if s.Reset {
			return "reconnected with a sequence reset (141=Y)", nil
		}
		return "reconnected, sequence continued", nil
	case "disconnect_abrupt":
		run.down = true
		if err := d.Abrupt(); err != nil {
			return "", infra("disconnect: %v", err)
		}
		return "socket closed without Logout", nil
	case "skip_outbound_seq":
		n, err := run.sub(s.N)
		if err != nil {
			return "", err
		}
		k, cerr := strconv.Atoi(n)
		if cerr != nil || k < 1 {
			return "", infra("skip_outbound_seq: bad count %q", n)
		}
		if err := d.SkipOutboundSeq(k); err != nil {
			return "", infra("skip_outbound_seq: %v", err)
		}
		return fmt.Sprintf("skipped %d outbound seq(s)", k), nil
	case "resend_request":
		b, err := run.sub(s.Begin)
		if err != nil {
			return "", err
		}
		e, err := run.sub(s.End)
		if err != nil {
			return "", err
		}
		bi, e1 := strconv.Atoi(b)
		ei, e2 := strconv.Atoi(e)
		if e1 != nil || e2 != nil {
			return "", infra("resend_request: begin/end must be numbers, got %q/%q", b, e)
		}
		if err := d.ResendRequest(bi, ei); err != nil {
			return "", infra("ResendRequest: %v", err)
		}
		return fmt.Sprintf("sent ResendRequest 7=%d 16=%d", bi, ei), nil
	case "resend_order":
		rf := run.refs[s.Order]
		if rf == nil {
			return "", infra("resend_order: unknown ref %q", s.Order)
		}
		seq := rf.seq
		if !rf.raw {
			info, ok := d.Order(rf.root)
			if !ok || len(info.Seqs) == 0 {
				return "", infra("resend_order: order %q has no sent request", s.Order)
			}
			seq = info.Seqs[0]
		}
		if err := d.ResendStored(seq); err != nil {
			return "", infra("resend_order: %v", err)
		}
		return fmt.Sprintf("re-sent seq %d with PossDupFlag=Y", seq), nil
	case "wait":
		w, err := run.sub(s.Wait)
		if err != nil {
			return "", err
		}
		dur, perr := ParseDuration(w)
		if perr != nil {
			return "", infra("wait: %v", perr)
		}
		end := run.r.opt.Clock.Now().Add(dur)
		if end.After(run.deadline) {
			return "", infra("wait %s would exceed the case timeout %s", dur, run.r.opt.CaseTimeout)
		}
		for run.r.opt.Clock.Now().Before(end) {
			if !d.Connected() && !run.down {
				return "", run.dropped()
			}
			run.r.opt.Sleep(pollInterval * 5)
		}
		return "waited " + dur.String(), nil
	}
	return "", infra("unknown session action %q", s.Action)
}

// ------------------------------------------------------------ control

func (run *caseRun) control(c ControlStep) (string, *stepErr) {
	t := run.r.opt.Target
	path := c.Path
	counterparty := t.Sessions[run.r.info.SessionID]
	if counterparty == "" {
		counterparty = run.r.info.SessionID
	}
	path = strings.ReplaceAll(path, "{session}", counterparty)
	path, err := run.sub(path)
	if err != nil {
		return "", err
	}
	var body io.Reader
	bodyText := ""
	if c.Body != nil {
		v, err := run.decodeBody(c.Body)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(v)
		bodyText = string(b)
		body = bytes.NewReader(b)
	} else if c.Method == "POST" {
		body = strings.NewReader("{}")
		bodyText = "{}"
	}
	url := strings.TrimRight(t.ControlAPI, "/") + path
	req, rerr := http.NewRequest(c.Method, url, body)
	if rerr != nil {
		return "", infra("control %s %s: %v", c.Method, path, rerr)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, herr := run.r.opt.HTTP.Do(req)
	if herr != nil {
		return "", infra("control %s %s: %v", c.Method, url, herr)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(data))
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	if resp.StatusCode/100 != 2 {
		return "", infra("control %s %s %s -> HTTP %d %s", c.Method, path, bodyText, resp.StatusCode, text)
	}
	return fmt.Sprintf("%s (%s %s %s -> %d)", c.Description, c.Method, path, bodyText, resp.StatusCode), nil
}

// decodeBody substitutes {{vars}} in a YAML body's scalars and decodes it
// with YAML's own typing (so 40 is a number and "40" a string).
func (run *caseRun) decodeBody(n *yaml.Node) (any, *stepErr) {
	var walk func(*yaml.Node) (*yaml.Node, *stepErr)
	walk = func(x *yaml.Node) (*yaml.Node, *stepErr) {
		x = resolve(x)
		cp := *x
		if x.Kind == yaml.ScalarNode {
			v, err := run.sub(x.Value)
			if err != nil {
				return nil, err
			}
			cp.Value = v
			return &cp, nil
		}
		cp.Content = nil
		for _, ch := range x.Content {
			w, err := walk(ch)
			if err != nil {
				return nil, err
			}
			cp.Content = append(cp.Content, w)
		}
		return &cp, nil
	}
	w, err := walk(n)
	if err != nil {
		return nil, err
	}
	var v any
	if derr := w.Decode(&v); derr != nil {
		return nil, infra("control body: %v", derr)
	}
	return v, nil
}

// ------------------------------------------------------------ asserts

func (run *caseRun) assert(dir string, a AssertStep) (string, string, *stepErr) {
	d := run.r.opt.Driver
	var ids map[string]bool
	var seqs map[int]bool
	if a.Order != "" {
		ids, seqs, _ = run.ids(a.Order)
	}
	msgType, err := run.sub(a.Msg)
	if err != nil {
		return "", "", err
	}
	var found *codec.Message
	for _, en := range d.History().Since(0) {
		if en.Dir != dir || en.Msg.MsgType() != msgType {
			continue
		}
		if a.Order != "" {
			// D/F/G we sent belong by 11/41 too.
			if !(ids[en.Msg.Value(11)] || ids[en.Msg.Value(41)] || (msgType != "D" && msgType != "F" && msgType != "G" && belongs(en.Msg, ids, seqs))) {
				continue
			}
		}
		found = en.Msg
		if a.Which == "first" {
			break
		}
	}
	who := "sent"
	if dir == "in" {
		who = "received"
	}
	scope := ""
	if a.Order != "" {
		scope = " for order " + a.Order
	}
	if found == nil {
		return "", "", fail("no %s 35=%s message%s", who, msgType, scope)
	}
	var problems []string
	var tags []int
	for t := range a.Tags {
		tags = append(tags, t)
	}
	sort.Ints(tags)
	for _, tag := range tags {
		var allowed []string
		for _, v := range a.Tags[tag] {
			s, err := run.sub(v)
			if err != nil {
				return "", "", err
			}
			allowed = append(allowed, s)
		}
		v, has := found.Get(tag)
		if !has {
			problems = append(problems, fmt.Sprintf("tag %d missing (want %s)", tag, strings.Join(allowed, "|")))
		} else if !contains(allowed, "*") && !contains(allowed, v) {
			problems = append(problems, fmt.Sprintf("%d=%s, want %s", tag, v, strings.Join(allowed, "|")))
		}
	}
	for _, tag := range a.Present {
		if !found.Has(tag) {
			problems = append(problems, fmt.Sprintf("tag %d missing", tag))
		}
	}
	for _, tag := range a.Absent {
		if found.Has(tag) {
			problems = append(problems, fmt.Sprintf("tag %d present (%s) but must be absent", tag, found.Value(tag)))
		}
	}
	desc := fmt.Sprintf("%s %s message: %s", a.Which, who, describe(found))
	if len(problems) > 0 {
		return "", "", fail("%s: %s", desc, strings.Join(problems, "; "))
	}
	var missing []string
	for _, tag := range a.Recommended {
		if !found.Has(tag) {
			missing = append(missing, strconv.Itoa(tag))
		}
	}
	if len(missing) > 0 {
		return desc, "recommended tag(s) absent: " + strings.Join(missing, ", "), nil
	}
	return desc, "", nil
}

// ------------------------------------------------------------ checks

func (run *caseRun) checks(kind string) (string, string, *stepErr) {
	d := run.r.opt.Driver
	switch kind {
	case "timeline":
		var fails, warns, done []string
		for _, name := range run.order {
			rf := run.refs[name]
			id := rf.root
			if rf.raw {
				id = rf.rawID
			}
			if id == "" {
				continue
			}
			c := d.Chain(id)
			rec := CheckRecord{Ref: name, ClOrdID: id, Verdict: c.Verdict(), Checks: c.Checks}
			run.cr.Checks = append(run.cr.Checks, rec)
			for _, x := range c.Checks {
				switch x.Status {
				case checks.FAIL:
					fails = append(fails, fmt.Sprintf("%s %s: %s", name, x.Name, x.Explanation))
				case checks.WARN:
					warns = append(warns, fmt.Sprintf("%s %s: %s", name, x.Name, x.Explanation))
				}
			}
			done = append(done, fmt.Sprintf("%s=%s", name, c.Verdict()))
		}
		if len(done) == 0 {
			return "", "", infra("checks: timeline: the case has no orders")
		}
		if len(fails) > 0 {
			return "", "", fail("order checks FAIL: %s", strings.Join(fails, "; "))
		}
		summary := "11 checks: " + strings.Join(done, ", ")
		if len(warns) > 0 {
			return summary, "WARN " + strings.Join(warns, "; "), nil
		}
		return summary, "", nil
	case "session_exec_ids":
		seen := map[string]bool{}
		reports, replays, strange := 0, 0, 0
		for _, en := range d.History().Since(0) {
			m := en.Msg
			if en.Dir != "in" || m.MsgType() != "8" {
				continue
			}
			id := m.Value(17)
			if id == "" {
				continue
			}
			if m.Value(43) == "Y" {
				replays++
				if !seen[id] {
					strange++
				}
				continue
			}
			reports++
			if seen[id] {
				return "", "", fail("ExecID %s used twice in the session (%s)", id, describe(m))
			}
			seen[id] = true
		}
		detail := fmt.Sprintf("%d ExecutionReport(s), all ExecIDs distinct; %d PossDup replay(s)", reports, replays)
		if strange > 0 {
			return detail, fmt.Sprintf("%d PossDup replay(s) carry an ExecID never seen live", strange), nil
		}
		return detail, "", nil
	}
	return "", "", infra("unknown checks %q", kind)
}

// ------------------------------------------------------------ exit codes

// Exit codes (A3 8).
const (
	ExitOK          = 0
	ExitLogonFailed = 1
	ExitDropped     = 3
	ExitLogoutTO    = 4
	ExitFail        = 5
	ExitIncomplete  = 7
	ExitRunnerError = 8
)

// ExitCode decides the run's exit code. sessionCode is the session's own
// A2 exit code at the end of the run (0 if it ended cleanly).
//
// Order: the session could not be established (1); the session died and
// could not be re-established (3); any ERROR (8); any required FAIL (5);
// any required BLOCKED/PENDING (7); a logout timeout at the very end (4);
// otherwise 0.
func ExitCode(r *RunResult, sessionCode int) int {
	if r.LogoutTimeout && sessionCode == ExitOK {
		sessionCode = ExitLogoutTO
	}
	switch {
	case r.NeverLoggedOn:
		return ExitLogonFailed
	case r.SessionDead:
		return ExitDropped
	case r.RunError != "" || r.Counts[StatusError] > 0:
		return ExitRunnerError
	case r.Required[StatusFail] > 0:
		return ExitFail
	case r.Required[StatusBlocked] > 0 || r.Required[StatusPending] > 0:
		return ExitIncomplete
	case sessionCode == ExitLogoutTO:
		return ExitLogoutTO
	}
	return ExitOK
}
