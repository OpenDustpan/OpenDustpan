package audit_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errReadFailed is the failure of a read in the abort tests.
var errReadFailed = errors.New("read failed")

// failingReader answers the entries below failAt from the store and fails
// the read at failAt and after it.
type failingReader struct {
	*memAuditStore

	failAt int64
}

func (r failingReader) EntryAt(ctx context.Context, ns namespace.Name, seq int64) (audit.Entry, error) {
	if seq >= r.failAt {
		return audit.Entry{}, errReadFailed
	}

	return r.memAuditStore.EntryAt(ctx, ns, seq)
}

// blockingReader answers the first entry and then waits until the context
// ends, like a store that stalls.
type blockingReader struct {
	*memAuditStore
}

func (r blockingReader) EntryAt(ctx context.Context, ns namespace.Name, seq int64) (audit.Entry, error) {
	if seq == 1 {
		return r.memAuditStore.EntryAt(ctx, ns, seq)
	}

	<-ctx.Done()

	return audit.Entry{}, fmt.Errorf("stalled read ended: %w", ctx.Err())
}

// serveExport serves the export request and reports whether the handler
// ended the transfer with panic(http.ErrAbortHandler). Any other panic goes
// on.
func serveExport(t *testing.T, handler http.Handler, target string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	rec := httptest.NewRecorder()
	aborted := false

	func() {
		defer func() {
			v := recover()
			if v == nil {
				return
			}

			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				aborted = true

				return
			}

			panic(v)
		}()

		handler.ServeHTTP(rec, authedRequest(t, target))
	}()

	return rec, aborted
}

// seededStore returns a store with three entries of acme and one of globex.
func seededStore(t *testing.T) *memAuditStore {
	t.Helper()

	store := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}
	for _, subject := range []string{"alice", "bob", "carol"} {
		seedEntry(t, store, "acme", subject)
	}

	seedEntry(t, store, "globex", "mallory")

	return store
}

// TestExportAbortsWhenAReadFailsAfterTheFirstLine proves that an entry that
// the store cannot read ends the transfer by force, so a client never takes a
// short export for a whole one.
func TestExportAbortsWhenAReadFailsAfterTheFirstLine(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	handler := newExportHandler(t, store, failingReader{memAuditStore: store, failAt: 2}, time.Minute)

	if _, aborted := serveExport(t, handler, "/api/audit/export?organization=acme"); !aborted {
		t.Fatal("handler did not end the transfer with panic(http.ErrAbortHandler)")
	}
}

// TestExportAbortsOnAnEntryOfAnotherScope proves the scope check of the
// stream alone: a reader that answers an entry of another scope aborts the
// transfer and no line of the other scope leaves.
func TestExportAbortsOnAnEntryOfAnotherScope(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	// The reader answers the entries of acme for every scope it is asked for.
	handler := newExportHandler(t, store, scopelessReader{memAuditStore: store, from: "acme"}, time.Minute)

	rec, aborted := serveExport(t, handler, "/api/audit/export?organization=globex")
	if !aborted {
		t.Fatal("handler did not end the transfer with panic(http.ErrAbortHandler)")
	}

	if strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("the export holds a line of another scope: %s", rec.Body.String())
	}
}

// TestExportStopsAtTheTimeLimit proves that a scan that stalls ends with the
// time limit of the export, by an aborted transfer.
func TestExportStopsAtTheTimeLimit(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	handler := newExportHandler(t, store, blockingReader{memAuditStore: store}, 20*time.Millisecond)

	done := make(chan bool, 1)

	go func() {
		_, aborted := serveExport(t, handler, "/api/audit/export?organization=acme")
		done <- aborted
	}()

	select {
	case aborted := <-done:
		if !aborted {
			t.Fatal("handler did not end the transfer with panic(http.ErrAbortHandler)")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the export did not stop at its time limit")
	}
}

// TestExportKeepsTheAnswerOfTheNamespace proves that a namespace that
// refuses the audit entry of the export answers in its own words, not with
// the code of a lost entry.
func TestExportKeepsTheAnswerOfTheNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cause    error
		wantCode int
	}{
		{"fenced", namespace.ErrFenced, http.StatusConflict},
		{"not home", namespace.ErrNotHome, http.StatusMisdirectedRequest},
		{"maintenance", maintenance.ErrInMaintenance, http.StatusServiceUnavailable},
		{"other", errAppendDiskFull, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := newHandlerWithAppendFailing(t, seededStore(t), tt.cause)

			rec, aborted := serveExport(t, handler, "/api/audit/export?organization=acme")
			if aborted {
				t.Fatal("handler ended the transfer by force")
			}

			if rec.Code != tt.wantCode || strings.Contains(rec.Body.String(), `"sequence":1`) {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}

			isLost := strings.Contains(rec.Body.String(), "audit.export-not-recorded")
			if isLost != (tt.name == "other") {
				t.Fatalf("code audit.export-not-recorded = %v for %s: %s", isLost, tt.name, rec.Body.String())
			}
		})
	}
}
