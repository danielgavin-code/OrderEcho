//go:build interop

package interop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A5 interop: the real emulator and the real agent service, driven the way
// the GUI drives it — JSON POSTs to /api/v1 with the CSRF token and the
// page's own Origin — with live updates read from /api/v1/events.

type guiClient struct {
	t      *testing.T
	base   string
	token  string
	origin string
}

func (g *guiClient) post(tool string, args any, withToken bool) (int, map[string]any) {
	g.t.Helper()
	body, _ := json.Marshal(args)
	req, _ := http.NewRequest("POST", g.base+"/api/v1/"+tool, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OrderEcho-Client", "gui")
	req.Header.Set("Origin", g.origin)
	if withToken {
		req.Header.Set("X-OrderEcho-CSRF", g.token)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		g.t.Fatalf("POST %s: %v", tool, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(data, &out)
	g.t.Logf("GUI POST %s %s -> %d %s", tool, body, resp.StatusCode, strOr(out["summary"], string(data)))
	return resp.StatusCode, out
}

func (g *guiClient) call(tool string, args any) map[string]any {
	g.t.Helper()
	code, out := g.post(tool, args, true)
	if code != 200 {
		g.t.Fatalf("%s: HTTP %d %v", tool, code, out)
	}
	return out
}

func strOr(v any, alt string) string {
	if s, ok := v.(string); ok {
		return s
	}
	if len(alt) > 300 {
		alt = alt[:300]
	}
	return alt
}

type liveEvent struct {
	ID, Type string
	Data     map[string]any
}

// events streams /api/v1/events into a channel.
func events(t *testing.T, base string) (<-chan liveEvent, func()) {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/events")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, resp)
	}
	ch := make(chan liveEvent, 10000)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<16), 1<<22)
		var ev liveEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Type != "" {
					ch <- ev
				}
				ev = liveEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Type = line[7:]
			case strings.HasPrefix(line, "data: "):
				json.Unmarshal([]byte(line[6:]), &ev.Data)
			}
		}
		close(ch)
	}()
	return ch, func() { resp.Body.Close() }
}

// await reads events until match returns true (others are kept in seen).
func await(t *testing.T, ch <-chan liveEvent, what string, timeout time.Duration, match func(liveEvent) bool) liveEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event stream closed waiting for %s", what)
			}
			if match(ev) {
				t.Logf("SSE %s #%s: %s", ev.Type, ev.ID, oneLineJSON(ev.Data))
				return ev
			}
		case <-deadline:
			t.Fatalf("no SSE event for %s within %s", what, timeout)
		}
	}
}

func oneLineJSON(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 260 {
		b = append(b[:260], []byte("…")...)
	}
	return string(b)
}

