package accesstoken_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestKindForServerPrincipal proves that no token kind exists for a
// server-made principal, so no token can name one.
func TestKindForServerPrincipal(t *testing.T) {
	t.Parallel()

	for _, k := range []principal.Kind{principal.KindServer, principal.KindWorkload, "", "Server"} {
		if got, err := accesstoken.KindFor(k); !errors.Is(err, accesstoken.ErrNoTokenForKind) || got != "" {
			t.Errorf("KindFor(%q) = %q, %v; want ErrNoTokenForKind", k, got, err)
		}
	}
}
