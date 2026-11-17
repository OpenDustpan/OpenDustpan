package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errUnitsDown is a job unit read that fails for a cause other than a missing
// job.
var errUnitsDown = errors.New("the job store is down")

// fakeJobUnits is an identity.JobUnits whose answer the test sets. It keeps the
// job IDs that the rule read, so a case proves whether the rule read the unit
// of its job.
type fakeJobUnits struct {
	units map[string]identity.JobUnit
	err   error
	reads []string
}

// UnitOfJob implements identity.JobUnits.
func (f *fakeJobUnits) UnitOfJob(_ context.Context, jobID string) (identity.JobUnit, error) {
	f.reads = append(f.reads, jobID)

	if f.err != nil {
		return identity.JobUnit{}, f.err
	}

	u, ok := f.units[jobID]
	if !ok {
		return identity.JobUnit{}, identity.ErrJobNotFound
	}

	return u, nil
}

// allowingAuthorizer is a hostile Authorizer of an embedder that allows every
// check. It keeps the principal of each check, so a case sees the marks that
// the rule cleared before it asked.
type allowingAuthorizer struct {
	seen  []identity.Principal
	scope identity.Scope
	err   error
}

// Authorize implements identity.Authorizer. It allows every check.
func (a *allowingAuthorizer) Authorize(
	_ context.Context, p identity.Principal, _ identity.Action, _ identity.Object,
) error {
	a.seen = append(a.seen, p)

	return a.err
}

// ListObjects implements identity.Authorizer. It answers the scope of the case.
func (a *allowingAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	a.seen = append(a.seen, p)

	if a.err != nil {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, a.err
	}

	return a.scope, nil
}

// unitOfJob returns the unit of a job as the port answers it.
func unitOfJob(t *testing.T, obj identity.Object) identity.JobUnit {
	t.Helper()

	unit, err := identity.NewJobUnit(obj)
	if err != nil {
		t.Fatalf("unit of a job: %v", err)
	}

	return unit
}

// jobUnitObject returns the reference of a unit with the stable ID id.
func jobUnitObject(t *testing.T, ns namespace.Name, name, id string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", ns, name)
	if err != nil {
		t.Fatalf("unit %s/%s of pulumi: %v", ns, name, err)
	}

	return obj.WithUnitID(id)
}

// unitScope returns the scope that names each unit of the test.
func unitScope(units ...identity.Object) identity.Scope {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: units, Principals: nil}
}

// jobToken returns a consistent job token of the starter alice.
func jobToken() identity.Principal {
	return identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialJobToken, JobID: "job-1",
	}
}

// withCredential returns p with the credential credential.
func withCredential(p identity.Principal, credential identity.Credential) identity.Principal {
	p.Credential = credential

	return p
}

// ruleCase is one row of TestJobTokenRuleAuthorize.
type ruleCase struct {
	name       string
	p          identity.Principal
	action     identity.Action
	obj        identity.Object
	known      bool
	unitsErr   error
	noPort     bool
	wantDenied bool
	// wantFailClosed says that the answer is an error that is not a denial,
	// as refusal.ReasonFor reports "authorization failed".
	wantFailClosed bool
	wantNext       bool
	wantReads      int
}

// allowCase returns the case of an action that the rule leaves to the next
// authorizer. The port of the units of jobs answers the unit of the job.
func allowCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: true, unitsErr: nil,
		noPort: false, wantDenied: false, wantFailClosed: false, wantNext: true, wantReads: 1,
	}
}

// refuseCase returns the case of an action that the rule refuses after it read
// the unit of the job.
func refuseCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: true, unitsErr: nil, noPort: false,
		wantDenied: true, wantFailClosed: false, wantNext: false, wantReads: 1,
	}
}

// unreadCase returns the case of a refusal that costs no read of a unit: the
// rule refuses an inconsistent job token before it reads one.
func unreadCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: true, unitsErr: nil, noPort: false,
		wantDenied: true, wantFailClosed: false, wantNext: false, wantReads: 0,
	}
}

