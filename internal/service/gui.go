package service

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/report"
	"github.com/danielgavin-code/OrderEcho/internal/version"
	"github.com/danielgavin-code/OrderEcho/web"
)

// A5 §4: the GUI is one page shell (web/index.html) served at every page
// path, plus app.js and the shared stylesheet. Every action it takes is a
// call to /api/v1, exactly as an MCP client's would be.

// CSRFHeader carries the per-process CSRF token on state-changing requests.
const CSRFHeader = "X-OrderEcho-CSRF"

// Pages are the GUI's paths (and the page each shows).
var Pages = map[string]string{
	"/":               "dashboard",
	"/orders":         "orders",
	"/messages":       "messages",
	"/certifications": "certifications",
	"/emulator":       "emulator",
	"/about":          "about",
}

const guiCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

const reportCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'; base-uri 'none'"

func securityHeaders(w http.ResponseWriter, csp string) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (s *Service) servePage(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shell, err := web.Files.ReadFile("index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		html := strings.NewReplacer("{{CSRF}}", s.csrf, "{{PAGE}}", page, "{{VERSION}}", version.Version+" ("+version.Build+")").Replace(string(shell))
		securityHeaders(w, guiCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	}
}

func serveAsset(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := web.Files.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		securityHeaders(w, guiCSP)
		w.Header().Set("Content-Type", ctype)
		w.Write(data)
	}
}

// serveReport serves a run's report.html.
func (s *Service) serveReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run_id")
	run, e := s.run(id)
	if e != nil {
		writeError(w, e)
		return
	}
	info := run.snapshot()
	if info.State == RunRunning {
		writeError(w, apiErr(409, "run_in_progress", "the report is written when the run ends", "cert run %s is still running", id))
		return
	}
	if !exists(filepath.Join(info.Dir, "results.json")) {
		writeError(w, apiErr(404, "no_results", "", "cert run %s has no results", id))
		return
	}
	// A GET never writes: serve report.html, or render it in memory when the
	// run predates reports (cert_run_report or "cert report" write the file).
	data, err := os.ReadFile(filepath.Join(info.Dir, report.FileName))
	if err != nil {
		m, berr := report.Build(info.Dir)
		if berr == nil {
			data, berr = report.Render(m)
		}
		if berr != nil {
			writeError(w, apiErr(500, "report_failed", "", "%v", berr))
			return
		}
	}
	securityHeaders(w, reportCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// RunRow is one GET /api/v1/certs row.
type RunRow struct {
	RunInfo
	Verdict   string `json:"verdict,omitempty"`
	ReportURL string `json:"report_url,omitempty"`
	Sealed    bool   `json:"sealed"`
}

// serveRuns is GET /api/v1/certs: runs newest first (?limit=N, default 50).
func (s *Service) serveRuns(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	dirs, _ := filepath.Glob(filepath.Join(s.certsDir(), "*"))
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	rows := []RunRow{}
	for _, d := range dirs {
		if len(rows) >= limit {
			break
		}
		id := filepath.Base(d)
		if !exists(filepath.Join(d, "results.json")) && !exists(filepath.Join(d, "run.json")) {
			continue
		}
		run, e := s.run(id)
		if e != nil {
			continue
		}
		row := RunRow{RunInfo: run.snapshot()}
		if row.State != RunRunning {
			if res, err := loadResults(d); err == nil {
				row.Verdict, _ = report.Verdict(res)
				row.Sealed = res.Integrity != nil
				row.ReportURL = "/certs/" + id + "/report"
			}
		}
		rows = append(rows, row)
	}
	writeHTTP(w, http.StatusOK, map[string]any{"runs": rows})
}

// serveVerify is GET /api/v1/certs/{run_id}/verify (reads only).
func (s *Service) serveVerify(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run_id")
	run, e := s.run(id)
	if e != nil {
		writeError(w, e)
		return
	}
	rep, err := cert.Verify(run.snapshot().Dir)
	if err == cert.ErrNotSealed {
		writeHTTP(w, http.StatusOK, map[string]any{"run_id": id, "sealed": false, "ok": false, "detail": err.Error()})
		return
	}
	if err != nil {
		writeError(w, apiErr(500, "verify_failed", "", "%v", err))
		return
	}
	writeHTTP(w, http.StatusOK, map[string]any{"run_id": id, "sealed": true, "ok": rep.OK, "files": rep.Files, "problems": len(rep.Problems())})
}

// About is GET /api/v1/about.
type About struct {
	Version       string            `json:"version"`
	Build         string            `json:"build"`
	Config        string            `json:"config"`
	WorkDir       string            `json:"work_dir"`
	Paths         map[string]string `json:"paths"`
	ServiceURL    string            `json:"service_url"`
	ControlAPI    string            `json:"control_api,omitempty"`
	EmulatorMap   map[string]string `json:"emulator_sessions,omitempty"`
	MCPHint       string            `json:"mcp_hint"`
	MCPTools      int               `json:"mcp_tools"`
	EvidenceFile  string            `json:"service_evidence"`
	EngineLogPath string            `json:"engine_log"`
}

func (s *Service) about() About {
	abs := func(p string) string {
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	}
	wd, _ := os.Getwd()
	st := s.cfg.Storage
	return About{Version: version.Version, Build: version.Build, Config: abs(s.cfg.Path), WorkDir: wd,
		Paths: map[string]string{"seqnums": abs(st.SeqnumDir), "msgstore": abs(st.MsgstoreDir), "evidence": abs(st.EvidenceDir),
			"certs": abs(st.CertsDir), "logs": abs(s.cfg.Logging.LogDir)},
		ServiceURL: s.cfg.Service.URL(), ControlAPI: s.cfg.Emulator.ControlAPI, EmulatorMap: s.cfg.Emulator.Sessions,
		MCPHint:  "orderecho --config " + abs(s.cfg.Path) + " mcp install-claude-desktop   (prints the Claude Desktop entry; add --write to merge it)",
		MCPTools: len(s.EnabledTools()), EvidenceFile: abs(s.ev.Path), EngineLogPath: abs(s.Log.Path())}
}

// ------------------------------------------------------------ CSRF / origin

func stateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin: no Origin (a non-browser client), or exactly this service's.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == "http://"+r.Host
}

// csrfOK checks the token in constant time.
func (s *Service) csrfOK(r *http.Request) bool {
	got := r.Header.Get(CSRFHeader)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) == 1
}

func jsonBody(v any) []byte { b, _ := json.Marshal(v); return b }
