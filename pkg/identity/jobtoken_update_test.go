package identity_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// updateGuardCase is one row of the update guard table.
type updateGuardCase struct {
	name        string
	p           identity.Principal
	facts       *fakeJobFacts
	noFacts     bool
	refusals    *fakeRefusals
	noRefusals  bool
	unit        identity.Object
	mutates     bool
	wantRefused bool
	wantReads   int
}

// TestCheckJobTokenUpdate proves the update guard refuses a job token that may
// not save state and passes every other caller.
func TestCheckJobTokenUpdate(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	other := jobTestUnit(t, "acme", "web/other", "unt-2")

	planOnly := func() *fakeJobFacts {
		return &fakeJobFacts{
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, err: nil, reads: 0, gotJobID: "",
		}
	}

	changing := func() *fakeJobFacts {
		return &fakeJobFacts{
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, err: nil, reads: 0, gotJobID: "",
		}
	}

	cases := []updateGuardCase{
		{
			name: "a plan-only job may not start a mutating update", p: jobToken(unit, "job-a"),
			facts: planOnly(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: true, wantRefused: true, wantReads: 1,
		},
		{
			name: "a plan-only job may preview", p: jobToken(unit, "job-a"),
			facts: planOnly(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: false, wantRefused: false, wantReads: 1,
		},
		{
			name: "a plan-only job may dry run", p: jobToken(unit, "job-a"),
			facts: planOnly(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: false, wantRefused: false, wantReads: 1,
		},
		{
			name: "a changing job may start a mutating update", p: jobToken(unit, "job-a"),
			facts: changing(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: true, wantRefused: false, wantReads: 1,
		},
		{
			name: "a read error refuses", p: jobToken(unit, "job-a"),
			facts: &fakeJobFacts{
				fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
				err:  errJobReadFailed, reads: 0, gotJobID: "",
			},
			noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false,
			unit: unit, mutates: false, wantRefused: true, wantReads: 1,
		},
		{
			name: "a missing job refuses", p: jobToken(unit, "job-a"),
			facts: &fakeJobFacts{
				fact: identity.JobFact{Unit: identity.Object{}, Changes: false, Source: ""},
				err:  errJobNotFound, reads: 0, gotJobID: "",
			},
			noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false,
			unit: unit, mutates: false, wantRefused: true, wantReads: 1,
		},
		{
			name: "a job of another unit refuses", p: jobToken(unit, "job-a"),
			facts: &fakeJobFacts{
				fact: identity.JobFact{Unit: other, Changes: true, Source: "api"},
				err:  nil, reads: 0, gotJobID: "",
			},
			noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false,
			unit: unit, mutates: false, wantRefused: true, wantReads: 1,
		},
		{
			name: "an update of another unit refuses before a read", p: jobToken(unit, "job-a"),
			facts: &fakeJobFacts{
				fact: identity.JobFact{Unit: other, Changes: true, Source: "api"},
				err:  nil, reads: 0, gotJobID: "",
			},
			noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false,
			unit: other, mutates: true, wantRefused: true, wantReads: 0,
		},
		{
			name: "no facts port refuses", p: jobToken(unit, "job-a"),
			facts: nil, noFacts: true, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: false, wantRefused: true, wantReads: 0,
		},
		{
			name: "an inconsistent token refuses",
			p: identity.Principal{
				Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "",
				TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
				Kind: identity.KindPerson, Credential: identity.Credential(""), JobID: "job-a",
				JobUnit: identity.Object{},
			},
			facts: changing(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: true, wantRefused: true, wantReads: 0,
		},
		{
			name: "no job token passes without a read", p: plainPrincipal(identity.CredentialAccessToken),
			facts: changing(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: nil},
			noRefusals: false, unit: unit, mutates: true, wantRefused: false, wantReads: 0,
		},
		{
			name: "a failed record still refuses", p: jobToken(unit, "job-a"),
			facts: planOnly(), noFacts: false, refusals: &fakeRefusals{recorded: nil, err: errRecordRefused},
			noRefusals: false, unit: unit, mutates: true, wantRefused: true, wantReads: 1,
		},
		{
			name: "no recorder refuses", p: jobToken(unit, "job-a"),
			facts: planOnly(), noFacts: false, refusals: nil, noRefusals: true,
			unit: unit, mutates: true, wantRefused: true, wantReads: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runUpdateGuardCase(t, tc)
		})
	}
}

// runUpdateGuardCase runs one row of the update guard table.
func runUpdateGuardCase(t *testing.T, tc updateGuardCase) {
	t.Helper()

	var facts identity.JobFacts
	if !tc.noFacts {
		facts = tc.facts
	}

	var refusals identity.JobTokenRefusals
	if !tc.noRefusals {
		refusals = tc.refusals
	}

	err := identity.CheckJobTokenUpdate(
		t.Context(), facts, refusals,
		identity.JobTokenUpdate{Principal: tc.p, Unit: tc.unit, Mutates: tc.mutates},
	)

	checkUpdateGuardError(t, tc, err)
	checkUpdateGuardReads(t, tc)
	checkUpdateGuardRefusals(t, tc)
}

// checkUpdateGuardError checks the error of one row.
func checkUpdateGuardError(t *testing.T, tc updateGuardCase, err error) {
	t.Helper()

	if !tc.wantRefused {
		if err != nil {
			t.Fatalf("CheckJobTokenUpdate = %v, want allowed", err)
		}

		return
	}

	if err == nil {
		t.Fatal("CheckJobTokenUpdate = nil, want ErrJobTokenAction and ErrDenied")
	}

	if !errors.Is(err, identity.ErrJobTokenAction) || !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("CheckJobTokenUpdate = %v, want ErrJobTokenAction and ErrDenied", err)
	}

	if !strings.Contains(err.Error(), "job token") || !strings.Contains(err.Error(), "update") {
		t.Errorf("error %q does not name the job token and the update", err)
	}

	if tc.refusals != nil && tc.refusals.err != nil && !errors.Is(err, errRecordRefused) {
		t.Errorf("CheckJobTokenUpdate = %v, want the record error %v", err, errRecordRefused)
	}
}

// checkUpdateGuardReads checks the reads of the facts fake of one row.
func checkUpdateGuardReads(t *testing.T, tc updateGuardCase) {
	t.Helper()

	if tc.facts != nil && tc.facts.reads != tc.wantReads {
		t.Errorf("the guard read the job %d times, want %d", tc.facts.reads, tc.wantReads)
	}
}

// checkUpdateGuardRefusals checks the recorded refusals of one row.
func checkUpdateGuardRefusals(t *testing.T, tc updateGuardCase) {
	t.Helper()

	if tc.refusals == nil {
		return
	}

	if tc.wantRefused {
		assertOneRefusal(t, tc.refusals, identity.JobTokenRefusal{
			Subject: tc.p.Subject, JobID: tc.p.JobID, Unit: tc.p.JobUnit, Action: identity.ActionUnitWrite,
			Reason: "access.job-token-action",
		})

		return
	}

	if len(tc.refusals.recorded) != 0 {
		t.Errorf("the guard recorded %d refusals, want 0", len(tc.refusals.recorded))
	}
}

// errPrivateRecord is a record error whose text must never reach a caller.
var errPrivateRecord = errors.New("audit store at db.internal.example refused the entry")

// TestJobTokenRefusalText proves that the caller text of a job token refusal
// names the refused action and never carries the text of a wrapped error.
func TestJobTokenRefusalText(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")

	update := func(t *testing.T) error {
		t.Helper()

		facts := &fakeJobFacts{
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, err: nil, reads: 0, gotJobID: "",
		}

		return identity.CheckJobTokenUpdate(t.Context(), facts, &fakeRefusals{recorded: nil, err: errPrivateRecord},
			identity.JobTokenUpdate{Principal: jobToken(unit, "job-a"), Unit: unit, Mutates: true})
	}

	action := func(t *testing.T) error {
		t.Helper()

		next := &recordingNext{
			allow: true, deny: "", scope: emptyTestScope(), err: nil, asked: 0, got: zeroPrincipal(),
		}
		rule := identity.JobTokenRule{
			Next: next, Jobs: matchingFacts(jobToken(unit, "job-a")),
			Refusals: &fakeRefusals{recorded: nil, err: errPrivateRecord},
		}

		return rule.Authorize(t.Context(), jobToken(unit, "job-a"), identity.ActionUnitManage, unit)
	}

	other := func(*testing.T) error { return errPrivateRecord }

	cases := []struct {
		name string
		err  func(*testing.T) error
		want string
	}{
		{name: "an update that saves state", err: update, want: "a job token may not start an update that saves state"},
		{name: "a refused action", err: action, want: "a job token may not " + string(identity.ActionUnitManage)},
		{name: "another error", err: other, want: "a job token may not ask for this action"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.err(t)
			if err == nil {
				t.Fatal("got no error, want a refusal")
			}

			got := identity.JobTokenRefusalText(err)
			if got != tc.want {
				t.Errorf("JobTokenRefusalText = %q, want %q", got, tc.want)
			}

			if strings.Contains(got, "db.internal.example") {
				t.Errorf("JobTokenRefusalText = %q, carries the text of the record error", got)
			}
		})
	}
}

// cancelGuardCase is one row of the cancel guard table.
type cancelGuardCase struct {
	name        string
	p           identity.Principal
	refusals    *fakeRefusals
	noRefusals  bool
	updateJobID string
	wantRefused bool
}

// TestCheckJobTokenCancel proves that a job token may cancel only the update of
// its own job, and that a principal without any part of a job token passes.
func TestCheckJobTokenCancel(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	inconsistent := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.Credential(""), JobID: "job-a", JobUnit: identity.Object{},
	}

	cases := []cancelGuardCase{
		{
			name: "a plan-only job may not cancel the update of an apply job", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "job-b",
			wantRefused: true,
		},
		{
			name: "an apply job may not cancel the update of another job", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "job-b",
			wantRefused: true,
		},
		{
			name: "a job token may not cancel an update that no job created", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "",
			wantRefused: true,
		},
		{
			name: "a job token may cancel the update of its own job", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "job-a",
			wantRefused: false,
		},
		{
			name: "no job token may cancel any update", p: plainPrincipal(identity.CredentialAccessToken),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "job-b",
			wantRefused: false,
		},
		{
			name: "an inconsistent token refuses", p: inconsistent,
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, updateJobID: "job-a",
			wantRefused: true,
		},
		{
			name: "no recorder refuses", p: jobToken(unit, "job-a"),
			refusals: nil, noRefusals: true, updateJobID: "job-b", wantRefused: true,
		},
		{
			name: "a failed record still refuses", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: errRecordRefused}, noRefusals: false,
			updateJobID: "job-b", wantRefused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCancelGuardCase(t, tc)
		})
	}
}

