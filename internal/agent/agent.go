// Package agent assembles one running FIX session from config: logs,
// evidence, stores, the pure session core, the order manager and the TCP
// transport. The CLI and the interop tests both use it, so what the tests
// exercise is what the CLI runs.
package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/evidence"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/fix/transport"
	"github.com/danielgavin-code/OrderEcho/internal/logs"
	"github.com/danielgavin-code/OrderEcho/internal/order"
	"github.com/danielgavin-code/OrderEcho/internal/store"
)

// Exit codes shared by every command that connects (A2 3.3).
const (
	ExitOK            = 0 // clean logout (and, for orders, PASS/WARN)
	ExitLogonFailed   = 1 // logon refused or failed
	ExitConfig        = 2 // config or usage error
	ExitDropped       = 3 // connection dropped after logon
	ExitLogoutTimeout = 4 // our Logout was never answered
	ExitChecksFail    = 5 // an order's checks FAIL
	ExitOrderTimeout  = 6 // an order did not reach a terminal state in time
	ExitForced        = 130
)

// Options for New.
type Options struct {
	Config  *config.Config
	Session config.Session // effective session (after CLI overrides)
	Clock   clock.Clock
	// Console receives FIX and INFO+ engine lines when non-nil.
	Console io.Writer
	// RunID names the evidence file and ClOrdIDs; default: now.
	RunID string
	IDs   *order.IDs // shared ClOrdID generator; default: one per agent
	Reset bool       // force a sequence reset on the next Logon
	Tick  time.Duration
	// EngineLevel overrides the config's engine log level ("" = config).
	EngineLevel string
	OnEvidence  func(session.Evidence)
}

// Agent is one session, ready to run.
type Agent struct {
	Opt       Options
	Profile   *profile.Profile
	Evidence  *evidence.Writer
	FixLog    *logs.FixLog
	EngineLog *logs.EngineLog
	SeqStore  *store.FileSeqStore
	MsgStore  *store.FileMessageStore
	Session   *session.Session
	Orders    *order.Manager
	Init      *transport.Initiator
	RunID     string
}

// New builds the agent (nothing connects until Run).
func New(opt Options) (*Agent, error) {
	sc, cfg := opt.Session, opt.Config
	if opt.Clock == nil {
		opt.Clock = clock.SystemClock{}
	}
	prof, err := profile.For(sc.FixVersion)
	if err != nil {
		return nil, err
	}
	clk := opt.Clock
	runID := opt.RunID
	if runID == "" {
		runID = evidence.MakeRunID(clk.Now())
	}
	levelName := cfg.Logging.EngineLevel
	if opt.EngineLevel != "" {
		levelName = opt.EngineLevel
	}
	level, _ := logs.ParseLevel(levelName)
	a := &Agent{Opt: opt, Profile: prof, RunID: runID}
	a.EngineLog = logs.NewEngineLog(cfg.Logging.LogDir, clk, level, opt.Console)
	a.FixLog = logs.NewFixLog(cfg.Logging.LogDir, sc.ID, clk, cfg.Logging.FixDelimiter, opt.Console)
	a.Evidence = evidence.Open(cfg.Storage.EvidenceDir, runID, clk)
	a.SeqStore = store.NewFileSeqStore(cfg.Storage.SeqnumDir, sc.ID)
	a.MsgStore, err = store.OpenFileMessageStore(cfg.Storage.MsgstoreDir, sc.ID)
	if err != nil {
		a.Close()
		return nil, fmt.Errorf("outbound message store: %w", err)
	}
	if v := a.MsgStore.StoredVersion(); v != "" && v != prof.Name {
		archived, aerr := a.MsgStore.Archive(clk.Now())
		msg := fmt.Sprintf("Message store holds %s messages but this session is %s; archived to %s so replay never sends another version's bytes", v, prof.Name, archived)
		if aerr != nil {
			msg = fmt.Sprintf("Message store holds %s messages but this session is %s; archive failed: %v", v, prof.Name, aerr)
		}
		a.EngineLog.Warning(sc.ID, msg)
		a.Evidence.Event(sc.ID, "store archived", msg, false)
	}
	ids := opt.IDs
	if ids == nil {
		ids = &order.IDs{Prefix: sc.ClOrdIDPrefix, RunID: runID}
	}
	a.Orders = order.NewManager(order.Options{
		Session: sc.ID, Profile: prof, IDs: ids, Clock: clk,
		Order: profile.OrderOptions{IncludeHandlInst: sc.IncludeHandlInst}, Account: sc.Account,
		SenderID: sc.SenderCompID,
	})
	a.Session, err = session.New(session.Config{
		SessionID: sc.ID, Profile: prof, SenderCompID: sc.SenderCompID, TargetCompID: sc.TargetCompID,
		HeartbeatSec: sc.HeartbeatSec, ResetOnLogon: sc.ResetOnLogon, LogonTimeout: sc.LogonTimeout(),
		LogoutTimeout: sc.LogoutTimeout(), HeartbeatGracePct: sc.HeartbeatGracePct,
		HeartbeatMismatch: sc.HeartbeatMismatch,
	}, a.SeqStore, a.MsgStore, clk, a.Orders)
	if err != nil {
		a.Close()
		return nil, err
	}
	if opt.Reset {
		a.Session.RequestReset()
	}
	a.Init = transport.New(transport.Options{
		Addr: sc.Addr(), Session: a.Session, Clock: clk, Evidence: a.Evidence, FixLog: a.FixLog,
		EngineLog: a.EngineLog, Reconnect: sc.Reconnect, ReconnectInterval: sc.ReconnectInterval(),
		DialTimeout: sc.LogonTimeout(), Tick: opt.Tick, OnEvidence: opt.OnEvidence,
	})
	return a, nil
}

