package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileSeqStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSeqStore(filepath.Join(dir, "seqnums"), "emu42")
	out, in, err := s.Load()
	if err != nil || out != 1 || in != 1 || s.Exists() {
		t.Fatalf("missing file: %d %d %v", out, in, err)
	}
	if err := s.Save(7, 12); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path)
	if string(data) != `{"next_out":7,"next_in":12}` {
		t.Fatalf("file %s", data)
	}
	if _, err := os.Stat(s.Path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
	out, in, _ = NewFileSeqStore(filepath.Join(dir, "seqnums"), "emu42").Load()
	if out != 7 || in != 12 {
		t.Fatalf("reload %d %d", out, in)
	}
	s.Reset()
	if out, in, _ = s.Load(); out != 1 || in != 1 {
		t.Fatal("reset")
	}
}

func TestFileSeqStoreReadsEmulatorFormat(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.json"), []byte(`{"next_out": 5, "next_in": 9}`), 0o644)
	out, in, err := NewFileSeqStore(dir, "x").Load()
	if err != nil || out != 5 || in != 9 {
		t.Fatalf("%d %d %v", out, in, err)
	}
}

func TestFileMessageStore(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenFileMessageStore(dir, "emu42")
	if err != nil {
		t.Fatal(err)
	}
	raw := "8=FIX.4.2\x019=5\x0135=0\x0110=000\x01"
	s.Append(Record{Seq: 1, MsgType: "A", FixVersion: "FIX.4.2", Raw: raw, SentTS: "t"})
	s.Append(Record{Seq: 2, MsgType: "D", FixVersion: "FIX.4.2", Raw: raw, Injected: true})
	s.Close()
	data, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(data), `\u0001`) || strings.Count(string(data), "\n") != 2 {
		t.Fatalf("file %q", data)
	}
	re, _ := OpenFileMessageStore(dir, "emu42")
	if re.Len() != 2 {
		t.Fatalf("reload len %d", re.Len())
	}
	r, ok := re.Get(2)
	if !ok || r.Raw != raw || !r.Injected || r.MsgType != "D" {
		t.Fatalf("record %+v", r)
	}
	if re.StoredVersion() != "FIX.4.2" {
		t.Fatal("version")
	}
	archived, err := re.Archive(time.Date(2026, 9, 29, 1, 2, 3, 0, time.UTC))
	if err != nil || !strings.HasSuffix(archived, "emu42.jsonl.20260929-010203") {
		t.Fatalf("archive %q %v", archived, err)
	}
	if re.Len() != 0 {
		t.Fatal("not emptied")
	}
	if _, err := os.Stat(archived); err != nil {
		t.Fatal("archive missing")
	}
	re.Append(Record{Seq: 1, MsgType: "A", Raw: raw})
	second, _ := re.Archive(time.Date(2026, 9, 29, 1, 2, 3, 0, time.UTC))
	if second == archived || !strings.HasSuffix(second, "-2") {
		t.Fatalf("second archive %q", second)
	}
	re.Close()
	if n, _, _ := CountFileRecords(dir, "emu42"); n != 0 {
		t.Fatalf("count %d", n)
	}
}

func TestFileMessageStoreSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("{bad\n{\"seq\":3,\"msg_type\":\"D\",\"raw\":\"x\"}\n\n"), 0o644)
	s, err := OpenFileMessageStore(dir, "s")
	if err != nil || s.Len() != 1 {
		t.Fatalf("%v %d", err, s.Len())
	}
}
