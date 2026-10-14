package ipallow_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errNextDenied is the error the fake Next returns when it denies.
var errNextDenied = errors.New("next denied")

// errStoreDown is the error the fake Rules returns for a failed read.
var errStoreDown = errors.New("store down")

// fakeNext is an identity.Authorizer that counts calls and answers err.
type fakeNext struct {
	calls int
	err   error
}

func (f *fakeNext) Authorize(
	_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object,
) error {
	f.calls++

	return f.err
}

// fakeRules is an ipallow Rules port over a fixed map that counts calls.
type fakeRules struct {
	calls int
	lists map[namespace.Name]ipallow.List
	errs  map[namespace.Name]error
}

func (f *fakeRules) NamespaceList(_ context.Context, ns namespace.Name) (ipallow.List, error) {
	f.calls++

	if err, ok := f.errs[ns]; ok {
		return ipallow.List{}, err
	}

	if l, ok := f.lists[ns]; ok {
		return l, nil
	}

	return ipallow.List{}, nil
}

// mustList parses items or fails the test.
func mustList(t *testing.T, items []string) ipallow.List {
	t.Helper()

	l, err := ipallow.ParseList(items)
	if err != nil {
		t.Fatalf("parse list %q: %v", items, err)
	}

	return l
}

// adminPrincipal returns an admin principal with subject.
func adminPrincipal(subject string) identity.Principal {
	return identity.Principal{
		Subject:        subject,
		DisplayName:    subject,
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
}

// userPrincipal returns a non-admin principal with subject.
func userPrincipal(subject string) identity.Principal {
	return identity.Principal{
		Subject:        subject,
		DisplayName:    subject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
}

// ctxWithAddr returns a context holding addr.
func ctxWithAddr(addr netip.Addr) context.Context {
	return clientip.WithAddr(context.Background(), addr)
}

// gateWith returns a Gate over next and rules with the flags.
func gateWith(next *fakeNext, rules *fakeRules, enabled, includeBootstrap bool) ipallow.Gate {
	return ipallow.Gate{Next: next, Rules: rules, Enabled: enabled, IncludeBootstrap: includeBootstrap}
}

func TestGateDisabledCallsNextOnly(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
		errs:  map[namespace.Name]error{},
	}
	gate := gateWith(next, rules, false, false)
	obj := identity.NamespaceObject("acme")
	addr := netip.MustParseAddr("192.0.2.10")
	ctx := ctxWithAddr(addr)

	if err := gate.Authorize(ctx, adminPrincipal("alice"), identity.ActionNamespaceRead, obj); err != nil {
		t.Fatalf("Authorize disabled = %v, want nil", err)
	}

	if next.calls != 1 {
		t.Fatalf("Next calls = %d, want 1", next.calls)
	}

	if rules.calls != 0 {
		t.Fatalf("Rules calls = %d, want 0", rules.calls)
	}
}

func TestGateNoRuleAllows(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{calls: 0, lists: map[namespace.Name]ipallow.List{}, errs: map[namespace.Name]error{}}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	if err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj); err != nil {
		t.Fatalf("Authorize with no rule = %v, want nil", err)
	}
}

func TestGateAddressInsideAllows(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
		errs:  map[namespace.Name]error{},
	}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("198.51.100.7"))

	if err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj); err != nil {
		t.Fatalf("Authorize inside the list = %v, want nil", err)
	}
}

func TestGateAddressOutsideDeniesWithoutSecrets(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
		errs:  map[namespace.Name]error{},
	}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj)
	if err == nil {
		t.Fatal("Authorize outside the list = nil, want ErrDenied")
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize outside the list = %v, want ErrDenied", err)
	}

	text := err.Error()
	if !strings.Contains(text, "acme") {
		t.Fatalf("denied text %q holds no namespace name", text)
	}

	if strings.Contains(text, "198.51.100.0/24") {
		t.Fatalf("denied text %q leaks the list", text)
	}

	if strings.Contains(text, "192.0.2.10") {
		t.Fatalf("denied text %q leaks the address", text)
	}
}

func TestGateUnknownAddressDenied(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr netip.Addr
		bare bool
	}{
		{name: "bare context", addr: netip.Addr{}, bare: true},
		{name: "stored zero", addr: netip.Addr{}, bare: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &fakeNext{calls: 0, err: nil}
			rules := &fakeRules{
				calls: 0,
				lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
				errs:  map[namespace.Name]error{},
			}
			gate := gateWith(next, rules, true, false)
			obj := identity.NamespaceObject("acme")
			ctx := context.Background()
			if !tc.bare {
				ctx = ctxWithAddr(tc.addr)
			}

			if err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj); !errors.Is(
				err,
				identity.ErrDenied,
			) {
				t.Fatalf("Authorize with an unknown address = %v, want ErrDenied", err)
			}
		})
	}
}

func TestGateIPv6(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		allowed bool
	}{
		{name: "inside", address: "2001:db8::7", allowed: true},
		{name: "outside", address: "2001:db9::7", allowed: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &fakeNext{calls: 0, err: nil}
			rules := &fakeRules{
				calls: 0,
				lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"2001:db8::/32"})},
				errs:  map[namespace.Name]error{},
			}
			gate := gateWith(next, rules, true, false)
			obj := identity.NamespaceObject("acme")
			ctx := ctxWithAddr(netip.MustParseAddr(tc.address))

			err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj)
			if tc.allowed && err != nil {
				t.Fatalf("Authorize %s = %v, want nil", tc.address, err)
			}

			if !tc.allowed && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("Authorize %s = %v, want ErrDenied", tc.address, err)
			}
		})
	}
}

