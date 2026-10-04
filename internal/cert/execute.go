package cert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// ExecOptions describe one live cert run. The CLI (cert run) and the agent
// service (start_cert_run) both use Execute, so a run is the same run
// whichever front door started it.
type ExecOptions struct {
	Ctx         context.Context
	Config      *config.Config
	Session     config.Session
	Suite       *Suite
	Target      *Target
	Attest      map[string]Attestation
	CLIVars     map[string]string
	Select      func(*Case) bool
	CaseTimeout time.Duration
	StopOnFail  bool
	RunID       string
	Console     io.Writer // FIX/engine echo (nil = none)
	Progress    func(done, total int, r *CaseResult)
	OnCaseStart func(c *Case)
	Abort       func() string
	// OnWire sees every message of the run's session (A5 live feed).
	OnWire func(dir string, m *codec.Message)
	// OnAgent is given the run's agent once built (to stop it from outside).
	OnAgent func(a *agent.Agent)
}

// Execute builds a fresh agent for the session, runs the suite on it with a
// sequence reset at the start, logs out, and writes the results directory.
// The error is only for failures before the run could start or while
// writing results; everything else is in the RunResult.
func Execute(o ExecOptions) (*RunResult, string, error) {
	clk := clock.SystemClock{}
	if o.RunID == "" {
		o.RunID = evidence.MakeRunID(clk.Now())
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	sc := o.Session
	sc.Reconnect = false // the runner decides when to reconnect
	hist := NewHistory(clk)
	a, err := agent.New(agent.Options{Config: o.Config, Session: sc, Clock: clk, Console: o.Console, RunID: o.RunID,
		OnWire: func(dir string, m *codec.Message) {
			hist.Add(dir, m)
			if o.OnWire != nil {
				o.OnWire(dir, m)
			}
		}})
	if err != nil {
		return nil, "", err
	}
	defer a.Close()
	if o.OnAgent != nil {
		o.OnAgent(a)
	}
	drv := NewLive(o.Ctx, a, hist)
	res := Run(Options{
		Suite: o.Suite, Target: o.Target, Attest: o.Attest, Driver: drv, Clock: clk, CLIVars: o.CLIVars,
		CaseTimeout: o.CaseTimeout, StopOnFail: o.StopOnFail, Select: o.Select, RunID: o.RunID,
		Version: version.Version, Build: version.Build, ConnectAtStart: true, ResetAtStart: true,
		Progress: o.Progress, OnCaseStart: o.OnCaseStart, Abort: o.Abort, Counterparty: Identify(o.Target),
	})
	sessionCode := agent.ExitOK
	if r, err := drv.Close(); err == nil && r != nil && !res.NeverLoggedOn && r.Outcome.DisconnectCause == "Logout timeout" {
		sessionCode = agent.ExitLogoutTimeout
		res.LogoutTimeout = true
	}
	res.Exit = ExitCode(res, sessionCode)
	dir := filepath.Join(o.Config.Storage.CertsDir, o.RunID)
	if err := WriteResults(dir, res); err != nil {
		return res, dir, err
	}
	return res, dir, nil
}

// Identify names the counterparty when the target can tell: a control API
// that answers /health with a version (the OrderEcho emulator does).
func Identify(t *Target) string {
	if t == nil || t.ControlAPI == "" {
		return ""
	}
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := c.Get(strings.TrimRight(t.ControlAPI, "/") + "/health")
	if err != nil {
		return fmt.Sprintf("%s (control API %s did not answer)", t.Name, t.ControlAPI)
	}
	defer resp.Body.Close()
	var h struct {
		Version string `json:"version"`
		Build   string `json:"build"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil || h.Version == "" {
		return fmt.Sprintf("%s (control API %s)", t.Name, t.ControlAPI)
	}
	return fmt.Sprintf("OrderEcho FIX emulator %s (%s), control API %s", h.Version, h.Build, t.ControlAPI)
}
