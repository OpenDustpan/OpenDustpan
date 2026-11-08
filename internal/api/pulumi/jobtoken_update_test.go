package pulumi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errGuardRead is the read error of the job facts fake.
var errGuardRead = errors.New("read failed")

// guardFacts is a JobFacts whose answer the test sets. It counts the reads.
type guardFacts struct {
	fact  identity.JobFact
	err   error
	reads int
}

// JobFact implements identity.JobFacts.
func (f *guardFacts) JobFact(context.Context, string) (identity.JobFact, error) {
	f.reads++

	return f.fact, f.err
}

// guardRefusals keeps each refusal of the update guard.
type guardRefusals struct {
	recorded []identity.JobTokenRefusal
}

// RecordJobTokenRefusal implements identity.JobTokenRefusals.
func (f *guardRefusals) RecordJobTokenRefusal(
	_ context.Context, r identity.JobTokenRefusal,
) error {
	f.recorded = append(f.recorded, r)

	return nil
}

// guardUnit returns the unit of the update fixture.
func guardUnit(t *testing.T) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit
}

// guardPrincipal returns the principal that the test authenticator signs in.
func guardPrincipal(credential identity.Credential, jobID string, unit identity.Object) identity.Principal {
	return identity.Principal{
		Subject: "tester", DisplayName: "Tester", Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: credential, JobID: jobID, JobUnit: unit,
	}
}

// guardAuthenticator signs in the test token as p.
type guardAuthenticator struct {
	principal identity.Principal
}

// AuthenticateToken implements identity.Authenticator.
func (a guardAuthenticator) AuthenticateToken(
	_ context.Context, raw string,
) (identity.Principal, error) {
	if raw != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return a.principal, nil
}

// newGuardFixture builds an update fixture whose caller is p, with facts and
// refusals for the update guard.
func newGuardFixture(
	t *testing.T, facts identity.JobFacts, refusals identity.JobTokenRefusals, p identity.Principal,
) updateFixture {
	t.Helper()

	return newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		guardAuthenticator{principal: p},
		fixtureOptions{
			manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil,
			jobs: facts, refusals: refusals,
		},
	)
}

// updateGuardAnswer is the Pulumi error shape of a refused request.
type updateGuardAnswer struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
}

// updateGuardError decodes the Pulumi error shape of a refused request.
func updateGuardError(t *testing.T, body string) updateGuardAnswer {
	t.Helper()

	var answer updateGuardAnswer

	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}

	return answer
}

// guardCreateCase is one row of the create guard table.
type guardCreateCase struct {
	name         string
	target       string
	body         string
	fact         identity.JobFact
	factErr      error
	credential   identity.Credential
	wantStatus   int
	wantReads    int
	wantRefusals int
}

// TestJobTokenUpdateGuardRefusesACreate proves a plan-only job token cannot
// create an update that saves state, and that a preview, a dry run and a
// changing job pass.
func TestJobTokenUpdateGuardRefusesACreate(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	other, err := identity.UnitObject("pulumi", unit.Namespace(), "web/other")
	if err != nil {
		t.Fatalf("other unit: %v", err)
	}

	cases := []guardCreateCase{
		{
			name:   "a plan-only job may not create a mutating update",
			target: "/api/stacks/acme/web/prod/update", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name:   "a plan-only job may preview",
			target: "/api/stacks/acme/web/prod/preview", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0,
		},
		{
			name:   "a plan-only job may dry run",
			target: "/api/stacks/acme/web/prod/update", body: `{"options":{"dryRun":true}}`,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0,
		},
		{
			name:   "a changing job may create a mutating update",
			target: "/api/stacks/acme/web/prod/update", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0,
		},
		{
			name:   "a read error refuses",
			target: "/api/stacks/acme/web/prod/preview", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: errGuardRead,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name:   "a job of another unit refuses",
			target: "/api/stacks/acme/web/prod/preview", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: other, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name:   "a principal with no job token passes without a read",
			target: "/api/stacks/acme/web/prod/update", body: `{"options":{"dryRun":false}}`,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialAccessToken, wantStatus: http.StatusOK,
			wantReads: 0, wantRefusals: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runGuardCreateCase(t, tc, unit)
		})
	}
}

