package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/cert"
)

// Run states.
const (
	RunRunning  = "running"
	RunFinished = "finished"
	RunError    = "error"
)

// RunInfo is a cert run's service-side record, persisted as run.json next
// to results.json so a restarted service knows what happened.
type RunInfo struct {
	RunID      string         `json:"run_id"`
	State      string         `json:"state"`
	Error      string         `json:"error,omitempty"`
	Suite      string         `json:"suite"`
	SuiteFile  string         `json:"suite_file"`
	Target     string         `json:"target"`
	TargetFile string         `json:"target_file"`
	SessionID  string         `json:"session_id"`
	Started    string         `json:"started"`
	Finished   string         `json:"finished,omitempty"`
	Total      int            `json:"total"`
	Done       int            `json:"done"`
	Current    string         `json:"current_case,omitempty"`
	Counts     map[string]int `json:"counts"`
	ExitCode   *int           `json:"exit_code,omitempty"`
	Dir        string         `json:"results_dir"`
	TookOver   bool           `json:"took_over_session,omitempty"`
	StartedBy  string         `json:"started_by"`
}

type runState struct {
	mu      sync.Mutex
	info    RunInfo
	aborted string
	a       *agent.Agent
	done    chan struct{}
	res     *cert.RunResult
	attest  sync.Mutex // serializes attestations on this run
}

func (r *runState) state() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info.State
}

func (r *runState) snapshot() RunInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.info
	info.Counts = map[string]int{}
	for k, v := range r.info.Counts {
		info.Counts[k] = v
	}
	return info
}

func (r *runState) save() {
	info := r.snapshot()
	_ = writeJSON(filepath.Join(info.Dir, "run.json"), info)
}

// abort stops a running run: the case in progress ends when the session is
// logged out under it, and every case not started becomes NOT_RUN.
func (r *runState) abort(why string) {
	r.mu.Lock()
	if r.aborted == "" {
		r.aborted = why
	}
	a := r.a
	r.mu.Unlock()
	if a != nil {
		if err := a.Init.Logout("OrderEcho agent service stopping"); err != nil {
			a.Init.Stop("service stopping")
		}
	}
}

// recoverRuns marks runs a previous service left "running" as ERROR.
func (s *Service) recoverRuns() {
	paths, _ := filepath.Glob(filepath.Join(s.certsDir(), "*", "run.json"))
	for _, p := range paths {
		var info RunInfo
		data, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(data, &info) != nil || info.State != RunRunning {
			continue
		}
		info.State = RunError
		info.Error = "service restarted"
		info.Finished = time.Now().UTC().Format(time.RFC3339)
		_ = writeJSON(p, info)
		// Keep any partial results consistent with the run's fate.
		if res, err := loadResults(filepath.Dir(p)); err == nil {
			res.RunError = "service restarted"
			res.Exit = cert.ExitCode(res, 0)
			_ = cert.WriteResults(filepath.Dir(p), res)
		}
		s.Log.Warning("engine", fmt.Sprintf("cert run %s was in progress when the service stopped: marked ERROR: service restarted", info.RunID))
		s.ev.Event(info.SessionID, "cert run recovered", fmt.Sprintf("%s marked ERROR: service restarted", info.RunID), false)
	}
}

func loadResults(dir string) (*cert.RunResult, error) {
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return nil, err
	}
	var res cert.RunResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ------------------------------------------------------------ discovery

func (s *Service) suites() (map[string]*cert.Suite, []string, []string) {
	suitePaths, _ := cert.Discover(s.root)
	out := map[string]*cert.Suite{}
	var names, bad []string
	for _, p := range suitePaths {
		su, err := cert.LoadSuite(p)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		out[su.Name] = su
		names = append(names, su.Name)
	}
	return out, names, bad
}

func (s *Service) resolveSuite(ref string) (*cert.Suite, *APIError) {
	all, names, _ := s.suites()
	if su, ok := all[ref]; ok {
		return su, nil
	}
	for _, su := range all {
		base := strings.TrimSuffix(filepath.Base(su.File), ".yaml")
		if ref == su.File || ref == filepath.Base(su.File) || ref == base || filepath.Clean(ref) == filepath.Clean(su.File) {
			return su, nil
		}
	}
	return nil, apiErr(404, "unknown_suite", "suites: "+strings.Join(names, ", ")+" (call list_cert_suites)", "no suite %q", ref)
}

