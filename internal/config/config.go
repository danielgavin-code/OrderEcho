// Package config loads and validates config/orderecho.yaml.
//
// The shape mirrors the emulator's multi-session config: a list of sessions,
// a defaults block every session starts from (and may override per session),
// storage and logging. Every validation error names the session and the key.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
)

// DefaultPath is where the CLI looks without --config.
const DefaultPath = "config/orderecho.yaml"

// Error is a config problem. The CLI exits 2 on it.
type Error struct{ Msg string }

func (e *Error) Error() string { return "config: " + e.Msg }

func errf(format string, args ...any) error { return &Error{Msg: fmt.Sprintf(format, args...)} }

// Defaults are the per-session settings a session may override.
type Defaults struct {
	LogonTimeoutSec      float64
	LogoutTimeoutSec     float64
	HeartbeatGracePct    float64
	Reconnect            bool
	ReconnectIntervalSec float64
	// A2 additions.
	HeartbeatMismatch string // warn | refuse
	IncludeHandlInst  bool   // 21=1 on FIX 4.4 D/G (always sent on 4.2)
	ClOrdIDPrefix     string // ClOrdIDs are <prefix>-<run_id>-<n>
	Account           string // tag 1 on D when set
	// A3 addition.
	AnswerGraceSec float64 // live requests_answered grace
}

// Session is one configured FIX session.
type Session struct {
	ID           string
	FixVersion   string
	SenderCompID string
	TargetCompID string
	Host         string
	Port         int
	HeartbeatSec int
	ResetOnLogon bool
	Defaults     // effective values after overrides
}

// LogonTimeout as a duration.
func (s Session) LogonTimeout() time.Duration { return secs(s.LogonTimeoutSec) }

// LogoutTimeout as a duration.
func (s Session) LogoutTimeout() time.Duration { return secs(s.LogoutTimeoutSec) }

// AnswerGrace as a duration.
func (s Session) AnswerGrace() time.Duration { return secs(s.AnswerGraceSec) }

// ReconnectInterval as a duration.
func (s Session) ReconnectInterval() time.Duration { return secs(s.ReconnectIntervalSec) }

// Addr is host:port.
func (s Session) Addr() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

func secs(v float64) time.Duration { return time.Duration(v * float64(time.Second)) }

// Storage says where persistent data goes.
type Storage struct {
	SeqnumDir   string
	MsgstoreDir string
	EvidenceDir string
	CertsDir    string // cert run results (A3)
}

// Logging configures the human-readable logs.
type Logging struct {
	LogDir       string
	FixDelimiter string // "|" or "SOH"
	EngineLevel  string // DEBUG | INFO | WARNING | ERROR
	Console      bool
}

// Service configures the long-running agent service (A4).
type Service struct {
	Host string // loopback only
	Port int
}

// Addr is host:port.
func (s Service) Addr() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// URL is the service's base URL.
func (s Service) URL() string { return "http://" + s.Addr() }

// MCP configures the MCP front door (A4).
type MCP struct {
	AllowOrders        bool   // send/cancel/replace (and cert runs) are registered
	AllowEmulatorTools bool   // emulator_* tools are registered (when a control API is configured)
	HTTPToken          string // bearer token for MCP over HTTP (env ORDERECHO_MCP_TOKEN wins)
}

// Emulator is the counterparty emulator's control API, when the sessions
// in Sessions talk to it (A4 emulator tools).
type Emulator struct {
	ControlAPI string
	Sessions   map[string]string // agent session id -> emulator session id
}

// Config is the whole file.
type Config struct {
	Path     string
	Sessions []Session
	Defaults Defaults
	Storage  Storage
	Logging  Logging
	Service  Service
	MCP      MCP
	Emulator Emulator
}

// EmulatorSession returns the emulator's id for an agent session when that
// session's counterparty is the emulator with a control API.
func (c *Config) EmulatorSession(id string) (string, bool) {
	if c.Emulator.ControlAPI == "" {
		return "", false
	}
	v, ok := c.Emulator.Sessions[id]
	return v, ok
}

