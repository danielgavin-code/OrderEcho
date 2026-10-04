package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/report"
	"github.com/danielgavin-code/OrderEcho/internal/report/reporttest"
	"github.com/danielgavin-code/OrderEcho/web"
)

// A5 §8 GUI/API unit tests. HTTP goes through recorders, except SSE, which
// needs a real stream (an httptest server on loopback).

func req(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:8190"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var externalRef = regexp.MustCompile(`(?i)((src|href|action)\s*=\s*["']?(https?:)?//|url\(\s*['"]?(https?:)?//|@import|https?://(?:[a-z0-9-]+\.)+[a-z]{2,})`)

func TestGUIPagesAndAssets(t *testing.T) {
	s := newTestService(t, "")
	h := s.Handler(HandlerOptions{})
	for path, page := range Pages {
		w := req(t, h, "GET", path, "", nil)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `<meta name="orderecho-csrf" content="`+s.CSRFToken()+`">`) ||
			!strings.Contains(body, `data-page="`+page+`"`) || !strings.Contains(body, `<script src="/assets/app.js"></script>`) {
			t.Fatalf("%s: %d\n%s", path, w.Code, body)
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: headers %v", path, w.Header())
		}
		for _, nav := range []string{`href="/orders"`, `href="/messages"`, `href="/certifications"`, `href="/emulator"`, `href="/about"`} {
			if !strings.Contains(body, nav) {
				t.Fatalf("%s: nav lacks %s", path, nav)
			}
		}
	}
	// Assets are the embedded files, byte for byte.
	for path, name := range map[string]string{"/assets/app.js": "app.js", "/assets/orderecho.css": "orderecho.css"} {
		want, _ := web.Files.ReadFile(name)
		w := req(t, h, "GET", path, "", nil)
		if w.Code != 200 || w.Body.String() != string(want) || len(want) < 1000 {
			t.Fatalf("%s: %d (%d bytes)", path, w.Code, w.Body.Len())
		}
	}
	// No page or asset loads anything from elsewhere.
	for _, name := range []string{"index.html", "app.js", "orderecho.css"} {
		data, _ := web.Files.ReadFile(name)
		if m := externalRef.FindString(string(data)); m != "" {
			t.Errorf("%s references external content: %q", name, m)
		}
	}
	// Every page the GUI's JS knows is served, and the JS only calls /api/v1.
	js, _ := web.Files.ReadFile("app.js")
	for _, page := range Pages {
		if !strings.Contains(string(js), page+": "+page) && page != "dashboard" {
			t.Errorf("app.js has no %s page", page)
		}
	}
	for _, m := range regexp.MustCompile(`(fetch|EventSource)\('([^']*)`).FindAllStringSubmatch(string(js), -1) {
		if m[2] != "/api/v1/" && m[2] != "/api/v1/events" {
			t.Errorf("app.js %s(%q)", m[1], m[2])
		}
	}
	if w := req(t, h, "GET", "/nope", "", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	s := newTestService(t, "")
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	h := s.Handler(HandlerOptions{MCP: mcp})
	tok := s.CSRFToken()
	if len(tok) != 64 {
		t.Fatal(tok)
	}
	code := func(w *httptest.ResponseRecorder) string {
		var e APIError
		json.Unmarshal(w.Body.Bytes(), &e)
		return fmt.Sprintf("%d %s", w.Code, e.Code)
	}
	cases := []struct {
		name, method, path string
		hdr                map[string]string
		want               string
	}{
		{"no token", "POST", "/api/v1/list_sessions", nil, "403 csrf_invalid"},
		{"wrong token", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: strings.Repeat("0", 64)}, "403 csrf_invalid"},
		{"short token", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok[:10]}, "403 csrf_invalid"},
		{"token", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok}, "200 "},
		{"token, own origin", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok, "Origin": "http://127.0.0.1:8190"}, "200 "},
		{"token, foreign origin", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok, "Origin": "https://evil.example"}, "403 forbidden_origin"},
		{"token, other local port", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok, "Origin": "http://127.0.0.1:3000"}, "403 forbidden_origin"},
		{"token, null origin", "POST", "/api/v1/list_sessions", map[string]string{CSRFHeader: tok, "Origin": "null"}, "403 forbidden_origin"},
		{"PUT without token", "PUT", "/api/v1/list_sessions", nil, "403 csrf_invalid"},
		{"DELETE without token", "DELETE", "/api/v1/certs/x/report", nil, "403 csrf_invalid"},
		{"GET, foreign origin", "GET", "/api/v1/status", map[string]string{"Origin": "https://evil.example"}, "403 forbidden_origin"},
		{"GET, no token needed", "GET", "/api/v1/status", nil, "200 "},
		{"GET csrf token", "GET", "/api/v1/csrf", nil, "200 "},
		{"MCP over HTTP is independent", "POST", "/mcp", nil, "299 "},
		{"MCP, foreign origin", "POST", "/mcp", map[string]string{"Origin": "https://evil.example"}, "403 forbidden_origin"},
	}
	for _, c := range cases {
		body := ""
		if c.method != "GET" {
			body = "{}"
		}
		if got := code(req(t, h, c.method, c.path, body, c.hdr)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// A foreign Host (DNS rebinding) is refused even with the token.
	r := httptest.NewRequest("POST", "http://evil.example:8190/api/v1/list_sessions", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(CSRFHeader, tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if code(w) != "403 forbidden_host" {
		t.Fatal(code(w))
	}
	// The token endpoint answers without CORS headers (a page elsewhere can't read it).
	w = req(t, h, "GET", "/api/v1/csrf", "", map[string]string{"Origin": "http://127.0.0.1:8190"})
	if w.Header().Get("Access-Control-Allow-Origin") != "" || !strings.Contains(w.Body.String(), tok) {
		t.Fatal(w.Header())
	}
	// A new process, a new token.
	if s2 := newTestService(t, ""); s2.CSRFToken() == tok {
		t.Fatal("token reused")
	}
}

// snapshotDir lists every file under dir with size and mtime.
func snapshotDir(t *testing.T, dir string) string {
	var lines []string
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			lines = append(lines, fmt.Sprintf("%s %d %d", p, fi.Size(), fi.ModTime().UnixNano()))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func fixtureRun(t *testing.T, s *Service, o reporttest.Options) string {
	t.Helper()
	dir := filepath.Join(s.certsDir(), reporttest.RunID)
	if _, err := reporttest.Build(dir, o); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGETsAreSideEffectFree(t *testing.T) {
	s := newTestService(t, emulatorBlock)
	h := s.Handler(HandlerOptions{})
	dir := fixtureRun(t, s, reporttest.Options{PendingManual: true}) // no report.html yet
	time.Sleep(300 * time.Millisecond)                               // the session watcher's first pass
	before := snapshotDir(t, filepath.Dir(s.certsDir()))
	sessBefore, _ := json.Marshal(s.sessionInfo(s.sessions["emu42"]))
	gets := []string{"/", "/orders", "/messages", "/certifications", "/emulator", "/about", "/assets/app.js", "/assets/orderecho.css",
		"/api/v1/health", "/api/v1/status", "/api/v1/tools", "/api/v1/csrf", "/api/v1/about", "/api/v1/certs", "/api/v1/certs?limit=1",
		"/api/v1/certs/" + reporttest.RunID + "/report", "/certs/" + reporttest.RunID + "/report", "/api/v1/certs/" + reporttest.RunID + "/verify",
		"/api/v1/certs/nope/report", "/api/v1/certs/nope/verify"}
	for _, p := range gets {
		w := req(t, h, "GET", p, "", nil)
		if w.Code >= 500 {
			t.Fatalf("GET %s: %d %s", p, w.Code, w.Body)
		}
	}
	// The report was rendered for the GET without being written.
	if exists(filepath.Join(dir, report.FileName)) {
		t.Fatal("GET wrote report.html")
	}
	after := snapshotDir(t, filepath.Dir(s.certsDir()))
	// The service's own log and evidence may grow with the watcher; the
	// certs tree must not change at all.
	if strings.Join(certLines(before), "\n") != strings.Join(certLines(after), "\n") {
		t.Fatalf("GETs changed files:\n%s\n---\n%s", before, after)
	}
	sessAfter, _ := json.Marshal(s.sessionInfo(s.sessions["emu42"]))
	if string(sessBefore) != string(sessAfter) {
		t.Fatal("GETs changed session state")
	}
	// Tools are POST only: a GET cannot invoke one.
	for _, tool := range []string{"connect_session", "send_order", "attest_cert_case", "start_cert_run"} {
		if w := req(t, h, "GET", "/api/v1/"+tool, "", nil); w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s: %d", tool, w.Code)
		}
	}
}

func certLines(snapshot string) []string {
	var out []string
	for _, l := range strings.Split(snapshot, "\n") {
		if strings.Contains(l, string(filepath.Separator)+"certs") {
			out = append(out, l)
		}
	}
	return out
}

func TestReportEndpointsAndTool(t *testing.T) {
	s := newTestService(t, "")
	h := s.Handler(HandlerOptions{})
	dir := fixtureRun(t, s, reporttest.Options{})
	// cert_run_report writes the report when missing and says where.
	r := invoke(t, s, ClientMCPStdio, "cert_run_report", map[string]any{"run_id": reporttest.RunID})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	out := r.Result.(map[string]any)
	url := "http://127.0.0.1:8190/certs/" + reporttest.RunID + "/report"
	if out["url"] != url || out["verdict"] != report.Certified || out["path"] != filepath.Join(dir, report.FileName) ||
		!strings.Contains(r.Summary, "CERTIFIED") || !strings.Contains(r.Summary, url) {
		t.Fatalf("%+v %s", out, r.Summary)
	}
	file, _ := os.ReadFile(filepath.Join(dir, report.FileName))
	for _, p := range []string{"/certs/" + reporttest.RunID + "/report", "/api/v1/certs/" + reporttest.RunID + "/report"} {
		w := req(t, h, "GET", p, "", nil)
		if w.Code != 200 || w.Body.String() != string(file) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	var runs struct {
		Runs []RunRow `json:"runs"`
	}
	json.Unmarshal(req(t, h, "GET", "/api/v1/certs", "", nil).Body.Bytes(), &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].Verdict != report.Certified || runs.Runs[0].ReportURL != "/certs/"+reporttest.RunID+"/report" || !runs.Runs[0].Sealed {
		t.Fatalf("%+v", runs)
	}
	w := req(t, h, "GET", "/api/v1/certs/"+reporttest.RunID+"/verify", "", nil)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"problems":0`) {
		t.Fatal(w.Body.String())
	}
	os.WriteFile(filepath.Join(dir, "4.1", "fix.log"), []byte("edited"), 0o644)
	w = req(t, h, "GET", "/api/v1/certs/"+reporttest.RunID+"/verify", "", nil)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"problems":1`) {
		t.Fatal(w.Body.String())
	}
	if r := invoke(t, s, ClientAPI, "cert_run_report", map[string]any{"run_id": "20990101-000000.000"}); r.Error == nil || r.Error.Code != "unknown_run" {
		t.Fatal(r)
	}
}

func TestGUIAttestationRegeneratesReport(t *testing.T) {
	s := newTestService(t, "")
	h := s.Handler(HandlerOptions{})
	dir := fixtureRun(t, s, reporttest.Options{PendingManual: true})
	report.Write(dir)
	before, _ := os.ReadFile(filepath.Join(dir, report.FileName))
	if !strings.Contains(string(before), `<div class="word">INCOMPLETE</div>`) {
		t.Fatal("fixture should start INCOMPLETE")
	}
	body := `{"run_id":"` + reporttest.RunID + `","case_id":"1.1","status":"pass","by":"Gui User","note":"checked the docs pack","user_confirmed":true}`
	hdr := map[string]string{CSRFHeader: s.CSRFToken(), ClientHeader: ClientGUI, "Origin": "http://127.0.0.1:8190"}
	w := req(t, h, "POST", "/api/v1/attest_cert_case", body, hdr)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	after, _ := os.ReadFile(filepath.Join(dir, report.FileName))
	a := string(after)
	if !strings.Contains(a, `<div class="word">CERTIFIED</div>`) || !strings.Contains(a, "Gui User") || !strings.Contains(a, "attest_cert_case (gui)") ||
		!strings.Contains(a, "checked the docs pack") {
		t.Fatal("report not regenerated with the attestation")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "results.json"))
	var res cert.RunResult
	json.Unmarshal(data, &res)
	if res.Cases[0].Attestation.By != "Gui User" || res.Cases[0].Attestation.File != "attest_cert_case (gui)" {
		t.Fatalf("%+v", res.Cases[0].Attestation)
	}
	if rep, err := cert.Verify(dir); err != nil || !rep.OK {
		t.Fatalf("attestation broke verification: %v %+v", err, rep)
	}
	trail, _ := os.ReadFile(filepath.Join(dir, "attestations.jsonl"))
	if !strings.Contains(string(trail), `"via":"gui"`) {
		t.Fatal(string(trail))
	}
	logPath := s.Log.Path()
	// A run whose evidence changed takes no more attestations.
	os.WriteFile(filepath.Join(dir, "4.1", "fix.log"), []byte("edited"), 0o644)
	w = req(t, h, "POST", "/api/v1/attest_cert_case", strings.Replace(body, `"1.1"`, `"9.2"`, 1), hdr)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"error":"tampered"`) || !strings.Contains(w.Body.String(), "4.1/fix.log MISMATCH") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s.Shutdown(0)
	engine, _ := os.ReadFile(logPath)
	if !strings.Contains(string(engine), "GUI  attest_cert_case by=Gui User case_id=1.1") {
		t.Fatalf("%s", engine)
	}
}

// ------------------------------------------------------------ SSE

type sseEvent struct {
	ID, Type, Data string
}

type sseStream struct {
	resp *http.Response
	ch   chan sseEvent
}

func openSSE(t *testing.T, url, lastID string) *sseStream {
	t.Helper()
	r, _ := http.NewRequest("GET", url+"/api/v1/events", nil)
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%v %v", err, resp)
	}
	st := &sseStream{resp: resp, ch: make(chan sseEvent, 4096)}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Type != "" {
					st.ch <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Type = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.Data = line[6:]
			}
		}
		close(st.ch)
	}()
	return st
}

// next returns the next event of one of the types (others skipped).
func (st *sseStream) next(t *testing.T, types ...string) sseEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-st.ch:
			if !ok {
				t.Fatal("stream closed")
			}
			for _, ty := range types {
				if ev.Type == ty {
					return ev
				}
			}
		case <-timeout:
			t.Fatalf("no %v event within 5s", types)
		}
	}
}

func (st *sseStream) close() { st.resp.Body.Close() }

func TestSSEOrderReconnectDropReset(t *testing.T) {
	s := newTestService(t, "")
	srv := httptest.NewServer(s.Handler(HandlerOptions{}))
	defer srv.Close()
	a := openSSE(t, srv.URL, "")
	time.Sleep(100 * time.Millisecond)
	for i := 1; i <= 5; i++ {
		s.events.publish(EvMessage, map[string]int{"n": i})
	}
	var lastID string
	for i := 1; i <= 3; i++ {
		ev := a.next(t, EvMessage)
		if ev.Data != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("event %d: %+v", i, ev)
		}
		lastID = ev.ID
	}
	// Drop the connection after the third; two more happen while away.
	a.close()
	for i := 6; i <= 7; i++ {
		s.events.publish(EvMessage, map[string]int{"n": i})
	}
	b := openSSE(t, srv.URL, lastID) // what EventSource sends on reconnect
	for i := 4; i <= 7; i++ {
		if ev := b.next(t, EvMessage, EvReset, EvDropped); ev.Type != EvMessage || ev.Data != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("after reconnect, want n=%d, got %+v", i, ev)
		}
	}
	s.events.publish(EvOrder, map[string]string{"order": "live"})
	if ev := b.next(t, EvOrder); !strings.Contains(ev.Data, "live") {
		t.Fatal(ev)
	}
	b.close()

	// A client further behind than its buffer: the oldest are dropped, and it is told.
	s.events.mu.Lock()
	s.events.bufMax = 3
	s.events.mu.Unlock()
	c := openSSE(t, srv.URL, lastID)
	ev := c.next(t, EvDropped, EvMessage)
	if ev.Type != EvDropped || !strings.Contains(ev.Data, `"dropped":`) {
		t.Fatalf("%+v", ev)
	}
	c.close()

	// Further behind than the hub remembers: a reset.
	for i := 0; i < ringEvents+10; i++ {
		s.events.publish(EvMessage, map[string]int{"n": i})
	}
	d := openSSE(t, srv.URL, "1")
	if ev := d.next(t, EvReset, EvMessage); ev.Type != EvReset {
		t.Fatalf("%+v", ev)
	}
	d.close()
}

func TestHubBufferDropsOldest(t *testing.T) {
	h := newHub()
	h.bufMax = 4
	sub, _ := h.subscribe(0)
	for i := 1; i <= 10; i++ {
		h.publish(EvMessage, i)
	}
	evs, dropped := sub.take()
	if dropped != 6 || len(evs) != 4 || string(evs[0].Data) != "7" || evs[3].ID != 10 {
		t.Fatalf("%d %+v", dropped, evs)
	}
	// Publishing never blocks, whatever the subscribers do.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			h.publish(EvMessage, i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish blocked")
	}
}
