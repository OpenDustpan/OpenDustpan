package manuallock_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// TestRemovalRoutesNameTheLockAPI proves the routes that remove the manual
// lock of each kind of object: the holder route and the force-unlock route.
// A reserved character of a name stays escaped, and the colon and the slash of
// a unit reference stay readable.
func TestRemovalRoutesNameTheLockAPI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target manuallock.Target
		remove string
		force  string
	}{
		{
			name:   "unit",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}),
			remove: "/api/inventory/unit-lock?unit=opentofu:acme/web",
			force:  "/api/inventory/unit-lock/force-unlock?unit=opentofu:acme/web",
		},
		{
			name:   "unit with a reserved character",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/a&b"}),
			remove: "/api/inventory/unit-lock?unit=pulumi:acme/web/a%26b",
			force:  "/api/inventory/unit-lock/force-unlock?unit=pulumi:acme/web/a%26b",
		},
		{
			name:   "collection",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
			remove: "/api/dustpan/v1/namespaces/acme/collections/payments/lock",
			force:  "/api/dustpan/v1/namespaces/acme/collections/payments/lock/force-unlock",
		},
		{
			name:   "namespace",
			target: manuallock.NamespaceTarget("acme"),
			remove: "/api/dustpan/v1/namespaces/acme/lock",
			force:  "/api/dustpan/v1/namespaces/acme/lock/force-unlock",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tc.target.RemovalRoutes()
			if got.Remove != tc.remove || got.Force != tc.force {
				t.Fatalf("RemovalRoutes = %+v, want remove %q and force %q", got, tc.remove, tc.force)
			}

			l := manuallock.Lock{
				Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
				Target: tc.target, Holder: "alice", Reason: "change window closed",
			}
			e := &manuallock.LockedError{Lock: l, Noun: "workspace", Name: "acme/web"}

			want := e.Refusal() + ". To remove the lock, the holder sends DELETE " + tc.remove + "."
			if e.Error() != want {
				t.Fatalf("Error = %q, want %q", e.Error(), want)
			}

			if !strings.HasSuffix(e.Refusal(), ": change window closed") {
				t.Fatalf("Refusal = %q, want the text that ends with the reason", e.Refusal())
			}
		})
	}
}