// IsLoopbackHost reports whether host names this machine: localhost or a
// loopback IP. Any other name counts as external.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// External reports whether the session's counterparty is off this machine.
func (s Session) External() bool { return !IsLoopbackHost(s.Host) }

// Session returns the session with id.
func (c *Config) Session(id string) (Session, error) {
	for _, s := range c.Sessions {
		if s.ID == id {
			return s, nil
		}
	}
	ids := make([]string, 0, len(c.Sessions))
	for _, s := range c.Sessions {
		ids = append(ids, s.ID)
	}
	return Session{}, errf("no session %q (configured: %s)", id, strings.Join(ids, ", "))
}

var builtinDefaults = Defaults{
	LogonTimeoutSec:      10,
	LogoutTimeoutSec:     10,
	HeartbeatGracePct:    20,
	Reconnect:            false,
	ReconnectIntervalSec: 5,
	HeartbeatMismatch:    "warn",
	IncludeHandlInst:     true,
	ClOrdIDPrefix:        "OE",
	AnswerGraceSec:       5,
}

var (
	topKeys     = []string{"sessions", "defaults", "storage", "logging", "service", "mcp", "emulator"}
	defaultKeys = []string{"logon_timeout_sec", "logout_timeout_sec", "heartbeat_grace_pct", "reconnect", "reconnect_interval_sec",
		"heartbeat_mismatch", "include_handl_inst", "clordid_prefix", "account", "answer_grace_sec"}
	sessionKeys  = []string{"id", "fix_version", "sender_comp_id", "target_comp_id", "host", "port", "heartbeat_sec", "reset_on_logon"}
	storageKeys  = []string{"seqnum_dir", "msgstore_dir", "evidence_dir", "certs_dir"}
	loggingKeys  = []string{"log_dir", "fix_delimiter", "engine_level", "console"}
	validLevels  = []string{"DEBUG", "INFO", "WARNING", "ERROR"}
	requiredSess = []string{"id", "fix_version", "sender_comp_id", "target_comp_id", "host", "port", "heartbeat_sec"}
)

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errf("file not found: %s", path)
	}
	if err != nil {
		return nil, errf("cannot read %s: %v", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	cfg.Path = path
	return cfg, nil
}

// Parse validates YAML bytes.
func Parse(data []byte) (*Config, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, errf("invalid YAML: %v", err)
	}
	if raw == nil {
		return nil, errf("file is empty")
	}
	if err := unknownKeys(raw, topKeys, "top level"); err != nil {
		return nil, err
	}
	cfg := &Config{}

	// defaults
	cfg.Defaults = builtinDefaults
	if d, present, err := section(raw, "defaults"); err != nil {
		return nil, err
	} else if present {
		if err := unknownKeys(d, defaultKeys, "defaults"); err != nil {
			return nil, err
		}
		if err := applyDefaults(&cfg.Defaults, d, "defaults"); err != nil {
			return nil, err
		}
	}

	// storage
	cfg.Storage = Storage{SeqnumDir: "data/seqnums", MsgstoreDir: "data/msgstore", EvidenceDir: "data/evidence", CertsDir: "data/certs"}
	if st, present, err := section(raw, "storage"); err != nil {
		return nil, err
	} else if present {
		if err := unknownKeys(st, storageKeys, "storage"); err != nil {
			return nil, err
		}
		for key, dst := range map[string]*string{"seqnum_dir": &cfg.Storage.SeqnumDir, "msgstore_dir": &cfg.Storage.MsgstoreDir, "evidence_dir": &cfg.Storage.EvidenceDir, "certs_dir": &cfg.Storage.CertsDir} {
			if v, ok := st[key]; ok {
				s, err := asString(v, "storage."+key)
				if err != nil {
					return nil, err
				}
				*dst = s
			}
		}
	}

	// logging
	cfg.Logging = Logging{LogDir: "logs", FixDelimiter: "|", EngineLevel: "INFO", Console: true}
	if lg, present, err := section(raw, "logging"); err != nil {
		return nil, err
	} else if present {
		if err := unknownKeys(lg, loggingKeys, "logging"); err != nil {
			return nil, err
		}
		if v, ok := lg["log_dir"]; ok {
			if cfg.Logging.LogDir, err = asString(v, "logging.log_dir"); err != nil {
				return nil, err
			}
		}
		if v, ok := lg["fix_delimiter"]; ok {
			s, err := asString(v, "logging.fix_delimiter")
			if err != nil {
				return nil, err
			}
			if s != "|" && s != "SOH" {
				return nil, errf("'logging.fix_delimiter' must be '|' or 'SOH', got %q", s)
			}
			cfg.Logging.FixDelimiter = s
		}
		if v, ok := lg["engine_level"]; ok {
			s, err := asString(v, "logging.engine_level")
			if err != nil {
				return nil, err
			}
			s = strings.ToUpper(s)
			if !contains(validLevels, s) {
				return nil, errf("'logging.engine_level' must be one of %s, got %q", strings.Join(validLevels, ", "), s)
			}
			cfg.Logging.EngineLevel = s
		}
		if v, ok := lg["console"]; ok {
			if cfg.Logging.Console, err = asBool(v, "logging.console"); err != nil {
				return nil, err
			}
		}
	}

	if err := parseA4(raw, cfg); err != nil {
		return nil, err
	}

	// sessions
	rawSessions, ok := raw["sessions"]
	if !ok || rawSessions == nil {
		return nil, errf("missing required key 'sessions'")
	}
	list, ok := rawSessions.([]any)
	if !ok || len(list) == 0 {
		return nil, errf("'sessions' must be a non-empty list")
	}
	seen := map[string]bool{}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, errf("sessions[%d] must be a mapping", i)
		}
		s, err := parseSession(m, i, cfg.Defaults)
		if err != nil {
			return nil, err
		}
		if seen[s.ID] {
			return nil, errf("session %q: duplicate 'id'", s.ID)
		}
		seen[s.ID] = true
		cfg.Sessions = append(cfg.Sessions, s)
	}
	for id := range cfg.Emulator.Sessions {
		if !seen[id] {
			return nil, errf("'emulator.sessions': %q is not a configured session", id)
		}
	}
	return cfg, nil
}

