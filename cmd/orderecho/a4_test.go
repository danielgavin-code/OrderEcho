package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/report/reporttest"
)

const tinyConfig = `sessions:
  - { id: emu42, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9, heartbeat_sec: 30 }
service: { port: 1 }
`

func writeTiny(t *testing.T, extra string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orderecho.yaml")
	os.WriteFile(p, []byte(tinyConfig+extra), 0o644)
	return p
}

func TestServeMCPHTTPRefusesWithoutToken(t *testing.T) {
	t.Setenv("ORDERECHO_MCP_TOKEN", "")
	var out, errb bytes.Buffer
	code := run([]string{"--config", writeTiny(t, ""), "serve", "--mcp-http"}, &out, &errb)
	if code != exitConfig || !strings.Contains(errb.String(), "--mcp-http needs a bearer token") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	// The env var wins over config; either one is enough.
	cfg, _ := loadConfig(writeTiny(t, `mcp: { http_token: "from-config" }`), &errb)
	if mcpToken(cfg) != "from-config" {
		t.Fatal("config token")
	}
	t.Setenv("ORDERECHO_MCP_TOKEN", "from-env")
	if mcpToken(cfg) != "from-env" {
		t.Fatal("env token")
	}
}

func TestServeRefusesNonLoopbackHost(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--config", writeTiny(t, "") + "x", "serve"}, &out, &errb) // missing file -> 2
	if code != exitConfig {
		t.Fatal(code)
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(strings.Replace(tinyConfig, "service: { port: 1 }", "service: { host: 0.0.0.0, port: 8190 }", 1)), 0o644)
	errb.Reset()
	if code := run([]string{"--config", p, "serve"}, &out, &errb); code != exitConfig || !strings.Contains(errb.String(), "'service.host' must be a loopback address") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestServeStatusWhenNotRunning(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--config", writeTiny(t, ""), "serve", "--status"}, &out, &errb)
	if code != exitServeNotRunning || !strings.Contains(out.String(), "agent service: not running on 127.0.0.1:1") {
		t.Fatalf("exit %d: %s", code, out.String())
	}
}

func TestInstallClaudeDesktop(t *testing.T) {
	cfgPath := writeTiny(t, "")
	target := filepath.Join(t.TempDir(), "Claude", "claude_desktop_config.json")
	var out, errb bytes.Buffer

	// Print mode: the snippet, nothing written.
	if code := run([]string{"--config", cfgPath, "mcp", "install-claude-desktop", "--desktop-config", target}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("print mode wrote the file")
	}
	i := strings.Index(out.String(), "{")
	j := strings.LastIndex(out.String(), "}")
	var snip struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(out.String()[i:j+1]), &snip); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	e := snip.MCPServers["orderecho"]
	wd, _ := os.Getwd()
	if !filepath.IsAbs(e.Command) || len(e.Args) != 3 || e.Args[0] != "mcp" || e.Args[1] != "--config" || e.Args[2] != cfgPath || e.Env[homeEnv] != wd {
		t.Fatalf("%+v", e)
	}

	// --write on a missing file creates it.
	out.Reset()
	if code := run([]string{"--config", cfgPath, "mcp", "install-claude-desktop", "--write", "--desktop-config", target}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "mcpServers.orderecho added") || !strings.Contains(out.String(), "new file") {
		t.Fatal(out.String())
	}

	// --write on an existing file with other servers and settings: backup,
	// merge, others untouched.
	existing := `{"globalShortcut": "Cmd+Space", "mcpServers": {"filesystem": {"command": "npx", "args": ["-y", "@x/fs", "/tmp"]}, "orderecho": {"command": "/old/orderecho", "args": ["mcp"]}}}`
	os.WriteFile(target, []byte(existing), 0o600)
	out.Reset()
	if code := run([]string{"--config", cfgPath, "mcp", "install-claude-desktop", "--write", "--desktop-config", target}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	o := out.String()
	if !strings.Contains(o, "mcpServers.orderecho replaced") || !strings.Contains(o, `before: {"command": "/old/orderecho", "args": ["mcp"]}`) ||
		!strings.Contains(o, "other servers kept as they were: filesystem") || !strings.Contains(o, "backup: "+target+".bak-") {
		t.Fatal(o)
	}
	backups, _ := filepath.Glob(target + ".bak-*")
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	if b, _ := os.ReadFile(backups[0]); string(b) != existing {
		t.Fatal("backup is not the original")
	}
	var merged map[string]any
	data, _ := os.ReadFile(target)
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatal(err)
	}
	servers := merged["mcpServers"].(map[string]any)
	fsrv, _ := json.Marshal(servers["filesystem"])
	if merged["globalShortcut"] != "Cmd+Space" || string(fsrv) != `{"args":["-y","@x/fs","/tmp"],"command":"npx"}` {
		t.Fatalf("%s", data)
	}
	if servers["orderecho"].(map[string]any)["command"] != e.Command {
		t.Fatal("entry not merged")
	}
	// Again: nothing to change.
	out.Reset()
	run([]string{"--config", cfgPath, "mcp", "install-claude-desktop", "--write", "--desktop-config", target}, &out, &errb)
	if !strings.Contains(out.String(), "nothing changed") {
		t.Fatal(out.String())
	}
	// An invalid file is never touched.
	os.WriteFile(target, []byte(`{"mcpServers": {`), 0o600)
	errb.Reset()
	if code := run([]string{"--config", cfgPath, "mcp", "install-claude-desktop", "--write", "--desktop-config", target}, &out, &errb); code == 0 ||
		!strings.Contains(errb.String(), "is not valid JSON") {
		t.Fatalf("%d %s", code, errb.String())
	}
	if b, _ := os.ReadFile(target); string(b) != `{"mcpServers": {` {
		t.Fatal("invalid file was rewritten")
	}
}

