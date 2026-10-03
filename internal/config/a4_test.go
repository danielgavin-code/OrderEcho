package config

import (
	"strings"
	"testing"
)

const a4Base = `sessions:
  - { id: emu42, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: ORDERECHO, host: 127.0.0.1, port: 9878, heartbeat_sec: 30 }
  - { id: venue, fix_version: FIX.4.2, sender_comp_id: AGENT, target_comp_id: BRK, host: fix.example.com, port: 9878, heartbeat_sec: 30 }
`

func TestA4Sections(t *testing.T) {
	cfg, err := Parse([]byte(a4Base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Service.Addr() != "127.0.0.1:8190" || !cfg.MCP.AllowOrders || !cfg.MCP.AllowEmulatorTools || cfg.MCP.HTTPToken != "" || cfg.Emulator.ControlAPI != "" {
		t.Fatalf("defaults %+v %+v %+v", cfg.Service, cfg.MCP, cfg.Emulator)
	}
	if cfg.Sessions[0].External() || !cfg.Sessions[1].External() {
		t.Fatal("external")
	}
	cfg, err = Parse([]byte(a4Base + `service: { port: 9000 }
mcp: { allow_orders: false, allow_emulator_tools: false, http_token: abc }
emulator: { control_api: "http://127.0.0.1:8090/", sessions: { emu42: agent42 } }
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Service.URL() != "http://127.0.0.1:9000" || cfg.MCP.AllowOrders || cfg.MCP.AllowEmulatorTools || cfg.MCP.HTTPToken != "abc" {
		t.Fatalf("%+v %+v", cfg.Service, cfg.MCP)
	}
	if emu, ok := cfg.EmulatorSession("emu42"); !ok || emu != "agent42" || cfg.Emulator.ControlAPI != "http://127.0.0.1:8090" {
		t.Fatal("emulator")
	}
	if _, ok := cfg.EmulatorSession("venue"); ok {
		t.Fatal("venue is not the emulator")
	}
	for body, want := range map[string]string{
		`service: { host: 0.0.0.0 }`:                                          "'service.host' must be a loopback address",
		`service: { host: 192.168.1.5 }`:                                      "'service.host' must be a loopback address",
		`service: { port: 70000 }`:                                            "'service.port' must be 1..65535",
		`mcp: { allow_orders: yes please }`:                                   "'mcp.allow_orders' must be true or false",
		`mcp: { tokens: x }`:                                                  "mcp: unknown key(s) tokens",
		`emulator: { control_api: "127.0.0.1:8090", sessions: { emu42: a } }`: "must be an http(s) URL",
		`emulator: { control_api: "http://127.0.0.1:8090" }`:                  "'emulator.sessions' is required",
		`emulator: { control_api: "http://x", sessions: { nope: a } }`:        `'emulator.sessions': "nope" is not a configured session`,
	} {
		_, err := Parse([]byte(a4Base + body + "\n"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", body, want, err)
		}
	}
	for host, lo := range map[string]bool{"127.0.0.1": true, "::1": true, "[::1]": true, "localhost": true, "LOCALHOST": true, "127.9.9.9": true, "0.0.0.0": false, "10.0.0.1": false, "example.com": false} {
		if IsLoopbackHost(host) != lo {
			t.Errorf("%s loopback=%v", host, !lo)
		}
	}
}
