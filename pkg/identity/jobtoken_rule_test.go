package identity_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

// errListFailed is the error of a Next that fails to list.
var errListFailed = errors.New("list failed")

// recordingNext is an Authorizer whose answer the test sets. It records
// every call and the principal that it saw.
type recordingNext struct {
	allow bool
	scope identity.Scope
	err   error
	asked int
	got   identity.Principal
}

// Authorize implements identity.Authorizer.
func (n *recordingNext) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	n.asked++
	n.got = p

	if n.allow {
		return nil
	}

	return &identity.DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

// ListObjects implements identity.Authorizer.
func (n *recordingNext) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	n.asked++
	n.got = p

	if n.err != nil {
		return emptyTestScope(), n.err
	}

	return n.scope, nil
}

// emptyTestScope returns the empty scope.
func emptyTestScope() identity.Scope {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}
}

// scopeOfUnit returns the scope that names only unit.
func scopeOfUnit(unit identity.Object) identity.Scope {
	return identity.Scope{
		Namespaces:  nil,
		Collections: nil,
		Units:       []identity.Object{unit},
		Principals:  nil,
		All:         false,
	}
}

// jobTestUnit returns the unit of the test world.
func jobTestUnit(t *testing.T, nsName, unitName, unitID string) identity.Object {
	t.Helper()

	return jobTestUnitIn(t, "pulumi", nsName, unitName, unitID)
}

// jobTestUnitIn returns the unit of tool in the test world.
func jobTestUnitIn(t *testing.T, tool, nsName, unitName, unitID string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject(tool, mustName(t, nsName), unitName)
	if err != nil {
		t.Fatalf("unit %q in %q: %v", unitName, nsName, err)
	}

	if unitID != "" {
		unit = unit.WithUnitID(unitID)
	}

	return unit
}

// jobToken returns a consistent job token principal of jobID on unit.
func jobToken(unit identity.Object, jobID string) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialJobToken, JobID: jobID, JobUnit: unit,
	}
}

// plainPrincipal returns a principal that carries no job token part.
func plainPrincipal(cred identity.Credential) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: cred, JobID: "", JobUnit: identity.Object{},
	}
}

// zeroPrincipal returns a principal with no value.
func zeroPrincipal() identity.Principal {
	return identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.Kind(""),
		Credential: identity.Credential(""), JobID: "", JobUnit: identity.Object{},
	}
}

// ruleCase is one row of the Authorize table of JobTokenRule.
type ruleCase struct {
	name        string
	p           identity.Principal
	action      identity.Action
	obj         identity.Object
	nextAllows  bool
	wantAllowed bool
	wantAsked   int
	checkNext   func(t *testing.T, next *recordingNext)
}

