package evidence

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danielgavin-code/OrderEcho/internal/clock"
	"github.com/danielgavin-code/OrderEcho/internal/fix/codec"
)

func TestEvidenceSchema(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 9, 26, 14, 3, 22, 114_000_000, time.UTC))
	runID := MakeRunID(clk.Now())
	if runID != "20260926-140322" {
		t.Fatal(runID)
	}
	w := Open(dir, runID, clk)
	raw := codec.Encode("FIX.4.2", "1", codec.Header{SenderCompID: "AGENT", TargetCompID: "ORDERECHO", MsgSeqNum: 5, SendingTime: "20260926-14:03:22.110"}, []codec.Field{codec.F(112, "TEST-1")})
	msg, _ := codec.DecodeOne(raw)
	w.Message(KindOut, "emu42", 5, msg, "", false)
	w.Discarded("emu42", &codec.DiscardedFrame{Raw: []byte("8=FIX.4.2\x01junk"), Reason: "bad"})
	w.Event("emu42", "injected seq gap", "2 -> 5", true)
	w.Close()

	f, _ := os.Open(w.Path)
	defer f.Close()
	sc := bufio.NewScanner(f)
	var recs []map[string]any
	var lines []string
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, m)
		lines = append(lines, sc.Text())
	}
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	keys := `{"ts":"2026-09-26T14:03:22.114Z","run_id":"20260926-140322","kind":"out","session":"emu42","seq":5,"msg_type":"1","raw":"8=FIX.4.2|`
	if !strings.HasPrefix(lines[0], keys) {
		t.Fatalf("key order/values: %s", lines[0])
	}
	for _, k := range []string{"ts", "run_id", "kind", "session", "seq", "msg_type", "raw", "fields", "detail", "order", "injected"} {
		for i, r := range recs {
			if _, ok := r[k]; !ok {
				t.Fatalf("record %d lacks %s", i, k)
			}
		}
	}
	fields := recs[0]["fields"].([]any)
	if first := fields[0].([]any); first[0] != "8" || first[1] != "FIX.4.2" {
		t.Fatalf("fields %v", fields)
	}
	if recs[1]["kind"] != "discarded" || recs[1]["detail"] != "bad" || recs[1]["seq"] != nil {
		t.Fatalf("discard %v", recs[1])
	}
	if recs[2]["kind"] != "event" || recs[2]["detail"] != "injected seq gap: 2 -> 5" || recs[2]["injected"] != true || recs[2]["fields"] != nil {
		t.Fatalf("event %v", recs[2])
	}
}