func (s *Service) targets() ([]*cert.Target, []string) {
	_, paths := cert.Discover(s.root)
	var out []*cert.Target
	var bad []string
	for _, p := range paths {
		t, err := cert.LoadTarget(p)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		out = append(out, t)
	}
	return out, bad
}

func targetKey(t *cert.Target) string { return strings.TrimSuffix(filepath.Base(t.File), ".yaml") }

func (s *Service) resolveTarget(ref string) (*cert.Target, *APIError) {
	all, _ := s.targets()
	var names []string
	for _, t := range all {
		if ref == t.Name || ref == targetKey(t) || ref == t.File || ref == filepath.Base(t.File) || filepath.Clean(ref) == filepath.Clean(t.File) {
			return t, nil
		}
		names = append(names, targetKey(t))
	}
	return nil, apiErr(404, "unknown_target", "targets: "+strings.Join(names, ", ")+" (call list_cert_targets)", "no target %q", ref)
}

func hListSuites(_ context.Context, s *Service, _ *call) (any, string, *APIError) {
	all, names, bad := s.suites()
	sort.Strings(names)
	var rows []map[string]any
	var parts []string
	for _, n := range names {
		su := all[n]
		modes := map[string]int{}
		perSection := map[string]int{}
		for _, c := range su.Cases {
			modes[c.Mode]++
			perSection[c.Section]++
		}
		var secs []map[string]any
		for _, sec := range su.Sections {
			secs = append(secs, map[string]any{"id": sec.ID, "name": sec.Name, "cases": perSection[sec.ID]})
		}
		rows = append(rows, map[string]any{"suite": su.Name, "file": su.File, "title": su.Title, "fix_version": su.FixVersion,
			"cases": len(su.Cases), "modes": modes, "sections": secs})
		parts = append(parts, fmt.Sprintf("%s (%s, %d cases)", su.Name, su.FixVersion, len(su.Cases)))
	}
	out := map[string]any{"suites": rows}
	if len(bad) > 0 {
		out["invalid"] = bad
	}
	return out, fmt.Sprintf("%d suite(s): %s", len(rows), strings.Join(parts, "; ")), nil
}

func hListTargets(_ context.Context, s *Service, _ *call) (any, string, *APIError) {
	all, bad := s.targets()
	var rows []map[string]any
	var parts []string
	for _, t := range all {
		rows = append(rows, map[string]any{"target": targetKey(t), "name": t.Name, "file": t.File, "control_api": t.ControlAPI,
			"sessions": t.Sessions, "not_applicable": len(t.NotApplicable)})
		api := "no control API"
		if t.ControlAPI != "" {
			api = "control API"
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %s, %d N/A)", targetKey(t), t.Name, api, len(t.NotApplicable)))
	}
	out := map[string]any{"targets": rows}
	if len(bad) > 0 {
		out["invalid"] = bad
	}
	return out, fmt.Sprintf("%d target(s): %s", len(rows), strings.Join(parts, "; ")), nil
}

// ------------------------------------------------------------ runs