// unknownCase returns the case of a token of a job that the port does not hold.
func unknownCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: false, unitsErr: nil, noPort: false,
		wantDenied: true, wantFailClosed: false, wantNext: false, wantReads: 1,
	}
}

// failedCase returns the case of a read of the unit of the job that fails for a
// cause other than a missing job.
func failedCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: true, unitsErr: errUnitsDown, noPort: false,
		wantDenied: false, wantFailClosed: true, wantNext: false, wantReads: 1,
	}
}

// noPortCase returns the case of a rule that holds no port of the units of
// jobs.
func noPortCase(name string, p identity.Principal, action identity.Action, obj identity.Object) ruleCase {
	return ruleCase{
		name: name, p: p, action: action, obj: obj, known: true, unitsErr: nil, noPort: true,
		wantDenied: true, wantFailClosed: false, wantNext: false, wantReads: 0,
	}
}

// TestJobTokenRuleAuthorize proves the decision of the rule of a job token: the
// token acts on the unit of its job only, an inconsistent token is refused
// every action, and no administrator mark widens it. Each case sends hostile
// input straight to the rule, as if every other layer had passed.
func TestJobTokenRuleAuthorize(t *testing.T) {
	t.Parallel()

	unit := jobUnitObject(t, "acme", "web/prod", "unt-1")
	otherUnit := jobUnitObject(t, "acme", "web/dev", "unt-2")
	otherNamespace := jobUnitObject(t, "other", "web/prod", "unt-3")

	// The port answers the unit of the job job-1.
	job := unitOfJob(t, unit)

	token := jobToken()
	withAccessToken := withCredential(token, identity.CredentialAccessToken)
	withNoCredential := withCredential(token, "")
	withNoJob := jobToken()
	withNoJob.JobID = ""

	admin := jobToken()
	admin.Admin = true

	tempAdmin := jobToken()
	tempAdmin.TemporaryAdministrator = true
	tempAdmin.TemporaryExpiresAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	server := identity.ServerObject()

	cases := []ruleCase{
		allowCase("the unit of the job is allowed", token, identity.ActionUnitRead, unit),
		refuseCase("another unit is refused", token, identity.ActionUnitRead, otherUnit),
		refuseCase("a unit of another namespace is refused", token, identity.ActionUnitRead, otherNamespace),
		refuseCase("a namespace is refused", token, identity.ActionNamespaceRead, identity.NamespaceObject("acme")),
		refuseCase(
			"a collection is refused", token, identity.ActionCollectionRead,
			identity.CollectionObject("acme", "web"),
		),
		refuseCase("a principal is refused", token, identity.ActionUnitRead, identity.PrincipalObject("prn-1")),
		refuseCase("the server is refused", token, identity.ActionServerBackup, server),
		allowCase(
			"the output read of another unit is decided by the next authorizer", token,
			identity.ActionUnitReadOutputs, otherUnit,
		),
		allowCase(
			"the secret read of another unit is decided by the next authorizer", token,
			identity.ActionUnitReadSecrets, otherUnit,
		),
		unreadCase(
			"a job ID with the access token credential is refused", withAccessToken,
			identity.ActionUnitRead, unit,
		),
		unreadCase("a job ID with no credential is refused", withNoCredential, identity.ActionServerBackup, server),
		unreadCase("the job token credential with no job ID is refused", withNoJob, identity.ActionUnitRead, unit),
		unknownCase("an unknown job is refused", token, identity.ActionUnitRead, unit),
		failedCase("a failed unit read fails closed", token, identity.ActionUnitRead, unit),
		noPortCase("a rule with no port refuses a job token", token, identity.ActionUnitRead, unit),
		allowCase("an administrator mark gives the token nothing more", admin, identity.ActionUnitRead, unit),
		allowCase("a temporary admin mark gives the token nothing more", tempAdmin, identity.ActionUnitRead, unit),
		refuseCase("an administrator mark does not open another unit", admin, identity.ActionUnitRead, otherUnit),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRuleAuthorize(t, tc, job)
		})
	}
}

