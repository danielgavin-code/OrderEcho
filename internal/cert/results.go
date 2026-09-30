package cert

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

// statusOrder is how counts are listed.
var statusOrder = []string{StatusPass, StatusFail, StatusBlocked, StatusPending, StatusNA, StatusError, StatusNotRun}

// CountsLine renders counts as "PASS 40, FAIL 0, ...".
func CountsLine(counts map[string]int) string {
	var parts []string
	for _, s := range statusOrder {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, n))
		}
	}
	if len(parts) == 0 {
		return "no cases"
	}
	return strings.Join(parts, ", ")
}

// WriteSummary writes the human table.
func WriteSummary(w io.Writer, r *RunResult) {
	fmt.Fprintf(w, "%s — %s\n", r.Suite, r.Title)
	fmt.Fprintf(w, "target %s, session %s (%s), run %s, agent %s (%s)\n\n", r.Target, r.Session, r.FixVersion, r.RunID, r.Version, r.Build)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tREQ\tMODE\tSTATUS\tREASON")
	for _, c := range r.Cases {
		req := "opt"
		if c.Required {
			req = "req"
		}
		title := c.Title
		if len([]rune(title)) > 48 {
			title = string([]rune(title)[:47]) + "…"
		}
		reason := c.Reason
		if c.Status == StatusPass && len(c.Warnings) > 0 {
			reason = "warning: " + strings.Join(c.Warnings, "; ")
		}
		if len([]rune(reason)) > 150 {
			reason = string([]rune(reason)[:149]) + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, title, req, c.Mode, c.Status, reason)
	}
	tw.Flush()
	fmt.Fprintf(w, "\nall cases     : %s\n", CountsLine(r.Counts))
	fmt.Fprintf(w, "required cases: %s\n", CountsLine(r.Required))
	if r.SessionErr != "" {
		fmt.Fprintf(w, "session       : %s\n", r.SessionErr)
	}
	fmt.Fprintf(w, "exit code     : %d\n", r.Exit)
}

// WriteResults writes results.json, summary.txt and each case's slice of the
// FIX log and evidence into dir.
func WriteResults(dir string, r *RunResult) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "summary.txt"))
	if err != nil {
		return err
	}
	WriteSummary(f, r)
	f.Close()
	for _, c := range r.Cases {
		if !c.Ran {
			continue
		}
		cdir := filepath.Join(dir, c.ID)
		if err := os.MkdirAll(cdir, 0o755); err != nil {
			return err
		}
		if err := copySlice(c.startOff.FixPath, c.endOff.FixPath, c.startOff.FixOff, c.endOff.FixOff, filepath.Join(cdir, "fix.log")); err != nil {
			return err
		}
		if err := copySlice(c.startOff.EvPath, c.endOff.EvPath, c.startOff.EvOff, c.endOff.EvOff, filepath.Join(cdir, "evidence.jsonl")); err != nil {
			return err
		}
	}
	return nil
}

// copySlice copies bytes [from, to) of a file; if the file rolled over
// (UTC midnight) mid-case it copies the tail of the first and the head of
// the second.
func copySlice(pathA, pathB string, from, to int64, dst string) error {
	var out []byte
	read := func(path string, a, b int64) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if b < 0 || b > int64(len(data)) {
			b = int64(len(data))
		}
		if a > b {
			a = b
		}
		out = append(out, data[a:b]...)
	}
	if pathA == pathB {
		read(pathA, from, to)
	} else {
		read(pathA, from, -1)
		read(pathB, 0, to)
	}
	return os.WriteFile(dst, out, 0o644)
}

// Discover lists suites and targets under root/certs.
func Discover(root string) (suites, targets []string) {
	suites, _ = filepath.Glob(filepath.Join(root, "certs", "*.yaml"))
	targets, _ = filepath.Glob(filepath.Join(root, "certs", "targets", "*.yaml"))
	sort.Strings(suites)
	sort.Strings(targets)
	return suites, targets
}