func hStartRun(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in StartRunArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	st, e := s.session(in.SessionID)
	if e != nil {
		return nil, "", e
	}
	su, e := s.resolveSuite(in.Suite)
	if e != nil {
		return nil, "", e
	}
	tg, e := s.resolveTarget(in.Target)
	if e != nil {
		return nil, "", e
	}
	if su.FixVersion != st.cfg.FixVersion {
		var match []string
		all, names, _ := s.suites()
		for _, n := range names {
			if all[n].FixVersion == st.cfg.FixVersion {
				match = append(match, n)
			}
		}
		return nil, "", apiErr(400, "version_mismatch", "use a suite for "+st.cfg.FixVersion+": "+strings.Join(match, ", "),
			"suite %s is %s but session %s is %s", su.Name, su.FixVersion, st.cfg.ID, st.cfg.FixVersion)
	}
	if st.cfg.External() && !in.ConfirmExternal {
		return nil, "", apiErr(403, "confirmation_required",
			fmt.Sprintf("a cert run sends real orders to %s; ask the human first, and only if they explicitly approve call start_cert_run again with confirm_external: true", st.cfg.Addr()),
			"session %s is external: start_cert_run needs confirm_external: true", st.cfg.ID)
	}
	want := map[string]bool{}
	for _, id := range in.Cases {
		id = strings.TrimSpace(id)
		if su.Case(id) == nil {
			return nil, "", apiErr(400, "unknown_case", "case ids look like 4.1; list_cert_suites shows the sections", "suite %s has no case %q", su.Name, id)
		}
		want[id] = true
	}
	if in.Section != "" {
		found := false
		var secs []string
		for _, sec := range su.Sections {
			secs = append(secs, sec.ID)
			if sec.ID == in.Section {
				found = true
			}
		}
		if !found {
			return nil, "", apiErr(400, "unknown_section", "sections: "+strings.Join(secs, ", "), "suite %s has no section %q", su.Name, in.Section)
		}
	}
	var sel func(*cert.Case) bool
	if len(want) > 0 || in.Section != "" {
		sel = func(c *cert.Case) bool { return want[c.ID] || (in.Section != "" && c.Section == in.Section) }
	}
	total := 0
	for _, c := range su.Cases {
		if sel == nil || sel(c) {
			total++
		}
	}

	// Claim the session.
	st.op.Lock()
	defer st.op.Unlock()
	st.mu.Lock()
	if st.run != "" {
		run := st.run
		st.mu.Unlock()
		return nil, "", busyErr(st.cfg.ID, run)
	}
	runID := s.newRunID()
	st.run = runID
	st.mu.Unlock()

	r := &runState{done: make(chan struct{}), info: RunInfo{RunID: runID, State: RunRunning, Suite: su.Name, SuiteFile: su.File,
		Target: tg.Name, TargetFile: tg.File, SessionID: st.cfg.ID, Started: time.Now().UTC().Format(time.RFC3339),
		Total: total, Counts: map[string]int{}, Dir: filepath.Join(s.certsDir(), runID), StartedBy: c.client}}
	// The run takes the session over.
	if cur := st.current(); cur != nil && !cur.closed() {
		r.info.TookOver = cur.connected()
		cur.close()
	}
	s.mu.Lock()
	s.runs[runID] = r
	s.mu.Unlock()
	r.save()
	s.ev.Event(st.cfg.ID, "cert run started", fmt.Sprintf("%s suite=%s target=%s cases=%d", runID, su.Name, tg.Name, total), false)
	go s.execute(st, r, su, tg, sel)

	note := ""
	if r.info.TookOver {
		note = fmt.Sprintf(" (%s was connected: the run logged it out and logs on again with a sequence reset)", st.cfg.ID)
	}
	out := map[string]any{"run_id": runID, "state": RunRunning, "suite": su.Name, "target": tg.Name, "session_id": st.cfg.ID,
		"total": total, "took_over_session": r.info.TookOver, "results_dir": r.info.Dir,
		"next": fmt.Sprintf("poll cert_run_status {run_id: %q} until state is finished, then cert_run_results", runID)}
	return out, fmt.Sprintf("cert run %s started: %s vs %s on %s, %d case(s)%s; poll cert_run_status", runID, su.Name, tg.Name, st.cfg.ID, total, note), nil
}

func (s *Service) execute(st *sessState, r *runState, su *cert.Suite, tg *cert.Target, sel func(*cert.Case) bool) {
	defer close(r.done)
	defer func() {
		st.mu.Lock()
		st.run = ""
		st.mu.Unlock()
	}()
	res, dir, err := cert.Execute(cert.ExecOptions{
		Ctx: s.ctx, Config: s.cfg, Session: st.cfg, Suite: su, Target: tg, Select: sel, RunID: r.info.RunID,
		CaseTimeout: 60 * time.Second,
		OnAgent: func(a *agent.Agent) {
			r.mu.Lock()
			r.a = a
			r.mu.Unlock()
		},
		OnCaseStart: func(c *cert.Case) {
			r.mu.Lock()
			r.info.Current = c.ID
			r.mu.Unlock()
			r.save()
		},
		Progress: func(done, total int, cr *cert.CaseResult) {
			r.mu.Lock()
			r.info.Done = done
			r.info.Counts[cr.Status]++
			r.info.Current = ""
			r.mu.Unlock()
			r.save()
		},
		Abort: func() string {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.aborted
		},
	})
	r.mu.Lock()
	aborted := r.aborted
	r.a = nil
	r.info.Finished = time.Now().UTC().Format(time.RFC3339)
	r.info.Current = ""
	switch {
	case res == nil:
		r.info.State, r.info.Error = RunError, fmt.Sprintf("the run could not start: %v", err)
	case aborted != "":
		res.RunError = aborted
		res.Exit = cert.ExitCode(res, 0)
		_ = cert.WriteResults(dir, res)
		r.info.State, r.info.Error = RunError, aborted
	case err != nil:
		r.info.State, r.info.Error = RunError, fmt.Sprintf("writing results: %v", err)
	default:
		r.info.State = RunFinished
	}
	if res != nil {
		r.res = res
		r.info.Counts = res.Counts
		exit := res.Exit
		r.info.ExitCode = &exit
	}
	info := r.info
	r.mu.Unlock()
	r.save()
	counts := ""
	if res != nil {
		counts = cert.CountsLine(res.Counts)
	}
	s.Log.Info("engine", fmt.Sprintf("cert run %s %s: %s %s", info.RunID, info.State, counts, info.Error))
	s.ev.Event(info.SessionID, "cert run ended", fmt.Sprintf("%s %s %s %s", info.RunID, info.State, counts, info.Error), false)
}

