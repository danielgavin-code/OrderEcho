package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/report/reporttest"
	"github.com/danielgavin-code/OrderEcho/web"
)

var update = flag.Bool("update", false, "rewrite testdata/golden_report.html")

func fixture(t *testing.T, o reporttest.Options) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), reporttest.RunID)
	if _, err := reporttest.Build(dir, o); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGoldenReport(t *testing.T) {
	dir := fixture(t, reporttest.Options{})
	path, m, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	golden := filepath.Join("testdata", "golden_report.html")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/report -update once)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("report differs from %s (%d vs %d bytes); if the change is intended, rerun with -update", golden, len(got), len(want))
	}
	// Deterministic: the same results give byte-identical HTML, again and
	// from a fresh copy of the run in another directory.
	again, _ := Render(m)
	dir2 := fixture(t, reporttest.Options{})
	_, _, _ = Write(dir2)
	other, _ := os.ReadFile(filepath.Join(dir2, FileName))
	if !bytes.Equal(got, again) || !bytes.Equal(got, other) {
		t.Fatal("report is not deterministic")
	}
	if m.Verdict != Certified {
		t.Fatalf("fixture verdict %s (%s)", m.Verdict, m.VerdictReason)
	}
}

func TestReportContents(t *testing.T) {
	dir := fixture(t, reporttest.Options{})
	_, m, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(filepath.Join(dir, FileName))
	s := string(html)
	for _, c := range m.Res.Cases {
		if !strings.Contains(s, `id="case-`+c.ID+`"`) {
			t.Errorf("case %s missing", c.ID)
		}
	}
	// In order: header, verdict, sections, cases, deviations, attestations, integrity.
	order := []string{`<header class="top">`, `id="verdict"`, `id="sections"`, `id="cases"`, `id="deviations"`, `id="attestations"`, `id="integrity"`}
	last := -1
	for _, mark := range order {
		i := strings.Index(s, mark)
		if i < 0 || i < last {
			t.Fatalf("%s missing or out of order", mark)
		}
		last = i
	}
	for _, want := range []string{
		"emu42 — FIX.4.2, AGENT → ORDERECHO @ 127.0.0.1:9878", "OrderEcho FIX emulator 0.8.0 (cook8)", "OrderEcho 0.5.0 (build a5)",
		"2026-10-01T12:00:00.000Z", `<div class="word">CERTIFIED</div>`,
		"35=8 seq=3 11=OE-20261001-120000.000-1 150=2 39=2 32=100 31=227.50", // expectation matched
		"avg_px", "avg_px holds for every report of the chain", // checks with explanations
		"ExecutionReport ClOrdID=OE-20261001-120000.000-1 OrderID=O-1 ExecType=2(Fill) OrdStatus=2(Filled)", // decoded one-liner
		"|150=2|39=2|", // raw
		`<span class="badge INJECTED">INJECTED</span>`, `<span class="badge POSSDUP">POSSDUP</span>`,
		"Dana Ops", "draft reviewed, sent to the venue", "2026-10-01T12:00:08.000Z", // attestations
		"target orderecho-emulator: Emulator supports TimeInForce Day only — case(s) 4.5", "recommended tag(s) absent: 379 — case(s) 7.8", // deviations
		cert.ResultsDigest(dir), m.Res.Integrity.Files["4.1/fix.log"], "orderecho cert verify " + reporttest.RunID,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// Escaped, never executed: the task text in the fixture carries markup.
	if strings.Contains(s, "<script>alert(1)") || !strings.Contains(s, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("task text not escaped")
	}
	// Self-contained: no external URL is loaded or linked.
	for _, re := range []string{`(?i)(src|href|action)\s*=\s*"(https?:)?//`, `(?i)url\(\s*['"]?(https?:)?//`, `(?i)@import`, `<link`} {
		if regexp.MustCompile(re).MatchString(s) {
			t.Errorf("report references external content (%s)", re)
		}
	}
	for _, u := range regexp.MustCompile(`https?://[^\s"<)]+`).FindAllString(s, -1) {
		if !strings.HasPrefix(u, "http://127.0.0.1") {
			t.Errorf("non-loopback URL %s", u)
		}
	}
	// Readable without JS (details/summary), print stylesheet present, the
	// only script is the expand/collapse helper.
	if !strings.Contains(s, "@media print") || !strings.Contains(s, "break-before: page") || !strings.Contains(s, "beforeprint") {
		t.Error("print stylesheet / print expansion missing")
	}
	if strings.Count(s, "<script") != 1 || !strings.Contains(s, "<details class=\"case\"") {
		t.Error("unexpected scripts or no details")
	}
}

func TestVerdictLogic(t *testing.T) {
	dir := fixture(t, reporttest.Options{})
	m, _ := Build(dir)
	res := m.Res
	set := func(id, status string) {
		for _, c := range res.Cases {
			if c.ID == id {
				c.Status = status
			}
		}
	}
	if v, why := Verdict(res); v != Certified || why != "all 5 required case(s) are PASS or N/A" {
		t.Fatal(v, why)
	}
	set("7.8", cert.StatusNA) // N/A counts as done
	if v, _ := Verdict(res); v != Certified {
		t.Fatal(v)
	}
	set("1.1", cert.StatusPending)
	set("9.1", cert.StatusPending)
	if v, why := Verdict(res); v != Incomplete || why != "2 of 5 required case(s) are not PASS or N/A yet — PENDING 1.1, 9.1" {
		t.Fatal(v, why)
	}
	set("4.5", cert.StatusFail) // optional: never decides
	if v, _ := Verdict(res); v != Incomplete {
		t.Fatal(v)
	}
	set("4.1", cert.StatusFail)
	if v, why := Verdict(res); v != NotCertified || why != "1 required case(s) FAILED: 4.1" {
		t.Fatal(v, why)
	}
	set("4.1", cert.StatusError)
	if v, why := Verdict(res); v != Incomplete || !strings.Contains(why, "ERROR 4.1") {
		t.Fatal(v, why)
	}
	// A partial run cannot certify the suite.
	set("4.1", cert.StatusPass)
	set("1.1", cert.StatusPass)
	set("9.1", cert.StatusPass)
	res.RequiredInSuite = append(res.RequiredInSuite, "2.1", "2.2")
	if v, why := Verdict(res); v != Incomplete || !strings.Contains(why, "not in this run 2.1, 2.2") {
		t.Fatal(v, why)
	}
	res.RequiredInSuite = res.RequiredInSuite[:5]
	res.RunError = "service stopped while the run was in progress"
	if v, _ := Verdict(res); v != Incomplete {
		t.Fatal(v)
	}
	// Long lists are cut.
	if got := idList(strings.Split("a b c d e f g h i j k l m n", " ")); got != "a, b, c, d, e, f, g, h, i, j, k, l … (+2 more)" {
		t.Fatal(got)
	}
}

func TestVerifyDetectsOneByteEdit(t *testing.T) {
	dir := fixture(t, reporttest.Options{})
	rep, err := cert.Verify(dir)
	if err != nil || !rep.OK || len(rep.Files) != 5 {
		t.Fatalf("%v %+v", err, rep)
	}
	p := filepath.Join(dir, "7.8", "fix.log")
	data, _ := os.ReadFile(p)
	data[40] ^= 0x01
	os.WriteFile(p, data, 0o644)
	rep, _ = cert.Verify(dir)
	if rep.OK || len(rep.Problems()) != 1 || rep.Problems()[0].Path != "7.8/fix.log" || rep.Problems()[0].Status != "MISMATCH" {
		t.Fatalf("%+v", rep.Problems())
	}
	// results.json edited by hand; an added file; a deleted file.
	dir = fixture(t, reporttest.Options{})
	rj := filepath.Join(dir, "results.json")
	data, _ = os.ReadFile(rj)
	os.WriteFile(rj, bytes.Replace(data, []byte(`"status": "FAIL"`), []byte(`"status": "PASS"`), 1), 0o644)
	os.WriteFile(rj, append(data[:len(data)-1], ' ', '\n'), 0o644)
	os.WriteFile(filepath.Join(dir, "4.1", "extra.log"), []byte("x"), 0o644)
	os.Remove(filepath.Join(dir, "7.8", "evidence.jsonl"))
	rep, _ = cert.Verify(dir)
	got := map[string]string{}
	for _, f := range rep.Problems() {
		got[f.Path] = f.Status
	}
	if rep.OK || got["results.json"] != "MISMATCH" || got["4.1/extra.log"] != "UNRECORDED" || got["7.8/evidence.jsonl"] != "MISSING" {
		t.Fatalf("%v", got)
	}
	// The report itself is not evidence: regenerating it never breaks verification.
	dir = fixture(t, reporttest.Options{})
	Write(dir)
	if rep, _ := cert.Verify(dir); !rep.OK {
		t.Fatalf("%+v", rep.Problems())
	}
}

func TestStylesheetTokensOnly(t *testing.T) {
	css := web.CSS
	i := strings.Index(css, ":root {")
	j := strings.Index(css[i:], "\n}\n")
	if i < 0 || j < 0 {
		t.Fatal("no :root block")
	}
	root, rest := css[i:i+j], css[i+j:]
	// The emulator's Cook 8 token names are all here.
	for _, tok := range []string{"--bg", "--surface", "--surface-2", "--surface-3", "--text", "--muted", "--border", "--accent", "--accent-ink",
		"--accent-soft", "--pass", "--warn", "--fail", "--pass-soft", "--warn-soft", "--fail-soft", "--in", "--out", "--disc", "--flag", "--fill",
		"--font-sans", "--font-mono", "--size-base", "--size-small", "--size-tiny", "--size-code", "--line", "--line-tight",
		"--s1", "--s2", "--s3", "--s4", "--s5", "--s6", "--radius", "--radius-sm", "--content", "--sidebar", "--shadow", "--shadow-lifted"} {
		if !strings.Contains(root, tok+":") {
			t.Errorf("token %s missing", tok)
		}
	}
	// No colour outside :root.
	if m := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(`).FindString(rest); m != "" {
		t.Errorf("hard-coded colour outside :root: %s", m)
	}
	// Every status has a badge style.
	for _, cls := range []string{"PASS", "FAIL", "WARN", "PENDING", "BLOCKED", "NA", "ERROR", "CERTIFIED", "NOT-CERTIFIED", "INCOMPLETE"} {
		if !strings.Contains(rest, ".badge."+cls) {
			t.Errorf("no badge style for %s", cls)
		}
	}
}
