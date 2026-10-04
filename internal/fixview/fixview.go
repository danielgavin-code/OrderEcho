// Package fixview renders FIX messages for people: the decoded one-line
// view (message name plus the fields that matter for its type) and the
// full field-by-field decode with tag names and enum meanings. The service's
// tools, the GUI's live feed and the certification report all use it, so a
// message reads the same everywhere.
package fixview

import (
	"fmt"
	"strings"

	"github.com/danielgavin-code/OrderEcho/internal/fix/profile"
)

// Field is one decoded field.
type Field struct {
	Tag     int    `json:"tag"`
	Name    string `json:"name,omitempty"`
	Value   string `json:"value"`
	Meaning string `json:"meaning,omitempty"`
}

// Getter reads a tag's first value.
type Getter func(tag int) (string, bool)

// keyTags are the fields the one-line view shows, per MsgType.
var keyTags = map[string][]int{
	"8": {11, 41, 37, 150, 39, 55, 32, 31, 14, 151, 6, 103, 58},
	"D": {11, 55, 54, 38, 40, 44, 59},
	"F": {11, 41, 55, 54, 38},
	"G": {11, 41, 55, 54, 38, 40, 44},
	"9": {11, 41, 37, 39, 434, 102, 58},
	"3": {45, 371, 372, 373, 58},
	"j": {45, 372, 379, 380, 58},
	"0": {112},
	"1": {112},
	"A": {108, 141},
	"2": {7, 16},
	"4": {123, 36},
	"5": {58},
}

// Line is the one-line view: "ExecutionReport ClOrdID=… ExecType=2(Fill) …".
func Line(p *profile.Profile, msgType string, get Getter) string {
	var b strings.Builder
	b.WriteString(p.MsgTypeName(msgType))
	for _, tag := range keyTags[msgType] {
		v, ok := get(tag)
		if !ok {
			continue
		}
		name := profile.TagName(tag)
		if name == "" {
			name = fmt.Sprint(tag)
		}
		if vn := p.ValueName(tag, v); vn != "" {
			v += "(" + vn + ")"
		}
		fmt.Fprintf(&b, " %s=%s", name, v)
	}
	return b.String()
}

// Flags are the badges a message earns from its own fields.
func Flags(get Getter) []string {
	var out []string
	if v, _ := get(43); v == "Y" {
		out = append(out, "POSSDUP")
	}
	if v, _ := get(97); v == "Y" {
		out = append(out, "POSSRESEND")
	}
	return out
}

// Decode names every field (BodyLength and CheckSum, pure framing, left out).
func Decode(p *profile.Profile, pairs [][2]string) []Field {
	out := make([]Field, 0, len(pairs))
	for _, kv := range pairs {
		var tag int
		if _, err := fmt.Sscan(kv[0], &tag); err != nil {
			continue
		}
		if tag == 9 || tag == 10 {
			continue
		}
		out = append(out, Field{Tag: tag, Name: profile.TagName(tag), Value: kv[1], Meaning: p.ValueName(tag, kv[1])})
	}
	return out
}

// Pairs splits a "|"- or SOH-delimited raw message into tag/value pairs.
func Pairs(raw string) [][2]string {
	raw = strings.ReplaceAll(raw, "\x01", "|")
	var out [][2]string
	for _, part := range strings.Split(raw, "|") {
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out = append(out, [2]string{k, v})
	}
	return out
}

// GetterOf reads from pairs.
func GetterOf(pairs [][2]string) Getter {
	return func(tag int) (string, bool) {
		t := fmt.Sprint(tag)
		for _, kv := range pairs {
			if kv[0] == t {
				return kv[1], true
			}
		}
		return "", false
	}
}

// ProfileFor returns the version's profile, FIX 4.2 when unknown.
func ProfileFor(beginString string) *profile.Profile {
	if p, err := profile.For(beginString); err == nil {
		return p
	}
	return profile.FIX42
}