// checkRuleAuthorize runs one case of the table.
func checkRuleAuthorize(t *testing.T, tc ruleCase, job identity.JobUnit) {
	t.Helper()

	units := &fakeJobUnits{units: map[string]identity.JobUnit{}, err: tc.unitsErr, reads: nil}
	if tc.known {
		units.units["job-1"] = job
	}

	next := &allowingAuthorizer{
		seen:  nil,
		scope: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
		err:   nil,
	}

	rule := identity.JobTokenRule{Next: next, Units: units, UnitIDs: nil}
	if tc.noPort {
		rule.Units = nil
	}

	err := rule.Authorize(t.Context(), tc.p, tc.action, tc.obj)

	checkRuleAnswer(t, err, tc)

	if asked := len(next.seen) > 0; asked != tc.wantNext {
		t.Errorf("the next authorizer was asked %t, want %t", asked, tc.wantNext)
	}

	checkNoElevation(t, next.seen)

	if len(units.reads) != tc.wantReads {
		t.Errorf("the rule read the unit %d times, want %d: %v", len(units.reads), tc.wantReads, units.reads)
	}
}

// checkRuleAnswer proves the answer of the rule for one case.
func checkRuleAnswer(t *testing.T, err error, tc ruleCase) {
	t.Helper()

	switch {
	case tc.wantFailClosed:
		if err == nil || errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Authorize = %v, want a failure that is not a denial", err)
		}
	case tc.wantDenied:
		var denied *identity.DeniedError

		if !errors.As(err, &denied) || !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Authorize = %v, want a denial that wraps ErrDenied", err)
		}
	default:
		if err != nil {
			t.Fatalf("Authorize = %v, want allowed", err)
		}
	}
}

// checkNoElevation proves that no next authorizer saw a mark of the starter.
//
// Defense in depth: this check stays so that the rule never asks a next
// authorizer with an administrator mark of the starter.
func checkNoElevation(t *testing.T, seen []identity.Principal) {
	t.Helper()

	for i := range seen {
		if seen[i].Admin || seen[i].TemporaryAdministrator || !seen[i].TemporaryExpiresAt.IsZero() {
			t.Errorf("the next authorizer saw %+v, want no elevation", seen[i])
		}
	}
}

// listCase is one row of TestJobTokenRuleListObjects.
type listCase struct {
	name      string
	p         identity.Principal
	next      identity.Scope
	nextErr   error
	unitsErr  error
	known     bool
	noPort    bool
	wantUnits []identity.Object
	wantErr   bool
}

// listOf returns the case of a scope that the rule narrows to want.
func listOf(name string, p identity.Principal, next identity.Scope, want ...identity.Object) listCase {
	return listCase{
		name: name, p: p, next: next, nextErr: nil, unitsErr: nil, known: true, noPort: false,
		wantUnits: want, wantErr: false,
	}
}

// listUnknownJob returns the case of a token of a job that the port does not
// hold.
func listUnknownJob(name string, p identity.Principal, next identity.Scope) listCase {
	return listCase{
		name: name, p: p, next: next, nextErr: nil, unitsErr: nil, known: false, noPort: false,
		wantUnits: nil, wantErr: false,
	}
}

// listNoPortCase returns the case of a rule that holds no port of the units of
// jobs.
func listNoPortCase(name string, p identity.Principal, next identity.Scope) listCase {
	return listCase{
		name: name, p: p, next: next, nextErr: nil, unitsErr: nil, known: true, noPort: true,
		wantUnits: nil, wantErr: false,
	}
}

// listUnitReadFailed returns the case of a read of the unit of the job that
// fails.
func listUnitReadFailed(name string, p identity.Principal, next identity.Scope) listCase {
	return listCase{
		name: name, p: p, next: next, nextErr: nil, unitsErr: errUnitsDown, known: true,
		noPort: false, wantUnits: nil, wantErr: true,
	}
}

