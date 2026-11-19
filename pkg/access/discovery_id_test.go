package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// idAuthorizer is an Authorizer that names a fixed discovery grant ID.
type idAuthorizer struct {
	identity.Authorizer

	id  string
	err error
}

func (a idAuthorizer) DiscoveryGrantID(
	_ context.Context, _ identity.Principal, _ namespace.Name,
) (string, error) {
	return a.id, a.err
}

// discoveryIDCase is one ID that an authorizer names, and whether the port
// layer must accept it.
type discoveryIDCase struct {
	name  string
	id    string
	valid bool
}

// TestDiscoveryGrantIDRefusesBadShape proves that the port layer refuses an ID
// that is not a grant ID, in particular the bare prefix.
func TestDiscoveryGrantIDRefusesBadShape(t *testing.T) {
	t.Parallel()

	tests := []discoveryIDCase{
		{name: "bare prefix", id: access.GrantIDPrefix, valid: false},
		{name: "wrong prefix", id: "usr-abc", valid: false},
		{name: "prefix only in the middle", id: "x-grt-abc", valid: false},
		{name: "header injection", id: "grt-a\r\nX: y", valid: false},
		{name: "too long", id: access.GrantIDPrefix + strings.Repeat("a", 61), valid: false},
		{name: "one byte after prefix", id: access.GrantIDPrefix + "a", valid: true},
		{name: "valid", id: access.GrantIDPrefix + "abcdefghij0123456789", valid: true},
		{name: "longest", id: access.GrantIDPrefix + strings.Repeat("a", 60), valid: true},
		{name: "empty means no grant", id: "", valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkDiscoveryID(t, tt)
		})
	}
}

func checkDiscoveryID(t *testing.T, tt discoveryIDCase) {
	t.Helper()

	authz := idAuthorizer{Authorizer: nil, id: tt.id, err: nil}

	id, found, err := access.DiscoveryGrantID(t.Context(), authz, person(), "acme")
	if !found {
		t.Fatal("found = false; want true")
	}

	if !tt.valid {
		if !errors.Is(err, access.ErrInvalid) || id != "" {
			t.Fatalf("DiscoveryGrantID(%q) = %q, %v; want no ID, ErrInvalid", tt.id, id, err)
		}

		return
	}

	if err != nil || id != tt.id {
		t.Fatalf("DiscoveryGrantID(%q) = %q, %v; want the ID, nil", tt.id, id, err)
	}
}

// TestDiscoveryGrantIDKeepsAuthorizerError proves that an error of the
// authorizer reaches the caller with no ID.
func TestDiscoveryGrantIDKeepsAuthorizerError(t *testing.T) {
	t.Parallel()

	authz := idAuthorizer{Authorizer: nil, id: "", err: errReadFailed}

	id, found, err := access.DiscoveryGrantID(t.Context(), authz, person(), "acme")
	if !found || id != "" || !errors.Is(err, errReadFailed) {
		t.Fatalf("DiscoveryGrantID = %q, %v, %v; want no ID, true, the error", id, found, err)
	}
}

// TestGrantIDRefOfBarePrefix proves that the audit reference refuses the bare prefix.
func TestGrantIDRefOfBarePrefix(t *testing.T) {
	t.Parallel()

	if got := access.GrantIDRef(access.GrantIDPrefix); got != "grant:invalid" {
		t.Fatalf("GrantIDRef(bare prefix) = %q; want grant:invalid", got)
	}
}

// person returns a principal for the fake authorizer, which ignores it.
func person() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "usr-test",
		DisplayName:            "",
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}
