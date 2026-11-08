package tofuhttp_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errWriteGuardRead is the read error of the job facts fake.
var errWriteGuardRead = errors.New("read failed")

// writeGuardFacts is a JobFacts whose answer the test sets. It counts reads.
type writeGuardFacts struct {
	fact  identity.JobFact
	err   error
	reads int
}

// JobFact implements identity.JobFacts.
func (f *writeGuardFacts) JobFact(context.Context, string) (identity.JobFact, error) {
	f.reads++

	return f.fact, f.err
}

// writeGuardRefusals keeps each refusal of the state write guard.
type writeGuardRefusals struct {
	recorded []identity.JobTokenRefusal
}

// RecordJobTokenRefusal implements identity.JobTokenRefusals.
func (f *writeGuardRefusals) RecordJobTokenRefusal(
	_ context.Context, r identity.JobTokenRefusal,
) error {
	f.recorded = append(f.recorded, r)

	return nil
}

// writeGuardUnit returns the unit of the state write route.
func writeGuardUnit(t *testing.T) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("opentofu", ns, "net")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit
}

// writeGuardPrincipal returns the principal that the test authenticator signs
// in.
func writeGuardPrincipal(
	credential identity.Credential, jobID string, unit identity.Object,
) identity.Principal {
	return identity.Principal{
		Subject: "tester", DisplayName: "Tester", Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: credential, JobID: jobID, JobUnit: unit,
	}
}

// writeGuardAuthenticator signs in the test token as p.
type writeGuardAuthenticator struct {
	principal identity.Principal
}

// AuthenticateToken implements identity.Authenticator.
func (a writeGuardAuthenticator) AuthenticateToken(
	_ context.Context, raw string,
) (identity.Principal, error) {
	if raw != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return a.principal, nil
}

// newWriteGuardHandler builds a handler with the job facts and refusals of the
// state write guard.
func newWriteGuardHandler(
	t *testing.T, facts identity.JobFacts, refusals identity.JobTokenRefusals, p identity.Principal,
) http.Handler {
	t.Helper()

	handler, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  writeGuardAuthenticator{principal: p},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     newFakeUnits(true),
		Namespaces:     fakeNamespaces{},
		Audit:          newTestAudit(),
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}.WithJobTokenPorts(facts, refusals))
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return handler
}

// writeGuardCase is one row of the state write guard table.
type writeGuardCase struct {
	name         string
	fact         identity.JobFact
	factErr      error
	credential   identity.Credential
	wantStatus   int
	wantReads    int
	wantRefusals int
}

// TestStateWriteJobTokenGuard proves a plan-only job token cannot write state,
// while a changing job passes the guard.
func TestStateWriteJobTokenGuard(t *testing.T) {
	t.Parallel()

	unit := writeGuardUnit(t)

	other, err := identity.UnitObject("opentofu", unit.Namespace(), "other")
	if err != nil {
		t.Fatalf("other unit: %v", err)
	}

	cases := []writeGuardCase{
		{
			name: "a plan-only job may not write state",
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name: "a read error refuses",
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: errWriteGuardRead,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name: "a job of another unit refuses",
			fact: identity.JobFact{Unit: other, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name: "a changing job passes the guard",
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusOK,
			wantReads: 1, wantRefusals: 0,
		},
		{
			name: "a principal with no job token passes without a read",
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialAccessToken, wantStatus: http.StatusOK,
			wantReads: 0, wantRefusals: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runWriteGuardCase(t, tc, unit)
		})
	}
}

// runWriteGuardCase runs one row of the state write guard table.
func runWriteGuardCase(t *testing.T, tc writeGuardCase, unit identity.Object) {
	t.Helper()

	facts := &writeGuardFacts{fact: tc.fact, err: tc.factErr, reads: 0}
	refusals := &writeGuardRefusals{recorded: nil}
	jobID := "job-1"

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		jobID = ""
		callerUnit = identity.Object{}
	}

	handler := newWriteGuardHandler(
		t, facts, refusals, writeGuardPrincipal(tc.credential, jobID, callerUnit),
	)

	rec := do(handler, http.MethodPost, unitPath, `{"version":4}`, bearer("test-token"))
	if rec.Code != tc.wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
	}

	if facts.reads != tc.wantReads {
		t.Errorf("the guard read the job %d times, want %d", facts.reads, tc.wantReads)
	}

	if len(refusals.recorded) != tc.wantRefusals {
		t.Fatalf("the guard recorded %d refusals, want %d", len(refusals.recorded), tc.wantRefusals)
	}

	if tc.wantRefusals == 0 {
		return
	}

	want := identity.JobTokenRefusal{
		Subject: "tester", JobID: "job-1", Unit: unit, Action: identity.ActionUnitWrite,
		Reason: "access.job-token-action",
	}
	if got := refusals.recorded[0]; got != want {
		t.Errorf("refusal = %+v, want %+v", got, want)
	}

	if rec.Header().Get("Content-Type") == "application/vnd.api+json" {
		t.Error("media type is JSON:API, want the plain text shape")
	}
}
