// Package report renders a certification run's directory (results.json plus
// each case's fix.log and evidence.jsonl) as one self-contained HTML file,
// report.html: inline CSS, readable without JavaScript, deterministic (the
// same results give byte-identical HTML), printable to PDF.
//
// The report never decides anything: statuses, reasons and checks are the
// runner's, copied from results.json. The verdict banner is a fixed
// function of the required cases' statuses (Verdict).
package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
	"github.com/danielgavin-code/OrderEcho/internal/fixview"
	"github.com/danielgavin-code/OrderEcho/web"
)

// FileName is the report's name inside a run directory.
const FileName = "report.html"

// Verdicts.
const (
	Certified    = "CERTIFIED"
	NotCertified = "NOT CERTIFIED"
	Incomplete   = "INCOMPLETE"
)

// Statuses in display order.
var Statuses = []string{cert.StatusPass, cert.StatusFail, cert.StatusBlocked, cert.StatusPending, cert.StatusNA, cert.StatusError, cert.StatusNotRun}

// Verdict decides the banner from the required cases of the suite:
//   - NOT CERTIFIED: a required case FAILed (the counterparty did not do
//     what the checklist requires);
//   - INCOMPLETE: no required FAIL, but a required case is not PASS or N/A
//     yet (PENDING, BLOCKED, ERROR, NOT_RUN, or not part of this run), or
//     the run itself ended in error;
//   - CERTIFIED: every required case of the suite is PASS or N/A.
//
// It returns the verdict and a one-line reason.
func Verdict(res *cert.RunResult) (string, string) {
	byID := map[string]*cert.CaseResult{}
	for _, c := range res.Cases {
		byID[c.ID] = c
	}
	required := res.RequiredInSuite
	if len(required) == 0 {
		for _, c := range res.Cases {
			if c.Required {
				required = append(required, c.ID)
			}
		}
	}
	var failed []string
	open := map[string][]string{} // status -> ids
	var openOrder []string
	nOpen := 0
	for _, id := range required {
		c := byID[id]
		st := ""
		switch {
		case c == nil:
			st = "not in this run"
		case c.Status == cert.StatusFail:
			failed = append(failed, id)
		case c.Status != cert.StatusPass && c.Status != cert.StatusNA:
			st = c.Status
		}
		if st != "" {
			if _, ok := open[st]; !ok {
				openOrder = append(openOrder, st)
			}
			open[st] = append(open[st], id)
			nOpen++
		}
	}
	switch {
	case len(failed) > 0:
		return NotCertified, fmt.Sprintf("%d required case(s) FAILED: %s", len(failed), idList(failed))
	case nOpen > 0:
		var parts []string
		for _, st := range openOrder {
			parts = append(parts, fmt.Sprintf("%s %s", st, idList(open[st])))
		}
		return Incomplete, fmt.Sprintf("%d of %d required case(s) are not PASS or N/A yet — %s", nOpen, len(required), strings.Join(parts, "; "))
	case res.RunError != "":
		return Incomplete, "the run ended in error: " + res.RunError
	}
	return Certified, fmt.Sprintf("all %d required case(s) are PASS or N/A", len(required))
}

// BadgeClass maps a status or verdict onto its CSS class.
func BadgeClass(s string) string {
	switch s {
	case cert.StatusNA:
		return "NA"
	case NotCertified:
		return "NOT-CERTIFIED"
	}
	return strings.ReplaceAll(s, " ", "-")
}

// ------------------------------------------------------------ model

// EvidenceRow is one message of a case's FIX log slice.
type EvidenceRow struct {
	Time, Dir, Seq, Type, Line string
	Flags                      []string
}

// CaseView is one case as the report shows it.
type CaseView struct {
	*cert.CaseResult
	Badge        string
	Expectations []string
	Evidence     []EvidenceRow
	Raw          []string
	EvidenceNote string
	Events       int // evidence.jsonl records
}

// SectionRow is one line of the summary by section.
type SectionRow struct {
	ID, Name string
	Counts   []int // in Statuses order
	Total    int
}

// FileHash is one integrity row.
type FileHash struct{ Path, SHA256 string }

