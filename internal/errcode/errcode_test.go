package errcode_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

var errOther = errors.New("other")

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
		{"csrf before other site", errors.Join(sessionauth.ErrOtherSite, sessionauth.ErrCSRF), errcode.SessionCSRF},
		{"csrf before allowlist", errors.Join(ipallow.ErrAddressNotAllowed, sessionauth.ErrCSRF), errcode.SessionCSRF},
		{"refusal that is not a permission", errcode.Refusal("sign-in refused"), ""},
		{"wrapped refusal", fmt.Errorf("route: %w", errcode.Refusal("no action")), ""},
		{"refusal with a session error", errors.Join(errcode.Refusal("x"), sessionauth.ErrCSRF), ""},
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

	err := errcode.Refusal("sign-in refused")

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Refusal does not match identity.ErrDenied")
	}

	if got, want := err.Error(), "permission denied: sign-in refused"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}