// DefaultServicePort is the agent service's default port.
const DefaultServicePort = 8190

func parseA4(raw map[string]any, cfg *Config) error {
	cfg.Service = Service{Host: "127.0.0.1", Port: DefaultServicePort}
	if sv, present, err := section(raw, "service"); err != nil {
		return err
	} else if present {
		if err := unknownKeys(sv, []string{"host", "port"}, "service"); err != nil {
			return err
		}
		if v, ok := sv["host"]; ok {
			h, err := asString(v, "'service.host'")
			if err != nil {
				return err
			}
			if !IsLoopbackHost(h) {
				return errf("'service.host' must be a loopback address (127.0.0.1, ::1 or localhost); the service never listens beyond this machine, got %q", h)
			}
			cfg.Service.Host = h
		}
		if v, ok := sv["port"]; ok {
			p, err := asInt(v, "'service.port'")
			if err != nil {
				return err
			}
			if p < 1 || p > 65535 {
				return errf("'service.port' must be 1..65535, got %d", p)
			}
			cfg.Service.Port = p
		}
	}
	cfg.MCP = MCP{AllowOrders: true, AllowEmulatorTools: true}
	if m, present, err := section(raw, "mcp"); err != nil {
		return err
	} else if present {
		if err := unknownKeys(m, []string{"allow_orders", "allow_emulator_tools", "http_token"}, "mcp"); err != nil {
			return err
		}
		if v, ok := m["allow_orders"]; ok {
			if cfg.MCP.AllowOrders, err = asBool(v, "'mcp.allow_orders'"); err != nil {
				return err
			}
		}
		if v, ok := m["allow_emulator_tools"]; ok {
			if cfg.MCP.AllowEmulatorTools, err = asBool(v, "'mcp.allow_emulator_tools'"); err != nil {
				return err
			}
		}
		if v, ok := m["http_token"]; ok && v != nil {
			s, ok := v.(string)
			if !ok {
				return errf("'mcp.http_token' must be a string")
			}
			cfg.MCP.HTTPToken = s
		}
	}
	if e, present, err := section(raw, "emulator"); err != nil {
		return err
	} else if present {
		if err := unknownKeys(e, []string{"control_api", "sessions"}, "emulator"); err != nil {
			return err
		}
		if v, ok := e["control_api"]; ok {
			if cfg.Emulator.ControlAPI, err = asString(v, "'emulator.control_api'"); err != nil {
				return err
			}
			if !strings.HasPrefix(cfg.Emulator.ControlAPI, "http://") && !strings.HasPrefix(cfg.Emulator.ControlAPI, "https://") {
				return errf("'emulator.control_api' must be an http(s) URL, got %q", cfg.Emulator.ControlAPI)
			}
			cfg.Emulator.ControlAPI = strings.TrimRight(cfg.Emulator.ControlAPI, "/")
		}
		cfg.Emulator.Sessions = map[string]string{}
		if v, ok := e["sessions"]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return errf("'emulator.sessions' must map agent session ids to emulator session ids")
			}
			for k, val := range m {
				s, err := asString(val, fmt.Sprintf("'emulator.sessions.%s'", k))
				if err != nil {
					return err
				}
				cfg.Emulator.Sessions[k] = s
			}
		}
		if cfg.Emulator.ControlAPI != "" && len(cfg.Emulator.Sessions) == 0 {
			return errf("'emulator.sessions' is required with 'emulator.control_api' (which agent sessions talk to the emulator, and its id for each)")
		}
	}
	return nil
}

