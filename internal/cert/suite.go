// Package cert is the certification runner: YAML suites made from a venue's
// checklist, adapted to a counterparty by a target file, executed case by
// case against one FIX session with evidence-backed per-case results.
//
// Pass/fail always comes from code: explicit expectations plus the eleven
// order checks. Cases a program cannot decide (environment, credentials,
// sign-off) are "manual" and take their result from a human attestation;
// cases that need the counterparty to act are "assisted" and use the
// counterparty's control API when the target has one.
package cert

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Modes.
const (
	ModeAuto     = "auto"
	ModeAssisted = "assisted"
	ModeManual   = "manual"
)

// Section is one checklist section.
type Section struct {
	ID   string
	Name string
}

// Suite is a loaded, validated suite.
type Suite struct {
	File       string
	Name       string // "suite:"
	Title      string
	Source     string
	FixVersion string
	Vars       map[string]string
	Sections   []Section
	Cases      []*Case
	Extends    string
}

// Case is one checklist row made executable.
type Case struct {
	ID       string
	Section  string
	Title    string
	Task     string
	Area     string
	Required bool
	Level    string
	Mode     string
	Steps    []*Step
}

// Step is one step; exactly one of the pointers is set.
type Step struct {
	Type    string // send | expect | session | control | assert_sent | assert_received | checks | manual | review
	Send    *SendStep
	Expect  *ExpectStep
	Session *SessionStep
	Control *ControlStep
	Assert  *AssertStep
	Checks  string // timeline | session_exec_ids
	Manual  *ManualStep
	Review  string // required_cases | deviations (A4: sign-off rows decided over the whole run)
}

// Review kinds. A review step looks at the other cases' results instead of
// the session, so the runner evaluates it after every other case, and again
// whenever an attestation changes a result.
const (
	// ReviewRequiredCases passes iff every other required case of the suite
	// is PASS or N/A (9.1).
	ReviewRequiredCases = "required_cases"
	// ReviewDeviations drafts the deviations (warnings and N/A reasons); a
	// human still attests it (9.2).
	ReviewDeviations = "deviations"
)

// SendStep sends D/F/G via the normal path, or raw fields via SendRaw.
type SendStep struct {
	Msg           string // D | F | G (normal path)
	Ref           string // name for the order (D) or the raw message
	Order         string // F/G: the order to cancel/replace
	Side          string
	OrdType       string
	Symbol        string
	Qty           string
	Price         string
	TIF           string
	Account       string
	AllowTerminal bool        // F: cancel even if the order looks done
	Raw           [][2]string // raw: [[tag, value], ...]
}

// Matcher describes an expected message.
type Matcher struct {
	Msg       []string
	ExecType  []string
	OrdStatus []string
	Side      string
	Tags      map[int][]string
	Present   []int
	Absent    []int
}

// ExpectStep waits for a message.
type ExpectStep struct {
	Matcher
	AnyOf    []Matcher
	From     string // counterparty (default) | us
	Order    string // order ref filter
	Within   string // duration, may use {{vars}}
	Optional bool
	None     bool // PASS if no such message arrives within the window
}

// SessionStep is a session action.
type SessionStep struct {
	Action        string // testreq | logout | reconnect | disconnect_abrupt | skip_outbound_seq | resend_request | resend_order | wait
	N             string
	Begin, End    string
	Wait          string
	Reset         bool
	RequireAnswer bool
	Order         string
	Within        string
}

// ControlStep is a counterparty-side action.
type ControlStep struct {
	Description string
	Method      string // POST (default) or GET
	Path        string // "" = no endpoint anywhere: always needs a human
	Body        *yaml.Node
}

// AssertStep checks tags on a message we sent or received.
type AssertStep struct {
	Order       string
	Msg         string
	Which       string // last (default) | first
	Tags        map[int][]string
	Present     []int
	Absent      []int
	Recommended []int // missing -> warning, not failure
}

// ManualStep is a prompt for a human.
type ManualStep struct {
	Prompt string
}

// ------------------------------------------------------------ errors

// Error is a suite/target/attestation problem, naming where.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errAt(file, where, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if where != "" {
		msg = where + ": " + msg
	}
	return &Error{Msg: fmt.Sprintf("%s: %s", file, msg)}
}

// ------------------------------------------------------------ YAML helpers