// runGuardCreateCase runs one row of the create guard table.
func runGuardCreateCase(t *testing.T, tc guardCreateCase, unit identity.Object) {
	t.Helper()

	facts := &guardFacts{fact: tc.fact, err: tc.factErr, reads: 0}
	refusals := &guardRefusals{recorded: nil}
	jobID := "job-1"

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		jobID = ""
		callerUnit = identity.Object{}
	}

	f := newGuardFixture(
		t, facts, refusals, guardPrincipal(tc.credential, jobID, callerUnit),
	)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, tc.target, tc.body))
	if status != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
	}

	if facts.reads != tc.wantReads {
		t.Errorf("the guard read the job %d times, want %d", facts.reads, tc.wantReads)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	if tc.wantRefusals == 1 {
		checkGuardRefusal(t, refusals.recorded[0], unit)
	}

	if tc.wantStatus != http.StatusForbidden {
		return
	}

	answer := updateGuardError(t, body)
	if answer.Code != http.StatusForbidden || answer.ErrorCode != "access.job-token-action" {
		t.Errorf(
			"error = code %d, errorCode %q, want 403 access.job-token-action",
			answer.Code, answer.ErrorCode,
		)
	}

	if !strings.Contains(answer.Message, "job token") || !strings.Contains(answer.Message, "update") {
		t.Errorf("message %q does not name the job token and the update", answer.Message)
	}
}

// guardStartCase is one row of the start guard table.
type guardStartCase struct {
	name         string
	plan         stack.Plan
	fact         identity.JobFact
	wantStatus   int
	wantRefusals int
}

// TestJobTokenUpdateGuardRefusesAStart proves a plan-only job token cannot
// start an update that saves state, and that a preview passes.
func TestJobTokenUpdateGuardRefusesAStart(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	cases := []guardStartCase{
		{
			name: "a plan-only job may not start a mutating update",
			plan: stack.Plan{
				Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, JobID: "job-1",
			},
			fact:       identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			wantStatus: http.StatusForbidden, wantRefusals: 1,
		},
		{
			name: "a plan-only job may start a preview",
			plan: stack.Plan{
				Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, JobID: "job-1",
			},
			fact:       identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			wantStatus: http.StatusOK, wantRefusals: 0,
		},
		{
			name: "a changing job may start a mutating update",
			plan: stack.Plan{
				Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, JobID: "job-1",
			},
			fact:       identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			wantStatus: http.StatusOK, wantRefusals: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runGuardStartCase(t, tc, unit)
		})
	}
}

// runGuardStartCase runs one row of the start guard table.
func runGuardStartCase(t *testing.T, tc guardStartCase, unit identity.Object) {
	t.Helper()

	facts := &guardFacts{fact: tc.fact, err: nil, reads: 0}
	refusals := &guardRefusals{recorded: nil}
	f := newGuardFixture(
		t, facts, refusals, guardPrincipal(identity.CredentialJobToken, "job-1", unit),
	)

	u, err := f.updates.Create(t.Context(), f.ref, tc.plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	target := "/api/stacks/acme/web/prod/update/" + u.ID.String()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, target, ""))
	if status != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	if tc.wantRefusals == 0 {
		return
	}

	checkGuardRefusal(t, refusals.recorded[0], unit)

	got, err := f.updates.Get(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("read update: %v", err)
	}

	if got.Status != stack.StatusNotStarted {
		t.Errorf("update status = %q, want %q", got.Status, stack.StatusNotStarted)
	}
}

