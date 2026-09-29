package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/danielgavin-code/OrderEcho/internal/checks"
	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
)

// Timeline exit codes, as the Python viewer's timeline command.
const (
	timelinePass = 0
	timelineWarn = 1
	timelineFail = 2
)

// expandPaths is expand_paths: glob patterns, files kept in the order given.
func expandPaths(patterns []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range patterns {
		matches, _ := filepath.Glob(p)
		if len(matches) == 0 {
			if _, err := os.Stat(p); err == nil {
				matches = []string{p}
			}
		}
		sort.Strings(matches)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// TimelineJSON is the --json output.
type TimelineJSON struct {
	Seed     string          `json:"seed"`
	ClOrdIDs []string        `json:"cl_ord_ids"`
	OrderIDs []string        `json:"order_ids"`
	Steps    int             `json:"steps"`
	Checks   []checks.Result `json:"checks"`
	Verdict  string          `json:"verdict"`
}

func cmdTimeline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("orderecho timeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	clOrdID := fs.String("clordid", "", "ClOrdID of any message in the chain")
	orderID := fs.String("order-id", "", "OrderID of the chain")
	asJSON := fs.Bool("json", false, "print JSON instead of the table")
	files, err := parseInterleaved(fs, args)
	if err != nil {
		return timelineFail
	}
	paths := expandPaths(files)
	if len(paths) == 0 {
		fmt.Fprintf(stderr, "error: no such file(s): %v\n", files)
		return timelineFail
	}
	if *clOrdID == "" && *orderID == "" {
		fmt.Fprintln(stderr, "error: timeline needs --clordid or --order-id")
		return timelineFail
	}
	var p checks.Parser
	messages := checks.MergeChronologically(p.ParseFiles(paths))
	chain := checks.BuildChain(messages, *clOrdID, *orderID)
	if *asJSON {
		out := TimelineJSON{Seed: chain.Seed, ClOrdIDs: chain.SortedIDs(), OrderIDs: chain.SortedOrderIDs(),
			Steps: len(chain.Steps), Checks: chain.Checks, Verdict: chain.Verdict()}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		printTimeline(stdout, chain, profile.FIX42)
	}
	if len(chain.Steps) == 0 {
		return timelineFail
	}
	return chain.ExitCode()
}