func TestGateMappedIPv4InsideIPv4Rule(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
		errs:  map[namespace.Name]error{},
	}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("::ffff:198.51.100.7"))

	if err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj); err != nil {
		t.Fatalf("Authorize 4in6 inside an IPv4 rule = %v, want nil", err)
	}
}

func TestGateNextDeniesWithoutRulesCall(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: errNextDenied}
	rules := &fakeRules{calls: 0, lists: map[namespace.Name]ipallow.List{}, errs: map[namespace.Name]error{}}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj)
	if !errors.Is(err, errNextDenied) {
		t.Fatalf("Authorize with a denying Next = %v, want the Next error", err)
	}

	if rules.calls != 0 {
		t.Fatalf("Rules calls = %d, want 0", rules.calls)
	}
}

func TestGateBootstrapExemption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		principal        identity.Principal
		includeBootstrap bool
		allowed          bool
	}{
		{
			name:             "bootstrap exempt",
			principal:        adminPrincipal("admin"),
			includeBootstrap: false,
			allowed:          true,
		},
		{
			name:             "bootstrap included",
			principal:        adminPrincipal("admin"),
			includeBootstrap: true,
			allowed:          false,
		},
		{
			name:             "non-admin named admin not exempt",
			principal:        userPrincipal("admin"),
			includeBootstrap: false,
			allowed:          false,
		},
		{
			name:             "admin with another subject not exempt",
			principal:        adminPrincipal("alice"),
			includeBootstrap: false,
			allowed:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &fakeNext{calls: 0, err: nil}
			rules := &fakeRules{
				calls: 0,
				lists: map[namespace.Name]ipallow.List{"acme": mustList(t, []string{"198.51.100.0/24"})},
				errs:  map[namespace.Name]error{},
			}
			gate := gateWith(next, rules, true, tc.includeBootstrap)
			obj := identity.NamespaceObject("acme")
			ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

			err := gate.Authorize(ctx, tc.principal, identity.ActionNamespaceRead, obj)
			if tc.allowed && err != nil {
				t.Fatalf("Authorize = %v, want nil", err)
			}

			if !tc.allowed && !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("Authorize = %v, want ErrDenied", err)
			}
		})
	}
}

func TestGateServerAndCallerObjectsSkipRules(t *testing.T) {
	t.Parallel()

	objs := []struct {
		name string
		obj  identity.Object
	}{
		{name: "installation", obj: identity.ServerObject()},
		{name: "caller", obj: identity.CallerObject()},
	}

	for _, tc := range objs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next := &fakeNext{calls: 0, err: nil}
			rules := &fakeRules{calls: 0, lists: map[namespace.Name]ipallow.List{}, errs: map[namespace.Name]error{}}
			gate := gateWith(next, rules, true, false)
			ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

			if err := gate.Authorize(ctx, adminPrincipal("alice"), identity.ActionServerObserve, tc.obj); err != nil {
				t.Fatalf("Authorize %s object = %v, want nil", tc.name, err)
			}

			if rules.calls != 0 {
				t.Fatalf("Rules calls = %d, want 0", rules.calls)
			}
		})
	}
}

func TestGateUnknownNamespaceAllows(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{},
		errs:  map[namespace.Name]error{"ghost": namespace.ErrNotFound},
	}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("ghost")
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	if err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj); err != nil {
		t.Fatalf("Authorize unknown namespace = %v, want nil", err)
	}
}

func TestGateRulesErrorFailsClosed(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{},
		errs:  map[namespace.Name]error{"acme": errStoreDown},
	}
	gate := gateWith(next, rules, true, false)
	obj := identity.NamespaceObject("acme")
	ctx := ctxWithAddr(netip.MustParseAddr("192.0.2.10"))

	err := gate.Authorize(ctx, userPrincipal("alice"), identity.ActionNamespaceRead, obj)
	if err == nil {
		t.Fatal("Authorize with a failed read = nil, want an error")
	}

	if errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize with a failed read = %v, want an error that is not ErrDenied", err)
	}

	if !strings.Contains(err.Error(), "acme") {
		t.Fatalf("failed-read text %q holds no namespace name", err.Error())
	}
}

func TestGateEachNamespaceUsesItsOwnList(t *testing.T) {
	t.Parallel()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{
			"acme": mustList(t, []string{"198.51.100.0/24"}),
			"glob": mustList(t, []string{"192.0.2.0/24"}),
		},
		errs: map[namespace.Name]error{},
	}
	gate := gateWith(next, rules, true, false)
	ctx := ctxWithAddr(netip.MustParseAddr("198.51.100.7"))

	acmeErr := gate.Authorize(
		ctx,
		userPrincipal("alice"),
		identity.ActionNamespaceRead,
		identity.NamespaceObject("acme"),
	)
	if acmeErr != nil {
		t.Fatalf("Authorize acme = %v, want nil", acmeErr)
	}

	globErr := gate.Authorize(
		ctx,
		userPrincipal("alice"),
		identity.ActionNamespaceRead,
		identity.NamespaceObject("glob"),
	)
	if !errors.Is(globErr, identity.ErrDenied) {
		t.Fatalf("Authorize glob = %v, want ErrDenied", globErr)
	}
}