// listNextFailed returns the case of a scope that the next authorizer cannot
// answer.
func listNextFailed(name string, p identity.Principal, nextErr error) listCase {
	return listCase{
		name: name, p: p, next: identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil},
		nextErr: nextErr, unitsErr: nil, known: true, noPort: false, wantUnits: nil, wantErr: true,
	}
}

// TestJobTokenRuleListObjects proves that a job token lists the unit of its job
// only, and nothing when the scope of the next authorizer does not name that
// unit.
func TestJobTokenRuleListObjects(t *testing.T) {
	t.Parallel()

	unit := jobUnitObject(t, "acme", "web/prod", "unt-1")
	otherUnit := jobUnitObject(t, "acme", "web/dev", "unt-2")

	// The port answers the unit of the job job-1.
	job := unitOfJob(t, unit)

	token := jobToken()
	withAccessToken := withCredential(token, identity.CredentialAccessToken)

	cases := []listCase{
		listOf("the scope names the unit of the job", token, unitScope(unit), unit),
		listOf("the scope names another unit", token, unitScope(otherUnit)),
		listOf("the scope names the unit and another unit", token, unitScope(unit, otherUnit), unit),
		listOf("the scope names the namespace only", token, unitScope(identity.NamespaceObject("acme"))),
		listOf("the scope covers every object", token, identity.Every()),
		listUnknownJob("the job is unknown", token, unitScope(unit)),
		listUnitReadFailed("the unit read fails", token, unitScope(unit)),
		listOf("a job ID with another credential lists nothing", withAccessToken, unitScope(unit)),
		listNoPortCase("a rule with no port lists nothing", token, unitScope(unit)),
		listNextFailed("the next authorizer fails", token, errUnitsDown),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRuleListObjects(t, tc, job)
		})
	}
}

// checkRuleListObjects runs one case of the table.
func checkRuleListObjects(t *testing.T, tc listCase, job identity.JobUnit) {
	t.Helper()

	units := &fakeJobUnits{units: map[string]identity.JobUnit{}, err: tc.unitsErr, reads: nil}
	if tc.known {
		units.units["job-1"] = job
	}

	next := &allowingAuthorizer{seen: nil, scope: tc.next, err: tc.nextErr}

	rule := identity.JobTokenRule{Next: next, Units: units, UnitIDs: nil}
	if tc.noPort {
		rule.Units = nil
	}

	scope, err := rule.ListObjects(t.Context(), tc.p, identity.ActionUnitRead)

	if tc.wantErr {
		if err == nil {
			t.Fatal("ListObjects = nil, want an error")
		}

		return
	}

	if err != nil {
		t.Fatalf("ListObjects = %v, want nil", err)
	}

	checkScopeUnits(t, scope, tc.wantUnits)
}

// fakeUnitIDs is an identity.UnitIDReader whose answer the test sets. It keeps
// the objects that the rule asked about, so a case proves whether the rule
// resolved a unit that carries no stable ID.
type fakeUnitIDs struct {
	id    string
	found bool
	err   error
	reads []identity.Object
}

// UnitID implements identity.UnitIDReader.
func (f *fakeUnitIDs) UnitID(_ context.Context, obj identity.Object) (string, bool, error) {
	f.reads = append(f.reads, obj)

	return f.id, f.found, f.err
}

// resolvesCase is one row of TestJobTokenRuleResolvesUnitID.
type resolvesCase struct {
	name            string
	p               identity.Principal
	action          identity.Action
	obj             identity.Object
	jobKnown        bool
	unitsErr        error
	noJobPort       bool
	unitID          string
	unitIDFound     bool
	unitIDErr       error
	noUnitIDPort    bool
	wantDenied      bool
	wantFailClosed  bool
	wantNext        bool
	wantUnitIDReads int
	wantJobReads    int
}

