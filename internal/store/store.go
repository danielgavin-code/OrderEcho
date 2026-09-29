// Package store persists sequence numbers and outbound messages.
//
// The session owns the numbers and decides what to store; the stores only
// persist. File formats match the emulator's so the two tools can read each
// other's data:
//
//	data/seqnums/<session_id>.json    {"next_out": n, "next_in": m}
//	data/msgstore/<session_id>.jsonl  one {seq, msg_type, fix_version, raw, sent_ts, injected} per line
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ------------------------------------------------------------ seqnums

// SeqStore persists the next outbound and next expected inbound numbers.
type SeqStore interface {
	Load() (nextOut, nextIn int, err error)
	Save(nextOut, nextIn int) error
	Reset() error
}

// MemorySeqStore is a non-persistent SeqStore for tests.
type MemorySeqStore struct {
	mu      sync.Mutex
	NextOut int
	NextIn  int
	Saves   int
}

// NewMemorySeqStore returns a store starting at (out, in).
func NewMemorySeqStore(out, in int) *MemorySeqStore {
	return &MemorySeqStore{NextOut: out, NextIn: in}
}

// Load returns the stored numbers.
func (s *MemorySeqStore) Load() (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.NextOut, s.NextIn, nil
}

// Save stores the numbers.
func (s *MemorySeqStore) Save(out, in int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NextOut, s.NextIn = out, in
	s.Saves++
	return nil
}

// Reset sets both numbers to 1.
func (s *MemorySeqStore) Reset() error { return s.Save(1, 1) }

// FileSeqStore is a JSON file per session with atomic, write-through saves.
type FileSeqStore struct {
	Path string
}

// NewFileSeqStore returns the store for sessionID under dir.
func NewFileSeqStore(dir, sessionID string) *FileSeqStore {
	return &FileSeqStore{Path: filepath.Join(dir, sessionID+".json")}
}

type seqFile struct {
	NextOut int `json:"next_out"`
	NextIn  int `json:"next_in"`
}

// Load reads the file; a missing file means (1, 1).
func (s *FileSeqStore) Load() (int, int, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return 1, 1, nil
	}
	if err != nil {
		return 1, 1, err
	}
	var f seqFile
	if err := json.Unmarshal(data, &f); err != nil {
		return 1, 1, fmt.Errorf("seqnum file %s: %w", s.Path, err)
	}
	if f.NextOut < 1 {
		f.NextOut = 1
	}
	if f.NextIn < 1 {
		f.NextIn = 1
	}
	return f.NextOut, f.NextIn, nil
}

// Exists reports whether the file is on disk.
func (s *FileSeqStore) Exists() bool {
	_, err := os.Stat(s.Path)
	return err == nil
}

// Save writes a temp file, fsyncs it and renames it over the real one.
func (s *FileSeqStore) Save(out, in int) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	payload, _ := json.Marshal(seqFile{NextOut: out, NextIn: in})
	tmp := s.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Reset sets both numbers to 1.
func (s *FileSeqStore) Reset() error { return s.Save(1, 1) }

// ------------------------------------------------------ message store

// Record is one stored outbound message.
type Record struct {
	Seq        int    `json:"seq"`
	MsgType    string `json:"msg_type"`
	FixVersion string `json:"fix_version"`
	Raw        string `json:"raw"` // real SOH characters, JSON-escaped
	SentTS     string `json:"sent_ts"`
	Injected   bool   `json:"injected"`
}

// MessageStore keeps outbound messages by sequence number for replay.
type MessageStore interface {
	Append(rec Record) error
	Get(seq int) (Record, bool)
	Len() int
	// Archive sets the current contents aside (sequence numbers are about to
	// mean different messages) and starts fresh. It returns where the old
	// contents went, or "" if there was nothing to archive.
	Archive(now time.Time) (string, error)
}

// MemoryMessageStore is a non-persistent MessageStore for tests.
type MemoryMessageStore struct {
	mu       sync.Mutex
	records  map[int]Record
	Archives int
}

// NewMemoryMessageStore returns an empty store.
func NewMemoryMessageStore() *MemoryMessageStore {
	return &MemoryMessageStore{records: map[int]Record{}}
}

// Append stores rec, replacing any record with the same seq.
func (m *MemoryMessageStore) Append(rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.Seq] = rec
	return nil
}

// Get returns the record for seq.
func (m *MemoryMessageStore) Get(seq int) (Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[seq]
	return r, ok
}

// Len is the number of stored records.
func (m *MemoryMessageStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// Seqs returns the stored sequence numbers in order.
func (m *MemoryMessageStore) Seqs() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, 0, len(m.records))
	for s := range m.records {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// Archive clears the store.
func (m *MemoryMessageStore) Archive(time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	had := len(m.records)
	m.records = map[int]Record{}
	m.Archives++
	if had == 0 {
		return "", nil
	}
	return "memory", nil
}

// FileMessageStore is an append-only JSONL file, flushed per line and loaded
// at open so replay survives restarts.
type FileMessageStore struct {
	mu      sync.Mutex
	Path    string
	records map[int]Record
	file    *os.File
}

// OpenFileMessageStore opens (and loads) the store for sessionID under dir. A
// corrupt line is skipped, not fatal.
func OpenFileMessageStore(dir, sessionID string) (*FileMessageStore, error) {
	s := &FileMessageStore{Path: filepath.Join(dir, sessionID+".jsonl"), records: map[int]Record{}}
	return s, s.load()
}

func (s *FileMessageStore) load() error {
	f, err := os.Open(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if json.Unmarshal(line, &r) != nil || r.Seq == 0 {
			continue
		}
		s.records[r.Seq] = r
	}
	return sc.Err()
}

// Append writes rec as one JSON line and flushes it.
func (s *FileMessageStore) Append(rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.Seq] = rec
	if s.file == nil {
		if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		s.file = f
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := s.file.Write(line); err != nil {
		return err
	}
	return nil
}

// Get returns the record for seq.
func (s *FileMessageStore) Get(seq int) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[seq]
	return r, ok
}

// Len is the number of stored records.
func (s *FileMessageStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// Archive renames the file with a UTC timestamp suffix (never deletes it) and
// starts a fresh, empty store.
func (s *FileMessageStore) Archive(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
	s.records = map[int]Record{}
	if _, err := os.Stat(s.Path); err != nil {
		return "", nil
	}
	suffix := now.UTC().Format("20060102-150405")
	archived := s.Path + "." + suffix
	for n := 2; ; n++ {
		if _, err := os.Stat(archived); errors.Is(err, fs.ErrNotExist) {
			break
		}
		archived = fmt.Sprintf("%s.%s-%d", s.Path, suffix, n)
	}
	if err := os.Rename(s.Path, archived); err != nil {
		return "", err
	}
	return archived, nil
}

// Close closes the file handle.
func (s *FileMessageStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *FileMessageStore) closeLocked() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// CountFileRecords counts distinct stored seqs in a store file without
// keeping it open (for the offline status command).
func CountFileRecords(dir, sessionID string) (int, string, error) {
	s, err := OpenFileMessageStore(dir, sessionID)
	if err != nil {
		return 0, s.Path, err
	}
	return s.Len(), s.Path, nil
}

// StoredVersion is the FIX version of the highest-seq record that names one,
// or "". Replaying another version's bytes would be worse than not replaying
// at all, so the CLI archives a store whose version disagrees with the session.
func (s *FileMessageStore) StoredVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	best, version := 0, ""
	for seq, r := range s.records {
		if r.FixVersion != "" && seq > best {
			best, version = seq, r.FixVersion
		}
	}
	return version
}