// inconsistentJobPrincipals returns principals that carry a part of a job
// token but are not a consistent job token.
func inconsistentJobPrincipals(t *testing.T, unit identity.Object) []ruleCase {
	t.Helper()

	missingJobID := jobToken(unit, "")
	missingUnit := jobToken(unit, "job-a")
	missingUnit.JobUnit = identity.Object{}

	notAUnit := jobToken(unit, "job-a")
	notAUnit.JobUnit = identity.NamespaceObject(mustName(t, "acme"))

	credentials := []struct {
		name string
		cred identity.Credential
	}{
		{name: "session", cred: identity.CredentialSession},
		{name: "access token", cred: identity.CredentialAccessToken},
		{name: "empty credential", cred: identity.Credential("")},
		{name: "trust token", cred: identity.CredentialTrustToken},
	}

	fields := []struct {
		name string
		set  func(*identity.Principal)
	}{
		{name: "a job ID", set: func(p *identity.Principal) { p.JobID = "job-a" }},
		{name: "a job unit", set: func(p *identity.Principal) { p.JobUnit = unit }},
	}

	out := make([]ruleCase, 0, 3+2*len(credentials)*len(fields))
	out = append(out,
		ruleCase{
			name: "credential job token with no job ID", p: missingJobID, action: identity.ActionUnitRead, obj: unit,
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		ruleCase{
			name: "job token with no job unit", p: missingUnit, action: identity.ActionUnitRead, obj: unit,
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		ruleCase{
			name:        "job token with a job unit that is not a unit",
			p:           notAUnit,
			action:      identity.ActionUnitRead,
			obj:         unit,
			nextAllows:  true,
			wantAllowed: false,
			wantAsked:   0,
			checkNext:   nil,
		},
	)

	for _, cred := range credentials {
		for _, field := range fields {
			for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionJobWriteCost} {
				p := plainPrincipal(cred.cred)
				field.set(&p)

				out = append(out, ruleCase{
					name: cred.name + " with " + field.name + " for " + string(action),
					p:    p, action: action, obj: unit, nextAllows: true, wantAllowed: false, wantAsked: 0,
					checkNext: nil,
				})
			}
		}
	}

	return out
}

// TestJobTokenRuleAuthorize proves that the rule limits a job token to the
// unit and the job of its token, and denies every inconsistent mix.
func TestJobTokenRuleAuthorize(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	other := jobTestUnit(t, "acme", "web/other", "unt-2")
	otherNS := jobTestUnit(t, "beta", "web/prod", "unt-3")
	collection := identity.CollectionObject(mustName(t, "acme"), "prod")
	principalObject, err := identity.ParsePrincipalObject("prn-alice")
	if err != nil {
		t.Fatalf("principal object: %v", err)
	}

	cases := append([]ruleCase{
		{
			name:        "a consistent job token on its unit, Next allows",
			p:           jobToken(unit, "job-a"),
			action:      identity.ActionUnitRead,
			obj:         unit,
			nextAllows:  true,
			wantAllowed: true,
			wantAsked:   1,
			checkNext:   nil,
		},
		{
			name:        "a consistent job token on its unit, Next denies",
			p:           jobToken(unit, "job-a"),
			action:      identity.ActionUnitRead,
			obj:         unit,
			nextAllows:  false,
			wantAllowed: false,
			wantAsked:   1,
			checkNext:   nil,
		},
		{
			name:        "another unit of the same namespace",
			p:           jobToken(unit, "job-a"),
			action:      identity.ActionUnitRead,
			obj:         other,
			nextAllows:  true,
			wantAllowed: false,
			wantAsked:   0,
			checkNext:   nil,
		},
		{
			name:        "a unit of another namespace",
			p:           jobToken(unit, "job-a"),
			action:      identity.ActionUnitRead,
			obj:         otherNS,
			nextAllows:  true,
			wantAllowed: false,
			wantAsked:   0,
			checkNext:   nil,
		},
		{
			name: "the namespace of its unit", p: jobToken(unit, "job-a"),
			action: identity.ActionNamespaceRead, obj: identity.NamespaceObject(unit.Namespace()),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "a collection", p: jobToken(unit, "job-a"),
			action: identity.ActionCollectionRead, obj: collection,
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "a principal", p: jobToken(unit, "job-a"),
			action: identity.ActionPrincipalManageTokens, obj: principalObject,
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "the caller", p: jobToken(unit, "job-a"),
			action: identity.ActionCallerRead, obj: identity.CallerObject(),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "the server", p: jobToken(unit, "job-a"),
			action: identity.ActionServerBackup, obj: identity.ServerObject(),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "the unit with another stable ID", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: jobTestUnit(t, "acme", "web/prod", "unt-9"),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "the unit with no stable ID in the object", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: jobTestUnit(t, "acme", "web/prod", ""),
			nextAllows: true, wantAllowed: true, wantAsked: 1, checkNext: nil,
		},
		{
			name: "a consistent job token with the administrator flag", p: withAdminFlag(jobToken(unit, "job-a")),
			action: identity.ActionUnitRead, obj: unit, nextAllows: true, wantAllowed: true, wantAsked: 1,
			checkNext: checkClearedFlags,
		},
		{
			name:   "a consistent job token with a temporary elevation",
			p:      withTemporaryFlag(jobToken(unit, "job-a")),
			action: identity.ActionUnitRead, obj: unit, nextAllows: true, wantAllowed: true, wantAsked: 1,
			checkNext: checkClearedFlags,
		},
		{
			name:        "another principal goes to Next unchanged",
			p:           withAdminFlag(plainPrincipal(identity.CredentialSession)),
			action:      identity.ActionServerBackup,
			obj:         identity.ServerObject(),
			nextAllows:  true,
			wantAllowed: true,
			wantAsked:   1,
			checkNext:   checkNextUnchanged,
		},
	}, inconsistentJobPrincipals(t, unit)...)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRuleCase(t, tc)
		})
	}
}