// TestJobTokenRuleResolvesUnitID proves that a job token reaches a unit that
// the route names without a stable ID: the rule resolves the stable ID through
// the port and compares it with the stable ID of the unit of the job. Each case
// sends hostile input straight to the rule, as if every other layer had passed.
func TestJobTokenRuleResolvesUnitID(t *testing.T) {
	t.Parallel()

	jobUnit := jobUnitObject(t, "acme", "web/prod", "unt-1")
	job := unitOfJob(t, jobUnit)

	ownByName, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit by name: %v", err)
	}

	otherByName, err := identity.UnitObject("pulumi", "acme", "web/dev")
	if err != nil {
		t.Fatalf("unit by name: %v", err)
	}

	stableOther := jobUnitObject(t, "acme", "web/dev", "unt-2")

	token := jobToken()

	otherPrincipal := identity.Principal{
		Subject: "bob", DisplayName: "bob", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}

	cases := []resolvesCase{
		{
			name: "the own unit by name allows", p: token, action: identity.ActionUnitRead,
			obj: ownByName, jobKnown: true, unitID: "unt-1", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: false, wantFailClosed: false, wantNext: true,
			wantUnitIDReads: 1, wantJobReads: 1,
		},
		{
			name: "another unit by name is refused", p: token, action: identity.ActionUnitRead,
			obj: otherByName, jobKnown: true, unitID: "unt-2", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: true, wantFailClosed: false, wantNext: false,
			wantUnitIDReads: 1, wantJobReads: 1,
		},
		{
			name: "a unit the port does not find is refused", p: token, action: identity.ActionUnitRead,
			obj: otherByName, jobKnown: true, unitID: "", unitIDFound: false,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: true, wantFailClosed: false, wantNext: false,
			wantUnitIDReads: 1, wantJobReads: 1,
		},
		{
			name: "a port that fails answers an error that is not a denial", p: token, action: identity.ActionUnitRead,
			obj: ownByName, jobKnown: true, unitIDErr: errUnitsDown,
			unitsErr: nil, noJobPort: false, unitID: "", unitIDFound: false, noUnitIDPort: false,
			wantDenied: false, wantFailClosed: true, wantNext: false,
			wantUnitIDReads: 1, wantJobReads: 1,
		},
		{
			name: "a rule with no unit ID port refuses a unit by name", p: token, action: identity.ActionUnitRead,
			obj: ownByName, jobKnown: true, unitID: "unt-1", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: true,
			wantDenied: true, wantFailClosed: false, wantNext: false,
			wantUnitIDReads: 0, wantJobReads: 1,
		},
		{
			name: "an object that carries a stable ID is not resolved again", p: token, action: identity.ActionUnitRead,
			obj: stableOther, jobKnown: true, unitID: "should-not-be-read", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: true, wantFailClosed: false, wantNext: false,
			wantUnitIDReads: 0, wantJobReads: 1,
		},
		{
			name: "a stable ID that matches allows without resolving", p: token, action: identity.ActionUnitRead,
			obj: jobUnit, jobKnown: true, unitID: "should-not-be-read", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: false, wantFailClosed: false, wantNext: true,
			wantUnitIDReads: 0, wantJobReads: 1,
		},
		{
			name: "a principal that is not a job token never calls the port", p: otherPrincipal,
			action: identity.ActionUnitRead,
			obj:    ownByName, jobKnown: true, unitID: "unt-1", unitIDFound: true,
			unitsErr: nil, noJobPort: false, unitIDErr: nil, noUnitIDPort: false,
			wantDenied: false, wantFailClosed: false, wantNext: true,
			wantUnitIDReads: 0, wantJobReads: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkResolvesCase(t, tc, job)
		})
	}
}

