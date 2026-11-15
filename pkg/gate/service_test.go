package gate_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// stubStore, stubEngine and the other stubs fill the ports that a case does
// not reach. A call to a method of an embedded nil port panics, so a case
// that reaches one fails.
type stubStore struct{ gate.Store }

type stubEngine struct{ gate.Engine }

type stubFences struct{}

func (stubFences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}, nil
}

type stubUnits struct{}

func (stubUnits) Unit(context.Context, identity.Object) (gate.UnitInfo, error) {
	return gate.UnitInfo{Collection: "", Labels: nil}, nil
}

// subjects gives the Subject of each principal by its subject, with the
// break-glass flag for the subject "breakglass".
type subjects struct{}

func (subjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	return gate.Subject{
		ID: p.Subject, Groups: nil, Kind: gate.PrincipalPerson, Credential: gate.CredentialSession,
		AuthAt: time.Time{}, BreakGlassAccount: p.Subject == "breakglass", Job: nil,
	}, nil
}

func newService(t *testing.T) *gate.Service {
	t.Helper()

	svc, err := gate.NewService(gate.ServiceConfig{
		Store: stubStore{Store: nil}, Engine: stubEngine{Engine: nil}, Producers: nil, Units: stubUnits{},
		Subjects: subjects{}, Fences: stubFences{}, Now: nil, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	return svc
}

func TestNewServiceNeedsPorts(t *testing.T) {
	t.Parallel()

	_, err := gate.NewService(gate.ServiceConfig{
		Store:     nil,
		Engine:    nil,
		Producers: nil,
		Units:     nil,
		Subjects:  nil,
		Fences:    nil,
		Now:       nil,
		Log:       nil,
	})
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("NewService(nil ports) = %v, want ErrInvalid", err)
	}
}

func unitObject(t *testing.T, name string) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

// jobWrite returns a change at state.write with a job token of the job
// that holds the unit net.
func jobWrite(t *testing.T, unit string, passedApply, planLock bool) gate.Change {
	t.Helper()

	return gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme",
		Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""}, Source: "",
		Subject: gate.Subject{
			ID: "job:42", Groups: nil, Kind: gate.PrincipalService, Credential: gate.CredentialJobToken,
			AuthAt: time.Time{}, BreakGlassAccount: false,
			Job: &gate.JobClaim{
				Job: "42", Phase: "apply", PassedApply: passedApply, Units: []identity.Object{unitObject(t, "net")},
			},
		},
		Author: "alice", Operation: gate.OperationLock, Units: []identity.Object{unitObject(t, unit)},
		Holders: nil, PlanLock: planLock, BreakGlass: "",
		Fence: namespace.Fence{Namespace: "acme", FailoverVersion: 1}, State: nil,
	}
}

func TestEvaluateJobTokenAtStateWrite(t *testing.T) {
	t.Parallel()

	svc := newService(t)

	allowed := []struct {
		name   string
		change gate.Change
	}{
		{"a phase that passed change.apply writes its unit", jobWrite(t, "net", true, false)},
		{"the plan lock of a unit of the job", jobWrite(t, "net", false, true)},
	}

	for _, tc := range allowed {
		d, err := svc.Evaluate(t.Context(), tc.change)
		if err != nil || d.Result != gate.ResultAllow || d.Record != "" {
			t.Fatalf("%s: Evaluate() = %+v, %v, want allow with no record", tc.name, d, err)
		}
	}

	noClaim := jobWrite(t, "net", true, false)
	noClaim.Subject.Job = nil

	denied := []struct {
		name   string
		change gate.Change
	}{
		{"a phase that did not pass change.apply", jobWrite(t, "net", false, false)},
		{"a unit that is not of the job", jobWrite(t, "dns", true, false)},
		{"the plan lock of a unit that is not of the job", jobWrite(t, "dns", false, true)},
		{"a job token with no job", noClaim},
	}

	for _, tc := range denied {
		d, err := svc.Evaluate(t.Context(), tc.change)
		if !errors.Is(err, identity.ErrDenied) || d.Record != "" {
			t.Fatalf("%s: Evaluate() = %+v, %v, want identity.ErrDenied and no record", tc.name, d, err)
		}
	}
}

func TestEvaluateRefusesBadChange(t *testing.T) {
	t.Parallel()

	svc := newService(t)

	noUnit := jobWrite(t, "net", true, false)
	noUnit.Units = nil

	badPoint := jobWrite(t, "net", true, false)
	badPoint.Point = "job.start"

	notUnit := jobWrite(t, "net", true, false)
	notUnit.Units = []identity.Object{identity.NamespaceObject("acme")}

	for _, c := range []gate.Change{noUnit, badPoint, notUnit} {
		if _, err := svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("Evaluate(%s, %d units) = %v, want ErrInvalid", c.Point, len(c.Units), err)
		}
	}
}

func TestEvaluateNotImplementedFailsClosed(t *testing.T) {
	t.Parallel()

	c := jobWrite(t, "net", true, false)
	c.Point = gate.PointChangePlanned

	if _, err := newService(t).Evaluate(t.Context(), c); !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Evaluate(change.planned) = %v, want ErrUnavailable", err)
	}
}