// Model is everything the template needs.
type Model struct {
	Res            *cert.RunResult
	Verdict        string
	VerdictClass   string
	VerdictReason  string
	Statuses       []string
	StatusClasses  []string
	AllCounts      []int
	RequiredCounts []int
	Sections       []SectionRow
	Cases          []CaseView
	NA             []CaseView
	Attested       []CaseView
	ResultsDigest  string
	Hashes         []FileHash
	Sealed         bool
	CSS            template.CSS
	JS             template.JS
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func counts(m map[string]int) []int {
	out := make([]int, len(Statuses))
	for i, s := range Statuses {
		out[i] = m[s]
	}
	return out
}

// fixLine is the agent's FIX log line: stamp, direction, seq, 35=, raw, comment.
var fixLine = regexp.MustCompile(`^(\d{8}-\d\d:\d\d:\d\d\.\d{3}) (\S+)\s+(\S+)\s+(\S+)\s+(.*?)(?:  # (.*))?$`)

func readEvidence(dir string, cv *CaseView) {
	f, err := os.Open(filepath.Join(dir, cv.ID, "fix.log"))
	if err != nil {
		if _, derr := os.Stat(filepath.Join(dir, cv.ID)); derr == nil {
			cv.EvidenceNote = "fix.log missing"
		} else {
			cv.EvidenceNote = "no FIX evidence: this case did not talk to the session (" + strings.ToLower(cv.Mode) + ", " + cv.Status + ")"
		}
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		cv.Raw = append(cv.Raw, line)
		m := fixLine.FindStringSubmatch(line)
		if m == nil {
			cv.Evidence = append(cv.Evidence, EvidenceRow{Line: "(unparsed line)"})
			continue
		}
		pairs := fixview.Pairs(m[5])
		get := fixview.GetterOf(pairs)
		begin, _ := get(8)
		p := fixview.ProfileFor(begin)
		row := EvidenceRow{Time: m[1], Dir: strings.ToLower(m[2]), Seq: strings.TrimPrefix(m[3], "seq="), Flags: fixview.Flags(get)}
		mt, _ := get(35)
		row.Type = p.MsgTypeName(mt)
		row.Line = fixview.Line(p, mt, get)
		if row.Dir == "disc" {
			row.Flags = append(row.Flags, "BAD-FRAMING")
			row.Line = "discarded frame: " + m[6]
		} else if strings.HasPrefix(m[6], "injected") {
			row.Flags = append(row.Flags, "INJECTED")
		}
		cv.Evidence = append(cv.Evidence, row)
	}
	if data, err := os.ReadFile(filepath.Join(dir, cv.ID, "evidence.jsonl")); err == nil {
		cv.Events = bytes.Count(data, []byte("\n"))
	}
	if len(cv.Raw) == 0 {
		cv.EvidenceNote = "no FIX messages in this case's window"
	}
}

// Build loads a run directory into the report model.
func Build(dir string) (*Model, error) {
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return nil, err
	}
	var res cert.RunResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("results.json: %v", err)
	}
	m := &Model{Res: &res, Statuses: Statuses, CSS: template.CSS(web.CSS), JS: template.JS(reportJS)}
	for _, s := range Statuses {
		m.StatusClasses = append(m.StatusClasses, BadgeClass(s))
	}
	m.Verdict, m.VerdictReason = Verdict(&res)
	m.VerdictClass = BadgeClass(m.Verdict)
	m.AllCounts, m.RequiredCounts = counts(res.Counts), counts(res.Required)

	// Sections: the suite's order when recorded, else first appearance.
	names := map[string]string{}
	var order []string
	for _, s := range res.Sections {
		names[s.ID] = s.Name
		order = append(order, s.ID)
	}
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	for _, c := range res.Cases {
		if !seen[c.Section] {
			seen[c.Section] = true
			order = append(order, c.Section)
		}
	}
	for _, id := range order {
		row := SectionRow{ID: id, Name: dash(names[id]), Counts: make([]int, len(Statuses))}
		for _, c := range res.Cases {
			if c.Section != id {
				continue
			}
			row.Total++
			for i, s := range Statuses {
				if c.Status == s {
					row.Counts[i]++
				}
			}
		}
		if row.Total > 0 {
			m.Sections = append(m.Sections, row)
		}
	}

	for _, c := range res.Cases {
		cv := CaseView{CaseResult: c, Badge: BadgeClass(c.Status)}
		for _, st := range c.Steps {
			if st.Type == "expect" && strings.HasPrefix(st.Detail, "matched ") {
				cv.Expectations = append(cv.Expectations, strings.TrimPrefix(st.Detail, "matched "))
			}
		}
		readEvidence(dir, &cv)
		m.Cases = append(m.Cases, cv)
		if c.Status == cert.StatusNA {
			m.NA = append(m.NA, cv)
		}
		if c.Attestation != nil {
			m.Attested = append(m.Attested, cv)
		}
	}

	m.ResultsDigest = cert.ResultsDigest(dir)
	if res.Integrity != nil {
		m.Sealed = true
		paths := make([]string, 0, len(res.Integrity.Files))
		for p := range res.Integrity.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			m.Hashes = append(m.Hashes, FileHash{Path: p, SHA256: res.Integrity.Files[p]})
		}
	}
	return m, nil
}

// Render writes the model as HTML.
func Render(m *Model) ([]byte, error) {
	var b bytes.Buffer
	if err := page.Execute(&b, m); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Write builds and writes dir/report.html, returning its path.
func Write(dir string) (string, *Model, error) {
	m, err := Build(dir)
	if err != nil {
		return "", nil, err
	}
	html, err := Render(m)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, FileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, html, 0o644); err != nil {
		return "", nil, err
	}
	return path, m, os.Rename(tmp, path)
}

// idList shows up to 12 ids, then how many more.
func idList(ids []string) string {
	if len(ids) <= 12 {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s … (+%d more)", strings.Join(ids[:12], ", "), len(ids)-12)
}
