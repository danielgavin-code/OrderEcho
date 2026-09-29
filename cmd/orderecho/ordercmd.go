package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// printOrderEvent prints the order lines an Evidence action carries: each
// report with its live check line, and every check status change.
func printOrderEvent(w io.Writer, e session.Evidence) {
	switch {
	case e.Event == "order report":
		fmt.Fprintln(w, e.Detail)
	case strings.HasPrefix(e.Event, "check "):
		fmt.Fprintf(w, "   ! %s\n", e.Detail)
	case e.Event == "unsolicited or unknown order", e.Event == "duplicate request rejected", e.Event == "order request rejected":
		fmt.Fprintf(w, "   ! %s: %s\n", e.Event, e.Detail)
	case e.Event == "message queued", e.Event == "message dequeued":
		fmt.Fprintf(w, "   . %s: %s\n", e.Event, e.Detail)
	}
}

func cmdOrder(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("order", &configPath, stderr)
	cf := addConnFlags(fs)
	tif := fs.String("tif", "", "time in force: day, gtc, opg, ioc, fok (default: omit 59)")
	wait := fs.Duration("wait", 15*time.Second, "how long to wait for a terminal state")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return exitConfig
	}
	if len(pos) < 4 || len(pos) > 5 {
		fmt.Fprintln(stderr, "orderecho: usage: order --session ID SYM QTY buy|sell|short mkt|lmt [PX] [--tif day] [--wait 15s]")
		return exitConfig
	}
	spec := order.Spec{Symbol: pos[0], Qty: pos[1], Side: pos[2], OrdType: pos[3], TIF: *tif}
	if len(pos) == 5 {
		spec.Price = pos[4]
	}
	if _, _, _, err := spec.Validate(); err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	cfg, sc, ok := resolveSession(configPath, cf, stderr)
	if !ok {
		return exitConfig
	}
	c, ok := openConn(cfg, sc, *cf.reset, stdout, stderr)
	if !ok {
		return exitFailed
	}
	print := func(e session.Evidence) {
		if !c.echo { // with the console echo on, the engine log already shows these
			printOrderEvent(stdout, e)
		}
	}
	if !c.waitLogon(print) {
		return c.finish("", stderr, print)
	}

	o, err := c.a.NewOrder(spec)
	if err != nil {
		fmt.Fprintf(stdout, ">>> order not sent: %v\n", err)
		return c.finish("OrderEcho agent: order not sent", stderr, print)
	}
	fmt.Fprintf(stdout, ">> D 11=%s %s %s %s %s%s sent\n", o.Root, strings.ToUpper(spec.Side), spec.Qty, spec.Symbol,
		strings.ToUpper(spec.OrdType), priceSuffix(spec.Price))

	deadline := time.After(*wait)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	timedOut := false
wait:
	for c.res == nil {
		select {
		case e := <-c.events:
			print(e)
		case <-tick.C:
			if c.a.View(o).Terminal {
				break wait
			}
		case <-deadline:
			timedOut = true
			break wait
		case <-c.sigs:
			c.onSignal()
			break wait
		case r := <-c.resCh:
			c.res = &r
		}
	}
	// Let reports already on their way print before the timeline.
	for drained := false; !drained; {
		select {
		case e := <-c.events:
			print(e)
		default:
			drained = true
		}
	}
	view := c.a.View(o)
	if timedOut && !view.Terminal {
		fmt.Fprintf(stdout, ">>> order %s still %s after %s\n", o.Root, view.State, *wait)
	}
	fmt.Fprintln(stdout)
	printTimeline(stdout, c.a.Chain(o), c.a.Profile)
	verdict := view.Verdict

	code := c.finish("OrderEcho agent: order done", stderr, print)
	if c.forced {
		return code
	}
	switch {
	case code != agent.ExitOK:
		return code
	case verdict == checks.FAIL:
		return agent.ExitChecksFail
	case !view.Terminal:
		return agent.ExitOrderTimeout
	}
	return agent.ExitOK
}

func priceSuffix(px string) string {
	if px == "" {
		return ""
	}
	return " " + px
}

var arrows = map[string]string{checks.DirIn: "-->", checks.DirOut: "<--", checks.DirDisc: " x ", checks.DirUnknown: " ? "}

// printTimeline prints a chain the way the Python viewer's timeline does.
func printTimeline(w io.Writer, c *checks.Chain, prof *profile.Profile) {
	if len(c.Steps) == 0 {
		fmt.Fprintf(w, "no messages found for %s\n", c.Seed)
		return
	}
	fmt.Fprintf(w, "Order chain for %s\n", c.Seed)
	fmt.Fprintf(w, "  ClOrdIDs: %s\n", strings.Join(c.SortedIDs(), ", "))
	if len(c.OrderIDs) > 0 {
		fmt.Fprintf(w, "  OrderID : %s\n", strings.Join(c.SortedOrderIDs(), ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-21s %-4s %-20s %-28s %7s %14s %7s %7s %10s\n", "time", "dir", "type", "exec/status", "qty", "last", "cum", "leaves", "avg")
	for _, s := range c.Steps {
		m := s.Message
		p := prof
		if pv, err := profile.For(m.BeginString()); err == nil {
			p = pv
		}
		stamp := "-"
		if m.TS != nil {
			stamp = m.TS.UTC().Format("2006-01-02T15:04:05.000")
		}
		named := func(tag int, name func(string) string) string {
			v, ok := m.Get(tag)
			if !ok {
				return "-"
			}
			return fmt.Sprintf("%s (%s)", v, name(v))
		}
		exec := named(150, p.ExecTypeName) + " / " + named(39, p.OrdStatusName)
		if _, ok := m.Get(150); !ok {
			if _, ok2 := m.Get(39); !ok2 {
				exec = "- / -"
			}
		}
		last := "-"
		if q := m.Value(32); q != "" && q != "0" && q != "0.00" {
			last = q + "@" + m.Value(31)
		}
		line := fmt.Sprintf("  %-21s %-4s %-20s %-28s %7s %14s %7s %7s %10s", stamp, arrows[m.Direction],
			p.MsgTypeName(m.MsgType()), exec, dash(m.Value(38)), last, dash(m.Value(14)), dash(m.Value(151)), dash(m.Value(6)))
		if s.Replay {
			line += "  [replay]"
		}
		if m.Injected {
			line += " [injected]"
		}
		if m.Suspect() {
			line += " [bad framing]"
		}
		if t := m.Value(58); t != "" {
			line += fmt.Sprintf("  %q", t)
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Checks")
	for _, r := range c.Checks {
		fmt.Fprintf(w, "  [%s] %s: %s\n", r.Status, r.Name, r.Explanation)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  verdict: %s\n", c.Verdict())
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
