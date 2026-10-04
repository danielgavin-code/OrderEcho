package cert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A5 §3.2: a run's evidence is tamper-evident. At run end WriteResults
// records the SHA-256 of every per-case fix.log and evidence.jsonl in
// results.json ("integrity"), and the SHA-256 of results.json itself in the
// sidecar results.sha256 (a file cannot hold its own hash). Verify
// recomputes them all.

// Integrity is the hash record kept in results.json.
type Integrity struct {
	Algorithm string            `json:"algorithm"` // sha256
	Files     map[string]string `json:"files"`     // "<case>/fix.log" -> hex digest
}

// IntegritySidecar is the file holding results.json's own digest.
const IntegritySidecar = "results.sha256"

// NewIntegrity is an empty record (a run that will be sealed).
func NewIntegrity() *Integrity { return &Integrity{Algorithm: "sha256", Files: map[string]string{}} }

// FileSHA256 hashes a file.
func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func bytesSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// FileCheck is one verified file.
type FileCheck struct {
	Path   string `json:"path"` // relative to the run directory
	Want   string `json:"want"`
	Got    string `json:"got"`
	Status string `json:"status"` // ok | MISMATCH | MISSING | UNRECORDED
}

// VerifyReport is the outcome of Verify.
type VerifyReport struct {
	Dir   string      `json:"dir"`
	OK    bool        `json:"ok"`
	Files []FileCheck `json:"files"`
}

// Problems lists the files that did not verify.
func (v *VerifyReport) Problems() []FileCheck {
	var out []FileCheck
	for _, f := range v.Files {
		if f.Status != "ok" {
			out = append(out, f)
		}
	}
	return out
}

// ErrNotSealed: the run has no integrity record (written before 0.5.0).
var ErrNotSealed = errors.New("the run has no integrity record (results written before OrderEcho 0.5.0); it cannot be verified")

// Verify recomputes results.json's digest and every recorded per-case file
// digest, and flags case files that were never recorded.
func Verify(dir string) (*VerifyReport, error) {
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return nil, err
	}
	var res RunResult
	if err := json.Unmarshal(data, &res); err != nil {
		// Unreadable results are themselves a mismatch only if a digest exists.
		if _, serr := os.Stat(filepath.Join(dir, IntegritySidecar)); serr != nil {
			return nil, fmt.Errorf("results.json: %v", err)
		}
	}
	if res.Integrity == nil {
		if _, serr := os.Stat(filepath.Join(dir, IntegritySidecar)); serr != nil {
			return nil, ErrNotSealed
		}
	}
	rep := &VerifyReport{Dir: dir, OK: true}
	add := func(fc FileCheck) {
		if fc.Status != "ok" {
			rep.OK = false
		}
		rep.Files = append(rep.Files, fc)
	}
	// results.json against its sidecar.
	got := bytesSHA256(data)
	side, err := os.ReadFile(filepath.Join(dir, IntegritySidecar))
	want := ""
	if err == nil {
		want = strings.Fields(string(side) + " ")[0]
	}
	switch {
	case err != nil:
		add(FileCheck{Path: "results.json", Got: got, Status: "MISSING", Want: "(" + IntegritySidecar + " missing)"})
	case want != got:
		add(FileCheck{Path: "results.json", Want: want, Got: got, Status: "MISMATCH"})
	default:
		add(FileCheck{Path: "results.json", Want: want, Got: got, Status: "ok"})
	}
	recorded := map[string]bool{}
	if res.Integrity != nil {
		paths := make([]string, 0, len(res.Integrity.Files))
		for p := range res.Integrity.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			recorded[p] = true
			want := res.Integrity.Files[p]
			got, err := FileSHA256(filepath.Join(dir, filepath.FromSlash(p)))
			switch {
			case err != nil:
				add(FileCheck{Path: p, Want: want, Status: "MISSING"})
			case got != want:
				add(FileCheck{Path: p, Want: want, Got: got, Status: "MISMATCH"})
			default:
				add(FileCheck{Path: p, Want: want, Got: got, Status: "ok"})
			}
		}
	}
	// Files in case folders that nobody recorded.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(dir, e.Name()))
		for _, f := range files {
			p := e.Name() + "/" + f.Name()
			if !recorded[p] {
				got, _ := FileSHA256(filepath.Join(dir, e.Name(), f.Name()))
				add(FileCheck{Path: p, Got: got, Status: "UNRECORDED"})
			}
		}
	}
	return rep, nil
}

// ResultsDigest is results.json's recorded digest ("" when unsealed).
func ResultsDigest(dir string) string {
	side, err := os.ReadFile(filepath.Join(dir, IntegritySidecar))
	if err != nil {
		return ""
	}
	return strings.Fields(string(side) + " ")[0]
}
