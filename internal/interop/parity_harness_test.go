//go:build interop

package interop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
)

// parityScript runs the emulator's own Python checks: it imports
// orderecho_LogParse / orderecho_Timeline from ../OrderEchoFixEmulator, reads
// a JSON list of {files, clordid, order_id} on stdin and prints one
// {verdict, steps, cl_ord_ids, checks:[{name,status}]} per case.
const parityScript = `
import json, sys
sys.path.insert(0, sys.argv[1])
from orderecho_LogParse import LogParser, merge_chronologically
from orderecho_Timeline import build_chain
out = []
for case in json.load(sys.stdin):
    parser = LogParser()
    messages = merge_chronologically(parser.parse_files(case["files"]))
    chain = build_chain(messages, cl_ord_id=case.get("clordid") or None,
                        order_id=case.get("order_id") or None)
    out.append({"verdict": chain.verdict, "steps": len(chain.steps),
                "cl_ord_ids": sorted(chain.ids),
                "checks": [{"name": c.name, "status": c.status} for c in chain.checks]})
print(json.dumps(out))
`

type parityCase struct {
	Name    string   `json:"name"`
	Files   []string `json:"files"`
	ClOrdID string   `json:"clordid,omitempty"`
	OrderID string   `json:"order_id,omitempty"`
	// Divergence, when set, is a documented, expected difference: Go and
	// Python must disagree exactly on this check, with these statuses.
	Divergence *divergence `json:"-"`
}

type divergence struct {
	Check, Go, Py, Why string
}

type checkStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type parityResult struct {
	Verdict  string        `json:"verdict"`
	Steps    int           `json:"steps"`
	ClOrdIDs []string      `json:"cl_ord_ids"`
	Checks   []checkStatus `json:"checks"`
}

func (r parityResult) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s steps=%d ids=%s", r.Verdict, r.Steps, strings.Join(r.ClOrdIDs, ","))
	for _, c := range r.Checks {
		fmt.Fprintf(&b, " %s=%s", c.Name, c.Status)
	}
	return b.String()
}

func (r parityResult) nonPass() string {
	var bad []string
	for _, c := range r.Checks {
		if c.Status != checks.PASS {
			bad = append(bad, c.Name+"="+c.Status)
		}
	}
	if len(bad) == 0 {
		return "all PASS"
	}
	return strings.Join(bad, " ")
}

// parityLog collects every compared case for the summary TestMain prints.
var parityLog struct {
	sync.Mutex
	lines []string
	same  int
	diff  int
	divs  int
}

func goParity(c parityCase) parityResult {
	var p checks.Parser
	chain := checks.BuildChain(checks.MergeChronologically(p.ParseFiles(c.Files)), c.ClOrdID, c.OrderID)
	r := parityResult{Verdict: chain.Verdict(), Steps: len(chain.Steps), ClOrdIDs: chain.SortedIDs()}
	for _, x := range chain.Checks {
		r.Checks = append(r.Checks, checkStatus{x.Name, x.Status})
	}
	return r
}

func pythonParity(t *testing.T, cases []parityCase) []parityResult {
	t.Helper()
	input, _ := json.Marshal(cases)
	cmd := exec.Command(pythonBin, "-c", parityScript, emulatorDir)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python parity: %v\n%s", err, stderr.String())
	}
	var res []parityResult
	if err := json.Unmarshal(out, &res); err != nil || len(res) != len(cases) {
		t.Fatalf("python parity output: %v\n%s", err, out)
	}
	return res
}

// assertParity requires Go and Python to agree on verdict, step count,
// ClOrdIDs and every check's status, for every case.
func assertParity(t *testing.T, cases []parityCase) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatal("no parity cases")
	}
	py := pythonParity(t, cases)
	for i, c := range cases {
		g := goParity(c)
		if d := c.Divergence; d != nil {
			gs, ps := statusOf(g, d.Check), statusOf(py[i], d.Check)
			line := fmt.Sprintf("%-70s go=%s py=%s [%s: go %s, py %s] (documented, expected: %s)", c.Name, g.Verdict, py[i].Verdict, d.Check, gs, ps, d.Why)
			parityLog.Lock()
			parityLog.lines = append(parityLog.lines, "  DIVERGE  "+line)
			parityLog.divs++
			parityLog.Unlock()
			if gs != d.Go || ps != d.Py {
				t.Errorf("divergence %s: go %s=%s (want %s), python %s=%s (want %s)", c.Name, d.Check, gs, d.Go, d.Check, ps, d.Py)
			}
			continue
		}
		ok := g.key() == py[i].key()
		line := fmt.Sprintf("%-70s go=%s py=%s [%s]", c.Name, g.Verdict, py[i].Verdict, g.nonPass())
		parityLog.Lock()
		if ok {
			parityLog.same++
			parityLog.lines = append(parityLog.lines, "  AGREE    "+line)
		} else {
			parityLog.diff++
			parityLog.lines = append(parityLog.lines, "  DISAGREE "+line)
		}
		parityLog.Unlock()
		if !ok {
			t.Errorf("parity %s:\n  go: %s\n  py: %s", c.Name, g.key(), py[i].key())
		} else {
			t.Logf("parity %s: %s (%s)", c.Name, g.Verdict, g.nonPass())
		}
	}
}

// casesFor builds parity cases for every root on the agent's and the
// emulator's logs and evidence.
func casesFor(scenario string, a *agent, e *emulator, emuSession string, roots []string) []parityCase {
	files := map[string][]string{
		"agent FIX log":     {a.fixLogPath()},
		"agent evidence":    {a.ev.Path},
		"emulator FIX log":  globAll(filepath.Join(e.dir, "logs", "fix", emuSession+"_*.log")),
		"emulator evidence": globAll(filepath.Join(e.dir, "data", "evidence", "*.jsonl")),
	}
	kinds := make([]string, 0, len(files))
	for k := range files {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var out []parityCase
	for _, root := range roots {
		for _, k := range kinds {
			out = append(out, parityCase{Name: fmt.Sprintf("%s / %s / %s", scenario, k, root), Files: files[k], ClOrdID: root})
		}
	}
	return out
}

func printParitySummary() {
	parityLog.Lock()
	defer parityLog.Unlock()
	if parityLog.same+parityLog.diff == 0 {
		return
	}
	fmt.Printf("\nPARITY SUMMARY: %d case(s) compared, %d agree, %d disagree, %d documented divergence(s) as expected\n", parityLog.same+parityLog.diff, parityLog.same, parityLog.diff, parityLog.divs)
	for _, l := range parityLog.lines {
		fmt.Println(l)
	}
	verdictLog.Lock()
	defer verdictLog.Unlock()
	fmt.Printf("\nSCENARIO VERDICTS (Go, live):\n")
	for _, l := range verdictLog.lines {
		fmt.Println(l)
	}
}

var verdictLog struct {
	sync.Mutex
	lines []string
}

func recordVerdict(format string, args ...any) {
	verdictLog.Lock()
	defer verdictLog.Unlock()
	verdictLog.lines = append(verdictLog.lines, "  "+fmt.Sprintf(format, args...))
}
