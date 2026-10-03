package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/mcpserver"
	"github.com/danielgavin-code/OrderEcho/internal/service"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// Exit codes of serve.
const (
	exitServeNotRunning = 1 // serve --status: no service
	exitServeInUse      = 1 // the port is taken
)

// mcpToken is the bearer token for MCP over HTTP: env wins over config.
func mcpToken(cfg *config.Config) string {
	if t := os.Getenv("ORDERECHO_MCP_TOKEN"); t != "" {
		return t
	}
	return cfg.MCP.HTTPToken
}

func cmdServe(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("serve", &configPath, stderr)
	status := fs.Bool("status", false, "report whether the service is running, its sessions and active runs, then exit")
	mcpHTTP := fs.Bool("mcp-http", false, "also serve MCP at /mcp (needs a bearer token: mcp.http_token or ORDERECHO_MCP_TOKEN)")
	port := fs.Int("port", 0, "override service.port")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "orderecho: serve: unexpected arguments %v\n", fs.Args())
		return exitConfig
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return exitConfig
	}
	if *port != 0 {
		if *port < 1 || *port > 65535 {
			fmt.Fprintf(stderr, "orderecho: --port must be 1..65535\n")
			return exitConfig
		}
		cfg.Service.Port = *port
	}
	if *status {
		return serveStatus(cfg, stdout)
	}
	var token string
	if *mcpHTTP {
		if token = mcpToken(cfg); token == "" {
			fmt.Fprintln(stderr, "orderecho: --mcp-http needs a bearer token: set mcp.http_token in the config or ORDERECHO_MCP_TOKEN; refusing to serve MCP over HTTP without one")
			return exitConfig
		}
	}
	ln, err := net.Listen("tcp", cfg.Service.Addr())
	if err != nil {
		if h, herr := service.NewClient(cfg.Service.URL(), "").Health(context.Background()); herr == nil {
			fmt.Fprintf(stderr, "orderecho: an agent service is already running on %s (pid %d, version %s, config %s)\n", cfg.Service.Addr(), h.PID, h.Version, h.Config)
		} else {
			fmt.Fprintf(stderr, "orderecho: cannot listen on %s: %v\n", cfg.Service.Addr(), err)
		}
		return exitServeInUse
	}
	var console io.Writer
	if cfg.Logging.Console {
		console = stdout
	}
	svc, err := service.New(service.Options{Config: cfg, Console: console})
	if err != nil {
		ln.Close()
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	opt := service.HandlerOptions{}
	if *mcpHTTP {
		opt.MCP = mcpserver.HTTPHandler(svc, token)
	}
	srv := &http.Server{Handler: svc.Handler(opt), ReadHeaderTimeout: 10 * time.Second}
	mcpNote := "off"
	if *mcpHTTP {
		mcpNote = "http://" + cfg.Service.Addr() + "/mcp (bearer token required)"
	}
	banner := fmt.Sprintf("OrderEcho agent service %s (%s) pid %d on http://%s  api /api/v1  mcp-http %s  config %s  sessions %d  tools %d/%d for MCP",
		version.Version, version.Build, os.Getpid(), cfg.Service.Addr(), mcpNote, cfg.Path, len(cfg.Sessions), len(svc.EnabledTools()), len(service.Catalog()))
	svc.Log.Info("engine", "Startup: "+banner)
	if console == nil {
		fmt.Fprintln(stdout, banner)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "orderecho: serve: %v\n", err)
		}
		svc.Shutdown(30 * time.Second)
		return exitFailed
	case sig := <-sigs:
		svc.Log.Info("engine", fmt.Sprintf("%s: logging out every session, stopping active cert runs", sig))
	}
	done := make(chan struct{})
	go func() {
		svc.Shutdown(30 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		close(done)
	}()
	select {
	case <-done:
		return exitOK
	case <-sigs:
		fmt.Fprintln(stderr, "orderecho: second signal: exiting without waiting")
		return 130
	}
}

func serveStatus(cfg *config.Config, stdout io.Writer) int {
	c := service.NewClient(cfg.Service.URL(), "")
	st, err := c.Status(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "agent service: not running on %s (%v)\n", cfg.Service.Addr(), err)
		return exitServeNotRunning
	}
	fmt.Fprintf(stdout, "agent service: running on %s, pid %d, version %s (%s), since %s\n", cfg.Service.Addr(), st.PID, st.Version, st.Build, st.Started)
	fmt.Fprintf(stdout, "config       : %s\n", st.Config)
	mcpHTTP := "off"
	if st.MCPHTTP {
		mcpHTTP = "on (/mcp)"
	}
	fmt.Fprintf(stdout, "mcp over http: %s\n\n", mcpHTTP)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tVERSION\tROUTE\tADDRESS\tSTATE\tORDERS\tOPEN\tNOTE")
	for _, s := range st.Sessions {
		note := ""
		if s.External {
			note = "external"
		}
		if s.EmulatorSession != "" {
			note = "emulator:" + s.EmulatorSession
		}
		if s.CertRun != "" {
			note += " cert run " + s.CertRun
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", s.SessionID, s.FixVersion, s.Route, s.Address, s.State, s.Orders, s.OpenOrders, note)
	}
	tw.Flush()
	fmt.Fprintf(stdout, "\nactive cert runs: %d\n", len(st.Runs))
	for _, r := range st.Runs {
		cur := ""
		if r.Current != "" {
			cur = ", running " + r.Current
		}
		fmt.Fprintf(stdout, "  %s  %s vs %s on %s  %s/%s done%s\n", r.RunID, r.Suite, r.Target, r.SessionID, strconv.Itoa(r.Done), strconv.Itoa(r.Total), cur)
	}
	return exitOK
}
