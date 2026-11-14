//go:build e2e

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// exportLine is one line of GET /api/audit/export: every field that the hash
// covers, with the previous hash and the hash.
type exportLine struct {
	Namespace     string         `json:"namespace"`
	AtMs          int64          `json:"atMs"`
	Sequence      int64          `json:"sequence"`
	Subject       string         `json:"subject"`
	IDPSubject    string         `json:"idpSubject"`
	TokenID       string         `json:"tokenID"`
	Action        string         `json:"action"`
	Object        string         `json:"object"`
	Version       string         `json:"version"`
	ValuePath     string         `json:"valuePath"`
	ValueCount    *int64         `json:"valueCount"`
	Changes       []audit.Change `json:"changes"`
	SourceIP      string         `json:"sourceIP"`
	UserAgent     string         `json:"userAgent"`
	Channel       string         `json:"channel"`
	Kind          string         `json:"kind"`
	Outcome       string         `json:"outcome"`
	RequestID     string         `json:"requestID"`
	ObjectID      string         `json:"objectID"`
	PrevHash      string         `json:"prevHash"`
	Hash          string         `json:"hash"`
	FenceVersion  int64          `json:"fenceVersion"`
	Reason        string         `json:"reason"`
	PrincipalKind string         `json:"principalKind"`
	JobID         string         `json:"jobID"`
}

// entry returns the audit entry that the line holds, so the test computes
// the hash again with the public function of the audit package.
func (l exportLine) entry() audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(l.AtMs).UTC(),
		Namespace:     namespace.Name(l.Namespace),
		Subject:       l.Subject,
		IDPSubject:    l.IDPSubject,
		TokenID:       l.TokenID,
		Action:        l.Action,
		Object:        l.Object,
		Version:       l.Version,
		ValuePath:     l.ValuePath,
		ValueCount:    l.ValueCount,
		Changes:       l.Changes,
		SourceIP:      l.SourceIP,
		UserAgent:     l.UserAgent,
		Channel:       audit.Channel(l.Channel),
		Kind:          audit.Kind(l.Kind),
		Outcome:       audit.Outcome(l.Outcome),
		RequestID:     l.RequestID,
		ObjectID:      l.ObjectID,
		PrevHash:      l.PrevHash,
		Hash:          l.Hash,
		Sequence:      l.Sequence,
		FenceVersion:  l.FenceVersion,
		PrincipalKind: l.PrincipalKind,
		JobID:         l.JobID,
		Reason:        l.Reason,
	}
}

// checkAuditExport proves that GET /api/audit/export streams the entries of
// org oldest first with the chain fields, that a range from the middle of
// the log verifies offline, and that the export wrote its own audit-read
// entry.
func checkAuditExport(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	base := "/api/audit/export?organization=" + url.QueryEscape(org)
	all := readAuditExport(ctx, t, env, base)

	if len(all) < 2 {
		t.Fatalf("export holds %d lines, want at least 2", len(all))
	}

	verifyExportRange(t, all)

	// A range that starts in the middle of the chain verifies by itself: the
	// first line names its previous hash, and the lines after it link to it.
	from := all[len(all)/2].Sequence
	part := readAuditExport(ctx, t, env, base+"&fromSequence="+strconv.FormatInt(from, 10))

	if len(part) == 0 || part[0].Sequence != from {
		t.Fatalf("export from sequence %d starts at %+v", from, part)
	}

	verifyExportRange(t, part)

	if part[0].Hash != all[len(all)/2].Hash {
		t.Fatalf("the range holds another entry at sequence %d", from)
	}

	checkAuditExportCEF(ctx, t, env, base, part)

	// The first export wrote one audit-read entry, which the second export
	// reads. The entry names the scope.
	found := false

	for i := range part {
		line := &part[i]
		if line.Kind == string(audit.KindAuditRead) && strings.HasPrefix(line.Object, "audit-export "+org+" ") {
			found = true
		}
	}

	if !found {
		t.Fatal("the second export holds no audit-read entry of the first export")
	}
}

