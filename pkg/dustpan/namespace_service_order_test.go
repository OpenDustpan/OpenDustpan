package dustpan_test

import (
	"errors"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// orderCase is one call of the wired authorizer with the namespace service
// principal of acme. wantDenied says that the call is refused; wantAddr says
// that the refusal comes from the IP allowlist gate and not from the rule.
type orderCase struct {
	name       string
	addr       netip.Addr
	action     identity.Action
	obj        identity.Object
	wantDenied bool
	wantAddr   bool
}

// TestNamespaceServiceRuleAnswersBeforeAllowlist pins the order of the checks
// of the wired authorizer. The namespace service rule answers first, and the
// IP allowlist gate answers only when the rule allows. So when both would
// refuse, the caller gets the refusal of the rule, not the address refusal.
// A swap of the two layers in newAuthPorts lets an address outside the list use
// the one action of the principal, which this test catches.
func TestNamespaceServiceRuleAnswersBeforeAllowlist(t *testing.T) {
	t.Parallel()

	inList := netip.MustParseAddr("198.51.100.7")
	outList := netip.MustParseAddr("192.0.2.1")

	for _, tc := range []orderCase{
		{
			name: "the rule allows and the address is in the list", addr: inList,
			action: identity.ActionUnitRunPlan, obj: ownUnit(t), wantDenied: false, wantAddr: false,
		},
		{
			name: "the rule allows and the address is outside the list", addr: outList,
			action: identity.ActionUnitRunPlan, obj: ownUnit(t), wantDenied: true, wantAddr: true,
		},
		{
			name: "the rule refuses and the address is in the list", addr: inList,
			action: identity.ActionUnitRead, obj: ownUnit(t), wantDenied: true, wantAddr: false,
		},
		{
			name: "the rule refuses and the address is outside the list", addr: outList,
			action: identity.ActionUnitRead, obj: ownUnit(t), wantDenied: true, wantAddr: false,
		},
		{
			name: "a server action refused by the rule from outside the list", addr: outList,
			action: identity.ActionServerBackup, obj: identity.ServerObject(), wantDenied: true, wantAddr: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkOrderCase(t, tc)
		})
	}
}

// checkOrderCase runs one call of the wired authorizer and compares its
// refusal with tc.
func checkOrderCase(t *testing.T, tc orderCase) {
	t.Helper()

	svc, err := identity.NamespaceService("acme")
	if err != nil {
		t.Fatalf("namespace service principal: %v", err)
	}

	ctx := clientip.WithAddr(t.Context(), tc.addr)

	err = dustpan.AuthorizeWiredAllowlisted(ctx, allowEverything{}, svc, tc.action, tc.obj)

	if !tc.wantDenied {
		if err != nil {
			t.Fatalf("Authorize(%s, %s) = %v, want allowed", tc.action, tc.obj, err)
		}

		return
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Authorize(%s, %s) = %v, want ErrDenied", tc.action, tc.obj, err)
	}

	if isAddr := errors.Is(err, ipallow.ErrAddressNotAllowed); isAddr != tc.wantAddr {
		t.Errorf("Authorize(%s, %s) = %v, address refusal %t, want %t",
			tc.action, tc.obj, err, isAddr, tc.wantAddr)
	}
}

// ownUnit returns the object of a unit of acme.
func ownUnit(t *testing.T) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj
}