// checkGuardRefusal checks the one refusal of the job token update guard.
func checkGuardRefusal(t *testing.T, got identity.JobTokenRefusal, unit identity.Object) {
	t.Helper()

	want := identity.JobTokenRefusal{
		Subject: "tester", JobID: "job-1", Unit: unit, Action: identity.ActionUnitWrite,
		Reason: "access.job-token-action",
	}

	if got != want {
		t.Errorf("refusal = %+v, want %+v", got, want)
	}
}

// errPrivateGuard is an error whose text must never reach the caller.
var errPrivateGuard = errors.New("audit store at db.internal.example refused the entry")

// failingRefusals is a JobTokenRefusals that refuses each record with errPrivateGuard.
type failingRefusals struct{}

// RecordJobTokenRefusal implements identity.JobTokenRefusals.
func (failingRefusals) RecordJobTokenRefusal(context.Context, identity.JobTokenRefusal) error {
	return errPrivateGuard
}

// TestJobTokenUpdateGuardHidesWrappedErrors proves that a refusal of the update
// guard still refuses when the read or the record fails, and that the message
// to the caller never carries the text of the wrapped error.
func TestJobTokenUpdateGuardHidesWrappedErrors(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	cases := []struct {
		name    string
		fact    identity.JobFact
		factErr error
	}{
		{name: "the record fails", fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil},
		{
			name: "the read and the record fail", fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			factErr: errPrivateGuard,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			facts := &guardFacts{fact: tc.fact, err: tc.factErr, reads: 0}
			f := newGuardFixture(
				t, facts, failingRefusals{}, guardPrincipal(identity.CredentialJobToken, "job-1", unit),
			)

			status, body := f.doUpdate(
				t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/update", `{"options":{"dryRun":false}}`),
			)
			if status != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", status, body)
			}

			answer := updateGuardError(t, body)
			if answer.ErrorCode != "access.job-token-action" {
				t.Errorf("errorCode = %q, want access.job-token-action", answer.ErrorCode)
			}

			if strings.Contains(body, "db.internal.example") {
				t.Errorf("body %q carries the text of a wrapped error", body)
			}
		})
	}
}

// guardStateRouteCase is one row of the import and restore guard table.
type guardStateRouteCase struct {
	name         string
	target       string
	body         string
	seed         bool
	fact         identity.JobFact
	factErr      error
	credential   identity.Credential
	wantStatus   int
	wantReads    int
	wantRefusals int
	wantUpdates  int
}

// TestJobTokenUpdateGuardRefusesAnImportOrRestore proves a plan-only job token
// cannot import or restore state, while a changing job passes the guard.
func TestJobTokenUpdateGuardRefusesAnImportOrRestore(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	cases := []guardStateRouteCase{
		{
			name: "a plan-only job may not import", target: "/api/stacks/acme/web/prod/import",
			body: `{"version":3,"deployment":{"resources":[]}}`, seed: false,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1, wantUpdates: 0,
		},
		{
			name: "a changing job may import", target: "/api/stacks/acme/web/prod/import",
			body: `{"version":3,"deployment":{"resources":[]}}`, seed: false,
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0, wantUpdates: 1,
		},
		{
			name: "a principal with no job token imports without a read", target: "/api/stacks/acme/web/prod/import",
			body: `{"version":3,"deployment":{"resources":[]}}`, seed: false,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialAccessToken, wantStatus: http.StatusOK,
			wantReads: 0, wantRefusals: 0, wantUpdates: 1,
		},
		{
			name: "a plan-only job may not restore", target: "/api/stacks/acme/web/prod/restore/1",
			body: "", seed: true,
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1, wantUpdates: 1,
		},
		{
			name: "a changing job may restore", target: "/api/stacks/acme/web/prod/restore/1",
			body: "", seed: true,
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0, wantUpdates: 2,
		},
		{
			name:         "a principal with no job token restores without a read",
			target:       "/api/stacks/acme/web/prod/restore/1",
			body:         "",
			seed:         true,
			fact:         identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			factErr:      nil,
			credential:   identity.CredentialAccessToken,
			wantStatus:   http.StatusOK,
			wantReads:    0,
			wantRefusals: 0,
			wantUpdates:  2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runGuardStateRouteCase(t, tc, unit)
		})
	}
}