// TestJobTokenRuleUnitWithNoStableID proves that the rule compares the
// namespace, the tool and the name of a unit when neither side carries a
// stable unit ID.
func TestJobTokenRuleUnitWithNoStableID(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "")

	cases := []ruleCase{
		{
			name: "another namespace", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: jobTestUnitIn(t, "pulumi", "beta", "web/prod", ""),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "another tool", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: jobTestUnitIn(t, "opentofu", "acme", "web/prod", ""),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "another name", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: jobTestUnitIn(t, "pulumi", "acme", "web/other", ""),
			nextAllows: true, wantAllowed: false, wantAsked: 0, checkNext: nil,
		},
		{
			name: "the same unit", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead, obj: unit,
			nextAllows: true, wantAllowed: true, wantAsked: 1, checkNext: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRuleCase(t, tc)
		})
	}
}

// runRuleCase runs one row of the Authorize table of JobTokenRule.
func runRuleCase(t *testing.T, tc ruleCase) {
	t.Helper()

	next := &recordingNext{
		allow: tc.nextAllows, scope: emptyTestScope(), err: nil, asked: 0, got: zeroPrincipal(),
	}
	rule := identity.JobTokenRule{Next: next}

	err := rule.Authorize(t.Context(), tc.p, tc.action, tc.obj)

	if tc.wantAllowed && err != nil {
		t.Errorf("Authorize = %v, want allowed", err)
	}

	if !tc.wantAllowed && !errors.Is(err, identity.ErrDenied) {
		t.Errorf("Authorize = %v, want ErrDenied", err)
	}

	if next.asked != tc.wantAsked {
		t.Errorf("Next asked %d times, want %d", next.asked, tc.wantAsked)
	}

	if tc.checkNext != nil {
		tc.checkNext(t, next)
	}
}

// withAdminFlag returns p with the administrator flag.
func withAdminFlag(p identity.Principal) identity.Principal {
	p.Admin = true

	return p
}

// withTemporaryFlag returns p with a temporary elevation.
func withTemporaryFlag(p identity.Principal) identity.Principal {
	p.TemporaryAdministrator = true
	p.TemporaryExpiresAt = time.Unix(1_700_000_000, 0).UTC()

	return p
}

// checkClearedFlags proves that Next saw no administrator flag.
func checkClearedFlags(t *testing.T, next *recordingNext) {
	t.Helper()

	if next.got.Admin || next.got.TemporaryAdministrator || !next.got.TemporaryExpiresAt.IsZero() {
		t.Fatalf(
			"Next saw admin %t, temporary %t, expiry %s; want all cleared",
			next.got.Admin, next.got.TemporaryAdministrator, next.got.TemporaryExpiresAt,
		)
	}
}

// checkNextUnchanged proves that Next saw the principal as it was.
func checkNextUnchanged(t *testing.T, next *recordingNext) {
	t.Helper()

	want := withAdminFlag(plainPrincipal(identity.CredentialSession))
	if next.got != want {
		t.Fatalf("Next saw %+v, want %+v", next.got, want)
	}
}

// TestJobTokenRuleWriteCost proves that job:write-cost is a rule of the job
// token alone: Next never gives it, and no other principal holds it.
func TestJobTokenRuleWriteCost(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	other := jobTestUnit(t, "acme", "web/other", "unt-2")

	agentService := plainPrincipal(identity.CredentialAccessToken)
	agentService.Kind = identity.KindService

	agentPerson := plainPrincipal(identity.CredentialAccessToken)
	agentPerson.Kind = identity.KindPerson

	adminToken := withAdminFlag(plainPrincipal(identity.CredentialAccessToken))
	adminEmpty := withAdminFlag(plainPrincipal(identity.Credential("")))

	cases := []struct {
		name        string
		p           identity.Principal
		obj         identity.Object
		wantAllowed bool
	}{
		{
			name: "the job token of the job on its unit", p: jobToken(unit, "job-a"), obj: unit,
			wantAllowed: true,
		},
		{
			name: "the job token on another unit", p: jobToken(unit, "job-a"), obj: other,
			wantAllowed: false,
		},
		{
			name: "the job token on the namespace of its unit", p: jobToken(unit, "job-a"),
			obj: identity.NamespaceObject(unit.Namespace()), wantAllowed: false,
		},
		{
			name: "the session of the starter", p: plainPrincipal(identity.CredentialSession), obj: unit,
			wantAllowed: false,
		},
		{
			name: "the access token of the starter", p: plainPrincipal(identity.CredentialAccessToken), obj: unit,
			wantAllowed: false,
		},
		{
			name: "an agent of kind service", p: agentService, obj: unit, wantAllowed: false,
		},
		{
			name: "an agent of kind person", p: agentPerson, obj: unit, wantAllowed: false,
		},
		{
			name: "an administrator with an access token", p: adminToken, obj: unit, wantAllowed: false,
		},
		{
			name: "an administrator with an empty credential", p: adminEmpty, obj: unit, wantAllowed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &recordingNext{allow: true, scope: emptyTestScope(), err: nil, asked: 0, got: zeroPrincipal()}
			rule := identity.JobTokenRule{Next: next}

			err := rule.Authorize(t.Context(), tc.p, identity.ActionJobWriteCost, tc.obj)

			if tc.wantAllowed && err != nil {
				t.Errorf("Authorize = %v, want allowed", err)
			}

			if !tc.wantAllowed && !errors.Is(err, identity.ErrDenied) {
				t.Errorf("Authorize = %v, want ErrDenied", err)
			}

			if next.asked != 0 {
				t.Errorf("Next asked %d times, want 0: only the job token rule gives job:write-cost", next.asked)
			}
		})
	}
}

