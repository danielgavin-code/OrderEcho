package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/mcpserver"
	"github.com/danielgavin-code/OrderEcho/internal/service"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

const mcpUsage = `usage: orderecho mcp [--config PATH]
       orderecho mcp install-claude-desktop [--config PATH] [--write] [--desktop-config FILE]

  mcp                      MCP server on stdio (for Claude Desktop). It talks to the running
                           agent service (orderecho serve) and starts one in the background
                           if none is running (logs under <log_dir>/engine/).
  install-claude-desktop   print the Claude Desktop config snippet for this agent; with
                           --write, merge it into Claude Desktop's config file (backed up first)
`

// homeEnv names the working directory (the repo) for the MCP server when a
// client such as Claude Desktop cannot set one.
const homeEnv = "ORDERECHO_HOME"

func cmdMCP(args []string, configPath string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "install-claude-desktop":
			return cmdInstallClaudeDesktop(args[1:], configPath, stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, mcpUsage)
			return exitOK
		}
	}
	fs := subFlags("mcp", &configPath, stderr)
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "orderecho: mcp: unexpected arguments %v\n%s", fs.Args(), mcpUsage)
		return exitConfig
	}
	cfg, ok := loadConfig(configPath, stderr)
	if !ok {
		return exitConfig
	}
	absConfig, _ := filepath.Abs(configPath)
	client := service.NewClient(cfg.Service.URL(), service.ClientMCPStdio)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigs; cancel() }()

	h, err := ensureService(ctx, cfg, absConfig, client, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho mcp: %v\n", err)
		return exitFailed
	}
	if h.Version != version.Version || h.Build != version.Build {
		fmt.Fprintf(stderr, "orderecho mcp: warning: the running service is %s (%s), this binary is %s (%s); stop the service to pick up this version\n",
			h.Version, h.Build, version.Version, version.Build)
	}
	infos, err := client.Tools(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho mcp: listing the service's tools: %v\n", err)
		return exitFailed
	}
	var tools []*service.Tool
	for _, ti := range infos {
		if !ti.Enabled {
			continue
		}
		if t, ok := service.Lookup(ti.Name); ok {
			tools = append(tools, t)
		} else {
			fmt.Fprintf(stderr, "orderecho mcp: warning: the service offers %q, which this binary does not know; skipped\n", ti.Name)
		}
	}
	// If the service goes away mid-conversation, start it again once per call.
	client.OnDown = func() error {
		_, err := ensureService(ctx, cfg, absConfig, client, stderr)
		return err
	}
	fmt.Fprintf(stderr, "orderecho mcp %s: stdio MCP server, %d tool(s), service %s (pid %d)\n", version.Version, len(tools), cfg.Service.URL(), h.PID)
	srv := mcpserver.New(tools, client)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "orderecho mcp: %v\n", err)
		return exitFailed
	}
	return exitOK
}

// ensureService returns the running service's health, starting one in the
// background (detached, output under <log_dir>/engine/) when none answers.
func ensureService(ctx context.Context, cfg *config.Config, absConfig string, client *service.Client, stderr io.Writer) (*service.Health, error) {
	if h, err := client.Health(ctx); err == nil {
		return h, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find this executable to start the service: %v", err)
	}
	logDir := filepath.Join(cfg.Logging.LogDir, "engine")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	outPath := filepath.Join(logDir, "service_"+time.Now().UTC().Format("20060102")+".out")
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	fmt.Fprintf(out, "\n==== %s orderecho mcp starting the agent service\n", time.Now().UTC().Format(time.RFC3339))
	cmd := exec.Command(exe, "--config", absConfig, "serve")
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survives this process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the agent service: %v", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	fmt.Fprintf(stderr, "orderecho mcp: no agent service on %s; started one (pid %d, output %s)\n", cfg.Service.Addr(), pid, outPath)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			tail, _ := os.ReadFile(outPath)
			if len(tail) > 2000 {
				tail = tail[len(tail)-2000:]
			}
			return nil, fmt.Errorf("the agent service exited at start (%v):\n%s", err, tail)
		default:
		}
		if h, err := client.Health(ctx); err == nil {
			return h, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("the agent service (pid %d) did not answer its health check within 20s; see %s", pid, outPath)
}

// ------------------------------------------------------------ Claude Desktop

func defaultDesktopConfig() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
}