// runGuardStateRouteCase runs one row of the import and restore guard table.
func runGuardStateRouteCase(t *testing.T, tc guardStateRouteCase, unit identity.Object) {
	t.Helper()

	facts := &guardFacts{fact: tc.fact, err: tc.factErr, reads: 0}
	refusals := &guardRefusals{recorded: nil}
	jobID := "job-1"

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		jobID = ""
		callerUnit = identity.Object{}
	}

	f := newGuardFixture(t, facts, refusals, guardPrincipal(tc.credential, jobID, callerUnit))

	if tc.seed {
		seedAccessCheckpoint(t, f)
	}

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, tc.target, tc.body))
	if status != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
	}

	if facts.reads != tc.wantReads {
		t.Errorf("the guard read the job %d times, want %d", facts.reads, tc.wantReads)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	if tc.wantRefusals == 1 {
		checkGuardRefusal(t, refusals.recorded[0], unit)
	}

	if tc.wantStatus == http.StatusForbidden {
		answer := updateGuardError(t, body)
		if answer.Code != http.StatusForbidden || answer.ErrorCode != "access.job-token-action" {
			t.Errorf(
				"error = code %d, errorCode %q, want 403 access.job-token-action",
				answer.Code, answer.ErrorCode,
			)
		}
	}

	list, err := f.updates.History(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}

	if len(list) != tc.wantUpdates {
		t.Fatalf("history holds %d updates, want %d", len(list), tc.wantUpdates)
	}
}

// guardCancelCase is one row of the cancel guard table.
type guardCancelCase struct {
	name         string
	updateJobID  string
	fact         identity.JobFact
	credential   identity.Credential
	jobID        string
	wantStatus   int
	wantRefusals int
}

// TestJobTokenCancelGuard proves a job token may cancel only the update of its
// own job, and that a principal with no job token cancels as before.
func TestJobTokenCancelGuard(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	cases := []guardCancelCase{
		{
			name:        "a plan-only job may not cancel the update of an apply job",
			updateJobID: "job-b", fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			credential: identity.CredentialJobToken, jobID: "job-a",
			wantStatus: http.StatusForbidden, wantRefusals: 1,
		},
		{
			name:        "an apply job may not cancel the update of another job",
			updateJobID: "job-b", fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			credential: identity.CredentialJobToken, jobID: "job-a",
			wantStatus: http.StatusForbidden, wantRefusals: 1,
		},
		{
			name:        "a job token may not cancel an update that no job created",
			updateJobID: "", fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			credential: identity.CredentialJobToken, jobID: "job-a",
			wantStatus: http.StatusForbidden, wantRefusals: 1,
		},
		{
			name:        "a job token may cancel the update of its own job",
			updateJobID: "job-a", fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			credential: identity.CredentialJobToken, jobID: "job-a",
			wantStatus: http.StatusOK, wantRefusals: 0,
		},
		{
			name:        "a principal with no job token may cancel any update",
			updateJobID: "job-b", fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			credential: identity.CredentialAccessToken, jobID: "",
			wantStatus: http.StatusOK, wantRefusals: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runGuardCancelCase(t, tc, unit)
		})
	}
}