// checkResolvesCase runs one row of TestJobTokenRuleResolvesUnitID.
func checkResolvesCase(t *testing.T, tc resolvesCase, job identity.JobUnit) {
	t.Helper()

	units := &fakeJobUnits{units: map[string]identity.JobUnit{}, err: tc.unitsErr, reads: nil}
	if tc.jobKnown {
		units.units["job-1"] = job
	}

	fakeIDs := &fakeUnitIDs{id: tc.unitID, found: tc.unitIDFound, err: tc.unitIDErr, reads: nil}

	// The interface field stays a nil interface for a rule without the port,
	// so the rule's own nil check sees it.
	var unitIDs identity.UnitIDReader = fakeIDs
	if tc.noUnitIDPort {
		unitIDs = nil
		fakeIDs = nil
	}

	if tc.noJobPort {
		units = nil
	}

	next := &allowingAuthorizer{seen: nil, scope: identity.Scope{
		Namespaces: nil, Collections: nil, Units: nil, Principals: nil,
	}, err: nil}

	rule := identity.JobTokenRule{Next: next, Units: units, UnitIDs: unitIDs}
	if tc.noJobPort {
		rule.Units = nil
	}

	err := rule.Authorize(t.Context(), tc.p, tc.action, tc.obj)

	checkResolvesAnswer(t, err, tc)

	if asked := len(next.seen) > 0; asked != tc.wantNext {
		t.Errorf("the next authorizer was asked %t, want %t", asked, tc.wantNext)
	}

	checkResolvesReads(t, fakeIDs, units, tc)
}

// checkResolvesAnswer proves the answer of the rule for one resolves case.
func checkResolvesAnswer(t *testing.T, err error, tc resolvesCase) {
	t.Helper()

	switch {
	case tc.wantFailClosed:
		if err == nil {
			t.Fatal("Authorize = nil, want an error that is not a denial")
		}

		if _, ok := errors.AsType[*identity.DeniedError](err); ok {
			t.Fatalf("Authorize = %v, want an error that is not a denial", err)
		}
	case tc.wantDenied:
		if _, ok := errors.AsType[*identity.DeniedError](err); !ok {
			t.Fatalf("Authorize = %v, want a denial", err)
		}
	default:
		if err != nil {
			t.Fatalf("Authorize = %v, want allowed", err)
		}
	}
}

// checkResolvesReads proves the read counts of one resolves case.
func checkResolvesReads(t *testing.T, unitIDs *fakeUnitIDs, units *fakeJobUnits, tc resolvesCase) {
	t.Helper()

	if unitIDsReads(unitIDs) != tc.wantUnitIDReads {
		if unitIDs == nil {
			t.Errorf("unit ID reads = 0, want %d", tc.wantUnitIDReads)
		} else {
			t.Errorf(
				"the rule resolved the unit %d times, want %d: %v", len(unitIDs.reads),
				tc.wantUnitIDReads, unitIDs.reads,
			)
		}
	}

	jobReads := 0
	if units != nil {
		jobReads = len(units.reads)
	}

	if jobReads != tc.wantJobReads {
		t.Errorf("the rule read the job unit %d times, want %d", jobReads, tc.wantJobReads)
	}
}

func unitIDsReads(u *fakeUnitIDs) int {
	if u == nil {
		return 0
	}

	return len(u.reads)
}

func checkScopeUnits(t *testing.T, scope identity.Scope, want []identity.Object) {
	t.Helper()

	if len(scope.Namespaces)+len(scope.Collections)+len(scope.Principals) != 0 {
		t.Errorf("scope = %+v, want units only", scope)
	}

	if len(scope.Units) != len(want) {
		t.Fatalf("scope units = %v, want %v", scope.Units, want)
	}

	for i := range want {
		if scope.Units[i].UnitID() != want[i].UnitID() {
			t.Errorf("scope unit %d = %s, want %s", i, scope.Units[i], want[i])
		}
	}
}

// TestJobTokenRuleListObjectsKeepsOtherPrincipals proves that the rule changes
// nothing for a caller that carries no job token.
func TestJobTokenRuleListObjectsKeepsOtherPrincipals(t *testing.T) {
	t.Parallel()

	unit := jobUnitObject(t, "acme", "web/prod", "unt-1")

	next := &allowingAuthorizer{seen: nil, scope: unitScope(unit), err: nil}

	// A rule with no port: another principal never reads it.
	rule := identity.JobTokenRule{Next: next, Units: nil, UnitIDs: nil}

	person := identity.Principal{
		Subject: "bob", DisplayName: "bob", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}

	scope, err := rule.ListObjects(t.Context(), person, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("ListObjects = %v, want nil", err)
	}

	checkScopeUnits(t, scope, []identity.Object{unit})
}
