package codec

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sample() []byte {
	return Encode("FIX.4.2", "1", Header{
		SenderCompID: "AGENT", TargetCompID: "ORDERECHO", MsgSeqNum: 5,
		SendingTime: "20260926-14:03:22.110",
	}, []Field{F(112, "TEST-1")})
}

func TestEncodeKnownMessage(t *testing.T) {
	// Hand-computed: body "35=1|49=AGENT|56=ORDERECHO|34=5|52=20260926-14:03:22.110|112=TEST-1|" is 68 bytes.
	body := "35=1\x0149=AGENT\x0156=ORDERECHO\x0134=5\x0152=20260926-14:03:22.110\x01112=TEST-1\x01"
	head := "8=FIX.4.2\x019=" + strconv.Itoa(len(body)) + "\x01"
	sum := 0
	for _, b := range []byte(head + body) {
		sum += int(b)
	}
	want := head + body + fmt.Sprintf("10=%03d\x01", sum%256)
	if got := string(sample()); got != want {
		t.Fatalf("encode:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(want, "\x019=68\x01") {
		t.Fatalf("body length not 68: %q", want)
	}
}

func TestHeaderOrderWithPossDup(t *testing.T) {
	raw := Encode("FIX.4.4", "4", Header{
		SenderCompID: "A", TargetCompID: "B", MsgSeqNum: 3, SendingTime: "S",
		PossDup: true, OrigSendingTime: "O",
	}, []Field{F(123, "Y"), F(36, "7")})
	msg, err := DecodeOne(raw)
	if err != nil {
		t.Fatal(err)
	}
	var tags []int
	for _, f := range msg.Fields {
		tags = append(tags, f.Tag)
	}
	want := []int{8, 9, 35, 49, 56, 34, 52, 43, 122, 123, 36, 10}
	if len(tags) != len(want) {
		t.Fatalf("tags %v want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("tags %v want %v", tags, want)
		}
	}
}

func TestRoundTripPreservesOrderAndRepeats(t *testing.T) {
	body := []Field{F(453, "2"), F(448, "X"), F(447, "D"), F(448, "Y"), F(447, "D"), F(58, "a=b")}
	raw := Encode("FIX.4.4", "D", Header{SenderCompID: "S", TargetCompID: "T", MsgSeqNum: 9, SendingTime: "20260101-00:00:00.000"}, body)
	msg, err := DecodeOne(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msg.Raw, raw) {
		t.Fatal("raw not preserved")
	}
	if v, _ := msg.GetNth(448, 2); v != "Y" {
		t.Fatalf("second 448 = %q", v)
	}
	if msg.Value(58) != "a=b" {
		t.Fatalf("58 = %q", msg.Value(58))
	}
	if !bytes.Equal(Rebuild(msg), raw) {
		t.Fatal("rebuild differs")
	}
	if seq, ok := msg.SeqNum(); !ok || seq != 9 {
		t.Fatalf("seq %d %v", seq, ok)
	}
}

func TestBadChecksumDiscarded(t *testing.T) {
	raw := sample()
	bad := append([]byte{}, raw[:len(raw)-4]...)
	bad = append(bad, []byte("000\x01")...)
	if bytes.Equal(bad, raw) {
		t.Skip("checksum happened to be 000")
	}
	var d Decoder
	items := d.Decode(bad)
	if len(items) != 1 {
		t.Fatalf("items %d", len(items))
	}
	df, ok := items[0].(*DiscardedFrame)
	if !ok {
		t.Fatalf("got %T", items[0])
	}
	if !strings.Contains(df.Reason, "Bad CheckSum") || !bytes.Equal(df.Raw, bad) {
		t.Fatalf("reason %q", df.Reason)
	}
}

func TestBadBodyLengthDiscarded(t *testing.T) {
	raw := string(sample())
	for _, declared := range []string{"9=50", "9=75"} {
		bad := strings.Replace(raw, "9=68", declared, 1)
		var d Decoder
		items := d.Decode([]byte(bad))
		// A following good message must still be decoded.
		items = append(items, d.Decode(sample())...)
		if len(items) != 2 {
			t.Fatalf("%s: items %d", declared, len(items))
		}
		df, ok := items[0].(*DiscardedFrame)
		if !ok || !strings.Contains(df.Reason, "Bad BodyLength") {
			t.Fatalf("%s: got %#v", declared, items[0])
		}
		if _, ok := items[1].(*Message); !ok {
			t.Fatalf("%s: second item %T", declared, items[1])
		}
	}
}

func TestFramingErrors(t *testing.T) {
	cases := map[string]string{
		"junk before":      "garbage" + string(sample()),
		"no 9 second":      "8=FIX.4.2\x0135=0\x01" + string(sample()),
		"9 not a number":   "8=FIX.4.2\x019=xx\x0135=0\x01" + string(sample()),
		"checksum not num": strings.Replace(string(sample()), "10=", "10=a", 1)[:len(sample())] + string(sample()),
	}
	for name, input := range cases {
		var d Decoder
		items := d.Decode([]byte(input))
		if len(items) < 2 {
			t.Fatalf("%s: items %d", name, len(items))
		}
		if _, ok := items[0].(*DiscardedFrame); !ok {
			t.Fatalf("%s: first item %T", name, items[0])
		}
		if _, ok := items[len(items)-1].(*Message); !ok {
			t.Fatalf("%s: last item %T", name, items[len(items)-1])
		}
	}
}

func TestTwoMessagesOneChunk(t *testing.T) {
	var d Decoder
	items := d.Decode(append(sample(), sample()...))
	if len(items) != 2 {
		t.Fatalf("items %d", len(items))
	}
}

func TestSplitAcrossChunks(t *testing.T) {
	raw := sample()
	var d Decoder
	a, b := len(raw)/3, 2*len(raw)/3
	if n := len(d.Decode(raw[:a])); n != 0 {
		t.Fatalf("chunk1 items %d", n)
	}
	if n := len(d.Decode(raw[a:b])); n != 0 {
		t.Fatalf("chunk2 items %d", n)
	}
	items := d.Decode(raw[b:])
	if len(items) != 1 {
		t.Fatalf("chunk3 items %d", len(items))
	}
	if _, ok := items[0].(*Message); !ok {
		t.Fatalf("got %T", items[0])
	}
	if len(d.Buffered()) != 0 {
		t.Fatal("leftover bytes")
	}
}

func TestByteAtATime(t *testing.T) {
	raw := append(sample(), sample()...)
	var d Decoder
	count := 0
	for i := range raw {
		count += len(d.Decode(raw[i : i+1]))
	}
	if count != 2 {
		t.Fatalf("decoded %d", count)
	}
}

func TestOtherBeginStringStillDecoded(t *testing.T) {
	raw := Encode("FIX.4.4", "5", Header{SenderCompID: "A", TargetCompID: "B", MsgSeqNum: 1, SendingTime: "x"}, []Field{F(58, "Incorrect BeginString, expected FIX.4.2")})
	msg, err := DecodeOne(raw)
	if err != nil || msg.Value(8) != "FIX.4.4" {
		t.Fatalf("%v %v", msg, err)
	}
}

func TestFormatTimeAndPipe(t *testing.T) {
	ts := time.Date(2026, 9, 26, 14, 3, 22, 114_999_999, time.FixedZone("X", 3600))
	if got := FormatTime(ts); got != "20260926-13:03:22.114" {
		t.Fatalf("FormatTime %q", got)
	}
	if got := ToPipe([]byte("8=FIX.4.2\x019=5\x01")); got != "8=FIX.4.2|9=5|" {
		t.Fatalf("ToPipe %q", got)
	}
	if got := ToDelimiter([]byte("a\x01"), "SOH"); got != "a\x01" {
		t.Fatalf("SOH delimiter %q", got)
	}
}

// TestEmulatorFixture decodes real lines from the Python emulator's FIX logs
// and re-encodes each byte-identical, both by re-framing the decoded fields
// and by rebuilding through Encode's header layout.
func TestEmulatorFixture(t *testing.T) {
	f, err := os.Open("../../../testdata/emulator_lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	seen := map[string]bool{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		raw := FromPipe(line)
		msg, err := DecodeOne(raw)
		if err != nil {
			t.Fatalf("line %d: %v: %s", n, err, line)
		}
		seen[msg.Value(8)+" "+msg.MsgType()] = true
		if got := Rebuild(msg); !bytes.Equal(got, raw) {
			t.Fatalf("line %d: rebuild differs\n got %s\nwant %s", n, ToPipe(got), line)
		}
		// Through Encode: header 49,56,34,52,[43,122] then body.
		h := Header{SenderCompID: msg.Value(49), TargetCompID: msg.Value(56), SendingTime: msg.Value(52),
			PossDup: msg.Value(43) == "Y", OrigSendingTime: msg.Value(122)}
		h.MsgSeqNum, _ = msg.SeqNum()
		var body []Field
		for _, fld := range msg.Fields {
			switch fld.Tag {
			case 8, 9, 10, 35, 49, 56, 34, 52, 43, 122:
				continue
			}
			body = append(body, fld)
		}
		if got := Encode(msg.Value(8), msg.MsgType(), h, body); !bytes.Equal(got, raw) {
			t.Fatalf("line %d: Encode differs\n got %s\nwant %s", n, ToPipe(got), line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 18 {
		t.Fatalf("only %d fixture lines", n)
	}
	for _, v := range []string{"FIX.4.2", "FIX.4.4"} {
		for _, mt := range []string{"A", "0", "D", "8", "5"} {
			if !seen[v+" "+mt] {
				t.Errorf("fixture lacks %s 35=%s", v, mt)
			}
		}
	}
	t.Logf("%d emulator lines decoded and re-encoded byte-identical", n)
}
