package codec_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// knownRow returns a row with the kind kind and no other field but the ID.
func knownRow(kind string) codec.AccessTokenRow {
	return codec.AccessTokenRow{
		ID: "tok-1", PrincipalID: "", Name: "", Kind: kind, ReplacedBy: "", CreatedBy: "",
		CreatedAtMs: 0, ExpiresAtMs: 0, LastUsedAtMs: 0, RevokedAtMs: 0, DisabledAtMs: 0,
	}
}

// TestFromAccessTokenRowKinds checks the allowlist of the stored kind: a known
// kind gives a token, any other value gives ErrUnknownKind and no token.
func TestFromAccessTokenRowKinds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind    string
		unknown bool
	}{
		{string(accesstoken.KindPersonal), false},
		{string(accesstoken.KindService), false},
		{string(accesstoken.KindJWTExchange), false},
		{"trust", true},
		{"", true},
		{"Personal", true},
		{"service ", true},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()

			tok, err := codec.FromAccessTokenRow(knownRow(tc.kind))

			if got := errors.Is(err, accesstoken.ErrUnknownKind); got != tc.unknown {
				t.Fatalf("kind %q: err = %v, want ErrUnknownKind = %v", tc.kind, err, tc.unknown)
			}

			if want := accesstoken.Kind(tc.kind); tc.unknown == (tok.Kind == want) && tc.kind != "" {
				t.Errorf("kind %q: token kind %q does not match the verdict", tc.kind, tok.Kind)
			}

			if tc.unknown && tok.ID != "" {
				t.Errorf("kind %q: returned token %+v, want the zero token", tc.kind, tok)
			}
		})
	}
}
