package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/config"
	"github.com/danielgavin-code/OrderEcho/internal/version"
)

// ClientHeader tells the service which front door a call came through.
const ClientHeader = "X-OrderEcho-Client"

// Health is GET /api/v1/health.
type Health struct {
	OK       bool   `json:"ok"`
	Service  string `json:"service"`
	Version  string `json:"version"`
	Build    string `json:"build"`
	PID      int    `json:"pid"`
	Config   string `json:"config"`
	Started  string `json:"started"`
	Addr     string `json:"addr"`
	MCPHTTP  bool   `json:"mcp_http"`
	Stopping bool   `json:"stopping,omitempty"`
}

// Status is GET /api/v1/status (serve --status).
type Status struct {
	Health
	Sessions []SessionInfo `json:"sessions"`
	Runs     []RunInfo     `json:"active_runs"`
}

// ToolInfo is one GET /api/v1/tools row.
type ToolInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled_for_mcp"`
	Group   string `json:"group"`
}

// HandlerOptions configure the HTTP front.
type HandlerOptions struct {
	// MCP is served at /mcp when non-nil (already wrapped with its auth).
	MCP http.Handler
}

// Handler is the service's HTTP API: /api/v1/health, /status, /tools and
// POST /api/v1/<tool> for every tool, plus /mcp when enabled.
func (s *Service) Handler(opt HandlerOptions) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeHTTP(w, http.StatusOK, s.health(r, opt.MCP != nil))
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		st := Status{Health: s.health(r, opt.MCP != nil)}
		for _, id := range s.order {
			st.Sessions = append(st.Sessions, s.sessionInfo(s.sessions[id]))
		}
		s.mu.Lock()
		for _, run := range s.runs {
			if run.state() == RunRunning {
				st.Runs = append(st.Runs, run.snapshot())
			}
		}
		s.mu.Unlock()
		if st.Runs == nil {
			st.Runs = []RunInfo{}
		}
		writeHTTP(w, http.StatusOK, st)
	})
	mux.HandleFunc("GET /api/v1/tools", func(w http.ResponseWriter, r *http.Request) {
		var out []ToolInfo
		for _, t := range Catalog() {
			out = append(out, ToolInfo{Name: t.Name, Enabled: s.Enabled(t), Group: t.Group})
		}
		writeHTTP(w, http.StatusOK, map[string]any{"tools": out})
	})
	mux.HandleFunc("POST /api/v1/{tool}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("tool")
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			writeError(w, apiErr(415, "unsupported_media_type", "send Content-Type: application/json", "tool calls take a JSON body"))
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, apiErr(400, "bad_request", "", "reading the body: %v", err))
			return
		}
		client := r.Header.Get(ClientHeader)
		if client != ClientMCPStdio {
			client = ClientAPI
		}
		resp := s.Dispatch(r.Context(), client, name, body)
		if resp.Error != nil {
			writeError(w, resp.Error)
			return
		}
		writeHTTP(w, http.StatusOK, map[string]any{"result": resp.Result, "summary": resp.Summary})
	})
	if opt.MCP != nil {
		mux.Handle("/mcp", opt.MCP)
		mux.Handle("/mcp/", opt.MCP)
	}
	return s.guard(mux)
}

func (s *Service) health(r *http.Request, mcpHTTP bool) Health {
	return Health{OK: true, Service: "orderecho-agent", Version: version.Version, Build: version.Build, PID: os.Getpid(),
		Config: s.cfg.Path, Started: s.started.UTC().Format(time.RFC3339), Addr: r.Host, MCPHTTP: mcpHTTP, Stopping: s.Stopping()}
}

// guard keeps the API to this machine's own callers: the Host must be a
// loopback name (DNS rebinding) and a browser Origin, if any, must be
// loopback too (a web page cannot drive the agent).
func (s *Service) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !config.IsLoopbackHost(host) {
			writeError(w, apiErr(403, "forbidden_host", "call the service on 127.0.0.1", "Host %q is not a loopback name", r.Host))
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			oh := strings.TrimPrefix(strings.TrimPrefix(o, "http://"), "https://")
			if h, _, err := net.SplitHostPort(oh); err == nil {
				oh = h
			}
			if !config.IsLoopbackHost(oh) {
				writeError(w, apiErr(403, "forbidden_origin", "the agent service does not accept calls from web pages", "Origin %q is not allowed", o))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeHTTP(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, e *APIError) {
	status := e.Status
	if status == 0 {
		status = http.StatusBadRequest
	}
	writeHTTP(w, status, e)
}

// ------------------------------------------------------------ client

// Client calls a running service over its HTTP API (the stdio MCP server
// and serve --status use it).
type Client struct {
	Base   string // http://127.0.0.1:8190
	Name   string // sent as X-OrderEcho-Client
	HTTP   *http.Client
	OnDown func() error // called once when the service cannot be reached; nil = no retry
}

// NewClient returns a client for base.
func NewClient(base, name string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Name: name, HTTP: &http.Client{Timeout: 0}}
}

// Health fetches /api/v1/health with a short timeout.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	var h Health
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.get(ctx, "/api/v1/health", &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Status fetches /api/v1/status.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var st Status
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.get(ctx, "/api/v1/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Tools fetches /api/v1/tools.
func (c *Client) Tools(ctx context.Context) ([]ToolInfo, error) {
	var out struct {
		Tools []ToolInfo `json:"tools"`
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.get(ctx, "/api/v1/tools", &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// Call runs one tool on the service: the result as raw JSON and its
// summary, or the service's error.
func (c *Client) Call(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, string, *APIError) {
	res, summary, aerr, err := c.call(ctx, tool, args)
	if err != nil && c.OnDown != nil && ctx.Err() == nil {
		if derr := c.OnDown(); derr == nil {
			res, summary, aerr, err = c.call(ctx, tool, args)
		}
	}
	if err != nil {
		return nil, "", apiErr(503, "service_unreachable",
			"the OrderEcho agent service is not running or not reachable; start it with 'orderecho serve' (orderecho mcp starts it automatically) and retry",
			"calling %s on %s: %v", tool, c.Base, err)
	}
	return res, summary, aerr
}

func (c *Client) call(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, string, *APIError, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/v1/"+tool, bytes.NewReader(args))
	if err != nil {
		return nil, "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Name != "" {
		req.Header.Set(ClientHeader, c.Name)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e APIError
		if json.Unmarshal(data, &e) != nil || e.Code == "" {
			e = APIError{Code: fmt.Sprintf("http_%d", resp.StatusCode), Detail: strings.TrimSpace(string(data))}
		}
		e.Status = resp.StatusCode
		return nil, "", &e, nil
	}
	var ok struct {
		Result  json.RawMessage `json:"result"`
		Summary string          `json:"summary"`
	}
	if err := json.Unmarshal(data, &ok); err != nil {
		return nil, "", nil, err
	}
	return ok.Result, ok.Summary, nil, nil
}
