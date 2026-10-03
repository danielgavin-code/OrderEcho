// Command orderecho is the OrderEcho FIX agent (initiator / buy side).
//
// See usage() for commands and exit codes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/danielgavin-code/OrderEcho/internal/agent"
	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/store"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

const (
	exitOK     = agent.ExitOK
	exitFailed = agent.ExitLogonFailed
	exitConfig = agent.ExitConfig
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: orderecho [--config PATH] <command> [flags]

commands:
  version                      print version and build
  sessions                     list configured sessions
  status --session ID          persisted seqnums and outbound store size (offline)
  connect --session ID         log on and stay connected
          [--duration 30s]     log out cleanly after this long (default: until Ctrl+C)
          [--test-request]     send one TestRequest after logon
  order --session ID SYM QTY buy|sell|short mkt|lmt [PX]
          [--tif day|gtc|opg|ioc|fok] [--wait 15s]
                               send one order, print each report with its live
                               checks, wait for a terminal state, print the
                               timeline and verdict, log out
  session --session ID         interactive (and pipe-friendly) order session:
                                 order SYM QTY buy|sell|short mkt|lmt [PX] [TIF]
                                 cancel <ClOrdID|last>
                                 replace <ClOrdID|last> QTY [PX]
                                 status | timeline <ClOrdID|last> | resend B [E]
                                 testreq | help | quit
  cert list | show --suite F | run --suite F --target F --session ID [...]
                               certification runner (see "orderecho cert help")
  timeline FILE... (--clordid X | --order-id X) [--json]
                               offline checks on any log (OrderEcho FIX log,
                               evidence JSONL, or raw FIX lines)
  serve [--mcp-http] [--port N]
                               the agent service: owns every session, order
                               manager and cert run; JSON API on 127.0.0.1
                               (service.port, default 8190) under /api/v1;
                               --mcp-http also serves MCP at /mcp (bearer token)
  serve --status               is the service running, its sessions, active runs
  mcp                          MCP server on stdio for Claude Desktop (starts the
                               service in the background if none is running)
  mcp install-claude-desktop [--write]
                               print (or merge) the Claude Desktop config entry

flags for every command that connects (connect, order, session):
  --session ID                 the configured session
  --fix-version FIX.4.2|FIX.4.4  override the session's fix_version
  --reset                      reset sequence numbers (Logon carries 141=Y)
  --reconnect                  reconnect after a dropped connection

exit codes (commands that connect):
  0  clean logout (order: checks PASS or WARN)
  1  logon refused or failed
  2  config or usage error
  3  connection dropped after logon
  4  logout timeout (our Logout was never answered)
  5  order checks FAIL
  6  timed out waiting for an order to reach a terminal state
  130 second Ctrl+C (exit without waiting)
exit codes (timeline): 0 PASS, 1 WARN, 2 FAIL or nothing found, as the Python viewer.
exit codes (cert run): 0 all required PASS/N/A, 5 required FAIL, 7 required BLOCKED/PENDING,
  8 runner ERROR, 1/3/4 session failure, 2 usage.
exit codes (serve): 0 stopped cleanly, 1 port in use / not running (--status), 2 config or usage.

env: ORDERECHO_HOME=DIR  change to DIR first (Claude Desktop cannot set a working directory)
     ORDERECHO_MCP_TOKEN  bearer token for serve --mcp-http (wins over mcp.http_token)
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	if home := os.Getenv(homeEnv); home != "" {
		if err := os.Chdir(home); err != nil {
			fmt.Fprintf(stderr, "orderecho: %s=%s: %v\n", homeEnv, home, err)
			return exitConfig
		}
	}
	global := flag.NewFlagSet("orderecho", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { usage(stderr) }
	configPath := global.String("config", config.DefaultPath, "path to the YAML config")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitConfig
	}
	rest := global.Args()
	if len(rest) == 0 {
		usage(stderr)
		return exitConfig
	}
	cmd, cmdArgs := rest[0], rest[1:]
	switch cmd {
	case "version":
		fmt.Fprintf(stdout, "orderecho %s (build %s)\n", version.Version, version.Build)
		return exitOK
	case "sessions":
		return cmdSessions(cmdArgs, *configPath, stdout, stderr)
	case "status":
		return cmdStatus(cmdArgs, *configPath, stdout, stderr)
	case "connect":
		return cmdConnect(cmdArgs, *configPath, stdout, stderr)
	case "order":
		return cmdOrder(cmdArgs, *configPath, stdout, stderr)
	case "session":
		return cmdSession(cmdArgs, *configPath, os.Stdin, stdout, stderr)
	case "timeline":
		return cmdTimeline(cmdArgs, stdout, stderr)
	case "cert":
		return cmdCert(cmdArgs, *configPath, stdout, stderr)
	case "serve":
		return cmdServe(cmdArgs, *configPath, stdout, stderr)
	case "mcp":
		return cmdMCP(cmdArgs, *configPath, stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	fmt.Fprintf(stderr, "orderecho: unknown command %q\n", cmd)
	usage(stderr)
	return exitConfig
}

// subFlags gives every subcommand its own --config too, so it may come after
// the command name.
func subFlags(name string, configPath *string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("orderecho "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(configPath, "config", *configPath, "path to the YAML config")
	return fs
}

func loadConfig(path string, stderr io.Writer) (*config.Config, bool) {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return nil, false
	}
	return cfg, true
}

// ------------------------------------------------------------ sessions

func cmdSessions(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("sessions", &configPath, stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return exitConfig
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tVERSION\tROUTE\tADDRESS\tHB\tRESET\tRECONNECT")
	for _, s := range cfg.Sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s -> %s\t%s\t%ds\t%v\t%v\n",
			s.ID, s.FixVersion, s.SenderCompID, s.TargetCompID, s.Addr(), s.HeartbeatSec, s.ResetOnLogon, s.Reconnect)
	}
	tw.Flush()
	return exitOK
}

// -------------------------------------------------------------- status

func cmdStatus(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("status", &configPath, stderr)
	id := fs.String("session", "", "session id")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return exitConfig
	}
	if *id == "" {
		fmt.Fprintln(stderr, "orderecho: status needs --session ID")
		return exitConfig
	}
	s, err := cfg.Session(*id)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	seq := store.NewFileSeqStore(cfg.Storage.SeqnumDir, s.ID)
	out, in, err := seq.Load()
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	count, storePath, err := store.CountFileRecords(cfg.Storage.MsgstoreDir, s.ID)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	note := ""
	if !seq.Exists() {
		note = "  (no file yet; a first connect starts at 1/1)"
	}
	fmt.Fprintf(stdout, "session        : %s (%s %s -> %s @ %s)\n", s.ID, s.FixVersion, s.SenderCompID, s.TargetCompID, s.Addr())
	fmt.Fprintf(stdout, "seqnum file    : %s%s\n", seq.Path, note)
	fmt.Fprintf(stdout, "next_out       : %d\n", out)
	fmt.Fprintf(stdout, "next_in        : %d\n", in)
	fmt.Fprintf(stdout, "outbound store : %s (%d message(s))\n", storePath, count)
	return exitOK
}
