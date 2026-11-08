package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
)

// cancelPath returns the cancel route of the restore id.
func cancelPath(id string) string {
	return "/api/dustpan/v1/restores/" + id + "/cancel"
}

// TestCancelRestoreBeforeTheSwap proves that a cancel before the swap answers
// 202 with the restore ID, that the restore ends failed with the cancel text,
// that a second cancel answers 202 again, and that each cancel writes an
// audit entry.
func TestCancelRestoreBeforeTheSwap(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)
	gate := make(chan struct{})
	released := false

	defer func() {
		if !released {
			close(gate)
		}
	}()

	// The gate holds the restore in its stage step, before the swap.
	fix := newRestoreFixtureOver(
		t,
		stores,
		&gatedRestoreTarget{inner: stores.target, gate: gate, reads: atomic.Int32{}},
	)
	doc := startRestore(t, fix.handler, `{"backupID":"bkp-missing000000000001"}`)

	for range 2 {
		status, body := serveRestore(t, fix.handler, http.MethodPost, cancelPath(doc.ID), "")
		if status != http.StatusAccepted {
			t.Fatalf("POST cancel answers %d, want %d (%s)", status, http.StatusAccepted, body)
		}

		var answer struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal([]byte(body), &answer); err != nil || answer.ID != doc.ID {
			t.Fatalf("POST cancel answers %q, want the id %s: %v", body, doc.ID, err)
		}
	}

	close(gate)
	released = true

	failed := waitRestoreState(t, fix.handler, doc.ID, "failed")
	if failed.Error != backup.RestoreCanceledText {
		t.Fatalf("canceled restore error = %q, want %q", failed.Error, backup.RestoreCanceledText)
	}

	if got := cancelEntries(t, fix, doc.ID, audit.OutcomeSuccess); got != 2 {
		t.Fatalf("cancel audit entries = %d, want 2", got)
	}
}

// TestCancelRestoreAfterTheSwap proves that a cancel after the swap answers
// 409 with restore.swapped, that the restore still succeeds, and that the
// refused cancel writes a failure entry.
func TestCancelRestoreAfterTheSwap(t *testing.T) {
	t.Parallel()

	reached := make(chan struct{})
	release := make(chan struct{})

	var once atomic.Bool

	stores := openBackupDeps(t)
	fix := newRestoreFixtureHooked(t, stores, stores.target, func(ctx context.Context) error {
		if once.CompareAndSwap(false, true) {
			close(reached)
		}

		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	backupID := backup.ID("bkp-cancel0000000000001")
	if _, err := fix.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup %s: %v", backupID, err)
	}

	doc := startRestore(t, fix.handler, `{"backupID":"`+backupID.String()+`"}`)

	select {
	case <-reached:
	case <-t.Context().Done():
		t.Fatalf("restore %s never reached the after-apply step", doc.ID)
	}

	status, body := serveRestore(t, fix.handler, http.MethodPost, cancelPath(doc.ID), "")

	close(release)

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, []byte(body),
		errcodetest.Want{Code: errcode.RestoreSwapped, Args: map[string]any{"restore": doc.ID}, RequestBody: nil})

	if status != http.StatusConflict {
		t.Fatalf("POST cancel answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	waitRestoreState(t, fix.handler, doc.ID, "succeeded")

	if got := cancelEntries(t, fix, doc.ID, audit.OutcomeFailure); got != 1 {
		t.Fatalf("refused cancel audit entries = %d, want 1", got)
	}
}

// TestCancelRestoreAfterTheEnd proves that a cancel of a restore that ended
// answers 409 with restore.ended.
func TestCancelRestoreAfterTheEnd(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)
	doc := startRestore(t, fix.handler, `{"backupID":"bkp-missing000000000001"}`)
	waitRestoreState(t, fix.handler, doc.ID, "failed")

	status, body := serveRestore(t, fix.handler, http.MethodPost, cancelPath(doc.ID), "")
	if status != http.StatusConflict {
		t.Fatalf("POST cancel answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, []byte(body),
		errcodetest.Want{Code: errcode.RestoreEnded, Args: map[string]any{"restore": doc.ID}, RequestBody: nil})
}

// TestCancelRestoreUnknownAnswers404 proves that a cancel of an unknown
// restore ID, or of an ID that cannot be a restore ID, answers 404 and writes
// no audit entry.
func TestCancelRestoreUnknownAnswers404(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	for _, id := range []string{
		backup.MintRestoreID().String(),
		"rst-NOTVALID",
		"bkp-aaaaaaaaaaaaaaaaaaaa",
		"rst-aaaaaaaaaaaaaaaaaaaa%0Ainjected",
	} {
		status, body := serveRestore(t, fix.handler, http.MethodPost, cancelPath(id), "")
		if status != http.StatusNotFound {
			t.Fatalf("POST cancel %q answers %d, want %d (%s)", id, status, http.StatusNotFound, body)
		}
	}

	listing, err := fix.audit.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	for i := range listing.Entries {
		if object := listing.Entries[i].Object; strings.HasPrefix(object, "cancel of restore") {
			t.Fatalf("audit entry %q for an unknown restore", object)
		}
	}
}

// cancelEntries counts the audit entries of cancels of the restore id with
// the outcome want.
func cancelEntries(t *testing.T, fix restoreFixture, id string, want audit.Outcome) int {
	t.Helper()

	listing, err := fix.audit.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		if entry := &listing.Entries[i]; entry.Object == "cancel of restore "+id && entry.Outcome == want {
			n++
		}
	}

	return n
}
