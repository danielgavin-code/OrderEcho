package cert

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

// ------------------------------------------------------ semantic names

// execTypeNames maps a suite's ExecType name to a predicate on a report,
// per FIX version. FIX 4.4 folded both fill kinds into 150=F (Trade), told
// apart by OrdStatus; 4.2 reports 150=1 / 150=2.
var execTypeNames = map[string]func(version string, m *codec.Message) bool{
	"NEW":             exact("0"),
	"PARTIAL_FILL":    fill("1", "1"),
	"FILL":            fill("2", "2"),
	"TRADE":           trade,
	"DONE_FOR_DAY":    exact("3"),
	"CANCELED":        exact("4"),
	"REPLACED":        exact("5"),
	"PENDING_CANCEL":  exact("6"),
	"STOPPED":         exact("7"),
	"REJECTED":        exact("8"),
	"SUSPENDED":       exact("9"),
	"PENDING_NEW":     exact("A"),
	"CALCULATED":      exact("B"),
	"EXPIRED":         exact("C"),
	"RESTATED":        exact("D"),
	"PENDING_REPLACE": exact("E"),
}

func exact(v string) func(string, *codec.Message) bool {
	return func(_ string, m *codec.Message) bool { return m.Value(150) == v }
}

// fill matches a 4.2 fill ExecType, or 4.4's 150=F with the OrdStatus that
// tells partial from full.
func fill(v42, status44 string) func(string, *codec.Message) bool {
	return func(version string, m *codec.Message) bool {
		et := m.Value(150)
		if version == "FIX.4.4" {
			return et == "F" && m.Value(39) == status44
		}
		return et == v42
	}
}

func trade(version string, m *codec.Message) bool {
	et := m.Value(150)
	if version == "FIX.4.4" {
		return et == "F"
	}
	return et == "1" || et == "2"
}

var ordStatusNames = map[string]string{
	"NEW": "0", "PARTIALLY_FILLED": "1", "FILLED": "2", "DONE_FOR_DAY": "3", "CANCELED": "4",
	"REPLACED": "5", "PENDING_CANCEL": "6", "STOPPED": "7", "REJECTED": "8", "SUSPENDED": "9",
	"PENDING_NEW": "A", "CALCULATED": "B", "EXPIRED": "C", "ACCEPTED_FOR_BIDDING": "D", "PENDING_REPLACE": "E",
}

var sideNames = map[string]string{"BUY": "1", "SELL": "2", "BUY_MINUS": "3", "SELL_PLUS": "4", "SELL_SHORT": "5", "SELL_SHORT_EXEMPT": "6"}

// ExecTypeMatches reports whether m's ExecType is the named one for version.
func ExecTypeMatches(name, version string, m *codec.Message) bool {
	f, ok := execTypeNames[name]
	return ok && f(version, m)
}

// OrdStatusValue returns the FIX value of an OrdStatus name.
func OrdStatusValue(name string) (string, bool) { v, ok := ordStatusNames[name]; return v, ok }

// ------------------------------------------------------------ durations

// ParseDuration accepts Go durations ("5s", "1m30s") and plain seconds.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	return d, nil
}

// ------------------------------------------------------------ variables

var varPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// Resolver supplies values for {{name}} placeholders that are not plain
// vars (built-ins such as {{uid}} or {{order.o1.root}}).
type Resolver func(name string) (string, bool)

// Substitute replaces every {{name}} in s from vars, then the resolver. An
// unknown name is an error naming it.
func Substitute(s string, vars map[string]string, extra Resolver) (string, error) {
	var missing []string
	out := varPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := varPattern.FindStringSubmatch(m)[1]
		if extra != nil {
			if v, ok := extra(name); ok {
				return v
			}
		}
		if v, ok := vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return out, fmt.Errorf("unknown variable(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// MergeVars layers maps: later ones win.
func MergeVars(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range layers {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}

// ------------------------------------------------------------ target

// Target adapts a suite to one counterparty.
type Target struct {
	File          string
	Name          string
	ControlAPI    string            // "" = no control API: control steps need a human
	Sessions      map[string]string // agent session id -> counterparty session id for {session}
	Vars          map[string]string
	NotApplicable map[string]string // case id -> reason
}

// LoadTarget reads a target file.
func LoadTarget(path string) (*Target, error) {
	doc, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	top, _, ok := mapping(doc)
	if !ok {
		return nil, errAt(path, "", "top level must be a mapping")
	}
	if bad := unknownKeys(top, "target", "control_api", "sessions", "vars", "not_applicable"); len(bad) > 0 {
		return nil, errAt(path, "", "unknown key(s) %s", strings.Join(bad, ", "))
	}
	t := &Target{File: path, Sessions: map[string]string{}, Vars: map[string]string{}, NotApplicable: map[string]string{}}
	if t.Name, ok = scalar(top["target"]); !ok || t.Name == "" {
		return nil, errAt(path, "", "'target' (a name) is required")
	}
	if v, has := top["control_api"]; has {
		t.ControlAPI, _ = scalar(v)
		if !strings.HasPrefix(t.ControlAPI, "http://") && !strings.HasPrefix(t.ControlAPI, "https://") {
			return nil, errAt(path, "", "control_api must be an http(s) URL, got %q", t.ControlAPI)
		}
	}
	for key, dst := range map[string]map[string]string{"sessions": t.Sessions, "vars": t.Vars, "not_applicable": t.NotApplicable} {
		v, has := top[key]
		if !has {
			continue
		}
		m, _, ok := mapping(v)
		if !ok {
			return nil, errAt(path, "", "'%s' must be a mapping", key)
		}
		for k, vn := range m {
			s, ok := scalar(vn)
			if !ok {
				return nil, errAt(path, key, "'%s' must be a scalar", k)
			}
			dst[k] = s
		}
	}
	return t, nil
}

// ------------------------------------------------------------ attestation

// Attestation is a human's statement about one case, recorded verbatim.
type Attestation struct {
	Status string `json:"status"` // pass | fail | na
	By     string `json:"by"`
	Note   string `json:"note"`
	At     string `json:"at,omitempty"`
	File   string `json:"file"`
}

// LoadAttestations reads step -> {status, by, note[, at]}.
func LoadAttestations(path string) (map[string]Attestation, error) {
	doc, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	top, _, ok := mapping(doc)
	if !ok {
		return nil, errAt(path, "", "top level must map case ids to {status, by, note}")
	}
	out := map[string]Attestation{}
	for id, n := range top {
		m, _, ok := mapping(n)
		if !ok {
			return nil, errAt(path, fmt.Sprintf("%q", id), "must be a mapping")
		}
		if bad := unknownKeys(m, "status", "by", "note", "at"); len(bad) > 0 {
			return nil, errAt(path, fmt.Sprintf("%q", id), "unknown key(s) %s", strings.Join(bad, ", "))
		}
		a := Attestation{File: filepath.Base(path)}
		a.Status, _ = scalar(m["status"])
		a.By, _ = scalar(m["by"])
		a.Note, _ = scalar(m["note"])
		a.At, _ = scalar(m["at"])
		a.Status = strings.ToLower(a.Status)
		if a.Status != "pass" && a.Status != "fail" && a.Status != "na" {
			return nil, errAt(path, fmt.Sprintf("%q", id), "status must be pass, fail or na, got %q", a.Status)
		}
		if strings.TrimSpace(a.By) == "" {
			return nil, errAt(path, fmt.Sprintf("%q", id), "'by' (who attests) is required")
		}
		out[id] = a
	}
	return out, nil
}

// fileExists is a small helper for the CLI.
func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
