package config

import (
	"strings"
	"testing"
)

func TestShippedConfig(t *testing.T) {
	cfg, err := Load("../../config/orderecho.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sessions) != 3 {
		t.Fatalf("%d sessions", len(cfg.Sessions))
	}
	s, err := cfg.Session("strict")
	if err != nil || s.Port != 9879 || s.TargetCompID != "STRICTBRK" || s.FixVersion != "FIX.4.2" || !s.ResetOnLogon {
		t.Fatalf("%+v %v", s, err)
	}
	if s.LogonTimeoutSec != 10 || s.LogoutTimeoutSec != 10 || s.HeartbeatGracePct != 20 || s.Reconnect || s.ReconnectIntervalSec != 5 {
		t.Fatalf("defaults %+v", s.Defaults)
	}
	if cfg.Storage.MsgstoreDir != "data/msgstore" || cfg.Logging.FixDelimiter != "|" || !cfg.Logging.Console {
		t.Fatalf("%+v %+v", cfg.Storage, cfg.Logging)
	}
}

const base = `
sessions:
  - { id: a, fix_version: FIX.4.4, sender_comp_id: AGENT, target_comp_id: X, host: 127.0.0.1, port: 9878, heartbeat_sec: 30 %s }
defaults: { logon_timeout_sec: 10, reconnect: false }
`

func parse(extra string) (*Config, error) {
	return Parse([]byte(strings.Replace(base, "%s", extra, 1)))
}

func TestPerSessionOverride(t *testing.T) {
	cfg, err := parse(", reconnect: true, logon_timeout_sec: 2.5")
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Sessions[0]
	if !s.Reconnect || s.LogonTimeoutSec != 2.5 || cfg.Defaults.Reconnect {
		t.Fatalf("%+v", s.Defaults)
	}
	if s.LogonTimeout().Milliseconds() != 2500 {
		t.Fatal("duration")
	}
}

func TestValidationErrorsNameSessionAndKey(t *testing.T) {
	cases := map[[2]string][]string{
		{"port: 9878", "port: 70000"}:             {`session "a"`, "port"},
		{"heartbeat_sec: 30", "heartbeat_sec: 0"}: {`session "a"`, "heartbeat_sec"},
		{"%s", ", reconnect: maybe"}:              {`session "a"`, "reconnect"},
		{"%s", ", colour: blue"}:                  {`session "a"`, "colour"},
		{"%s", ", logout_timeout_sec: -1"}:        {`session "a"`, "logout_timeout_sec"},
		{"port: 9878", "port: '9878'"}:            {`session "a"`, "port"},
	}
	for repl, wants := range cases {
		extra := repl
		_, err := Parse([]byte(strings.Replace(strings.Replace(base, repl[0], repl[1], 1), "%s", "", 1)))
		if err == nil {
			t.Fatalf("%s: accepted", extra)
		}
		for _, w := range wants {
			if !strings.Contains(err.Error(), w) {
				t.Fatalf("%s: %q lacks %q", extra, err, w)
			}
		}
	}
	_, err := Parse([]byte(strings.Replace(base, "FIX.4.4", "FIX.9.9", 1)))
	if err == nil || !strings.Contains(err.Error(), "fix_version") || !strings.Contains(err.Error(), `session "a"`) {
		t.Fatalf("version: %v", err)
	}
	_, err = Parse([]byte(strings.Replace(base, "host: 127.0.0.1,", "", 1)))
	if err == nil || !strings.Contains(err.Error(), "missing required key 'host'") {
		t.Fatalf("missing host: %v", err)
	}
	_, err = Parse([]byte(base + "\nlogging: { fix_delimiter: ';' }\n"))
	if err == nil || !strings.Contains(err.Error(), "fix_delimiter") {
		t.Fatalf("delimiter: %v", err)
	}
	_, err = Parse([]byte(strings.Replace(base, "%s", "", 1) + "  - { id: a, fix_version: FIX.4.2, sender_comp_id: A, target_comp_id: B, host: h, port: 1, heartbeat_sec: 1 }\n"))
	if err == nil {
		t.Fatal("duplicate id accepted")
	}
	_, err = Load("/nonexistent/orderecho.yaml")
	var ce *Error
	if err == nil || !asConfigError(err, &ce) {
		t.Fatalf("missing file: %v", err)
	}
}

func asConfigError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestA2Keys(t *testing.T) {
	cfg, err := Load("../../config/orderecho.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Sessions[0]
	if s.HeartbeatMismatch != "warn" || !s.IncludeHandlInst || s.ClOrdIDPrefix != "OE" || s.Account != "" {
		t.Fatalf("defaults %+v", s.Defaults)
	}
	c, err := Parse([]byte(strings.Replace(base, "%s", ", heartbeat_mismatch: refuse, include_handl_inst: false, clordid_prefix: QA, account: ACC1", 1)))
	if err != nil {
		t.Fatal(err)
	}
	s = c.Sessions[0]
	if s.HeartbeatMismatch != "refuse" || s.IncludeHandlInst || s.ClOrdIDPrefix != "QA" || s.Account != "ACC1" {
		t.Fatalf("%+v", s.Defaults)
	}
	for _, bad := range []string{", heartbeat_mismatch: maybe", ", include_handl_inst: 1", ", clordid_prefix: 'A B'"} {
		if _, err := Parse([]byte(strings.Replace(base, "%s", bad, 1))); err == nil || !strings.Contains(err.Error(), `session "a"`) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	if _, err := s.WithFixVersion("FIX.9"); err == nil {
		t.Fatal("bad --fix-version accepted")
	}
	if v, _ := s.WithFixVersion("FIX.4.2"); v.FixVersion != "FIX.4.2" {
		t.Fatal("override")
	}
}
