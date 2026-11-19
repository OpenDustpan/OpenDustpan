package access_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestValidGrantID checks the shape rule with the hostile table and with the
// shapes that the service mints.
func TestValidGrantID(t *testing.T) {
	t.Parallel()

	for _, id := range accesstest.HostileGrantIDs() {
		if access.ValidGrantID(id) {
			t.Errorf("ValidGrantID(%.20q) = true, want false", id)
		}
	}

	for _, id := range []string{"grt-a", "grt-0123456789abcdefghij", "grt-a.b_c-d", accesstest.GrantID("x")} {
		if !access.ValidGrantID(id) {
			t.Errorf("ValidGrantID(%q) = false, want true", id)
		}
	}
}

// readCountingStore counts the grant reads that reach the store.
type readCountingStore struct {
	access.Store

	reads *atomic.Int32
}

func (s *readCountingStore) GetGrant(ctx context.Context, id string) (access.Grant, error) {
	s.reads.Add(1)

	return s.Store.GetGrant(ctx, id)
}

// TestServiceRefusesAnIDThatIsNotAGrantID is the hostile table of the service
// layer. Each of the three reads by ID answers ErrInvalid before any store
// read. The test fails when a guard of the service is removed, because the
// store read count is then not 0.
func TestServiceRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	counting := &readCountingStore{Store: f.store, reads: new(atomic.Int32)}
	svc := f.serviceOverGrantStore(t, counting)

	calls := map[string]func(id string) error{
		"GetGrant": func(id string) error {
			_, err := svc.GetGrant(t.Context(), id)

			return err
		},
		"Revoke": func(id string) error {
			_, err := svc.Revoke(t.Context(), testEntry(), admin(), identity.ServerObject(), id)

			return err
		},
		"RevokeByID": func(id string) error {
			_, err := svc.RevokeByID(t.Context(), testEntry(), admin(), id)

			return err
		},
	}

	for name, call := range calls {
		for _, id := range accesstest.HostileGrantIDs() {
			err := call(id)
			if !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("%s(%.20q) = %v, want ErrInvalid", name, id, err)
			}

			if len(err.Error()) > 200 || (len(id) > 3 && strings.Contains(err.Error(), id)) {
				t.Fatalf("%s error text holds the ID: %.80q", name, err.Error())
			}
		}
	}

	if n := counting.reads.Load(); n != 0 {
		t.Fatalf("the service read the store %d times for IDs that are not grant IDs, want 0", n)
	}
}