// listCase is one row of the ListObjects table of JobTokenRule.
type listCase struct {
	name      string
	p         identity.Principal
	action    identity.Action
	nextScope identity.Scope
	nextErr   error
	wantScope identity.Scope
	wantErr   error
	wantAsked int
}

// TestJobTokenRuleListObjects proves that a list of a job token names its
// unit only, and that an inconsistent token lists nothing.
func TestJobTokenRuleListObjects(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	scopeUnit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	namespace := identity.NamespaceObject(unit.Namespace())
	collection := identity.CollectionObject(mustName(t, "acme"), "prod")

	cases := []listCase{
		{
			name: "Next gives every object", p: jobToken(unit, "job-a"), action: identity.ActionUnitRead,
			nextScope: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true},
			nextErr:   nil, wantScope: scopeOfUnit(unit), wantErr: nil, wantAsked: 1,
		},
		{
			name: "Next gives no object", p: jobToken(unit, "job-a"), action: identity.ActionUnitRead,
			nextScope: emptyTestScope(), nextErr: nil, wantScope: emptyTestScope(), wantErr: nil, wantAsked: 1,
		},
		{
			name: "Next gives the namespace of the unit", p: jobToken(unit, "job-a"),
			action: identity.ActionUnitRead,
			nextScope: identity.Scope{
				Namespaces: []identity.Object{namespace}, Collections: nil, Units: nil, Principals: nil, All: false,
			},
			nextErr: nil, wantScope: scopeOfUnit(unit), wantErr: nil, wantAsked: 1,
		},
		{
			name: "Next gives the unit", p: jobToken(unit, "job-a"), action: identity.ActionUnitRead,
			nextScope: identity.Scope{
				Namespaces: nil, Collections: nil, Units: []identity.Object{scopeUnit}, Principals: nil, All: false,
			},
			nextErr: nil, wantScope: scopeOfUnit(unit), wantErr: nil, wantAsked: 1,
		},
		{
			name: "Next gives only a collection", p: jobToken(unit, "job-a"), action: identity.ActionUnitRead,
			nextScope: identity.Scope{
				Namespaces: nil, Collections: []identity.Object{collection}, Units: nil, Principals: nil, All: false,
			},
			nextErr: nil, wantScope: emptyTestScope(), wantErr: nil, wantAsked: 1,
		},
		{
			name:   "Next gives the unit of another namespace",
			p:      jobToken(unit, "job-a"),
			action: identity.ActionUnitRead,
			nextScope: identity.Scope{
				Namespaces:  nil,
				Collections: nil,
				Units:       []identity.Object{jobTestUnit(t, "beta", "web/prod", "unt-3")},
				Principals:  nil,
				All:         false,
			},
			nextErr:   nil,
			wantScope: emptyTestScope(),
			wantErr:   nil,
			wantAsked: 1,
		},
		{
			name: "Next fails", p: jobToken(unit, "job-a"), action: identity.ActionUnitRead,
			nextScope: emptyTestScope(), nextErr: errListFailed, wantScope: emptyTestScope(),
			wantErr: errListFailed, wantAsked: 1,
		},
		{
			name: "an inconsistent job token", p: jobToken(unit, ""), action: identity.ActionUnitRead,
			nextScope: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true},
			nextErr:   nil, wantScope: emptyTestScope(), wantErr: nil, wantAsked: 0,
		},
		{
			name: "job:write-cost of a consistent job token", p: jobToken(unit, "job-a"),
			action: identity.ActionJobWriteCost,
			nextScope: identity.Scope{
				Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true,
			},
			nextErr: nil, wantScope: scopeOfUnit(unit), wantErr: nil, wantAsked: 0,
		},
		{
			name:   "job:write-cost of another principal",
			p:      withAdminFlag(plainPrincipal(identity.CredentialAccessToken)),
			action: identity.ActionJobWriteCost,
			nextScope: identity.Scope{
				Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true,
			},
			nextErr:   nil,
			wantScope: emptyTestScope(),
			wantErr:   nil,
			wantAsked: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &recordingNext{allow: true, scope: tc.nextScope, err: tc.nextErr, asked: 0, got: zeroPrincipal()}
			rule := identity.JobTokenRule{Next: next}

			scope, err := rule.ListObjects(t.Context(), tc.p, tc.action)

			if tc.wantErr == nil && err != nil {
				t.Fatalf("ListObjects = %v, want no error", err)
			}

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("ListObjects = %v, want %v", err, tc.wantErr)
			}

			if !sameScope(scope, tc.wantScope) {
				t.Errorf("scope = %+v, want %+v", scope, tc.wantScope)
			}

			if next.asked != tc.wantAsked {
				t.Errorf("Next asked %d times, want %d", next.asked, tc.wantAsked)
			}
		})
	}
}

