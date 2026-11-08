package storetest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// RemovalBackupStore is the store of the removal log backup round trip: the
// removal log and the portable restore ports.
type RemovalBackupStore interface {
	PortableRestoreStore
	principal.RemovalLog
}

// RemovalBackupFactory returns a fresh, empty store for one side of the
// round trip.
type RemovalBackupFactory func(t *testing.T) RemovalBackupStore

// RunRemovalBackup proves that a backup and a restore keep the rows of the
// removal log. It stores removals of two sources in one store, restores a
// backup of it into a second store, then adds a removal at the same time and
// in the same window to both stores. The restored log must count the
// restored rows in its totals, as the log of the source store does.
func RunRemovalBackup(t *testing.T, newStore RemovalBackupFactory) {
	t.Helper()

	ctx := t.Context()
	src := newStore(t)
	dst := newStore(t)

	newNamespace(t, src, "acme")

	scim := principal.RemovalSource{Kind: principal.RemovalSourceSCIMToken, ID: "tok-1"}
	idp := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-1"}

	base := now()
	window := 2 * time.Hour

	for _, r := range []principal.Removal{
		{Source: scim, GroupID: "grp-eng", Removed: 5, MembersBefore: 50, At: base},
		{Source: idp, GroupID: "grp-eng", Removed: 3, MembersBefore: 40, At: base.Add(10 * time.Minute)},
		{Source: scim, GroupID: "grp-ops", Removed: 2, MembersBefore: 10, At: base.Add(20 * time.Minute)},
	} {
		if _, err := src.AddRemoval(ctx, r, window); err != nil {
			t.Fatalf("add a removal of %d members at %s: %v", r.Removed, r.At, err)
		}
	}

	applySnapshot(t, portableRestoreStores(t, src, dst))

	at := base.Add(time.Hour)

	cases := []struct {
		name    string
		removal principal.Removal
		want    principal.RemovalTotals
	}{
		{
			name:    "the SCIM token",
			removal: principal.Removal{Source: scim, GroupID: "grp-eng", Removed: 4, MembersBefore: 60, At: at},
			want:    principal.RemovalTotals{SourceRemoved: 11, GroupRemoved: 9, GroupMembers: 60},
		},
		{
			name:    "the identity provider",
			removal: principal.Removal{Source: idp, GroupID: "grp-eng", Removed: 1, MembersBefore: 45, At: at},
			want:    principal.RemovalTotals{SourceRemoved: 4, GroupRemoved: 4, GroupMembers: 45},
		},
	}

	for i := range cases {
		tt := cases[i]

		gotSrc, err := src.AddRemoval(ctx, tt.removal, window)
		if err != nil {
			t.Fatalf("%s: add a removal to the source store: %v", tt.name, err)
		}

		gotDst, err := dst.AddRemoval(ctx, tt.removal, window)
		if err != nil {
			t.Fatalf("%s: add a removal to the restored store: %v", tt.name, err)
		}

		if gotSrc != tt.want {
			t.Fatalf("%s: totals of the source store = %+v, want %+v", tt.name, gotSrc, tt.want)
		}

		if gotDst != tt.want {
			t.Fatalf("%s: totals of the restored store = %+v, want %+v", tt.name, gotDst, tt.want)
		}
	}
}
