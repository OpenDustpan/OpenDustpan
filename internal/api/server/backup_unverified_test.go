package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestTooManyUnverifiedAnswer proves that the refusal of a backup at the bound
// on the unverified keys answers a coded 422 whose body names the count and
// the bound, also when the refusal is wrapped.
func TestTooManyUnverifiedAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		limit int
	}{
		{name: "one above the package bound", count: backup.MaxUnverifiedKeys + 1, limit: backup.MaxUnverifiedKeys},
		{name: "one above a small bound", count: 4, limit: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			refusal := &backup.TooManyUnverifiedError{Count: tc.count, Limit: tc.limit}

			checkTooManyUnverifiedAnswer(t, refusal, tc.count, tc.limit)
			checkTooManyUnverifiedAnswer(t, fmt.Errorf("run backup bkp-a: %w", refusal), tc.count, tc.limit)
		})
	}
}

// checkTooManyUnverifiedAnswer serves err and proves that the answer is the
// coded 422 whose text and arguments name count and limit.
func checkTooManyUnverifiedAnswer(t *testing.T, err error, count, limit int) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, err)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{
			Code:        errcode.BackupTooManyUnverified,
			Args:        map[string]any{"count": int64(count), "limit": int64(limit)},
			RequestBody: nil,
		})

	doc := decodeError(t, rec.Body.String())

	for _, want := range []string{strconv.Itoa(count), strconv.Itoa(limit)} {
		if !strings.Contains(doc.Message, want) {
			t.Fatalf("message = %q, want %q", doc.Message, want)
		}
	}
}

// TestTooManyUnverifiedSentinelAnswer proves that a refusal that carries no
// count answers the same coded 422 with zero for the count and the bound: the
// mapping never falls back to an uncoded 500.
func TestTooManyUnverifiedSentinelAnswer(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, backup.ErrTooManyUnverified)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{
			Code:        errcode.BackupTooManyUnverified,
			Args:        map[string]any{"count": int64(0), "limit": int64(0)},
			RequestBody: nil,
		})
}

// TestGetBackupShowsTheUnverifiedKeyLimit proves that the answer of a
// succeeded backup shows the bound on its unverified keys as an effective
// value, with the count of the keys of the manifest.
func TestGetBackupShowsTheUnverifiedKeyLimit(t *testing.T) {
	t.Parallel()

	fix := quickBackupFixture(t, nil)

	id := startBackup(t, fix.handler)
	doc := pollBackupSucceeded(t, fix.handler, id)

	if doc.UnverifiedKeyLimit != backup.MaxUnverifiedKeys {
		t.Fatalf("unverifiedKeyLimit = %d, want %d", doc.UnverifiedKeyLimit, backup.MaxUnverifiedKeys)
	}

	if doc.UnverifiedKeyCount != 0 {
		t.Fatalf("unverifiedKeyCount = %d, want 0", doc.UnverifiedKeyCount)
	}
}
