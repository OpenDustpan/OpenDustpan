package jobcheck_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/jobcheck"
)

// TestCheckJobRowRefusesHostileRows is the table of hostile job rows for the
// layer of the job store rules. Each row must fail with a damaged job error
// that names the job, the unit and a reason, and no body text.
func TestCheckJobRowRefusesHostileRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "text that is not JSON", body: "not json"},
		{name: "an empty object", body: "{}"},
		{name: "null", body: "null"},
		{name: "an array", body: "[]"},
		{name: "an empty body", body: ""},
		{name: "a body cut short", body: `{"spec":{"unit":`},
		{name: "a very long body that is not JSON", body: strings.Repeat("x", 1<<20)},
		{name: "a control character", body: "\x00\x1b[31m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := backup.JobRow{
				ID: "job-dmg1", Namespace: "acme", NamespaceID: "ns-1", UnitID: "ws-1", State: "queued",
				Wait: "", Operation: "", Key: "", Body: tc.body, Seq: 1, Phase: 0, Version: 1, CreatedMs: 1,
				UpdatedMs: 1, ChangedMs: 1, Changes: false,
			}

			err := jobcheck.Checker{}.CheckJobRow(row)

			damaged, ok := errors.AsType[*backup.DamagedJobError](err)
			if !ok || damaged == nil {
				t.Fatalf("check = %v, want a damaged job error", err)
			}

			if damaged.JobID != "job-dmg1" || damaged.UnitID != "ws-1" || damaged.Reason == "" {
				t.Fatalf("damaged = %+v, want the job, the unit and a reason", damaged)
			}

			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}

			if len(err.Error()) > 1024 {
				t.Fatalf("the text holds %d bytes, want a bound", len(err.Error()))
			}
		})
	}
}
