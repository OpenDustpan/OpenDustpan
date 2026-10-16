package ipallow_test

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// checkAddressCase is one CheckAddress case: the gate flags, the namespace,
// the rule tables, the client address and the want.
type checkAddressCase struct {
	name             string
	enabled          bool
	includeBootstrap bool
	ns               namespace.Name
	lists            map[namespace.Name]ipallow.List
	errs             map[namespace.Name]error
	addr             netip.Addr
	bare             bool
	wantDenied       bool
	wantErr          bool
}

// checkAddressCases returns every CheckAddress case.
func checkAddressCases(outside, inside netip.Addr) []checkAddressCase {
	return []checkAddressCase{
		{
			name:             "off allows without a store read",
			enabled:          false,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             outside,
			bare:             false,
			wantDenied:       false,
			wantErr:          false,
		},
		{
			name:             "empty namespace allows",
			enabled:          true,
			includeBootstrap: false,
			ns:               "",
			lists:            nil,
			errs:             nil,
			addr:             outside,
			bare:             false,
			wantDenied:       false,
			wantErr:          false,
		},
		{
			name:             "no rule allows",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            map[namespace.Name]ipallow.List{},
			errs:             map[namespace.Name]error{},
			addr:             outside,
			bare:             false,
			wantDenied:       false,
			wantErr:          false,
		},
		{
			name:             "inside allows",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             inside,
			bare:             false,
			wantDenied:       false,
			wantErr:          false,
		},
		{
			name:             "outside denies",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             outside,
			bare:             false,
			wantDenied:       true,
			wantErr:          false,
		},
		{
			name:             "unknown address denies",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             netip.Addr{},
			bare:             true,
			wantDenied:       true,
			wantErr:          false,
		},
		{
			name:             "store error fails closed",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             map[namespace.Name]error{"acme": errStoreDown},
			addr:             outside,
			bare:             false,
			wantDenied:       false,
			wantErr:          true,
		},
		{
			name:             "unknown namespace reads as no rule",
			enabled:          true,
			includeBootstrap: false,
			ns:               "ghost",
			lists:            nil,
			errs:             map[namespace.Name]error{"ghost": namespace.ErrNotFound},
			addr:             outside,
			bare:             false,
			wantDenied:       false,
			wantErr:          false,
		},
		{
			name:             "no bootstrap exemption when excluded",
			enabled:          true,
			includeBootstrap: false,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             outside,
			bare:             false,
			wantDenied:       true,
			wantErr:          false,
		},
		{
			name:             "no bootstrap exemption when included",
			enabled:          true,
			includeBootstrap: true,
			ns:               "acme",
			lists:            nil,
			errs:             nil,
			addr:             outside,
			bare:             false,
			wantDenied:       true,
			wantErr:          false,
		},
	}
}

// checkAddressGate returns a gate for tc with its call counters.
func checkAddressGate(t *testing.T, tc checkAddressCase) (ipallow.Gate, *fakeNext, *fakeRules) {
	t.Helper()

	next := &fakeNext{calls: 0, err: nil}
	lists := map[namespace.Name]ipallow.List{
		"acme": mustList(t, []string{"198.51.100.0/24"}),
	}

	if tc.lists != nil {
		lists = map[namespace.Name]ipallow.List{}
		maps.Copy(lists, tc.lists)
	}

	errs := map[namespace.Name]error{}
	maps.Copy(errs, tc.errs)

	rules := &fakeRules{calls: 0, lists: lists, errs: errs}

	return gateWith(next, rules, tc.enabled, tc.includeBootstrap), next, rules
}

// expectDenied proves err denies for ns without naming the list or addr.
func expectDenied(t *testing.T, err error, ns namespace.Name, addr netip.Addr) {
	t.Helper()

	if err == nil {
		t.Fatal("CheckAddress = nil, want ErrDenied")

		return
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("CheckAddress = %v, want ErrDenied", err)
	}

	text := err.Error()
	if !strings.Contains(text, string(ns)) {
		t.Fatalf("denied text %q holds no namespace name", text)
	}

	if strings.Contains(text, "198.51.100.0/24") {
		t.Fatalf("denied text %q leaks the list", text)
	}

	if addr.IsValid() && strings.Contains(text, addr.String()) {
		t.Fatalf("denied text %q leaks the address", text)
	}
}

// expectStoreError proves err fails closed for ns without ErrDenied.
func expectStoreError(t *testing.T, err error, ns namespace.Name) {
	t.Helper()

	if err == nil {
		t.Fatal("CheckAddress = nil, want a store error")
	}

	if errors.Is(err, identity.ErrDenied) {
		t.Fatalf("CheckAddress = %v, want an error that is not ErrDenied", err)
	}

	if !strings.Contains(err.Error(), string(ns)) {
		t.Fatalf("failed-read text %q holds no namespace name", err.Error())
	}
}

// runCheckAddressCase runs one case and proves the outcome and the calls.
func runCheckAddressCase(t *testing.T, tc checkAddressCase) {
	t.Helper()

	gate, next, rules := checkAddressGate(t, tc)

	ctx := ctxWithAddr(tc.addr)
	if tc.bare {
		ctx = context.Background()
	}

	err := gate.CheckAddress(ctx, tc.ns)

	switch {
	case tc.wantErr:
		expectStoreError(t, err, tc.ns)
	case tc.wantDenied:
		expectDenied(t, err, tc.ns, tc.addr)
	default:
		if err != nil {
			t.Fatalf("CheckAddress = %v, want nil", err)
		}
	}

	if !tc.enabled && rules.calls != 0 {
		t.Fatalf("Rules calls = %d, want 0", rules.calls)
	}

	if tc.ns == "" && rules.calls != 0 {
		t.Fatalf("Rules calls = %d, want 0", rules.calls)
	}

	if next.calls != 0 {
		t.Fatalf("Next calls = %d, want 0: CheckAddress never asks Next", next.calls)
	}
}

// TestCheckAddress proves the address-only check for the capability routes:
// it allows when the feature is off, the namespace is empty or holds no
// rule, and the client address is inside the list; it denies an outside or
// unknown address with ErrDenied that names the namespace and never the
// list; a store error fails closed; an unknown namespace reads as no rule;
// and there is no bootstrap exemption because no principal exists here.
func TestCheckAddress(t *testing.T) {
	t.Parallel()

	inside := netip.MustParseAddr("198.51.100.7")
	outside := netip.MustParseAddr("192.0.2.10")

	for _, tc := range checkAddressCases(outside, inside) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCheckAddressCase(t, tc)
		})
	}
}
