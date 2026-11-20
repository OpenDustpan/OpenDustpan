package backuptest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/backuptest"
)

// bodyChecker accepts a row whose body is "ok" and refuses every other row
// with a well-formed damaged job error.
type bodyChecker struct{}

func (bodyChecker) CheckJobRow(row backup.JobRow) error {
	if row.Body == "ok" {
		return nil
	}

	return backup.NewDamagedJobError(row.ID, row.UnitID, "the body is not JSON")
}

// refuseAll refuses every row, even a valid one.
type refuseAll struct{}

func (refuseAll) CheckJobRow(row backup.JobRow) error {
	return backup.NewDamagedJobError(row.ID, row.UnitID, "refused")
}

// acceptAll accepts every row.
type acceptAll struct{}

func (acceptAll) CheckJobRow(backup.JobRow) error { return nil }

// plainError refuses with an error that is not a damaged job error.
type plainError struct{}

func (plainError) CheckJobRow(backup.JobRow) error { return backup.ErrCorrupt }

func validRows() []backup.JobRow {
	var row backup.JobRow
	row.ID, row.Namespace, row.UnitID, row.Body = "job-ok", "acme", "ws-1", "ok"

	return []backup.JobRow{row}
}

// TestSuitePassesAConformingAdapter proves the suite accepts an adapter that
// accepts a valid row and refuses every hostile row.
func TestSuitePassesAConformingAdapter(t *testing.T) {
	t.Parallel()

	backuptest.RunJobRowChecker(t, bodyChecker{}, validRows())
}

// TestSuiteFailsANonConformingAdapter proves each way to break the port fails
// the suite: refuse every row, accept every row, refuse with another error
// type, and give no row to accept.
func TestSuiteFailsANonConformingAdapter(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		checker backup.JobRowChecker
		accept  []backup.JobRow
	}{
		"an adapter that refuses every row":     {checker: refuseAll{}, accept: validRows()},
		"an adapter that accepts every row":     {checker: acceptAll{}, accept: validRows()},
		"an adapter with another error type":    {checker: plainError{}, accept: validRows()},
		"a suite with no row to accept":         {checker: bodyChecker{}, accept: nil},
		"an adapter that refuses a valid row":   {checker: refuseAll{}, accept: validRows()},
		"an adapter that accepts a hostile row": {checker: acceptAll{}, accept: validRows()},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if findings := backuptest.JobRowCheckerFindings(tc.checker, tc.accept); len(findings) == 0 {
				t.Fatal("the suite found nothing, want a finding")
			}
		})
	}
}