// Run connects and runs until the session ends for good.
func (a *Agent) Run(ctx context.Context) transport.Result { return a.Init.Run(ctx) }

// Close closes files.
func (a *Agent) Close() {
	if a.MsgStore != nil {
		a.MsgStore.Close()
	}
	if a.Evidence != nil {
		a.Evidence.Close()
	}
	if a.FixLog != nil {
		a.FixLog.Close()
	}
	if a.EngineLog != nil {
		a.EngineLog.Close()
	}
}

// send runs prepare (under the lock), sends what it built, and undoes the
// preparation if the send fails.
func (a *Agent) send(msgType string, prepare func(m *order.Manager) (*order.Order, []codec.Field, error)) (*order.Order, string, error) {
	var o *order.Order
	var id string
	err := a.Init.Exec(func(s *session.Session) ([]session.Action, error) {
		var body []codec.Field
		var err error
		o, body, err = prepare(a.Orders)
		if err != nil {
			return nil, err
		}
		id = fieldValue(body, 11)
		_, actions, err := s.SendApp(msgType, body)
		if err != nil {
			a.Orders.Forget(o, id)
			return actions, err
		}
		return actions, nil
	})
	return o, id, err
}

func fieldValue(fields []codec.Field, tag int) string {
	for _, f := range fields {
		if f.Tag == tag {
			return f.Value
		}
	}
	return ""
}

// NewOrder sends a NewOrderSingle.
func (a *Agent) NewOrder(spec order.Spec) (*order.Order, error) {
	o, _, err := a.send("D", func(m *order.Manager) (*order.Order, []codec.Field, error) { return m.NewOrder(spec) })
	return o, err
}

// Cancel sends an OrderCancelRequest for ref (a ClOrdID, an OrderID or "last").
func (a *Agent) Cancel(ref string) (*order.Order, string, error) {
	return a.send("F", func(m *order.Manager) (*order.Order, []codec.Field, error) { return m.Cancel(ref) })
}

// Replace sends an OrderCancelReplaceRequest.
func (a *Agent) Replace(ref, qty, price string) (*order.Order, string, error) {
	return a.send("G", func(m *order.Manager) (*order.Order, []codec.Field, error) { return m.Replace(ref, qty, price) })
}