// desktopEntry is the "orderecho" server entry for Claude Desktop.
func desktopEntry(configPath string) (map[string]any, error) {
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	repo, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(repo, "bin", "orderecho")
	if _, err := os.Stat(bin); err != nil {
		if exe, err2 := os.Executable(); err2 == nil {
			bin = exe
		}
	}
	return map[string]any{
		"command": bin,
		"args":    []string{"mcp", "--config", absConfig},
		// Claude Desktop has no working-directory setting; the agent
		// changes to this directory at start (relative data/, logs/ and
		// certs/ paths resolve against the repo).
		"env": map[string]string{homeEnv: repo},
	}, nil
}

func cmdInstallClaudeDesktop(args []string, configPath string, stdout, stderr io.Writer) int {
	fs := subFlags("mcp install-claude-desktop", &configPath, stderr)
	write := fs.Bool("write", false, "merge the entry into Claude Desktop's config (backing it up first)")
	target := fs.String("desktop-config", defaultDesktopConfig(), "Claude Desktop's config file")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if _, ok := loadConfig(configPath, stderr); !ok {
		return exitConfig
	}
	entry, err := desktopEntry(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitConfig
	}
	snippet := map[string]any{"mcpServers": map[string]any{"orderecho": entry}}
	if !*write {
		data, _ := json.MarshalIndent(snippet, "", "  ")
		fmt.Fprintf(stdout, "Add this to %s\n(merge into \"mcpServers\" if the file already has other servers), then restart Claude Desktop:\n\n%s\n\nOr run: orderecho mcp install-claude-desktop --write\n", *target, data)
		return exitOK
	}
	return writeDesktopConfig(*target, entry, stdout, stderr)
}

// writeDesktopConfig merges entry as mcpServers.orderecho into path, keeping
// every other key and server exactly as they were.
func writeDesktopConfig(path string, entry map[string]any, stdout, stderr io.Writer) int {
	top := map[string]json.RawMessage{}
	old, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "orderecho: reading %s: %v\n", path, err)
		return exitFailed
	}
	if existed && len(bytes.TrimSpace(old)) > 0 {
		if err := json.Unmarshal(old, &top); err != nil {
			fmt.Fprintf(stderr, "orderecho: %s is not valid JSON (%v); not touching it — fix it or move it away first\n", path, err)
			return exitFailed
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			fmt.Fprintf(stderr, "orderecho: %s: mcpServers is not an object (%v); not touching it\n", path, err)
			return exitFailed
		}
	}
	newEntry, _ := json.Marshal(entry)
	prev, had := servers["orderecho"]
	change := "added"
	if had {
		if jsonEqual(prev, newEntry) {
			fmt.Fprintf(stdout, "%s already has this orderecho entry; nothing changed\n", path)
			return exitOK
		}
		change = "replaced"
	}
	servers["orderecho"] = newEntry
	top["mcpServers"], _ = json.Marshal(servers)
	out, err := marshalSorted(top)
	if err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		fmt.Fprintf(stderr, "orderecho: the merged config does not validate (%v); nothing written\n", err)
		return exitFailed
	}
	backup := ""
	if existed {
		backup = path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			fmt.Fprintf(stderr, "orderecho: backing up %s: %v; nothing written\n", path, err)
			return exitFailed
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "orderecho: %v\n", err)
		return exitFailed
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		fmt.Fprintf(stderr, "orderecho: writing %s: %v\n", path, err)
		return exitFailed
	}
	var others []string
	for k := range servers {
		if k != "orderecho" {
			others = append(others, k)
		}
	}
	sort.Strings(others)
	fmt.Fprintf(stdout, "%s: mcpServers.orderecho %s\n", path, change)
	if had {
		fmt.Fprintf(stdout, "  before: %s\n", prev)
	}
	fmt.Fprintf(stdout, "  now   : %s\n", newEntry)
	if backup != "" {
		fmt.Fprintf(stdout, "  backup: %s\n", backup)
	} else {
		fmt.Fprintln(stdout, "  (new file; nothing to back up)")
	}
	if len(others) > 0 {
		fmt.Fprintf(stdout, "  other servers kept as they were: %s\n", strings.Join(others, ", "))
	}
	fmt.Fprintln(stdout, "Restart Claude Desktop to load it.")
	return exitOK
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

// marshalSorted renders top-level keys sorted, each value as it was
// (re-indented), so other servers' entries keep their content.
func marshalSorted(top map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		var v bytes.Buffer
		if err := json.Indent(&v, top[k], "  ", "  "); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "  %s: %s", kb, v.Bytes())
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}
