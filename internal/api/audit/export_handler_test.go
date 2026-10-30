package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// exportedLine is one decoded line of an export.
type exportedLine struct {
	Namespace string `json:"namespace"`
	AtMs      int64  `json:"atMs"`
	Sequence  int64  `json:"sequence"`
	Subject   string `json:"subject"`
	PrevHash  string `json:"prevHash"`
	Hash      string `json:"hash"`
}

// decodeExport reads the lines of an export body.
func decodeExport(t *testing.T, body string) []exportedLine {
	t.Helper()

	var out []exportedLine

	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		var line exportedLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("decode line %q: %v", sc.Text(), err)
		}

		out = append(out, line)
	}

	return out
}

// TestExportStreamsOldestFirstWithChainFields proves that the export lists
// the entries of the scope oldest first and that the lines link by hash.
func TestExportStreamsOldestFirstWithChainFields(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})

	for _, subject := range []string{"alice", "bob", "carol"} {
		seedEntry(t, store, "acme", subject)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/export?organization=acme"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/x-ndjson") {
		t.Fatalf("content type %q", got)
	}

	lines := decodeExport(t, rec.Body.String())
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	for i, line := range lines {
		if line.Sequence != int64(i+1) || line.Namespace != "acme" {
			t.Fatalf("line %d = %+v", i, line)
		}

		if i > 0 && line.PrevHash != lines[i-1].Hash {
			t.Fatalf("line %d does not link to line %d", i, i-1)
		}
	}

	checkExportRecorded(t, store.entries["acme"])
}

// checkExportRecorded fails unless the export wrote its own entry after the
// three seeded ones.
func checkExportRecorded(t *testing.T, stored []audit.Entry) {
	t.Helper()

	if len(stored) != 4 {
		t.Fatalf("store holds %d entries, want 4", len(stored))
	}

	got := stored[3]
	if got.Kind != audit.KindAuditRead || got.Action != "namespace:read-audit" ||
		!strings.HasPrefix(got.Object, "audit-export acme ") || got.Subject != "tester" {
		t.Fatalf("export entry = %+v", got)
	}
}

// checkExportAnswer fails when a refused export wrote an entry, or when an
// answered export holds another count of lines.
func checkExportAnswer(t *testing.T, body string, code, wantLines, stored int) {
	t.Helper()

	if code != http.StatusOK {
		if stored != 3 {
			t.Fatal("a refused export wrote an entry")
		}

		return
	}

	if got := len(decodeExport(t, body)); got != wantLines {
		t.Fatalf("got %d lines, want %d", got, wantLines)
	}
}

// TestExportRangeAndErrors proves the sequence and time range filters and
// the refusal of each invalid query with no entry written.
func TestExportRangeAndErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		wantCode  int
		wantLines int
	}{
		{"from sequence", "&fromSequence=2", http.StatusOK, 2},
		{"window includes", "&from=" + ms(changeTime()) + "&to=" + ms(changeTime()), http.StatusOK, 3},
		{"window excludes", "&to=" + ms(changeTime().Add(-time.Second)), http.StatusOK, 0},
		{"bad format", "&format=cef", http.StatusBadRequest, 0},
		{"bad from", "&from=yesterday", http.StatusBadRequest, 0},
		{"negative to", "&to=-1", http.StatusBadRequest, 0},
		{"reversed range", "&from=10&to=5", http.StatusBadRequest, 0},
		{"zero sequence", "&fromSequence=0", http.StatusBadRequest, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
			for _, subject := range []string{"alice", "bob", "carol"} {
				seedEntry(t, store, "acme", subject)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, authedRequest(t, "/api/audit/export?organization=acme"+tt.query))

			if rec.Code != tt.wantCode {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}

			checkExportAnswer(t, rec.Body.String(), tt.wantCode, tt.wantLines, len(store.entries["acme"]))
		})
	}
}

// TestExportFailsClosedWhenTheEntryIsNotWritten proves that the export sends
// no line when it cannot record itself.
func TestExportFailsClosedWhenTheEntryIsNotWritten(t *testing.T) {
	t.Parallel()

	_, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	handler := newHandlerWithAppendFailing(t, store, errAppendDiskFull)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/export?organization=acme"))

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), `"sequence"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

// errAppendDiskFull is the failure of the append in the fail-closed test.
var errAppendDiskFull = errors.New("disk full")

// appendFailingStore reads from the store it wraps and fails every append
// with err.
type appendFailingStore struct {
	*memAuditStore

	err error
}

func (s appendFailingStore) Append(_ context.Context, _ namespace.Fence, _ audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, s.err
}

// newExportHandler builds an enforcing handler over entries and reader with
// the time limit of one export.
func newExportHandler(
	t *testing.T, entries audit.Store, reader audit.EntryReader, limit time.Duration,
) *apitaudit.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		OnAuditFailure: nil,
		Namespaces:     namespaces,
		Entries:        entries,
		EntryReader:    reader,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     &recordingAuthorizer{calls: nil},
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	handler.SetExportLimit(limit)

	return handler
}

// newHandlerWithAppendFailing builds an enforcing handler that reads store
// and fails every append with err.
func newHandlerWithAppendFailing(t *testing.T, store *memAuditStore, err error) *apitaudit.Handler {
	t.Helper()

	return newExportHandler(t, appendFailingStore{memAuditStore: store, err: err}, store, time.Minute)
}

// TestExportErrorsHaveCodes proves the codes of the export refusals.
func TestExportErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		apitaudit.ErrInvalidFormat, apitaudit.ErrInvalidTime, apitaudit.ErrInvalidRange,
		apitaudit.ErrInvalidFromSequence, apitaudit.ErrExportNotRecorded,
	} {
		rec := httptest.NewRecorder()
		apitaudit.WriteError(t.Context(), rec, err)

		if rec.Code == http.StatusInternalServerError && !errors.Is(err, apitaudit.ErrExportNotRecorded) {
			t.Fatalf("%v has no catalog code", err)
		}

		if !strings.Contains(rec.Body.String(), `"code"`) {
			t.Fatalf("%v answers %s", err, rec.Body.String())
		}
	}
}

func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