// runCancelGuardCase runs one row of the cancel guard table.
func runCancelGuardCase(t *testing.T, tc cancelGuardCase) {
	t.Helper()

	var refusals identity.JobTokenRefusals
	if !tc.noRefusals {
		refusals = tc.refusals
	}

	err := identity.CheckJobTokenCancel(t.Context(), refusals, tc.p, tc.updateJobID)

	if !tc.wantRefused {
		if err != nil {
			t.Fatalf("CheckJobTokenCancel = %v, want allowed", err)
		}

		if tc.refusals != nil && len(tc.refusals.recorded) != 0 {
			t.Errorf("the guard recorded %d refusals, want 0", len(tc.refusals.recorded))
		}

		return
	}

	if err == nil || !errors.Is(err, identity.ErrJobTokenAction) || !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("CheckJobTokenCancel = %v, want ErrJobTokenAction and ErrDenied", err)
	}

	if got := identity.JobTokenRefusalText(err); got != "a job token may not cancel the update of another job" {
		t.Errorf("JobTokenRefusalText = %q, want the cancel refusal text", got)
	}

	if tc.refusals == nil {
		return
	}

	assertOneRefusal(t, tc.refusals, identity.JobTokenRefusal{
		Subject: tc.p.Subject, JobID: tc.p.JobID, Unit: tc.p.JobUnit, Action: identity.ActionUnitWrite,
		Reason: "access.job-token-action",
	})
}