// node is a YAML value with scalars kept as their literal text, so 1.00
// stays "1.00" and 0100 stays "0100".
type node struct {
	n *yaml.Node
}

func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return resolve(n.Content[0])
	}
	return n
}

// mapping returns key -> value for a mapping node, applying "<<" merges.
func mapping(n *yaml.Node) (map[string]*yaml.Node, []string, bool) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	out := map[string]*yaml.Node{}
	var order []string
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := resolve(n.Content[i]), n.Content[i+1]
		if k.Value == "<<" {
			merges = append(merges, v)
			continue
		}
		if _, dup := out[k.Value]; !dup {
			order = append(order, k.Value)
		}
		out[k.Value] = v
	}
	for _, mv := range merges {
		mv = resolve(mv)
		srcs := []*yaml.Node{mv}
		if mv.Kind == yaml.SequenceNode {
			srcs = mv.Content
		}
		for _, src := range srcs {
			m, keys, ok := mapping(src)
			if !ok {
				continue
			}
			for _, k := range keys {
				if _, have := out[k]; !have {
					out[k] = m[k]
					order = append(order, k)
				}
			}
		}
	}
	return out, order, true
}

func scalar(n *yaml.Node) (string, bool) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

func seq(n *yaml.Node) ([]*yaml.Node, bool) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, false
	}
	return n.Content, true
}