// A5: cert report / cert verify from the CLI, on the fixed fixture run.
func TestCertReportAndVerifyCLI(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, reporttest.RunID)
	if _, err := reporttest.Build(dir, reporttest.Options{}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"cert", "verify", dir}, &out, &errb); code != 0 || !strings.Contains(out.String(), "intact: 5 file(s) match their recorded SHA-256") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"cert", "report", dir}, &out, &errb); code != 0 || !strings.Contains(out.String(), "verdict: CERTIFIED") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "report.html")); err != nil {
		t.Fatal(err)
	}
	// One byte of one case's fix.log: exit 9, naming the file.
	p := filepath.Join(dir, "4.1", "fix.log")
	data, _ := os.ReadFile(p)
	data[100] ^= 0x20
	os.WriteFile(p, data, 0o644)
	out.Reset()
	code := run([]string{"cert", "verify", dir}, &out, &errb)
	if code != exitTampered || !strings.Contains(out.String(), "MISMATCH    4.1/fix.log") || !strings.Contains(out.String(), "TAMPERED: 1 of 5 file(s)") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	// By run id, through the config's certs_dir.
	cfg := filepath.Join(root, "c.yaml")
	os.WriteFile(cfg, []byte(tinyConfig+"storage: { certs_dir: "+root+" }\n"), 0o644)
	out.Reset()
	if code := run([]string{"--config", cfg, "cert", "verify", reporttest.RunID}, &out, &errb); code != exitTampered {
		t.Fatalf("by id: exit %d %s", code, errb.String())
	}
	// A run with no integrity record: not "tampered", a usage-level 2.
	os.Remove(filepath.Join(dir, "results.sha256"))
	data, _ = os.ReadFile(filepath.Join(dir, "results.json"))
	os.WriteFile(filepath.Join(dir, "results.json"), bytes.Replace(data, []byte(`"integrity"`), []byte(`"integrity_x"`), 1), 0o644)
	errb.Reset()
	if code := run([]string{"cert", "verify", dir}, &out, &errb); code != exitConfig || !strings.Contains(errb.String(), "no integrity record") {
		t.Fatalf("unsealed: exit %d %s", code, errb.String())
	}
}