func parseSession(m map[string]any, index int, defaults Defaults) (Session, error) {
	where := fmt.Sprintf("sessions[%d]", index)
	if v, ok := m["id"]; ok {
		if id, err := asString(v, where+".id"); err == nil {
			where = fmt.Sprintf("session %q", id)
		}
	}
	allowed := append(append([]string{}, sessionKeys...), defaultKeys...)
	for key := range m {
		if !contains(allowed, key) {
			return Session{}, errf("%s: unknown key '%s' (allowed: %s)", where, key, strings.Join(allowed, ", "))
		}
	}
	for _, key := range requiredSess {
		if v, ok := m[key]; !ok || v == nil {
			return Session{}, errf("%s: missing required key '%s'", where, key)
		}
	}
	var s Session
	var err error
	if s.ID, err = asString(m["id"], where+": 'id'"); err != nil {
		return s, err
	}
	if s.FixVersion, err = asString(m["fix_version"], where+": 'fix_version'"); err != nil {
		return s, err
	}
	if _, perr := profile.For(s.FixVersion); perr != nil {
		return s, errf("%s: 'fix_version' %v", where, perr)
	}
	if s.SenderCompID, err = asString(m["sender_comp_id"], where+": 'sender_comp_id'"); err != nil {
		return s, err
	}
	if s.TargetCompID, err = asString(m["target_comp_id"], where+": 'target_comp_id'"); err != nil {
		return s, err
	}
	if s.Host, err = asString(m["host"], where+": 'host'"); err != nil {
		return s, err
	}
	if s.Port, err = asInt(m["port"], where+": 'port'"); err != nil {
		return s, err
	}
	if s.Port < 1 || s.Port > 65535 {
		return s, errf("%s: 'port' must be 1..65535, got %d", where, s.Port)
	}
	if s.HeartbeatSec, err = asInt(m["heartbeat_sec"], where+": 'heartbeat_sec'"); err != nil {
		return s, err
	}
	if s.HeartbeatSec < 1 {
		return s, errf("%s: 'heartbeat_sec' must be >= 1, got %d", where, s.HeartbeatSec)
	}
	if v, ok := m["reset_on_logon"]; ok {
		if s.ResetOnLogon, err = asBool(v, where+": 'reset_on_logon'"); err != nil {
			return s, err
		}
	}
	s.Defaults = defaults
	if err := applyDefaults(&s.Defaults, m, where); err != nil {
		return s, err
	}
	return s, nil
}