// runGuardCancelCase runs one row of the cancel guard table.
func runGuardCancelCase(t *testing.T, tc guardCancelCase, unit identity.Object) {
	t.Helper()

	facts := &guardFacts{fact: tc.fact, err: nil, reads: 0}
	refusals := &guardRefusals{recorded: nil}

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		callerUnit = identity.Object{}
	}

	f := newGuardFixture(t, facts, refusals, guardPrincipal(tc.credential, tc.jobID, callerUnit))

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, JobID: tc.updateJobID,
	})
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPost, "/api/stacks/acme/web/prod/update/"+u.ID.String()+"/cancel", "",
	))
	if status != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	got, err := f.updates.Get(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("read update: %v", err)
	}

	if tc.wantStatus == http.StatusForbidden {
		answer := updateGuardError(t, body)
		if answer.Code != http.StatusForbidden || answer.ErrorCode != "access.job-token-action" {
			t.Errorf(
				"error = code %d, errorCode %q, want 403 access.job-token-action",
				answer.Code, answer.ErrorCode,
			)
		}

		if !strings.Contains(answer.Message, "cancel the update of another job") {
			t.Errorf("message %q does not name the cancel refusal", answer.Message)
		}

		if got.Status == stack.StatusCanceled {
			t.Errorf("update status = %q, want it not canceled", got.Status)
		}

		return
	}

	if got.Status != stack.StatusCanceled {
		t.Errorf("update status = %q, want %q", got.Status, stack.StatusCanceled)
	}
}

// tagGuardCase is one row of the tag guard table.
type tagGuardCase struct {
	name         string
	fact         identity.JobFact
	credential   identity.Credential
	jobID        string
	wantStatus   int
	wantRefusals int
	wantTags     int
}

// TestJobTokenTagGuard proves no job token edits the tags of a unit, and that
// a principal with no job token edits them as before.
func TestJobTokenTagGuard(t *testing.T) {
	t.Parallel()

	unit := guardUnit(t)

	cases := []tagGuardCase{
		{
			name:         "a plan-only job may not edit tags",
			fact:         identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			credential:   identity.CredentialJobToken,
			jobID:        "job-1",
			wantStatus:   http.StatusForbidden,
			wantRefusals: 1,
			wantTags:     0,
		},
		{
			name: "an apply job may not edit tags", fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"},
			credential: identity.CredentialJobToken, jobID: "job-1",
			wantStatus: http.StatusForbidden, wantRefusals: 1, wantTags: 0,
		},
		{
			name:         "a principal with no job token may edit tags",
			fact:         identity.JobFact{Unit: unit, Changes: false, Source: "api"},
			credential:   identity.CredentialAccessToken,
			jobID:        "",
			wantStatus:   http.StatusOK,
			wantRefusals: 0,
			wantTags:     1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runTagGuardCase(t, tc, unit)
		})
	}
}

// runTagGuardCase runs one row of the tag guard table.
func runTagGuardCase(t *testing.T, tc tagGuardCase, unit identity.Object) {
	t.Helper()

	facts := &guardFacts{fact: tc.fact, err: nil, reads: 0}
	refusals := &guardRefusals{recorded: nil}

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		callerUnit = identity.Object{}
	}

	f := newGuardFixture(t, facts, refusals, guardPrincipal(tc.credential, tc.jobID, callerUnit))

	status, body := f.doUpdate(t, accessRequest(
		http.MethodPatch, "/api/stacks/acme/web/prod/tags", `{"team":"e2e"}`,
	))
	if status != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
	}

	if facts.reads != 0 {
		t.Errorf("the guard read the job %d times, want 0", facts.reads)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	if len(f.stores.tags) != tc.wantTags {
		t.Fatalf("updated tags %d times, want %d", len(f.stores.tags), tc.wantTags)
	}

	if tc.wantRefusals == 1 {
		checkGuardRefusal(t, refusals.recorded[0], unit)

		answer := updateGuardError(t, body)
		if answer.Code != http.StatusForbidden || answer.ErrorCode != "access.job-token-action" {
			t.Errorf(
				"error = code %d, errorCode %q, want 403 access.job-token-action",
				answer.Code, answer.ErrorCode,
			)
		}

		if !strings.Contains(answer.Message, "edit the tags of a unit") {
			t.Errorf("message %q does not name the tag refusal", answer.Message)
		}
	}
}