// tagGuardCase is one row of the tag guard table.
type tagGuardCase struct {
	name        string
	p           identity.Principal
	refusals    *fakeRefusals
	noRefusals  bool
	wantRefused bool
}

// TestCheckJobTokenTagEdit proves that no job token may edit the tags of a
// unit, and that a principal without any part of a job token passes.
func TestCheckJobTokenTagEdit(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	inconsistent := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialJobToken, JobID: "", JobUnit: identity.Object{},
	}

	cases := []tagGuardCase{
		{
			name: "a plan-only job may not edit tags", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, wantRefused: true,
		},
		{
			name: "an apply job may not edit tags", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, wantRefused: true,
		},
		{
			name: "an inconsistent token refuses", p: inconsistent,
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, wantRefused: true,
		},
		{
			name: "no recorder refuses", p: jobToken(unit, "job-a"),
			refusals: nil, noRefusals: true, wantRefused: true,
		},
		{
			name: "a failed record still refuses", p: jobToken(unit, "job-a"),
			refusals: &fakeRefusals{recorded: nil, err: errRecordRefused}, noRefusals: false,
			wantRefused: true,
		},
		{
			name: "no job token passes", p: plainPrincipal(identity.CredentialAccessToken),
			refusals: &fakeRefusals{recorded: nil, err: nil}, noRefusals: false, wantRefused: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runTagGuardCase(t, tc)
		})
	}
}

// runTagGuardCase runs one row of the tag guard table.
func runTagGuardCase(t *testing.T, tc tagGuardCase) {
	t.Helper()

	var refusals identity.JobTokenRefusals
	if !tc.noRefusals {
		refusals = tc.refusals
	}

	err := identity.CheckJobTokenTagEdit(t.Context(), refusals, tc.p)

	if !tc.wantRefused {
		if err != nil {
			t.Fatalf("CheckJobTokenTagEdit = %v, want allowed", err)
		}

		if tc.refusals != nil && len(tc.refusals.recorded) != 0 {
			t.Errorf("the guard recorded %d refusals, want 0", len(tc.refusals.recorded))
		}

		return
	}

	if err == nil || !errors.Is(err, identity.ErrJobTokenAction) || !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("CheckJobTokenTagEdit = %v, want ErrJobTokenAction and ErrDenied", err)
	}

	if got := identity.JobTokenRefusalText(err); got != "a job token may not edit the tags of a unit" {
		t.Errorf("JobTokenRefusalText = %q, want the tag refusal text", got)
	}

	if tc.refusals == nil {
		return
	}

	assertOneRefusal(t, tc.refusals, identity.JobTokenRefusal{
		Subject: tc.p.Subject, JobID: tc.p.JobID, Unit: tc.p.JobUnit, Action: identity.ActionUnitWrite,
		Reason: "access.job-token-action",
	})
}