func TestA5GUIAPIOrderSSEReportAndAttestation(t *testing.T) {
	e := startEmulator(t)
	dir, port := mcpDir(t, e)
	cmd := exec.Command(agentBin, "--config", "orderecho.yaml", "serve")
	cmd.Dir = dir
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ORDERECHO_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var logs lockedBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(20 * time.Second):
			cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("serve output:\n%s", logs.String())
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitFor(t, 15*time.Second, "service up", func() bool { _, err := serviceHealth(port); return err == nil })

	// The page shell carries the token; GET /api/v1/csrf gives the same one.
	resp, err := http.Get(base + "/orders")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var tok struct {
		Token string `json:"token"`
	}
	r2, _ := http.Get(base + "/api/v1/csrf")
	json.NewDecoder(r2.Body).Decode(&tok)
	r2.Body.Close()
	if len(tok.Token) != 64 || !strings.Contains(string(page), tok.Token) {
		t.Fatal("CSRF token not on the page")
	}
	g := &guiClient{t: t, base: base, token: tok.Token, origin: base}
	if code, out := g.post("connect_session", map[string]any{"session_id": "emu42"}, false); code != 403 || out["error"] != "csrf_invalid" {
		t.Fatalf("without the token: %d %v", code, out)
	}
	evs, stop := events(t, base)
	defer stop()

	// Connect emu42 and send an order exactly as the GUI's form does.
	g.call("connect_session", map[string]any{"session_id": "emu42"})
	await(t, evs, "emu42 ACTIVE", 10*time.Second, func(ev liveEvent) bool {
		return ev.Type == "session" && ev.Data["session_id"] == "emu42" && ev.Data["state"] == "ACTIVE"
	})
	out := g.call("send_order", map[string]any{"session_id": "emu42", "symbol": "AAPL", "side": "buy", "qty": "100", "ord_type": "mkt", "wait_for": "ack"})
	root := out["result"].(map[string]any)["cl_ord_id"].(string)
	await(t, evs, "our D on the feed", 10*time.Second, func(ev liveEvent) bool {
		return ev.Type == "message" && ev.Data["direction"] == "out" && ev.Data["msg_type"] == "D" && ev.Data["cl_ord_id"] == root
	})
	await(t, evs, "its ack on the feed", 10*time.Second, func(ev liveEvent) bool {
		return ev.Type == "message" && ev.Data["direction"] == "in" && ev.Data["msg_type"] == "8" && ev.Data["cl_ord_id"] == root &&
			strings.Contains(fmt.Sprint(ev.Data["line"]), "ExecType=0(New)")
	})
	filled := await(t, evs, "the order FILLED with its checks", 15*time.Second, func(ev liveEvent) bool {
		o, _ := ev.Data["order"].(map[string]any)
		return ev.Type == "order" && o != nil && o["root_cl_ord_id"] == root && o["state"] == "FILLED"
	})
	if o := filled.Data["order"].(map[string]any); o["verdict"] != "PASS" || len(o["checks"].(map[string]any)) != 11 {
		t.Fatalf("%v", o)
	}

	// The ORD section through the API; progress arrives live; the report is written at the end.
	out = g.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "section": "ORD"})
	runID := out["result"].(map[string]any)["run_id"].(string)
	lastDone := -1.0
	await(t, evs, "the run finished", 3*time.Minute, func(ev liveEvent) bool {
		if ev.Type != "cert" || ev.Data["run_id"] != runID {
			return false
		}
		done, _ := ev.Data["done"].(float64)
		if done < lastDone {
			t.Fatalf("progress went backwards: %v after %v", done, lastDone)
		}
		lastDone = done
		return ev.Data["state"] == "finished"
	})
	if lastDone != 9 {
		t.Fatalf("progress ended at %v/9", lastDone)
	}
	reportPath := filepath.Join(dir, "data", "certs", runID, "report.html")
	html := mustRead(t, reportPath)
	if !strings.Contains(html, "id=\"case-4.1\"") || !strings.Contains(html, `<div class="word">INCOMPLETE</div>`) {
		t.Fatal("report not written at run end, or wrong verdict (a section run cannot certify the suite)")
	}
	if r, _ := http.Get(base + "/certs/" + runID + "/report"); r == nil || r.StatusCode != 200 {
		t.Fatal("report not served")
	}

	// MCP: cert_run_report returns the URL.
	c := stdioClient(t, dir)
	rep, sum := c.call("cert_run_report", map[string]any{"run_id": runID})
	if str(rep, "url") != base+"/certs/"+runID+"/report" || str(rep, "verdict") != "INCOMPLETE" || str(rep, "path") != filepath.Join("data", "certs", runID, "report.html") {
		t.Fatalf("%v (%s)", rep, sum)
	}

	// Attest a manual case through the API (as the GUI's confirm dialog does): the report regenerates.
	out = g.call("start_cert_run", map[string]any{"suite": "order-entry-fix42", "target": "emulator", "session_id": "emu42", "cases": []string{"1.1", "4.1"}})
	run2 := out["result"].(map[string]any)["run_id"].(string)
	await(t, evs, "run 2 finished", 2*time.Minute, func(ev liveEvent) bool {
		return ev.Type == "cert" && ev.Data["run_id"] == run2 && ev.Data["state"] == "finished"
	})
	path2 := filepath.Join(dir, "data", "certs", run2, "report.html")
	before := mustRead(t, path2)
	if strings.Contains(before, "Interop GUI user") || !strings.Contains(before, "needs a human attestation") {
		t.Fatal("unexpected report before the attestation")
	}
	args := map[string]any{"run_id": run2, "case_id": "1.1", "status": "pass", "by": "Interop GUI user", "note": "docs pack received", "user_confirmed": true}
	if code, out := g.post("attest_cert_case", args, false); code != 403 || out["error"] != "csrf_invalid" {
		t.Fatalf("attest without the token: %d", code)
	}
	out = g.call("attest_cert_case", args)
	after := mustRead(t, path2)
	if !strings.Contains(after, "Interop GUI user") || !strings.Contains(after, "attest_cert_case (gui)") || !strings.Contains(after, "docs pack received") {
		t.Fatal("report did not regenerate with the attestation")
	}
	// The evidence still verifies after the attestation.
	run := runCLI(t, dir, time.Minute, "cert", "verify", run2)
	if run.code != 0 || !strings.Contains(run.out, "intact:") {
		t.Fatalf("verify exit %d", run.code)
	}
	if r := runCLI(t, dir, time.Minute, "cert", "verify", runID); r.code != 0 {
		t.Fatalf("verify %s exit %d", runID, r.code)
	}
	recordVerdict("%-44s %s", "A5 GUI API + SSE + report + attestation", "order FILLED PASS via API, SSE D/8/order/cert events, report auto, cert_run_report URL, attestation re-rendered, verify 0")
}