// sameScope reports whether a and b name the same objects.
func sameScope(a, b identity.Scope) bool {
	return a.All == b.All && slices.Equal(a.Namespaces, b.Namespaces) &&
		slices.Equal(a.Collections, b.Collections) && slices.Equal(a.Units, b.Units) &&
		slices.Equal(a.Principals, b.Principals)
}

// jobClaimCase is one row of the AuthorizeJobClaim table.
type jobClaimCase struct {
	name    string
	p       identity.Principal
	jobID   string
	wantErr bool
}

// TestAuthorizeJobClaim proves that a push for the phases of a job accepts
// only the job token of that job.
func TestAuthorizeJobClaim(t *testing.T) {
	t.Parallel()

	unit := jobTestUnit(t, "acme", "web/prod", "unt-1")
	token := jobToken(unit, "job-a")

	notAUnit := token
	notAUnit.JobUnit = identity.Object{}

	cases := []jobClaimCase{
		{name: "the job token of the job", p: token, jobID: "job-a", wantErr: false},
		{name: "the job token of another job", p: token, jobID: "job-b", wantErr: true},
		{name: "an empty target job ID", p: token, jobID: "", wantErr: true},
		{name: "a job token with no job unit", p: notAUnit, jobID: "job-a", wantErr: true},
		{
			name: "a session with the job ID", p: withJobFields(plainPrincipal(identity.CredentialSession), unit),
			jobID: "job-a", wantErr: true,
		},
		{
			name:    "an access token with the job ID",
			p:       withJobFields(plainPrincipal(identity.CredentialAccessToken), unit),
			jobID:   "job-a",
			wantErr: true,
		},
		{
			name: "an agent with the job ID", p: withJobFields(agentPrincipal(), unit),
			jobID: "job-a", wantErr: true,
		},
		{
			name:  "an administrator with the job ID",
			p:     withJobFields(withAdminFlag(plainPrincipal(identity.Credential(""))), unit),
			jobID: "job-a", wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runJobClaimCase(t, tc)
		})
	}
}

// runJobClaimCase runs one row of the AuthorizeJobClaim table.
func runJobClaimCase(t *testing.T, tc jobClaimCase) {
	t.Helper()

	err := identity.AuthorizeJobClaim(tc.p, tc.jobID)

	if !tc.wantErr {
		if err != nil {
			t.Fatalf("AuthorizeJobClaim = %v, want nil", err)
		}

		return
	}

	if err == nil {
		t.Fatal("AuthorizeJobClaim = nil, want ErrDenied")
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("AuthorizeJobClaim = %v, want ErrDenied", err)
	}

	for _, jobID := range []string{"job-a", "job-b"} {
		if strings.Contains(err.Error(), jobID) {
			t.Fatalf("error %q names the job ID %q", err, jobID)
		}
	}
}

// withJobFields returns p with a job ID and a job unit.
func withJobFields(p identity.Principal, unit identity.Object) identity.Principal {
	p.JobID = "job-a"
	p.JobUnit = unit

	return p
}

// agentPrincipal returns an access token principal of kind service.
func agentPrincipal() identity.Principal {
	p := plainPrincipal(identity.CredentialAccessToken)
	p.Kind = identity.KindService

	return p
}
