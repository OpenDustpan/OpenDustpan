package jobstore_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// TestCheckPlanBodyAcceptsAReserve proves that the plan row of a reserve, which
// has no part yet, passes the restore check, and that a row of another plan
// does not.
func TestCheckPlanBodyAcceptsAReserve(t *testing.T) {
	t.Parallel()

	const reserve = `{"id":"pln-1","itemKey":"ik-1","stateVersion":"sv-1","parts":[],` +
		`"createdAt":"2026-10-21T09:00:00Z","expiresAt":"2027-10-21T09:00:00Z","redactedAt":"0001-01-01T00:00:00Z"}`

	if err := jobstore.CheckPlanBody("pln-1", reserve); err != nil {
		t.Errorf("check of a reserve = %v, want nil", err)
	}

	if err := jobstore.CheckPlanBody("pln-2", reserve); !errors.Is(err, jobstore.ErrDamaged) {
		t.Errorf("check of a reserve under another ID = %v, want ErrDamaged", err)
	}
}

// TestCheckPlanBodyPartRules proves that the restore check refuses the parts
// that PutPlan refuses.
func TestCheckPlanBodyPartRules(t *testing.T) {
	t.Parallel()

	const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	body := func(parts string) string {
		return `{"id":"pln-1","itemKey":"ik-1","stateVersion":"sv-1","parts":` + parts +
			`,"createdAt":"2026-10-21T09:00:00Z","expiresAt":"2027-10-21T09:00:00Z","redactedAt":"0001-01-01T00:00:00Z"}`
	}

	part := func(kind string, size int64) string {
		return fmt.Sprintf(`{"kind":%q,"size":%d,"sha256":%q,"secret":%t}`, kind, size, hash, kind == "plan-apply")
	}

	cases := []struct {
		name, parts string
		ok          bool
	}{
		{name: "one display part", parts: "[" + part("plan-display", 1) + "]", ok: true},
		{
			name:  "a display and an apply part",
			parts: "[" + part("plan-display", 1) + "," + part("plan-apply", 1) + "]", ok: true,
		},
		{name: "a part at the size limit", parts: "[" + part("plan-apply", job.MaxPlanPart) + "]", ok: true},
		{name: "one kind twice", parts: "[" + part("plan-display", 1) + "," + part("plan-display", 1) + "]", ok: false},
		{name: "a part above the size limit", parts: "[" + part("plan-apply", job.MaxPlanPart+1) + "]", ok: false},
		{name: "a negative size", parts: "[" + part("plan-apply", -1) + "]", ok: false},
		{name: "an unknown kind", parts: "[" + part("other", 1) + "]", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := jobstore.CheckPlanBody("pln-1", body(tc.parts))
			if tc.ok && err != nil {
				t.Errorf("check = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, jobstore.ErrDamaged) {
				t.Errorf("check = %v, want ErrDamaged", err)
			}
		})
	}
}