// checkAuditExportCEF proves that the CEF export holds one CEF line for each
// JSON line, with the same sequence and the same hash.
func checkAuditExportCEF(ctx context.Context, t *testing.T, env *e2e.Env, base string, jsonLines []exportLine) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, base+"&format=cef", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("CEF export answers %d %v (%s)", status, err, body)
	}

	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	// The CEF export started later, so it also holds the entry of the JSON
	// exports before it.
	if len(lines) < len(jsonLines) {
		t.Fatalf("CEF export holds %d lines, want at least %d", len(lines), len(jsonLines))
	}

	jsonHash := make(map[int64]string, len(jsonLines))
	for i := range jsonLines {
		jsonHash[jsonLines[i].Sequence] = jsonLines[i].Hash
	}

	seen := checkCEFLines(t, lines, jsonHash)

	for seq := range jsonHash {
		if !seen[seq] {
			t.Fatalf("CEF export holds no line for sequence %d", seq)
		}
	}
}

// checkCEFLines checks each CEF line against jsonHash: the line has the
// prefix, the hash label, a sequence and a hash, and the hash of a sequence
// that jsonHash holds is equal. It returns the sequences that a CEF line
// carried. A sequence that jsonHash does not hold is allowed: the CEF export
// can hold entries before the range of the JSON export.
func checkCEFLines(t *testing.T, lines []string, jsonHash map[int64]string) map[int64]bool {
	t.Helper()

	seen := make(map[int64]bool, len(jsonHash))

	for _, line := range lines {
		if !strings.HasPrefix(line, "CEF:0|OpenDustpan|OpenDustpan|1|") ||
			!strings.Contains(line, " cs5Label=hash") {
			t.Fatalf("CEF line = %q", line)
		}

		sequence, ok := cefField(line, "cn1")
		if !ok {
			t.Fatalf("CEF line has no cn1: %q", line)
		}

		seq, err := strconv.ParseInt(sequence, 10, 64)
		if err != nil {
			t.Fatalf("CEF line has sequence %q: %v", sequence, err)
		}

		hash, ok := cefField(line, "cs5")
		if !ok {
			t.Fatalf("CEF line has no cs5: %q", line)
		}

		want, ok := jsonHash[seq]
		if !ok {
			continue
		}

		if hash != want {
			t.Fatalf("CEF line for sequence %d has hash %s, want %s", seq, hash, want)
		}

		seen[seq] = true
	}

	return seen
}

// cefField returns the value of key in a CEF line: the text after " <key>="
// up to the next space. A line with no such key gives false.
func cefField(line, key string) (string, bool) {
	_, rest, found := strings.Cut(line, " "+key+"=")
	if !found {
		return "", false
	}

	value, _, _ := strings.Cut(rest, " ")

	return value, value != ""
}

// readAuditExport reads the export at path and decodes each line.
func readAuditExport(ctx context.Context, t *testing.T, env *e2e.Env, path string) []exportLine {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("export audit log: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", path, status, http.StatusOK, body)
	}

	var lines []exportLine

	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		var line exportLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("decode export line %q: %v", sc.Text(), err)
		}

		lines = append(lines, line)
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("read export: %v", err)
	}

	return lines
}

// verifyExportRange checks the lines with no help from the server: the
// sequences go up by one, each previous hash is the hash before it, and each
// hash is what the hash function gives for the fields of the line.
func verifyExportRange(t *testing.T, lines []exportLine) {
	t.Helper()

	for i := range lines {
		line := &lines[i]
		if i > 0 {
			if line.Sequence != lines[i-1].Sequence+1 {
				t.Fatalf("export has a gap between sequence %d and %d", lines[i-1].Sequence, line.Sequence)
			}

			if line.PrevHash != lines[i-1].Hash {
				t.Fatalf("sequence %d does not link to sequence %d", line.Sequence, lines[i-1].Sequence)
			}
		}

		if line.Hash == "" {
			continue
		}

		if got := audit.ComputeHash(line.entry(), line.PrevHash); got != line.Hash {
			t.Fatalf("sequence %d: hash %s, computed %s", line.Sequence, line.Hash, got)
		}
	}
}
