// Package backuptest holds the conformance suite of the ports of package
// backup.
package backuptest

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// hostileBodyBytes is the size of the long hostile body: 1 MiB.
const hostileBodyBytes = 1 << 20

// hostileJobBodies are bodies that no adapter may accept as a job.
func hostileJobBodies() map[string]string {
	return map[string]string{
		"text that is not JSON": "not json",
		"an empty object":       "{}",
		"null":                  "null",
		"an array":              "[]",
		"an empty body":         "",
		"a body cut short":      `{"spec":{"unit":`,
		"a long body":           strings.Repeat("x", hostileBodyBytes),
		"control characters":    "\x00\x1b[31m\r\n",
	}
}

// JobRowCheckerFindings runs the cases of the JobRowChecker port against c and
// returns one text for each case that c fails. accept holds rows that the
// store of c reads: the suite expects c to accept each of them, and a call
// with no row fails, because an adapter that refuses every row must not pass.
// The refuse cases are the same for every adapter.
func JobRowCheckerFindings(c backup.JobRowChecker, accept []backup.JobRow) []string {
	var findings []string

	if len(accept) == 0 {
		findings = append(findings, "the suite has no row that the adapter must accept")
	}

	for i := range accept {
		if err := c.CheckJobRow(accept[i]); err != nil {
			findings = append(findings, fmt.Sprintf("accept row %d (%s): got %v, want nil", i, accept[i].ID, err))
		}
	}

	var base backup.JobRow
	if len(accept) > 0 {
		base = accept[0]
	}

	for name, body := range hostileJobBodies() {
		row := base
		row.ID, row.UnitID, row.Body = "job-dmg1", "ws-1", body

		findings = append(findings, refuseFindings(c, name, row)...)
	}

	return findings
}

// refuseFindings checks that c refuses row with a damaged job error that names
// the job and the unit, and no body text.
func refuseFindings(c backup.JobRowChecker, name string, row backup.JobRow) []string {
	err := c.CheckJobRow(row)
	if err == nil {
		return []string{fmt.Sprintf("refuse %s: got nil, want a damaged job error", name)}
	}

	damaged, ok := errors.AsType[*backup.DamagedJobError](err)
	if !ok || damaged == nil {
		return []string{fmt.Sprintf("refuse %s: got %v, want a *backup.DamagedJobError", name, err)}
	}

	var findings []string

	if !errors.Is(err, backup.ErrCorrupt) {
		findings = append(findings, fmt.Sprintf("refuse %s: the error does not wrap ErrCorrupt", name))
	}

	if damaged.JobID() != "job-dmg1" || damaged.UnitID() != "ws-1" || damaged.Reason() == "" {
		findings = append(findings, fmt.Sprintf("refuse %s: the error names %q, %q, %q", name,
			damaged.JobID(), damaged.UnitID(), damaged.Reason()))
	}

	if strings.ContainsAny(err.Error(), "\x00\r\n\x1b") || len(err.Error()) > 1024 {
		findings = append(
			findings,
			fmt.Sprintf("refuse %s: the text is not bounded or holds a control character", name),
		)
	}

	return findings
}

// RunJobRowChecker runs the conformance suite of the JobRowChecker port
// against c. accept holds rows that the store of c reads.
func RunJobRowChecker(t *testing.T, c backup.JobRowChecker, accept []backup.JobRow) {
	t.Helper()

	for _, finding := range JobRowCheckerFindings(c, accept) {
		t.Error(finding)
	}
}
