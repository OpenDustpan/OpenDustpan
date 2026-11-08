package tfe_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errStateGuardRead is the read error of the job facts fake.
var errStateGuardRead = errors.New("read failed")

// stateGuardFacts is a JobFacts whose answer the test sets. It counts reads.
type stateGuardFacts struct {
	fact  identity.JobFact
	err   error
	reads int
}

// JobFact implements identity.JobFacts.
func (f *stateGuardFacts) JobFact(context.Context, string) (identity.JobFact, error) {
	f.reads++

	return f.fact, f.err
}

// stateGuardRefusals keeps each refusal of the state guard.
type stateGuardRefusals struct {
	recorded []identity.JobTokenRefusal
}

// RecordJobTokenRefusal implements identity.JobTokenRefusals.
func (f *stateGuardRefusals) RecordJobTokenRefusal(
	_ context.Context, r identity.JobTokenRefusal,
) error {
	f.recorded = append(f.recorded, r)

	return nil
}

// stateGuardUnit returns the unit of the state write route.
func stateGuardUnit(t *testing.T) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("opentofu", ns, "web")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit
}

// stateGuardPrincipal returns the principal that the test authenticator signs
// in.
func stateGuardPrincipal(
	credential identity.Credential, jobID string, unit identity.Object,
) identity.Principal {
	return identity.Principal{
		Subject: "tester", DisplayName: "Tester", Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: credential, JobID: jobID, JobUnit: unit,
	}
}

// stateGuardAuthenticator signs in the test token as p.
type stateGuardAuthenticator struct {
	principal identity.Principal
}

// AuthenticateToken implements identity.Authenticator.
func (a stateGuardAuthenticator) AuthenticateToken(
	_ context.Context, raw string,
) (identity.Principal, error) {
	if raw != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return a.principal, nil
}

// newStateGuardHandler builds a TFE handler with the job facts and the
// refusals of the state guard.
func newStateGuardHandler(
	t *testing.T, facts identity.JobFacts, refusals identity.JobTokenRefusals, p identity.Principal,
) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  stateGuardAuthenticator{principal: p},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}.WithJobTokenPorts(facts, refusals))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// stateGuardErrorItem is one error of a JSON:API error document.
type stateGuardErrorItem struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// stateGuardErrorDoc is a JSON:API error document of the state guard.
type stateGuardErrorDoc struct {
	Errors []stateGuardErrorItem `json:"errors"`
}

// stateGuardError decodes a JSON:API error document of the state guard.
func stateGuardError(t *testing.T, rec *httptest.ResponseRecorder) stateGuardErrorItem {
	t.Helper()

	var doc stateGuardErrorDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(doc.Errors))
	}

	return doc.Errors[0]
}

// stateGuardCase is one row of the state version create guard table.
type stateGuardCase struct {
	name         string
	fact         identity.JobFact
	factErr      error
	credential   identity.Credential
	wantStatus   int
	wantReads    int
	wantRefusals int
}

// TestStateVersionCreateJobTokenGuard proves a plan-only job token cannot
// write state, while a changing job passes the guard.
func TestStateVersionCreateJobTokenGuard(t *testing.T) {
	t.Parallel()

	unit := stateGuardUnit(t)

	other, err := identity.UnitObject("opentofu", unit.Namespace(), "other")
	if err != nil {
		t.Fatalf("other unit: %v", err)
	}

	cases := []stateGuardCase{
		{
			name: "a plan-only job may not write state",
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialJobToken, wantStatus: http.StatusForbidden,
			wantReads: 1, wantRefusals: 1,
		},
		{
			name: "a read error refuses",
			fact: identity.JobFact{Unit: unit, Changes: true, Source: "api"}, factErr: errStateGuardRead,
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
			credential: identity.CredentialJobToken, wantStatus: http.StatusCreated,
			wantReads: 1, wantRefusals: 0,
		},
		{
			name: "a principal with no job token passes without a read",
			fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, factErr: nil,
			credential: identity.CredentialAccessToken, wantStatus: http.StatusCreated,
			wantReads: 0, wantRefusals: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runStateGuardCase(t, tc, unit)
		})
	}
}

// runStateGuardCase runs one row of the state version create guard table.
func runStateGuardCase(t *testing.T, tc stateGuardCase, unit identity.Object) {
	t.Helper()

	facts := &stateGuardFacts{fact: tc.fact, err: tc.factErr, reads: 0}
	refusals := &stateGuardRefusals{recorded: nil}
	jobID := "job-1"

	callerUnit := unit
	if tc.credential != identity.CredentialJobToken {
		jobID = ""
		callerUnit = identity.Object{}
	}

	handler := newStateGuardHandler(
		t, facts, refusals, stateGuardPrincipal(tc.credential, jobID, callerUnit),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, stateCreateOutputs()),
		),
	)

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

	answer := stateGuardError(t, rec)
	if answer.Status != "403" || answer.Code != "access.job-token-action" {
		t.Errorf("error = status %q code %q, want 403 access.job-token-action", answer.Status, answer.Code)
	}
}

// TestStateVersionCreateJobTokenGuardKeepsTextShape proves the refusal keeps
// the JSON:API media type and names no store detail.
func TestStateVersionCreateJobTokenGuardKeepsTextShape(t *testing.T) {
	t.Parallel()

	unit := stateGuardUnit(t)
	facts := &stateGuardFacts{
		fact: identity.JobFact{Unit: unit, Changes: false, Source: "api"}, err: nil, reads: 0,
	}
	refusals := &stateGuardRefusals{recorded: nil}
	handler := newStateGuardHandler(
		t, facts, refusals, stateGuardPrincipal(identity.CredentialJobToken, "job-1", unit),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(t, "/api/v2/workspaces/ws-test123/state-versions", stateCreateBody(4, stateCreateOutputs())),
	)

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Errorf("media type = %q, want application/vnd.api+json", got)
	}

	if strings.Contains(rec.Body.String(), "read failed") {
		t.Errorf("body %q holds the store detail", rec.Body.String())
	}
}
