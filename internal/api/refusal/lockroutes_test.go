package refusal_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestLockedTextKeepsTheRouteOfALongReason proves that a long lock reason never
// cuts the route that removes the lock. The text of the answer stays within the
// message limit, the route sentence is whole, and the text is valid UTF-8.
func TestLockedTextKeepsTheRouteOfALongReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target manuallock.Target
		reason string
		remove string
	}{
		{
			name:   "unit, 500 ASCII bytes",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}),
			reason: strings.Repeat("x", 500),
			remove: "/api/inventory/unit-lock?unit=opentofu:acme/web",
		},
		{
			name:   "collection, 500 two-byte runes",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
			reason: strings.Repeat("é", 500),
			remove: "/api/dustpan/v1/namespaces/acme/collections/payments/lock",
		},
		{
			name:   "namespace, 500 ASCII bytes",
			target: manuallock.NamespaceTarget("acme"),
			reason: strings.Repeat("x", 500),
			remove: "/api/dustpan/v1/namespaces/acme/lock",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := &manuallock.LockedError{
				Lock: manuallock.Lock{
					Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
					Target: tc.target, Holder: "alice", Reason: tc.reason,
				},
				Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"},
				Noun: "workspace", Name: "acme/web",
			}

			got := refusal.LockedText(e)

			if len(got) > refusal.MaxLockedTextBytes {
				t.Fatalf("LockedText has %d bytes, want at most %d", len(got), refusal.MaxLockedTextBytes)
			}

			want := ". To remove the lock, the holder sends DELETE " + tc.remove + "."
			if !strings.HasSuffix(got, want) {
				t.Fatalf("LockedText = %q, want it to end with %q", got, want)
			}

			if !utf8.ValidString(got) {
				t.Fatalf("LockedText = %q, want valid UTF-8", got)
			}

			if refusal.Clean(got, refusal.MaxLockedTextBytes) != got {
				t.Fatal("Clean changes LockedText, so a later cut would remove the route")
			}
		})
	}
}

// TestRemovalRouteOfTheLongestUnit proves that the removal route of the longest
// legal unit reference is whole in the text of the refusal, and that the text
// stays within MaxLockedTextBytes. The longest namespace is 100 bytes, the
// longest tool code is 100 bytes, and the longest unit name is 300 bytes. The
// worst unit name is one in which each byte is written as "%XX" in the query.
// The test builds each reference with identity.UnitObject, so a reference that
// the server refuses never counts as legal.
func TestRemovalRouteOfTheLongestUnit(t *testing.T) {
	t.Parallel()

	longestNamespace, err := namespace.ParseName(strings.Repeat("n", 100))
	if err != nil {
		t.Fatalf("ParseName of 100 bytes: %v", err)
	}

	tool := strings.Repeat("t", 100)

	cases := []struct {
		name     string
		unitName string
		wantSize int
	}{
		{"short unit", "web", 0},
		{"pulumi unit of two names of 100 bytes", strings.Repeat("p", 100) + "/" + strings.Repeat("s", 100), 0},
		{"unit name with colon-free reserved characters", "web/a&c%d+e f", 0},
		{"unit name of 300 bytes that all need escaping", strings.Repeat("&", 300), 1179},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkLongestUnitText(t, tool, longestNamespace, tc.unitName, tc.wantSize)
		})
	}
}

// checkLongestUnitText checks the text of the refusal of a lock on the legal
// unit reference of tool, ns and unitName. A wantSize that is not 0 is the size
// of the route sentence that the comment of MaxLockedTextBytes gives.
func checkLongestUnitText(t *testing.T, tool string, ns namespace.Name, unitName string, wantSize int) {
	t.Helper()

	if _, err := identity.UnitObject(tool, ns, unitName); err != nil {
		t.Fatalf("UnitObject: %v, want a legal unit reference", err)
	}

	target := manuallock.UnitTarget(collection.UnitRef{Namespace: ns, Tool: tool, Name: unitName})
	got := refusal.WithRemoval(strings.Repeat("x", 2000), target)

	suffix := ". To remove the lock, the holder sends DELETE " + refusal.RemovalRoutes(target).Remove + "."
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("WithRemoval has %d bytes and does not end with the whole route sentence %q", len(got), suffix)
	}

	if len(got) > refusal.MaxLockedTextBytes {
		t.Fatalf("WithRemoval has %d bytes, want at most %d; the route sentence alone is %d bytes",
			len(got), refusal.MaxLockedTextBytes, len(suffix))
	}

	if wantSize != 0 && len(suffix) != wantSize {
		t.Fatalf("the longest route sentence has %d bytes, the comment of MaxLockedTextBytes says %d",
			len(suffix), wantSize)
	}

	if refusal.Clean(got, refusal.MaxLockedTextBytes) != got {
		t.Fatal("the cut of the answer changes the text, so it would remove the route")
	}
}

// TestWithRemovalCutsAnIllegalUnit proves that the identity check refuses a
// unit name above the legal limit, and that the text of a lock on such a name,
// if one reached this code, still has at most MaxLockedTextBytes bytes.
func TestWithRemovalCutsAnIllegalUnit(t *testing.T) {
	t.Parallel()

	space := namespace.Name("acme")
	tooLong := strings.Repeat("&", 301)

	if _, err := identity.UnitObject("pulumi", space, tooLong); err == nil {
		t.Fatal("UnitObject accepts a unit name of 301 bytes, want a refusal")
	}

	target := manuallock.UnitTarget(collection.UnitRef{Namespace: space, Tool: "pulumi", Name: tooLong})

	got := refusal.WithRemoval("locked", target)
	if len(got) > refusal.MaxLockedTextBytes {
		t.Fatalf("WithRemoval has %d bytes, want at most %d", len(got), refusal.MaxLockedTextBytes)
	}

	if !utf8.ValidString(got) {
		t.Fatalf("WithRemoval = %q, want valid UTF-8", got)
	}
}

// TestRemovalRoutesNameTheLockAPI proves the routes that remove the manual
// lock of each kind of object: the holder route and the force-unlock route.
// A reserved character of a name stays escaped, and the colon and the slash of
// a unit reference stay readable. LockedText names the holder route after the
// refusal.
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

			got := refusal.RemovalRoutes(tc.target)
			if got.Remove != tc.remove || got.Force != tc.force {
				t.Fatalf("RemovalRoutes = %+v, want remove %q and force %q", got, tc.remove, tc.force)
			}

			l := manuallock.Lock{
				Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
				Target: tc.target, Holder: "alice", Reason: "change window closed",
			}
			e := &manuallock.LockedError{
				Lock: l, Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"},
				Noun: "workspace", Name: "acme/web",
			}

			want := e.Refusal() + ". To remove the lock, the holder sends DELETE " + tc.remove + "."
			if got := refusal.LockedText(e); got != want {
				t.Fatalf("LockedText = %q, want %q", got, want)
			}

			if strings.Contains(e.Error(), "/api/") {
				t.Fatalf("Error = %q, want the domain text with no route", e.Error())
			}

			if !strings.HasSuffix(e.Refusal(), ": change window closed") {
				t.Fatalf("Refusal = %q, want the text that ends with the reason", e.Refusal())
			}
		})
	}
}