func applyDefaults(d *Defaults, m map[string]any, where string) error {
	for _, key := range []string{"logon_timeout_sec", "logout_timeout_sec", "heartbeat_grace_pct", "reconnect_interval_sec", "answer_grace_sec"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		n, err := asNumber(v, fmt.Sprintf("%s: '%s'", where, key))
		if err != nil {
			return err
		}
		if n < 0 || (key != "heartbeat_grace_pct" && key != "answer_grace_sec" && n == 0) {
			return errf("%s: '%s' must be > 0, got %v", where, key, v)
		}
		switch key {
		case "logon_timeout_sec":
			d.LogonTimeoutSec = n
		case "logout_timeout_sec":
			d.LogoutTimeoutSec = n
		case "heartbeat_grace_pct":
			d.HeartbeatGracePct = n
		case "reconnect_interval_sec":
			d.ReconnectIntervalSec = n
		case "answer_grace_sec":
			d.AnswerGraceSec = n
		}
	}
	if v, ok := m["reconnect"]; ok {
		b, err := asBool(v, fmt.Sprintf("%s: 'reconnect'", where))
		if err != nil {
			return err
		}
		d.Reconnect = b
	}
	if v, ok := m["include_handl_inst"]; ok {
		b, err := asBool(v, fmt.Sprintf("%s: 'include_handl_inst'", where))
		if err != nil {
			return err
		}
		d.IncludeHandlInst = b
	}
	if v, ok := m["heartbeat_mismatch"]; ok {
		s, err := asString(v, fmt.Sprintf("%s: 'heartbeat_mismatch'", where))
		if err != nil {
			return err
		}
		if s != "warn" && s != "refuse" {
			return errf("%s: 'heartbeat_mismatch' must be warn or refuse, got %q", where, s)
		}
		d.HeartbeatMismatch = s
	}
	if v, ok := m["clordid_prefix"]; ok {
		s, err := asString(v, fmt.Sprintf("%s: 'clordid_prefix'", where))
		if err != nil {
			return err
		}
		if strings.ContainsAny(s, "\x01|= ") {
			return errf("%s: 'clordid_prefix' may not contain SOH, '|', '=' or spaces, got %q", where, s)
		}
		d.ClOrdIDPrefix = s
	}
	if v, ok := m["account"]; ok {
		s, err := asString(v, fmt.Sprintf("%s: 'account'", where))
		if err != nil {
			return err
		}
		d.Account = s
	}
	return nil
}

func section(raw map[string]any, name string) (map[string]any, bool, error) {
	v, ok := raw[name]
	if !ok || v == nil {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false, errf("'%s' must be a mapping", name)
	}
	return m, true, nil
}

func unknownKeys(m map[string]any, allowed []string, where string) error {
	var bad []string
	for k := range m {
		if !contains(allowed, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return errf("%s: unknown key(s) %s (allowed: %s)", where, strings.Join(bad, ", "), strings.Join(allowed, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func asString(v any, what string) (string, error) {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", errf("%s must be a non-empty string, got %v", what, v)
	}
	return s, nil
}

func asInt(v any, what string) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case uint64:
		return int(n), nil
	}
	return 0, errf("%s must be an integer, got %v", what, v)
}

func asNumber(v any, what string) (float64, error) {
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			break
		}
		return n, nil
	}
	return 0, errf("%s must be a number, got %v", what, v)
}

func asBool(v any, what string) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, errf("%s must be true or false, got %v", what, v)
	}
	return b, nil
}

// WithFixVersion returns s with its fix_version overridden (the CLI's
// --fix-version), validated like the config value.
func (s Session) WithFixVersion(v string) (Session, error) {
	if v == "" {
		return s, nil
	}
	if _, err := profile.For(v); err != nil {
		return s, errf("--fix-version: %v", err)
	}
	s.FixVersion = v
	return s, nil
}
