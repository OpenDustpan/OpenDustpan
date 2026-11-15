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
// removal log and the time of the last alert of each source. It stores
// removals of two sources and their alert markers in one store, restores a
// backup of it into a second store, then adds a removal at the same time and
// in the same window to both stores. The restored log must count the
// restored rows and markers in its totals, as the log of the source store
// does.
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

	scimAlertedAt := base.Add(30 * time.Minute)
	idpAlertedAt := base.Add(35 * time.Minute)

	for _, marker := range []struct {
		source principal.RemovalSource
		at     time.Time
	}{
		{source: scim, at: scimAlertedAt},
		{source: idp, at: idpAlertedAt},
	} {
		if err := src.MarkRemovalAlert(ctx, marker.source, marker.at); err != nil {
			t.Fatalf("mark the alert of the source at %s: %v", marker.at, err)
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
			want: principal.RemovalTotals{
				SourceRemoved: 11, GroupRemoved: 9, GroupMembers: 60, AlertedAt: scimAlertedAt,
			},
		},
		{
			name:    "the identity provider",
			removal: principal.Removal{Source: idp, GroupID: "grp-eng", Removed: 1, MembersBefore: 45, At: at},
			want: principal.RemovalTotals{
				SourceRemoved: 4, GroupRemoved: 4, GroupMembers: 45, AlertedAt: idpAlertedAt,
			},
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

		wantRemovalTotals(t, tt.name+" in the source store", gotSrc, tt.want)
		wantRemovalTotals(t, tt.name+" in the restored store", gotDst, tt.want)
	}
}

// wantRemovalTotals fails the test unless the totals are the wanted ones.
func wantRemovalTotals(t *testing.T, what string, got, want principal.RemovalTotals) {
	t.Helper()

	if got.SourceRemoved != want.SourceRemoved || got.GroupRemoved != want.GroupRemoved ||
		got.GroupMembers != want.GroupMembers || !got.AlertedAt.Equal(want.AlertedAt) {
		t.Fatalf("%s: totals = %+v, want %+v", what, got, want)
	}
}