// run finds a run in memory or on disk (CLI runs and earlier services').
func (s *Service) run(id string) (*runState, *APIError) {
	s.mu.Lock()
	r, ok := s.runs[id]
	s.mu.Unlock()
	if ok {
		return r, nil
	}
	dir := filepath.Join(s.certsDir(), filepath.Base(id))
	if id == "" || strings.ContainsAny(id, `/\`) || !exists(dir) {
		return nil, apiErr(404, "unknown_run", "recent runs: "+strings.Join(s.recentRuns(5), ", "), "no cert run %q", id)
	}
	r = &runState{done: make(chan struct{})}
	close(r.done)
	var info RunInfo
	if data, err := os.ReadFile(filepath.Join(dir, "run.json")); err == nil && json.Unmarshal(data, &info) == nil {
		r.info = info
	}
	res, err := loadResults(dir)
	if err == nil {
		r.res = res
		if r.info.RunID == "" { // a CLI run
			exit := res.Exit
			r.info = RunInfo{RunID: res.RunID, State: RunFinished, Suite: res.Suite, SuiteFile: res.SuiteFile, Target: res.Target,
				TargetFile: res.TargetFile, SessionID: res.Session, Started: res.Start, Finished: res.End, Total: len(res.Cases),
				Done: len(res.Cases), Counts: res.Counts, ExitCode: &exit, StartedBy: "cli"}
		}
	}
	if r.info.RunID == "" {
		return nil, apiErr(404, "unknown_run", "recent runs: "+strings.Join(s.recentRuns(5), ", "), "cert run %q has neither run.json nor results.json", id)
	}
	r.info.Dir = dir
	if r.info.Counts == nil {
		r.info.Counts = map[string]int{}
	}
	s.mu.Lock()
	if prev, ok := s.runs[id]; ok {
		r = prev
	} else {
		s.runs[id] = r
	}
	s.mu.Unlock()
	return r, nil
}

func (s *Service) recentRuns(n int) []string {
	dirs, _ := filepath.Glob(filepath.Join(s.certsDir(), "*"))
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	var out []string
	for _, d := range dirs {
		if len(out) == n {
			break
		}
		if exists(filepath.Join(d, "results.json")) || exists(filepath.Join(d, "run.json")) {
			out = append(out, filepath.Base(d))
		}
	}
	if len(out) == 0 {
		out = []string{"(none)"}
	}
	return out
}

func hRunStatus(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in RunArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	r, e := s.run(in.RunID)
	if e != nil {
		return nil, "", e
	}
	info := r.snapshot()
	out := map[string]any{"run": info}
	summary := fmt.Sprintf("cert run %s %s: %d/%d case(s) done", info.RunID, info.State, info.Done, info.Total)
	if info.Current != "" {
		summary += ", running case " + info.Current
		out["current_case"] = info.Current
	}
	if len(info.Counts) > 0 {
		summary += " (" + cert.CountsLine(info.Counts) + ")"
	}
	switch info.State {
	case RunRunning:
		if started, err := time.Parse(time.RFC3339, info.Started); err == nil {
			out["elapsed_sec"] = int(time.Since(started).Seconds())
		}
		out["next"] = "poll again in 10-30 seconds"
	case RunError:
		summary += "; ERROR: " + info.Error
		out["next"] = "call cert_run_results for whatever was recorded"
	default:
		out["next"] = "call cert_run_results"
	}
	if info.ExitCode != nil {
		summary += fmt.Sprintf("; exit code %d (%s)", *info.ExitCode, exitMeaning(*info.ExitCode))
	}
	return out, summary, nil
}

func exitMeaning(code int) string {
	switch code {
	case cert.ExitOK:
		return "every required case PASS or N/A"
	case cert.ExitFail:
		return "a required case FAILed"
	case cert.ExitIncomplete:
		return "no required FAIL, but required cases are BLOCKED or PENDING"
	case cert.ExitRunnerError:
		return "a runner ERROR (infrastructure, not the counterparty's fault)"
	case cert.ExitLogonFailed:
		return "the session could not log on"
	case cert.ExitDropped:
		return "the session dropped and could not be re-established"
	case cert.ExitLogoutTO:
		return "our final Logout was never answered"
	}
	return "unknown"
}

// CaseRow is one case in cert_run_results.
type CaseRow struct {
	ID          string            `json:"id"`
	Section     string            `json:"section"`
	Title       string            `json:"title"`
	Task        string            `json:"task"`
	Required    bool              `json:"required"`
	Mode        string            `json:"mode"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason"`
	Warnings    []string          `json:"warnings,omitempty"`
	Attestable  bool              `json:"attestable"`
	Attestation *cert.Attestation `json:"attestation,omitempty"`
	Orders      []string          `json:"orders,omitempty"`
}

func hRunResults(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in ResultsArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	r, e := s.run(in.RunID)
	if e != nil {
		return nil, "", e
	}
	info := r.snapshot()
	if info.State == RunRunning {
		return nil, "", apiErr(409, "run_in_progress", fmt.Sprintf("poll cert_run_status {run_id: %q} until state is finished", in.RunID),
			"cert run %s is still running (%d/%d done)", in.RunID, info.Done, info.Total)
	}
	r.attest.Lock()
	res, err := loadResults(info.Dir)
	r.attest.Unlock()
	if err != nil {
		out := map[string]any{"run": info, "results_dir": info.Dir}
		return out, fmt.Sprintf("cert run %s %s: %s; no results were written", in.RunID, info.State, info.Error), nil
	}
	only := in.Only
	var rows []CaseRow
	var attestable []string
	for _, cr := range res.Cases {
		if cr.Attestable && cr.Attestation == nil {
			attestable = append(attestable, cr.ID)
		}
		keep := false
		switch only {
		case "all":
			keep = true
		case "failed":
			keep = cr.Status == cert.StatusFail || cr.Status == cert.StatusError
		case "pending":
			keep = cr.Status == cert.StatusPending || cr.Status == cert.StatusBlocked || cr.Status == cert.StatusNotRun
		default:
			keep = cr.Status != cert.StatusPass && cr.Status != cert.StatusNA
		}
		if keep {
			rows = append(rows, CaseRow{ID: cr.ID, Section: cr.Section, Title: cr.Title, Task: cr.Task, Required: cr.Required,
				Mode: cr.Mode, Status: cr.Status, Reason: cr.Reason, Warnings: cr.Warnings, Attestable: cr.Attestable,
				Attestation: cr.Attestation, Orders: cr.Orders})
		}
	}
	if rows == nil {
		rows = []CaseRow{}
	}
	if only == "" {
		only = "failed+pending"
	}
	out := map[string]any{"run_id": res.RunID, "state": info.State, "suite": res.Suite, "target": res.Target, "session_id": res.Session,
		"fix_version": res.FixVersion, "exit_code": res.Exit, "exit_meaning": exitMeaning(res.Exit), "counts": res.Counts,
		"required_counts": res.Required, "listed": only, "cases": rows, "attestable_unattested": attestable,
		"results_dir": info.Dir, "results_json": filepath.Join(info.Dir, "results.json"), "summary_txt": filepath.Join(info.Dir, "summary.txt")}
	if info.Error != "" {
		out["run_error"] = info.Error
	}
	if res.Deviations != nil {
		out["deviations"] = res.Deviations
		out["deviations_md"] = filepath.Join(info.Dir, "deviations.md")
	}
	summary := fmt.Sprintf("cert run %s (%s vs %s on %s): %s; required: %s; exit %d (%s)", res.RunID, res.Suite, res.Target, res.Session,
		cert.CountsLine(res.Counts), cert.CountsLine(res.Required), res.Exit, exitMeaning(res.Exit))
	if info.Error != "" {
		summary += "; run ERROR: " + info.Error
	}
	var listed []string
	for _, row := range rows {
		listed = append(listed, row.ID+" "+row.Status)
	}
	if len(listed) > 0 {
		if len(listed) > 20 {
			listed = append(listed[:20], "…")
		}
		summary += "; listed (" + only + "): " + strings.Join(listed, ", ")
	}
	return out, summary, nil
}

func hAttest(_ context.Context, s *Service, c *call) (any, string, *APIError) {
	var in AttestArgs
	if e := c.decode(&in); e != nil {
		return nil, "", e
	}
	if !in.UserConfirmed {
		return nil, "", apiErr(403, "confirmation_required",
			fmt.Sprintf("do not attest on your own: show the human case %s and what it asks, ask them to confirm pass, fail or na and their name, then call attest_cert_case again with their answer and user_confirmed: true", in.CaseID),
			"attest_cert_case needs user_confirmed: true (a human's explicit confirmation)")
	}
	if strings.TrimSpace(in.Note) == "" {
		return nil, "", apiErr(400, "invalid_arguments", "put what the human said or checked in note", "note is required")
	}
	r, e := s.run(in.RunID)
	if e != nil {
		return nil, "", e
	}
	info := r.snapshot()
	if info.State == RunRunning {
		return nil, "", apiErr(409, "run_in_progress", "attest once cert_run_status says the run is finished", "cert run %s is still running", in.RunID)
	}
	r.attest.Lock()
	defer r.attest.Unlock()
	res, err := loadResults(info.Dir)
	if err != nil {
		return nil, "", apiErr(409, "no_results", "only a run that wrote results.json can be attested", "cert run %s has no results: %v", in.RunID, err)
	}
	a := cert.Attestation{Status: in.Status, By: in.By, Note: in.Note, At: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), File: "attest_cert_case (" + c.client + ")"}
	if err := cert.Attest(res, in.CaseID, a); err != nil {
		ae, _ := err.(*cert.AttestError)
		if ae == nil {
			return nil, "", apiErr(400, "invalid_attestation", "", "%v", err)
		}
		status := 400
		if ae.Code == "not_found" {
			status = 404
		}
		return nil, "", apiErr(status, ae.Code, ae.Hint, "%s", ae.Detail)
	}
	res.Exit = cert.ExitCode(res, 0)
	if err := cert.WriteResults(info.Dir, res); err != nil {
		return nil, "", apiErr(500, "write_failed", "check the certs directory is writable", "writing results: %v", err)
	}
	// An audit trail of every attestation made through the service.
	if f, err := os.OpenFile(filepath.Join(info.Dir, "attestations.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		line, _ := json.Marshal(map[string]any{"case_id": in.CaseID, "status": a.Status, "by": a.By, "note": a.Note, "at": a.At, "via": c.client})
		f.Write(append(line, '\n'))
		f.Close()
	}
	r.mu.Lock()
	r.res = res
	r.info.Counts = res.Counts
	exit := res.Exit
	r.info.ExitCode = &exit
	r.mu.Unlock()
	r.save()
	s.ev.Event(res.Session, "cert case attested", fmt.Sprintf("run %s case %s %s by %s: %s (via %s)", res.RunID, in.CaseID, a.Status, a.By, a.Note, c.client), false)
	var caseRow *cert.CaseResult
	reviews := map[string]string{}
	for _, cr := range res.Cases {
		if cr.ID == in.CaseID {
			caseRow = cr
		}
		if cr.Review != "" {
			reviews[cr.ID] = cr.Status
		}
	}
	out := map[string]any{"run_id": res.RunID, "case_id": in.CaseID, "case_status": caseRow.Status, "case_reason": caseRow.Reason,
		"attestation": caseRow.Attestation, "counts": res.Counts, "required_counts": res.Required, "exit_code": res.Exit,
		"exit_meaning": exitMeaning(res.Exit), "review_cases": reviews}
	summary := fmt.Sprintf("case %s attested %s by %s -> %s; run now %s; exit %d (%s)", in.CaseID, a.Status, a.By, caseRow.Status,
		cert.CountsLine(res.Counts), res.Exit, exitMeaning(res.Exit))
	return out, summary, nil
}
