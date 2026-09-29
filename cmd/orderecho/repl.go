package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/session"
	"github.com/danielgavin-code/OrderEcho/internal/order"
)

// answerWait is how long an order command waits for the first response to
// its request before reading the next command, so piped sessions read in
// the order things happened.
const answerWait = 5 * time.Second

const replHelp = `commands:
  order SYM QTY buy|sell|short mkt|lmt [PX] [TIF]
  cancel <ClOrdID|last>
  replace <ClOrdID|last> QTY [PX]
  status
  timeline <ClOrdID|last>
  resend BEGIN [END]
  testreq
  help
  quit`

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func cmdSession(args []string, configPath string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := subFlags("session", &configPath, stderr)
	cf := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "orderecho: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
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
	interactive := false
	if f, ok := stdin.(*os.File); ok {
		interactive = isTerminal(f)
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	prompt := func() {
		if interactive {
			fmt.Fprint(stdout, "> ")
		}
	}
	fmt.Fprintln(stdout, `>>> session ready; type "help" for commands`)
	prompt()
	r := &repl{c: c, out: stdout, print: print}
loop:
	for c.res == nil {
		select {
		case e := <-c.events:
			print(e)
		case <-c.sigs:
			c.onSignal()
			break loop
		case res := <-c.resCh:
			c.res = &res
		case line, open := <-lines:
			if !open {
				fmt.Fprintln(stdout, ">>> end of input: logging out")
				break loop
			}
			if strings.TrimSpace(line) != "" && !interactive {
				fmt.Fprintf(stdout, "> %s\n", line)
			}
			if r.exec(line) {
				break loop
			}
			prompt()
		}
	}
	code := c.finish("OrderEcho agent: session done", stderr, print)
	if code != agent.ExitOK || c.forced {
		return code
	}
	fail := false
	c.a.With(func(m *order.Manager) {
		for _, o := range m.Orders() {
			if o.Verdict == checks.FAIL {
				fail = true
			}
		}
	})
	if fail {
		return agent.ExitChecksFail
	}
	return agent.ExitOK
}

type repl struct {
	c     *conn
	out   io.Writer
	print func(session.Evidence)
}

// exec runs one command; true means quit.
func (r *repl) exec(line string) bool {
	f := strings.Fields(line)
	if len(f) == 0 {
		return false
	}
	a := r.c.a
	switch strings.ToLower(f[0]) {
	case "quit", "exit":
		return true
	case "help", "?":
		fmt.Fprintln(r.out, replHelp)
	case "order":
		if len(f) < 5 || len(f) > 7 {
			fmt.Fprintln(r.out, "usage: order SYM QTY buy|sell|short mkt|lmt [PX] [TIF]")
			return false
		}
		spec := order.Spec{Symbol: f[1], Qty: f[2], Side: f[3], OrdType: f[4]}
		rest := f[5:]
		if len(rest) > 0 && strings.EqualFold(spec.OrdType, "lmt") {
			spec.Price, rest = rest[0], rest[1:]
		}
		if len(rest) > 0 {
			spec.TIF, rest = rest[0], rest[1:]
		}
		if len(rest) > 0 {
			fmt.Fprintf(r.out, "error: unexpected %q\n", strings.Join(rest, " "))
			return false
		}
		o, err := a.NewOrder(spec)
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		fmt.Fprintf(r.out, ">> D 11=%s %s %s %s %s%s sent\n", o.Root, strings.ToUpper(spec.Side), spec.Qty, spec.Symbol,
			strings.ToUpper(spec.OrdType), priceSuffix(spec.Price))
		r.awaitAnswer(o, o.Root)
	case "cancel":
		if len(f) != 2 {
			fmt.Fprintln(r.out, "usage: cancel <ClOrdID|last>")
			return false
		}
		o, id, err := a.Cancel(f[1])
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		fmt.Fprintf(r.out, ">> F 11=%s 41=%s sent\n", id, requestOrig(a, o, id))
		r.awaitAnswer(o, id)
	case "replace":
		if len(f) < 3 || len(f) > 4 {
			fmt.Fprintln(r.out, "usage: replace <ClOrdID|last> QTY [PX]")
			return false
		}
		px := ""
		if len(f) == 4 {
			px = f[3]
		}
		o, id, err := a.Replace(f[1], f[2], px)
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		fmt.Fprintf(r.out, ">> G 11=%s 41=%s qty=%s%s sent\n", id, requestOrig(a, o, id), f[2], priceSuffix(px))
		r.awaitAnswer(o, id)
	case "status":
		a.With(func(m *order.Manager) {
			if len(m.Orders()) == 0 {
				fmt.Fprintln(r.out, "no orders yet")
				return
			}
			for _, l := range m.Status() {
				fmt.Fprintln(r.out, l)
			}
		})
	case "timeline":
		ref := "last"
		if len(f) > 1 {
			ref = f[1]
		}
		var o *order.Order
		var err error
		a.With(func(m *order.Manager) { o, err = m.Find(ref) })
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		printTimeline(r.out, a.Chain(o), a.Profile)
	case "resend":
		if len(f) < 2 || len(f) > 3 {
			fmt.Fprintln(r.out, "usage: resend BEGIN [END]")
			return false
		}
		b, err1 := strconv.Atoi(f[1])
		e := 0
		var err2 error
		if len(f) == 3 {
			e, err2 = strconv.Atoi(f[2])
		}
		if err1 != nil || err2 != nil {
			fmt.Fprintln(r.out, "error: BEGIN and END must be numbers")
			return false
		}
		err := a.Init.Exec(func(s *session.Session) ([]session.Action, error) { return s.SendResendRequest(b, e) })
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		fmt.Fprintf(r.out, ">> ResendRequest 7=%d 16=%d sent\n", b, e)
	case "testreq":
		id, err := a.Init.TestRequest()
		if err != nil {
			fmt.Fprintf(r.out, "error: %v\n", err)
			return false
		}
		fmt.Fprintf(r.out, ">> TestRequest %s sent\n", id)
		r.awaitTest(id)
	case "json": // undocumented: the shadow snapshot, for debugging
		a.With(func(m *order.Manager) {
			for _, o := range m.Orders() {
				b, _ := json.Marshal(o.Snapshot())
				fmt.Fprintln(r.out, string(b))
			}
		})
	default:
		fmt.Fprintf(r.out, "error: unknown command %q (try help)\n", f[0])
	}
	return false
}

func requestOrig(a *agent.Agent, o *order.Order, id string) string {
	for _, rq := range a.View(o).Requests {
		if rq.ClOrdID == id {
			return rq.OrigClOrdID
		}
	}
	return "?"
}

// awaitAnswer prints events until the request with ClOrdID id has its first
// answer, or answerWait passes.
func (r *repl) awaitAnswer(o *order.Order, id string) {
	deadline := time.Now().Add(answerWait)
	for time.Now().Before(deadline) {
		answered := false
		for _, rq := range r.c.a.View(o).Requests {
			if rq.ClOrdID == id && rq.Answered() {
				answered = true
			}
		}
		if answered {
			break
		}
		select {
		case e := <-r.c.events:
			r.print(e)
		case <-time.After(20 * time.Millisecond):
		}
		if r.c.done() {
			return
		}
	}
	// Anything that arrived together with the answer.
	for {
		select {
		case e := <-r.c.events:
			r.print(e)
			continue
		case <-time.After(50 * time.Millisecond):
		}
		return
	}
}

func (r *repl) awaitTest(id string) {
	deadline := time.Now().Add(answerWait)
	for time.Now().Before(deadline) {
		select {
		case e := <-r.c.events:
			r.print(e)
			if e.Event == "testrequest answered" && strings.TrimPrefix(e.Detail, "112=") == id {
				fmt.Fprintf(r.out, ">> TestRequest %s answered\n", id)
				return
			}
		case <-time.After(20 * time.Millisecond):
		}
		if r.c.done() {
			return
		}
	}
	fmt.Fprintf(r.out, ">> TestRequest %s not answered within %s\n", id, answerWait)
}
