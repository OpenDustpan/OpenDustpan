package errcode_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

var errOther = errors.New("other")

// limitedRefusal is the error of the limited authorizer: a gate refusal that
// identity.ErrDenied wraps.
func limitedRefusal(sentinel error) error {
	return fmt.Errorf("%w: %w", identity.ErrDenied, &gate.RefusalError{Err: sentinel, Record: "", Messages: nil})
}

// TestForbiddenOrder pins the order of the causes. Every session error and the
// allowlist error wrap identity.ErrDenied, so the first match wins and the
// more specific cause must come before the missing permission.
func TestForbiddenOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"missing permission", identity.ErrDenied, errcode.AccessDenied},
		{"wrapped permission", fmt.Errorf("authorize: %w", identity.ErrDenied), errcode.AccessDenied},
		{"csrf", sessionauth.ErrCSRF, errcode.SessionCSRF},
		{"other site", sessionauth.ErrOtherSite, errcode.SessionOtherSite},
		{"allowlist", ipallow.ErrAddressNotAllowed, errcode.AccessIPNotAllowed},
		{"csrf before denied", errors.Join(identity.ErrDenied, sessionauth.ErrCSRF), errcode.SessionCSRF},
		{
			"other site before denied",
			errors.Join(identity.ErrDenied, sessionauth.ErrOtherSite),
			errcode.SessionOtherSite,
		},
		{
			"allowlist before denied",
			errors.Join(identity.ErrDenied, ipallow.ErrAddressNotAllowed),
			errcode.AccessIPNotAllowed,
		},
		{"csrf before allowlist", errors.Join(ipallow.ErrAddressNotAllowed, sessionauth.ErrCSRF), errcode.SessionCSRF},
		{
			"refusal that is not a permission",
			errcode.Refusal(errcode.SignInRefused, "sign-in refused"),
			errcode.SignInRefused,
		},
		{
			"wrapped refusal",
			fmt.Errorf("route: %w", errcode.Refusal(errcode.AccessNoAction, "no action")),
			errcode.AccessNoAction,
		},
		{
			"refusal before a session error",
			errors.Join(errcode.Refusal(errcode.SignInRefused, "x"), sessionauth.ErrCSRF),
			errcode.SignInRefused,
		},
		{"access limit", gate.ErrLimited, errcode.GateLimited},
		{"access limit that wraps a permission", limitedRefusal(gate.ErrLimited), errcode.GateLimited},
		{"limit that failed to evaluate", limitedRefusal(gate.ErrRuleFailed), errcode.GateRuleFailed},
		{
			"allowlist before access limit",
			errors.Join(ipallow.ErrAddressNotAllowed, gate.ErrLimited),
			errcode.AccessIPNotAllowed,
		},
		{"csrf before access limit", errors.Join(gate.ErrLimited, sessionauth.ErrCSRF), errcode.SessionCSRF},
		{
			"refusal that is not a permission before access limit",
			errors.Join(errcode.Refusal(errcode.SignInRefused, "x"), gate.ErrLimited),
			errcode.SignInRefused,
		},
		{"refusal without a code", errcode.Refusal("", "x"), errcode.AccessDenied},
		{
			"refusal with a code that is not in the catalog",
			errcode.Refusal("sign-in.refsued", "x"),
			errcode.AccessDenied,
		},
		{
			"refusal with an unknown code before a session error",
			errors.Join(errcode.Refusal("sign-in.refsued", "x"), sessionauth.ErrCSRF),
			errcode.SessionCSRF,
		},
		{"no cause", errOther, ""},
		{"unauthenticated", identity.ErrUnauthenticated, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := errcode.Forbidden(tc.err); got != tc.want {
				t.Fatalf("Forbidden(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestRefusalKeepsTheDeniedText proves that a refusal answers 403 as a denied
// request with the text of the old sentinel wrap.
func TestRefusalKeepsTheDeniedText(t *testing.T) {
	t.Parallel()

	err := errcode.Refusal(errcode.SignInRefused, "sign-in refused")

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatal("Refusal does not match identity.ErrDenied")
	}

	if got, want := err.Error(), "permission denied: sign-in refused"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestForbiddenAlwaysCodesADenial proves that every error that wraps
// identity.ErrDenied gets a code of the catalog, so that no 403 answers
// without a code.
func TestForbiddenAlwaysCodesADenial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"denied", identity.ErrDenied},
		{"refused sign-in", errcode.Refusal(errcode.SignInRefused, "sign-in refused")},
		{"route without an action", fmt.Errorf("route: %w", errcode.Refusal(errcode.AccessNoAction, "no action"))},
		{"limited", limitedRefusal(gate.ErrLimited)},
		{"csrf", errors.Join(identity.ErrDenied, sessionauth.ErrCSRF)},
		{"refusal without a code", errcode.Refusal("", "x")},
		{"refusal with a code that is not in the catalog", errcode.Refusal("sign-in.refsued", "x")},
		{"refusal with a code that is not valid", errcode.Refusal("Not A Code", "x")},
		{"wrapped refusal without a code", fmt.Errorf("route: %w", errcode.Refusal("", "x"))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code := errcode.Forbidden(tc.err)
			if _, ok := errcode.Lookup(code); !ok {
				t.Fatalf("Forbidden(%v) = %q, want a code of the catalog", tc.err, code)
			}
		})
	}
}