// With runs fn with the order manager under the session lock.
func (a *Agent) With(fn func(m *order.Manager)) { a.Init.Locked(func() { fn(a.Orders) }) }

// OrderView is a consistent copy of an order's state.
type OrderView struct {
	Root, Current, OrderID, State, Verdict, CheckLine string
	Terminal                                          bool
	Requests                                          []order.Request
	Reports                                           int
}

// View returns a copy of the order's state under the lock.
func (a *Agent) View(o *order.Order) OrderView {
	var v OrderView
	a.With(func(*order.Manager) {
		v = OrderView{Root: o.Root, Current: o.Current, OrderID: o.OrderID, State: o.State,
			Verdict: o.Verdict, CheckLine: order.CheckLine(o), Terminal: o.Terminal(), Reports: o.Reports}
		for _, r := range o.Requests {
			v.Requests = append(v.Requests, *r)
		}
	})
	return v
}

// WaitFor polls until cond (evaluated under the lock) is true or timeout.
func (a *Agent) WaitFor(timeout time.Duration, cond func(m *order.Manager) bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		ok := false
		a.With(func(m *order.Manager) { ok = cond(m) })
		if ok {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Chain returns the order's chain as the checks see it now.
func (a *Agent) Chain(o *order.Order) *checks.Chain {
	var c *checks.Chain
	a.With(func(m *order.Manager) { c = m.Chain(o) })
	return c
}

// ------------------------------------------------------------ exit codes

// SessionExitCode maps how Run ended onto the A2 exit codes.
func SessionExitCode(res transport.Result) int {
	o := res.Outcome
	switch {
	case o.CleanLogout:
		return ExitOK
	case o.DisconnectCause == "Logout timeout":
		return ExitLogoutTimeout
	case res.Logons > 0 && o.LoggedOn:
		return ExitDropped
	case res.Logons > 0 && !o.LoggedOn && !o.Refused && res.DialErr == nil:
		// Logged on earlier, then a reconnect never got back in.
		return ExitDropped
	}
	return ExitLogonFailed
}

// Explain is the one-line reason for a non-zero session exit.
func Explain(res transport.Result, sc config.Session) string {
	o := res.Outcome
	switch {
	case o.CleanLogout:
		who := "counterparty"
		if o.LogoutByUs {
			who = "us"
		}
		text := ""
		if o.LogoutText != "" {
			text = fmt.Sprintf(" (their 58: %q)", o.LogoutText)
		}
		return fmt.Sprintf("Logged out cleanly (initiated by %s)%s", who, text)
	case res.DialErr != nil && res.Connects == 0:
		return fmt.Sprintf("cannot connect to %s: %v", sc.Addr(), res.DialErr)
	case o.Refused:
		return fmt.Sprintf("LOGON REFUSED by counterparty (Logout instead of Logon): %s", nonEmpty(o.RefusalText))
	case o.DisconnectCause == "Logout timeout":
		return fmt.Sprintf("LOGOUT TIMEOUT: no Logout reply within %gs", sc.LogoutTimeoutSec)
	case !o.LoggedOn && o.DisconnectCause == "Logon timeout":
		return fmt.Sprintf("no Logon reply from %s within %gs", sc.Addr(), sc.LogonTimeoutSec)
	case !o.LoggedOn && o.DisconnectCause == "" && res.Logons == 0:
		return fmt.Sprintf("counterparty at %s closed the connection without answering our Logon "+
			"(no Logout, no reason given) - check sender_comp_id=%s, target_comp_id=%s, fix_version=%s and the port",
			sc.Addr(), sc.SenderCompID, sc.TargetCompID, sc.FixVersion)
	case !o.LoggedOn && res.Logons == 0:
		return "LOGON FAILED: " + o.DisconnectCause
	case o.DisconnectCause != "":
		return "DROPPED after logon: " + o.DisconnectCause
	}
	return "DROPPED after logon: connection closed by counterparty"
}

func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no text)"
	}
	return s
}