// strList accepts a scalar or a list of scalars.
func strList(n *yaml.Node) ([]string, bool) {
	if s, ok := scalar(n); ok {
		return []string{s}, true
	}
	items, ok := seq(n)
	if !ok {
		return nil, false
	}
	var out []string
	for _, it := range items {
		s, ok := scalar(it)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func intList(n *yaml.Node) ([]int, bool) {
	ss, ok := strList(n)
	if !ok {
		return nil, false
	}
	var out []int
	for _, s := range ss {
		v, err := strconv.Atoi(s)
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func boolOf(n *yaml.Node) (bool, bool) {
	s, ok := scalar(n)
	if !ok {
		return false, false
	}
	switch strings.ToLower(s) {
	case "true", "yes", "y":
		return true, true
	case "false", "no", "n":
		return false, true
	}
	return false, false
}

func tagMap(n *yaml.Node) (map[int][]string, bool) {
	m, _, ok := mapping(n)
	if !ok {
		return nil, false
	}
	out := map[int][]string{}
	for k, v := range m {
		tag, err := strconv.Atoi(k)
		if err != nil {
			return nil, false
		}
		vals, ok := strList(v)
		if !ok {
			return nil, false
		}
		out[tag] = vals
	}
	return out, true
}

func unknownKeys(m map[string]*yaml.Node, allowed ...string) []string {
	var bad []string
	for k := range m {
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
			}
		}
		if !ok {
			bad = append(bad, k)
		}
	}
	sort.Strings(bad)
	return bad
}

// ------------------------------------------------------------ loading

func readYAML(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &Error{Msg: fmt.Sprintf("%s: %v", path, err)}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &Error{Msg: fmt.Sprintf("%s: invalid YAML: %v", path, err)}
	}
	return &doc, nil
}

var suiteKeys = []string{"suite", "title", "source", "fix_version", "vars", "sections", "cases", "extends"}

// LoadSuite reads and validates a suite file. A suite may extend another
// (extends: <file relative to this one>): it inherits the base's sections,
// cases and vars, overriding any top-level key it sets and merging vars.
func LoadSuite(path string) (*Suite, error) {
	return loadSuite(path, 0)
}

func loadSuite(path string, depth int) (*Suite, error) {
	if depth > 4 {
		return nil, &Error{Msg: path + ": extends chain too deep"}
	}
	doc, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	top, _, ok := mapping(doc)
	if !ok {
		return nil, errAt(path, "", "top level must be a mapping")
	}
	if bad := unknownKeys(top, suiteKeys...); len(bad) > 0 {
		return nil, errAt(path, "", "unknown key(s) %s (allowed: %s)", strings.Join(bad, ", "), strings.Join(suiteKeys, ", "))
	}
	s := &Suite{File: path, Vars: map[string]string{}}
	if ext, ok := top["extends"]; ok {
		name, ok := scalar(ext)
		if !ok {
			return nil, errAt(path, "", "'extends' must be a file name")
		}
		base, err := loadSuite(filepath.Join(filepath.Dir(path), name), depth+1)
		if err != nil {
			return nil, err
		}
		*s = *base
		s.File, s.Extends = path, name
		s.Vars = map[string]string{}
		for k, v := range base.Vars {
			s.Vars[k] = v
		}
	}
	for key, dst := range map[string]*string{"suite": &s.Name, "title": &s.Title, "source": &s.Source, "fix_version": &s.FixVersion} {
		if v, ok := top[key]; ok {
			str, ok := scalar(v)
			if !ok || str == "" {
				return nil, errAt(path, "", "'%s' must be a non-empty string", key)
			}
			*dst = str
		}
	}
	if v, ok := top["vars"]; ok {
		m, _, ok := mapping(v)
		if !ok {
			return nil, errAt(path, "", "'vars' must be a mapping")
		}
		for k, vn := range m {
			str, ok := scalar(vn)
			if !ok {
				return nil, errAt(path, "vars", "'%s' must be a scalar", k)
			}
			s.Vars[k] = str
		}
	}
	if v, ok := top["sections"]; ok {
		items, ok := seq(v)
		if !ok {
			return nil, errAt(path, "", "'sections' must be a list")
		}
		s.Sections = nil
		for i, it := range items {
			m, _, ok := mapping(it)
			id, ok1 := scalar(m["id"])
			name, ok2 := scalar(m["name"])
			if !ok || !ok1 || !ok2 {
				return nil, errAt(path, fmt.Sprintf("sections[%d]", i), "needs id and name")
			}
			s.Sections = append(s.Sections, Section{ID: id, Name: name})
		}
	}
	if v, ok := top["cases"]; ok {
		items, ok := seq(v)
		if !ok {
			return nil, errAt(path, "", "'cases' must be a list")
		}
		s.Cases = nil
		for i, it := range items {
			c, err := parseCase(path, i, it)
			if err != nil {
				return nil, err
			}
			s.Cases = append(s.Cases, c)
		}
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

var caseKeys = []string{"id", "section", "title", "task", "area", "required", "level", "mode", "steps"}

func parseCase(file string, index int, n *yaml.Node) (*Case, error) {
	m, _, ok := mapping(n)
	where := fmt.Sprintf("cases[%d]", index)
	if !ok {
		return nil, errAt(file, where, "must be a mapping")
	}
	c := &Case{}
	if id, ok := scalar(m["id"]); ok && id != "" {
		c.ID = id
		where = fmt.Sprintf("case %q", id)
	} else {
		return nil, errAt(file, where, "missing 'id'")
	}
	if bad := unknownKeys(m, caseKeys...); len(bad) > 0 {
		return nil, errAt(file, where, "unknown key(s) %s", strings.Join(bad, ", "))
	}
	for key, dst := range map[string]*string{"section": &c.Section, "title": &c.Title, "task": &c.Task, "area": &c.Area, "level": &c.Level, "mode": &c.Mode} {
		if v, ok := m[key]; ok {
			str, ok := scalar(v)
			if !ok {
				return nil, errAt(file, where, "'%s' must be a string", key)
			}
			*dst = str
		}
	}
	if v, ok := m["required"]; ok {
		b, ok := boolOf(v)
		if !ok {
			return nil, errAt(file, where, "'required' must be true or false")
		}
		c.Required = b
	}
	if v, ok := m["steps"]; ok {
		items, ok := seq(v)
		if !ok {
			return nil, errAt(file, where, "'steps' must be a list")
		}
		for i, it := range items {
			st, err := parseStep(file, fmt.Sprintf("%s step %d", where, i+1), it)
			if err != nil {
				return nil, err
			}
			c.Steps = append(c.Steps, st)
		}
	}
	return c, nil
}

var stepTypes = []string{"send", "expect", "session", "control", "assert_sent", "assert_received", "checks", "manual", "review"}

func parseStep(file, where string, n *yaml.Node) (*Step, error) {
	m, keys, ok := mapping(n)
	if !ok || len(keys) != 1 {
		return nil, errAt(file, where, "a step is a mapping with exactly one of: %s", strings.Join(stepTypes, ", "))
	}
	typ := keys[0]
	v := m[typ]
	st := &Step{Type: typ}
	var err error
	switch typ {
	case "send":
		st.Send, err = parseSend(file, where, v)
	case "expect":
		st.Expect, err = parseExpect(file, where, v)
	case "session":
		st.Session, err = parseSession(file, where, v)
	case "control":
		st.Control, err = parseControl(file, where, v)
	case "assert_sent", "assert_received":
		st.Assert, err = parseAssert(file, where, v)
	case "checks":
		s, ok := scalar(v)
		if !ok || (s != "timeline" && s != "session_exec_ids") {
			return nil, errAt(file, where, "checks must be 'timeline' or 'session_exec_ids'")
		}
		st.Checks = s
	case "manual":
		mm, _, ok := mapping(v)
		p, ok2 := scalar(mm["prompt"])
		if !ok || !ok2 || p == "" {
			return nil, errAt(file, where, "manual needs a 'prompt'")
		}
		st.Manual = &ManualStep{Prompt: p}
	case "review":
		r, ok := scalar(v)
		if !ok || (r != ReviewRequiredCases && r != ReviewDeviations) {
			return nil, errAt(file, where, "review must be '%s' or '%s'", ReviewRequiredCases, ReviewDeviations)
		}
		st.Review = r
	default:
		return nil, errAt(file, where, "unknown step type %q (known: %s)", typ, strings.Join(stepTypes, ", "))
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

func parseSend(file, where string, n *yaml.Node) (*SendStep, error) {
	m, _, ok := mapping(n)
	if !ok {
		return nil, errAt(file, where, "send must be a mapping")
	}
	if bad := unknownKeys(m, "msg", "ref", "order", "side", "ord_type", "symbol", "qty", "price", "tif", "account", "allow_terminal", "raw"); len(bad) > 0 {
		return nil, errAt(file, where, "send: unknown key(s) %s", strings.Join(bad, ", "))
	}
	s := &SendStep{}
	for key, dst := range map[string]*string{"msg": &s.Msg, "ref": &s.Ref, "order": &s.Order, "side": &s.Side, "ord_type": &s.OrdType,
		"symbol": &s.Symbol, "qty": &s.Qty, "price": &s.Price, "tif": &s.TIF, "account": &s.Account} {
		if v, ok := m[key]; ok {
			str, ok := scalar(v)
			if !ok {
				return nil, errAt(file, where, "send.%s must be a scalar", key)
			}
			*dst = str
		}
	}
	if v, ok := m["allow_terminal"]; ok {
		b, ok := boolOf(v)
		if !ok {
			return nil, errAt(file, where, "send.allow_terminal must be true or false")
		}
		s.AllowTerminal = b
	}
	if v, ok := m["raw"]; ok {
		items, ok := seq(v)
		if !ok || len(items) == 0 {
			return nil, errAt(file, where, "send.raw must be a list of [tag, value] pairs")
		}
		for _, it := range items {
			pair, ok := strList(it)
			if !ok || len(pair) != 2 {
				return nil, errAt(file, where, "send.raw entries must be [tag, value]")
			}
			if _, err := strconv.Atoi(pair[0]); err != nil {
				return nil, errAt(file, where, "send.raw: tag %q is not a number", pair[0])
			}
			s.Raw = append(s.Raw, [2]string{pair[0], pair[1]})
		}
		if s.Msg != "" {
			return nil, errAt(file, where, "send: use either msg or raw, not both")
		}
		return s, nil
	}
	switch s.Msg {
	case "D":
		if s.Side == "" || s.OrdType == "" || s.Symbol == "" || s.Qty == "" {
			return nil, errAt(file, where, "send D needs side, ord_type, symbol and qty")
		}
	case "F":
		if s.Order == "" {
			return nil, errAt(file, where, "send F needs order: <ref>")
		}
	case "G":
		if s.Order == "" || s.Qty == "" {
			return nil, errAt(file, where, "send G needs order: <ref> and qty")
		}
	default:
		return nil, errAt(file, where, "send.msg must be D, F or G (use raw for anything else), got %q", s.Msg)
	}
	return s, nil
}

var matcherKeys = []string{"msg", "exec_type", "ord_status", "side", "tags", "present", "absent"}

func parseMatcher(file, where string, m map[string]*yaml.Node) (Matcher, error) {
	var mt Matcher
	var ok bool
	if v, has := m["msg"]; has {
		if mt.Msg, ok = strList(v); !ok {
			return mt, errAt(file, where, "msg must be a MsgType or a list of them")
		}
	}
	if v, has := m["exec_type"]; has {
		if mt.ExecType, ok = strList(v); !ok {
			return mt, errAt(file, where, "exec_type must be a name or a list")
		}
		for _, name := range mt.ExecType {
			if _, known := execTypeNames[name]; !known {
				return mt, errAt(file, where, "unknown exec_type %q (known: %s)", name, strings.Join(sortedKeys(execTypeNames), ", "))
			}
		}
	}
	if v, has := m["ord_status"]; has {
		if mt.OrdStatus, ok = strList(v); !ok {
			return mt, errAt(file, where, "ord_status must be a name or a list")
		}
		for _, name := range mt.OrdStatus {
			if _, known := ordStatusNames[name]; !known {
				return mt, errAt(file, where, "unknown ord_status %q (known: %s)", name, strings.Join(sortedKeys(ordStatusNames), ", "))
			}
		}
	}
	if v, has := m["side"]; has {
		s, ok := scalar(v)
		if _, known := sideNames[s]; !ok || !known {
			return mt, errAt(file, where, "unknown side %q (known: %s)", s, strings.Join(sortedKeys(sideNames), ", "))
		}
		mt.Side = s
	}
	if v, has := m["tags"]; has {
		if mt.Tags, ok = tagMap(v); !ok {
			return mt, errAt(file, where, "tags must map tag numbers to a value or a list of values")
		}
	}
	if v, has := m["present"]; has {
		if mt.Present, ok = intList(v); !ok {
			return mt, errAt(file, where, "present must be a list of tag numbers")
		}
	}
	if v, has := m["absent"]; has {
		if mt.Absent, ok = intList(v); !ok {
			return mt, errAt(file, where, "absent must be a list of tag numbers")
		}
	}
	return mt, nil
}

func parseExpect(file, where string, n *yaml.Node) (*ExpectStep, error) {
	m, _, ok := mapping(n)
	if !ok {
		return nil, errAt(file, where, "expect must be a mapping")
	}
	allowed := append([]string{"any_of", "from", "order", "within", "optional", "none"}, matcherKeys...)
	if bad := unknownKeys(m, allowed...); len(bad) > 0 {
		return nil, errAt(file, where, "expect: unknown key(s) %s", strings.Join(bad, ", "))
	}
	mt, err := parseMatcher(file, where, m)
	if err != nil {
		return nil, err
	}
	e := &ExpectStep{Matcher: mt, From: "counterparty"}
	if v, has := m["any_of"]; has {
		items, ok := seq(v)
		if !ok || len(items) == 0 {
			return nil, errAt(file, where, "any_of must be a list of matchers")
		}
		for i, it := range items {
			im, _, ok := mapping(it)
			if !ok {
				return nil, errAt(file, where, "any_of[%d] must be a mapping", i)
			}
			if bad := unknownKeys(im, matcherKeys...); len(bad) > 0 {
				return nil, errAt(file, where, "any_of[%d]: unknown key(s) %s", i, strings.Join(bad, ", "))
			}
			alt, err := parseMatcher(file, fmt.Sprintf("%s any_of[%d]", where, i), im)
			if err != nil {
				return nil, err
			}
			e.AnyOf = append(e.AnyOf, alt)
		}
	}
	for key, dst := range map[string]*string{"from": &e.From, "order": &e.Order, "within": &e.Within} {
		if v, has := m[key]; has {
			s, ok := scalar(v)
			if !ok {
				return nil, errAt(file, where, "expect.%s must be a scalar", key)
			}
			*dst = s
		}
	}
	if e.From != "counterparty" && e.From != "us" {
		return nil, errAt(file, where, "expect.from must be counterparty or us")
	}
	for key, dst := range map[string]*bool{"optional": &e.Optional, "none": &e.None} {
		if v, has := m[key]; has {
			b, ok := boolOf(v)
			if !ok {
				return nil, errAt(file, where, "expect.%s must be true or false", key)
			}
			*dst = b
		}
	}
	if e.Within != "" && !strings.Contains(e.Within, "{{") {
		if _, err := ParseDuration(e.Within); err != nil {
			return nil, errAt(file, where, "expect.within: %v", err)
		}
	}
	return e, nil
}

var sessionActions = []string{"testreq", "logout", "reconnect", "disconnect_abrupt", "skip_outbound_seq", "resend_request", "resend_order", "wait"}

func parseSession(file, where string, n *yaml.Node) (*SessionStep, error) {
	s := &SessionStep{RequireAnswer: true}
	if a, ok := scalar(n); ok {
		s.Action = a
	} else {
		m, keys, ok := mapping(n)
		if !ok || len(keys) != 1 {
			return nil, errAt(file, where, "session must be an action name or a one-key mapping (%s)", strings.Join(sessionActions, ", "))
		}
		s.Action = keys[0]
		v := m[s.Action]
		switch s.Action {
		case "skip_outbound_seq":
			s.N, _ = scalar(v)
			if s.N == "" {
				return nil, errAt(file, where, "skip_outbound_seq needs a count")
			}
		case "wait":
			s.Wait, _ = scalar(v)
			if s.Wait == "" {
				return nil, errAt(file, where, "wait needs a duration")
			}
		case "resend_order":
			s.Order, _ = scalar(v)
			if s.Order == "" {
				return nil, errAt(file, where, "resend_order needs an order ref")
			}
		case "resend_request":
			mm, _, ok := mapping(v)
			b, ok1 := scalar(mm["begin"])
			e, ok2 := scalar(mm["end"])
			if !ok || !ok1 || !ok2 {
				return nil, errAt(file, where, "resend_request needs begin and end")
			}
			s.Begin, s.End = b, e
		case "reconnect":
			mm, _, ok := mapping(v)
			if !ok {
				return nil, errAt(file, where, "reconnect takes {reset: true|false}")
			}
			if r, has := mm["reset"]; has {
				b, ok := boolOf(r)
				if !ok {
					return nil, errAt(file, where, "reconnect.reset must be true or false")
				}
				s.Reset = b
			}
		case "testreq":
			mm, _, ok := mapping(v)
			if !ok {
				return nil, errAt(file, where, "testreq takes {require_answer: bool, within: dur}")
			}
			if r, has := mm["require_answer"]; has {
				b, ok := boolOf(r)
				if !ok {
					return nil, errAt(file, where, "testreq.require_answer must be true or false")
				}
				s.RequireAnswer = b
			}
			s.Within, _ = scalar(mm["within"])
		}
	}
	for _, a := range sessionActions {
		if a == s.Action {
			return s, nil
		}
	}
	return nil, errAt(file, where, "unknown session action %q (known: %s)", s.Action, strings.Join(sessionActions, ", "))
}

func parseControl(file, where string, n *yaml.Node) (*ControlStep, error) {
	m, _, ok := mapping(n)
	if !ok {
		return nil, errAt(file, where, "control must be a mapping")
	}
	if bad := unknownKeys(m, "description", "post", "get", "body"); len(bad) > 0 {
		return nil, errAt(file, where, "control: unknown key(s) %s", strings.Join(bad, ", "))
	}
	c := &ControlStep{Method: "POST"}
	c.Description, _ = scalar(m["description"])
	if c.Description == "" {
		return nil, errAt(file, where, "control needs a description (it is what a human is asked to do when there is no control API)")
	}
	if p, ok := scalar(m["post"]); ok {
		c.Path = p
	}
	if g, ok := scalar(m["get"]); ok {
		c.Path, c.Method = g, "GET"
	}
	if b, ok := m["body"]; ok {
		c.Body = b
	}
	return c, nil
}

func parseAssert(file, where string, n *yaml.Node) (*AssertStep, error) {
	m, _, ok := mapping(n)
	if !ok {
		return nil, errAt(file, where, "assert must be a mapping")
	}
	if bad := unknownKeys(m, "order", "msg", "which", "tags", "present", "absent", "recommended"); len(bad) > 0 {
		return nil, errAt(file, where, "assert: unknown key(s) %s", strings.Join(bad, ", "))
	}
	a := &AssertStep{Which: "last"}
	a.Order, _ = scalar(m["order"])
	a.Msg, _ = scalar(m["msg"])
	if w, ok := scalar(m["which"]); ok {
		if w != "first" && w != "last" {
			return nil, errAt(file, where, "assert.which must be first or last")
		}
		a.Which = w
	}
	if a.Msg == "" {
		return nil, errAt(file, where, "assert needs msg")
	}
	if v, has := m["tags"]; has {
		if a.Tags, ok = tagMap(v); !ok {
			return nil, errAt(file, where, "assert.tags must map tag numbers to values")
		}
	}
	for key, dst := range map[string]*[]int{"present": &a.Present, "absent": &a.Absent, "recommended": &a.Recommended} {
		if v, has := m[key]; has {
			if *dst, ok = intList(v); !ok {
				return nil, errAt(file, where, "assert.%s must be a list of tag numbers", key)
			}
		}
	}
	return a, nil
}

var caseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (s *Suite) validate() error {
	f := s.File
	if s.Name == "" || s.Title == "" || s.FixVersion == "" {
		return errAt(f, "", "suite, title and fix_version are required")
	}
	if len(s.Sections) == 0 || len(s.Cases) == 0 {
		return errAt(f, "", "sections and cases are required")
	}
	sections := map[string]bool{}
	for _, sec := range s.Sections {
		sections[sec.ID] = true
	}
	ids := map[string]bool{}
	for _, c := range s.Cases {
		where := fmt.Sprintf("case %q", c.ID)
		if !caseIDPattern.MatchString(c.ID) {
			return errAt(f, where, "id must be letters, digits, '.', '_' or '-'")
		}
		if ids[c.ID] {
			return errAt(f, where, "duplicate id")
		}
		ids[c.ID] = true
		if !sections[c.Section] {
			return errAt(f, where, "unknown section %q", c.Section)
		}
		if c.Title == "" || c.Task == "" {
			return errAt(f, where, "title and task are required")
		}
		switch c.Level {
		case "basic", "intermediate", "advanced":
		default:
			return errAt(f, where, "level must be basic, intermediate or advanced, got %q", c.Level)
		}
		if len(c.Steps) == 0 {
			return errAt(f, where, "no steps")
		}
		hasControl, hasManual, hasOther, review := false, false, false, ""
		refs := map[string]bool{}
		for i, st := range c.Steps {
			sw := fmt.Sprintf("%s step %d", where, i+1)
			switch st.Type {
			case "control":
				hasControl = true
			case "manual":
				hasManual = true
			case "review":
				review = st.Review
			default:
				hasOther = true
			}
			// Order refs must be defined before use.
			use := ""
			switch {
			case st.Send != nil && st.Send.Order != "":
				use = st.Send.Order
			case st.Expect != nil && st.Expect.Order != "":
				use = st.Expect.Order
			case st.Assert != nil && st.Assert.Order != "":
				use = st.Assert.Order
			case st.Session != nil && st.Session.Order != "":
				use = st.Session.Order
			}
			if use != "" && !refs[use] {
				return errAt(f, sw, "order %q is not defined by an earlier send", use)
			}
			if st.Send != nil && st.Send.Ref != "" {
				refs[st.Send.Ref] = true
			}
		}
		if review != "" {
			// A review is the whole case: it judges other cases' results.
			if len(c.Steps) != 1 {
				return errAt(f, where, "a review step must be the case's only step")
			}
			want := map[string]string{ReviewRequiredCases: ModeAuto, ReviewDeviations: ModeAssisted}[review]
			if c.Mode != want {
				return errAt(f, where, "review: %s belongs in a %s case, not %s", review, want, c.Mode)
			}
			continue
		}
		switch c.Mode {
		case ModeManual:
			if !hasManual || hasOther || hasControl {
				return errAt(f, where, "a manual case has only manual steps")
			}
		case ModeAssisted:
			if !hasControl || hasManual {
				return errAt(f, where, "an assisted case needs at least one control step and no manual steps")
			}
		case ModeAuto:
			if hasControl || hasManual {
				return errAt(f, where, "an auto case cannot have control or manual steps (make it assisted/manual)")
			}
		default:
			return errAt(f, where, "mode must be auto, assisted or manual, got %q", c.Mode)
		}
	}
	return nil
}

// ReviewOf returns the case's review kind ("" for an ordinary case).
func (c *Case) ReviewOf() string {
	if len(c.Steps) == 1 && c.Steps[0].Type == "review" {
		return c.Steps[0].Review
	}
	return ""
}

// Case returns the case with id.
func (s *Suite) Case(id string) *Case {
	for _, c := range s.Cases {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// SectionName returns a section's display name.
func (s *Suite) SectionName(id string) string {
	for _, sec := range s.Sections {
		if sec.ID == id {
			return sec.Name
		}
	}
	return id
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