func principal(subject string, admin bool) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: "", Admin: admin, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}
}

// grants allows each call of the subjects in allow.
type grants struct{ allow map[string]bool }

func (g grants) Authorize(_ context.Context, p identity.Principal, _ identity.Action, _ identity.Object) error {
	if g.allow[p.Subject] {
		return nil
	}

	return identity.ErrDenied
}

func (grants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Every(), nil
}

// TestLimitedAuthorizerScopeNotExact proves that the decorator does not say
// that its scope is exact: it gives the scope of the grants and checks the
// access limits for each object.
func TestLimitedAuthorizerScopeNotExact(t *testing.T) {
	t.Parallel()

	auth := gate.LimitedAuthorizer(grants{allow: nil}, newService(t))
	if identity.HasExactScope(auth) {
		t.Fatal("LimitedAuthorizer says that its scope is exact")
	}
}

func TestLimitedAuthorizer(t *testing.T) {
	t.Parallel()

	inner := grants{allow: map[string]bool{"breakglass": true, "admin": true}}
	auth := gate.LimitedAuthorizer(inner, newService(t))
	obj := identity.NamespaceObject("acme")

	err := auth.Authorize(t.Context(), principal("breakglass", false), identity.ActionNamespaceRead, obj)
	if err != nil {
		t.Fatalf("Authorize(break-glass account) = %v, want nil: it is outside the access limits", err)
	}

	// A namespace is not checked yet: that failure is not an access refusal, so
	// it keeps its cause and does not match identity.ErrDenied. The call is
	// still refused.
	err = auth.Authorize(t.Context(), principal("admin", true), identity.ActionNamespaceRead, obj)
	if errors.Is(err, identity.ErrDenied) || !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Authorize(admin) = %v, want ErrUnavailable without identity.ErrDenied", err)
	}

	err = auth.Authorize(t.Context(), principal("mallory", false), identity.ActionNamespaceRead, obj)
	if !errors.Is(err, identity.ErrDenied) || errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Authorize(no grant) = %v, want the error of the grants", err)
	}

	scope, err := auth.ListObjects(t.Context(), principal("admin", true), identity.ActionNamespaceRead)
	if err != nil || !scope.CoversEveryObject() {
		t.Fatalf("ListObjects() = %+v, %v, want the scope of the grants", scope, err)
	}
}

// TestLimitedAuthorizerKeepsAnInfrastructureFailure checks that a unit source
// that does not answer fails the call with its own cause. It is a store
// failure, not an access refusal, so it does not match identity.ErrDenied.
func TestLimitedAuthorizerKeepsAnInfrastructureFailure(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swFailingUnits{}, nil)
	auth := gate.LimitedAuthorizer(grants{allow: map[string]bool{"ci": true}}, e.svc)

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	err = auth.Authorize(t.Context(), principal("ci", false), identity.ActionUnitWrite, unit)
	if err == nil || !errors.Is(err, gate.ErrUnavailable) || !errors.Is(err, errUnitsDown) ||
		errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize(unit of an unreadable source) = %v, "+
			"want ErrUnavailable with the source error and no identity.ErrDenied", err)
	}
}

// TestLimitedAuthorizerErrorKinds checks the classes of the errors of the
// access limits. A refusal of a limit matches ErrLimited and identity.ErrDenied.
// A failure that is not a refusal keeps its own cause and does not match
// identity.ErrDenied.
func TestLimitedAuthorizerErrorKinds(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnits{collections: nil, labels: nil}, nil)
	e.putLimit(t, swUnitScope(t, "web"), "limit-1", forbidServices)

	auth := gate.LimitedAuthorizer(grants{allow: map[string]bool{"ci": true}}, e.svc)

	unit, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	err = auth.Authorize(t.Context(), principal("ci", false), identity.ActionUnitWrite, unit)
	if !errors.Is(err, gate.ErrLimited) || !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize(limited unit) = %v, want ErrLimited and identity.ErrDenied", err)
	}

	ns := identity.NamespaceObject("acme")

	err = auth.Authorize(t.Context(), principal("ci", false), identity.ActionNamespaceRead, ns)
	if err == nil || !errors.Is(err, gate.ErrUnavailable) || errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize(namespace) = %v, want ErrUnavailable without identity.ErrDenied", err)
	}
}

func TestUnitChain(t *testing.T) {
	t.Parallel()

	net := unitObject(t, "net")

	chain, err := gate.UnitChain(net, "Prod")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"installation", "namespace/acme", "collection/acme/prod", "unit/acme/opentofu/net"}
	if len(chain) != len(want) {
		t.Fatalf("UnitChain() = %v, want %v", chain, want)
	}

	for i, s := range chain {
		if s.Key() != want[i] {
			t.Fatalf("UnitChain()[%d] = %q, want %q", i, s, want[i])
		}
	}

	if chain, err = gate.UnitChain(net, ""); err != nil || len(chain) != 3 {
		t.Fatalf("UnitChain(no collection) = %v, %v, want 3 scopes", chain, err)
	}

	if _, err := gate.UnitChain(identity.NamespaceObject("acme"), ""); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("UnitChain(namespace) = %v, want ErrInvalid", err)
	}
}
