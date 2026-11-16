package errcode_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestCallerCodes proves that the catalog declares each code that a route of
// the caller answers with: the 401 of a request with no token, the 404 of an
// unknown token, the 400 of a bad query, page size, page token or token, and
// the 409 of a replay of a create, with the argument that names the token.
func TestCallerCodes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		code string
		args []string
	}{
		{errcode.AuthUnauthenticated, nil},
		{errcode.TokenNotFound, nil},
		{errcode.TokenInvalid, nil},
		{errcode.RequestInvalidQuery, nil},
		{errcode.RequestPageSizeInvalid, nil},
		{errcode.RequestPageTokenInvalid, nil},
		{errcode.RequestIdempotencyKeyReplayedSecret, []string{"objectID"}},
	} {
		spec, ok := errcode.Lookup(c.code)
		if !ok {
			t.Errorf("code %q is not in the catalog", c.code)

			continue
		}

		names := make([]string, 0, len(spec.Args))
		for _, a := range spec.Args {
			names = append(names, a.Name)
		}

		if len(names) != len(c.args) {
			t.Errorf("code %q has the arguments %v, want %v", c.code, names, c.args)

			continue
		}

		for i := range names {
			if names[i] != c.args[i] {
				t.Errorf("code %q has the arguments %v, want %v", c.code, names, c.args)
			}
		}
	}
}
