package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRegistryFilesTooLargeAnswer proves that the refusal of a backup at a
// limit of the registry files answers a coded 422 whose arguments name the
// size and the limit, also when the refusal is wrapped.
func TestRegistryFilesTooLargeAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		refusal   *backup.RegistryFilesTooLargeError
		wantSize  int64
		wantLimit int64
	}{
		{
			name: "one file above the file limit",
			refusal: &backup.RegistryFilesTooLargeError{
				Release: "acme/web", Version: "1.0.0", Limit: backup.RegistryFileLimitFile,
				Size: backup.MaxRegistryFileBytes + 1, Total: backup.MaxRegistryFileBytes + 1,
				Max: backup.MaxRegistryFileBytes,
			},
			wantSize:  backup.MaxRegistryFileBytes + 1,
			wantLimit: backup.MaxRegistryFileBytes,
		},
		{
			name: "namespace total above the namespace limit",
			refusal: &backup.RegistryFilesTooLargeError{
				Release: "acme/web", Version: "1.0.1", Limit: backup.RegistryFileLimitNamespace,
				Size: 1, Total: backup.MaxRegistryFilesTotalBytes + 1,
				Max: backup.MaxRegistryFilesTotalBytes,
			},
			wantSize:  backup.MaxRegistryFilesTotalBytes + 1,
			wantLimit: backup.MaxRegistryFilesTotalBytes,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRegistryFilesAnswer(t, tc.refusal, tc.wantSize, tc.wantLimit)
			checkRegistryFilesAnswer(t, fmt.Errorf("run backup bkp-a: %w", tc.refusal), tc.wantSize, tc.wantLimit)
		})
	}
}

// checkRegistryFilesAnswer serves err and proves that the answer is the coded
// 422 with the size and the limit as arguments.
func checkRegistryFilesAnswer(t *testing.T, err error, size, limit int64) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, err)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{
			Code:        errcode.BackupRegistryFilesTooLarge,
			Args:        map[string]any{"size": size, "limit": limit},
			RequestBody: nil,
		})
}

// TestRegistryFilesTooLargeSentinelAnswer proves that a refusal that carries no
// size answers the same coded 422 with zero for the size and the limit: the
// mapping never falls back to an uncoded 500.
func TestRegistryFilesTooLargeSentinelAnswer(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, backup.ErrRegistryFilesTooLarge)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, rec.Body.Bytes(),
		errcodetest.Want{
			Code:        errcode.BackupRegistryFilesTooLarge,
			Args:        map[string]any{"size": int64(0), "limit": int64(0)},
			RequestBody: nil,
		})
}

// TestRegistryFilesTooLargeAnswerCleansHostileNames proves that a release name
// with control characters never reaches the text of the answer.
func TestRegistryFilesTooLargeAnswerCleansHostileNames(t *testing.T) {
	t.Parallel()

	refusal := &backup.RegistryFilesTooLargeError{
		Release: "acme/web\r\nX-Evil: 1\x00", Version: "1.0.0\n", Limit: backup.RegistryFileLimitFile,
		Size: backup.MaxRegistryFileBytes + 1, Total: backup.MaxRegistryFileBytes + 1,
		Max: backup.MaxRegistryFileBytes,
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, refusal)

	doc := decodeError(t, rec.Body.String())
	if strings.ContainsAny(doc.Message, "\r\n\x00") {
		t.Fatalf("message = %q, want no control character", doc.Message)
	}
}

// TestRegistryFilesTooLargeAnswerLimitsTheText proves that a release name that
// a publisher chose cannot make the text of the answer grow without a limit.
func TestRegistryFilesTooLargeAnswerLimitsTheText(t *testing.T) {
	t.Parallel()

	const answerLimit = 600 // the text limit of a cause plus the fixed words

	refusal := &backup.RegistryFilesTooLargeError{
		Release: strings.Repeat("r", 100_000), Version: "1.0.0", Limit: backup.RegistryFileLimitFile,
		Size: backup.MaxRegistryFileBytes + 1, Total: backup.MaxRegistryFileBytes + 1,
		Max: backup.MaxRegistryFileBytes,
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/backups", http.NoBody)
	rec := httptest.NewRecorder()

	apiserver.WriteErrorForTest(rec, r, refusal)

	doc := decodeError(t, rec.Body.String())
	if len(doc.Message) > answerLimit {
		t.Fatalf("message has %d bytes, want at most %d", len(doc.Message), answerLimit)
	}
}
