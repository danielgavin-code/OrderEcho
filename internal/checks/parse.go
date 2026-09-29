package checks

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// orderechoLine is the OrderEcho FIX log line (the emulator's and ours):
//
//	20260927-08:54:19.100 IN   seq=1    35=A  8=FIX.4.2|...|10=070|  # comment
var orderechoLine = regexp.MustCompile(`^(\d{8}-\d{2}:\d{2}:\d{2}\.\d{3})\s+(IN|OUT|DISC)\s+(seq=\S+|-)\s+(35=\S+|-)\s+(.*)$`)

// quickfixPrefix and isoPrefix are the timestamp shapes of generic logs.
var (
	quickfixPrefix = regexp.MustCompile(`^(\d{8}-\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?)\s*:\s*(.*)$`)
	isoPrefix      = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?Z?)[\s,:|-]*(.*)$`)
	fixLogName     = regexp.MustCompile(`^(.+)_(\d{8})\.log$`)
)

// Stats is what the parser saw, including what it could not use.
type Stats struct {
	Messages     int
	SkippedLines int
	BadChecksum  int
	BadLength    int
	Events       int
	Files        []string
}

// Parser turns log lines into Messages; it never fails on bad input.
type Parser struct {
	Stats Stats
	index int
}

// ParseTimestamp is parse_timestamp: the handful of shapes logs use, UTC.
func ParseTimestamp(text string) *time.Time {
	t := strings.TrimSpace(text)
	t = strings.TrimSpace(strings.TrimRight(t, ":"))
	if t == "" {
		return nil
	}
	t = strings.TrimSuffix(t, "Z")
	for _, layout := range []string{"20060102-15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, t); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
		dot := strings.LastIndexByte(t, '.')
		if dot < 0 {
			continue
		}
		frac := t[dot+1:]
		if frac == "" || len(frac) > 6 || strings.Trim(frac, "0123456789") != "" {
			continue
		}
		parsed, err := time.Parse(layout, t[:dot])
		if err != nil {
			continue
		}
		us := 0
		for i := 0; i < 6; i++ {
			us *= 10
			if i < len(frac) {
				us += int(frac[i] - '0')
			}
		}
		parsed = parsed.Add(time.Duration(us) * time.Microsecond).UTC()
		return &parsed
	}
	return nil
}

// SessionFromFilename is session_from_filename: agent44_20260927.log -> agent44.
func SessionFromFilename(path string) (string, bool) {
	m := fixLogName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return "", false
	}
	return m[1], true
}

func (p *Parser) build(raw, delimiter string, m Message) *Message {
	normalized := Normalize(raw, delimiter)
	m.Raw = normalized
	m.Fields = SplitFields(normalized)
	m.BadLength, m.BadChecksum = VerifyFraming(normalized)
	if m.Direction == "" {
		m.Direction = DirUnknown
	}
	p.index++
	m.Index = p.index
	p.Stats.Messages++
	if m.BadChecksum {
		p.Stats.BadChecksum++
	}
	if m.BadLength {
		p.Stats.BadLength++
	}
	return &m
}

// ParseLine returns every message on one line.
func (p *Parser) ParseLine(line string, lineNo int, source, session string, hasSession bool) []*Message {
	text := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// 1. Our own evidence JSONL.
	stripped := strings.TrimLeft(text, " \t")
	if strings.HasPrefix(stripped, "{") {
		if out, ok := p.parseEvidence(stripped, lineNo, source); ok {
			return out
		}
	}
	// 2. Our own FIX log line.
	if m := orderechoLine.FindStringSubmatch(text); m != nil {
		if out, ok := p.parseOrderEcho(m, lineNo, source, session, hasSession); ok {
			return out
		}
	}
	// 3. Anything else with FIX in it.
	return p.parseGeneric(text, lineNo, source, session, hasSession)
}

func (p *Parser) parseEvidence(text string, lineNo int, source string) ([]*Message, bool) {
	var rec map[string]any
	if err := json.Unmarshal([]byte(text), &rec); err != nil || rec == nil {
		return nil, false
	}
	kind, hasKind := rec["kind"]
	if !hasKind {
		return nil, false
	}
	if kind == "event" {
		p.Stats.Events++
		return []*Message{}, true
	}
	raw, _ := rec["raw"].(string)
	if raw == "" {
		p.Stats.Events++
		return []*Message{}, true
	}
	dir := DirUnknown
	switch kind {
	case "in":
		dir = DirIn
	case "out":
		dir = DirOut
	case "discarded":
		dir = DirDisc
	}
	delim := "|"
	if strings.Contains(raw, SOH) {
		delim = SOH
	}
	m := Message{Direction: dir, Source: source, SourceLineNo: lineNo}
	if ts, ok := rec["ts"].(string); ok {
		m.TS = ParseTimestamp(ts)
	}
	if s, ok := rec["session"].(string); ok {
		m.Session, m.HasSession = s, true
	}
	if inj, ok := rec["injected"].(bool); ok {
		m.Injected = inj
	}
	if d, ok := rec["detail"].(string); ok {
		m.Comment = d
	}
	return []*Message{p.build(raw, delim, m)}, true
}

func (p *Parser) parseOrderEcho(match []string, lineNo int, source, session string, hasSession bool) ([]*Message, bool) {
	rest := match[5]
	comment := ""
	if i := strings.Index(rest, "  # "); i >= 0 {
		rest, comment = rest[:i], strings.TrimSpace(rest[i+4:])
	}
	found := FindMessages(rest)
	if len(found) == 0 {
		return nil, false
	}
	dir := map[string]string{"IN": DirIn, "OUT": DirOut, "DISC": DirDisc}[match[2]]
	ts := ParseTimestamp(match[1])
	var out []*Message
	for _, f := range found {
		out = append(out, p.build(f.Raw, f.Delim, Message{
			TS: ts, Direction: dir, Session: session, HasSession: hasSession,
			Injected: strings.HasPrefix(comment, "injected:"), Comment: comment,
			Source: source, SourceLineNo: lineNo,
		}))
	}
	return out, true
}

func (p *Parser) parseGeneric(text string, lineNo int, source, session string, hasSession bool) []*Message {
	found := FindMessages(text)
	if len(found) == 0 {
		p.Stats.SkippedLines++
		return nil
	}
	prefix := text[:strings.Index(text, found[0].Raw)]
	target := prefix
	if strings.TrimSpace(prefix) == "" {
		target = text
	}
	var ts *time.Time
	for _, re := range []*regexp.Regexp{quickfixPrefix, isoPrefix} {
		if m := re.FindStringSubmatch(target); m != nil {
			if ts = ParseTimestamp(m[1]); ts != nil {
				break
			}
		}
	}
	var out []*Message
	for _, f := range found {
		out = append(out, p.build(f.Raw, f.Delim, Message{
			TS: ts, Direction: DirUnknown, Session: session, HasSession: hasSession,
			Source: source, SourceLineNo: lineNo,
		}))
	}
	return out
}

// ParseLines parses a stream of lines.
func (p *Parser) ParseLines(lines []string, source, session string, hasSession bool) []*Message {
	var out []*Message
	for i, line := range lines {
		out = append(out, p.ParseLine(line, i+1, source, session, hasSession)...)
	}
	return out
}

// ParseFile parses one file; the session is taken from an OrderEcho FIX log
// file name. A file that cannot be read yields nothing (as in Python).
func (p *Parser) ParseFile(path string) []*Message {
	session, hasSession := SessionFromFilename(path)
	name := filepath.Base(path)
	seen := false
	for _, f := range p.Stats.Files {
		if f == name {
			seen = true
		}
	}
	if !seen {
		p.Stats.Files = append(p.Stats.Files, name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []*Message
	r := bufio.NewReader(f)
	lineNo := 0
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			lineNo++
			out = append(out, p.ParseLine(strings.ToValidUTF8(line, "�"), lineNo, name, session, hasSession)...)
		}
		if err != nil {
			break
		}
	}
	return out
}

// ParseFiles parses several files in order.
func (p *Parser) ParseFiles(paths []string) []*Message {
	var out []*Message
	for _, path := range paths {
		out = append(out, p.ParseFile(path)...)
	}
	return out
}

// MergeChronologically is merge_chronologically: orders messages from several
// files by time without shuffling a file. An unstamped line keeps the last
// stamp before it in its own file; an unstamped run at a file's head inherits
// the file's first stamp; a file with no stamps at all goes last. Stable.
func MergeChronologically(messages []*Message) []*Message {
	firstSeen := map[string]*time.Time{}
	for _, m := range messages {
		if m.TS != nil {
			if _, ok := firstSeen[m.Source]; !ok {
				firstSeen[m.Source] = m.TS
			}
		}
	}
	keys := make([]*time.Time, len(messages))
	carried := map[string]*time.Time{}
	for i, m := range messages {
		if m.TS != nil {
			carried[m.Source] = m.TS
		}
		if k, ok := carried[m.Source]; ok {
			keys[i] = k
		} else {
			keys[i] = firstSeen[m.Source]
		}
	}
	order := make([]int, len(messages))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ka, kb := keys[order[a]], keys[order[b]]
		if (ka == nil) != (kb == nil) {
			return kb == nil
		}
		if ka == nil {
			return false
		}
		return ka.Before(*kb)
	})
	out := make([]*Message, len(messages))
	for i, idx := range order {
		out[i] = messages[idx]
	}
	return out
}
