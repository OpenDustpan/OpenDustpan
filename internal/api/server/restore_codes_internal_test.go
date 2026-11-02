package server

import (
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreErrorCodesAreInTheCatalog proves that each code that a restore
// row can hold is in the error catalog, so a client finds its meaning.
func TestRestoreErrorCodesAreInTheCatalog(t *testing.T) {
	t.Parallel()

	for _, code := range backup.RestoreErrorCodes() {
		if _, ok := errcode.Lookup(string(code)); !ok {
			t.Errorf("restore error code %q is not in the catalog", code)
		}
	}
}

// TestRestoreAnswerCarriesTheErrorCode proves that the restore answer shows
// the code that the row stores, and no code for a row with no report.
func TestRestoreAnswerCarriesTheErrorCode(t *testing.T) {
	t.Parallel()

	var record backup.RestoreRecord

	if got := errorCodeOf(record); got != "" {
		t.Fatalf("errorCodeOf of a row with no report = %q, want empty", got)
	}

	report := backup.DevStripReport{
		Principals: nil, OtherPrincipals: 0, OtherCounts: nil, Classes: nil, Approvers: nil, Warnings: nil,
		PreBackupFound: nil, NotHeld: nil, ErrorCode: backup.CodeLiveBlobsMissing,
	}
	record.DevReport = &report

	if got := errorCodeOf(record); got != string(backup.CodeLiveBlobsMissing) {
		t.Fatalf("errorCodeOf = %q, want %q", got, backup.CodeLiveBlobsMissing)
	}

	if got := summarizeRestore(record).ErrorCode; got != string(backup.CodeLiveBlobsMissing) {
		t.Fatalf("summary errorCode = %q, want %q", got, backup.CodeLiveBlobsMissing)
	}
}
