package storetest

import (
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ProfileSeeder is an optional BackupStore capability: a store that keeps
// profiles gives its port, so the round trip fills and compares the two
// profile tables. A store with no profile port skips them: the fake of this
// package's own tests is one.
type ProfileSeeder interface {
	ProfileStore() profile.Store
}

// profileTables returns the profile tables in backup.Tables.
func profileTables() []string {
	return []string{"profiles", "profile_revisions"}
}

// seededProfiles names what seedBackupProfiles wrote.
type seededProfiles struct {
	kept string // A profile with two revisions.
	gone string // A deleted profile: its rows stay, with a delete time.
	id   map[string]profile.ID
}

// profileDocument is a small valid document with a sealed-looking value, so
// the round trip proves that the text travels byte for byte.
const profileDocument = "schemaVersion: 1\r\nvalues:\r\n  region: eu-west-1 # ünïcode ✓\r\n"

// seedBackupProfiles writes two profiles in acme, one with a second revision
// and one deleted. It returns nil for a store with no profile port.
func seedBackupProfiles(t *testing.T, s BackupStore, acme namespace.Namespace) *seededProfiles {
	t.Helper()

	seeder, ok := s.(ProfileSeeder)
	if !ok {
		return nil
	}

	store := seeder.ProfileStore()
	ctx := t.Context()
	seeded := &seededProfiles{kept: "team/kept", gone: "gone", id: map[string]profile.ID{}}

	for _, name := range []string{seeded.kept, seeded.gone} {
		id := profile.NewID()
		p := profile.Profile{
			ID: id, Namespace: acme.Name, Name: profile.Name(name), Latest: 1, CreatedAt: now(), CreatedBy: "alice",
			UpdatedAt: now(),
		}

		first := backupRevision(id, 1, name)

		_, err := store.Create(ctx, p, first, acme.Fence(), []audit.Entry{lifecycleEntry(acme.Name, "profile:write")})
		if err != nil {
			t.Fatalf("create profile %s: %v", name, err)
		}

		seeded.id[name] = id
	}

	keptID := seeded.id[seeded.kept]
	at := profile.Anchor{Namespace: acme.Name, ID: keptID, Latest: 1}

	second := backupRevision(keptID, 2, "again")

	_, err := store.Save(ctx, at, second, acme.Fence(), []audit.Entry{lifecycleEntry(acme.Name, "profile:write")})
	if err != nil {
		t.Fatalf("save profile %s: %v", seeded.kept, err)
	}

	gone := profile.Anchor{Namespace: acme.Name, ID: seeded.id[seeded.gone], Latest: 0}
	err = store.Delete(ctx, gone, acme.Fence(), []audit.Entry{lifecycleEntry(acme.Name, "profile:manage")})
	if err != nil {
		t.Fatalf("delete profile %s: %v", seeded.gone, err)
	}

	return seeded
}

func backupRevision(id profile.ID, number int, reason string) profile.Revision {
	return profile.Revision{
		Profile: id, Number: number, Document: []byte(profileDocument), SHA256: fmt.Sprintf("%064x", number),
		Author: "alice", CreatedAt: now().Add(time.Duration(number) * time.Second), Reason: reason,
	}
}

// checkProfileRowCounts checks that sum holds the rows of the profile tables,
// when the store keeps profiles: two profiles, one of them deleted, and three
// revisions.
func checkProfileRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	if _, ok := s.(ProfileSeeder); !ok {
		return
	}

	want := map[string]int64{"profiles": 2, "profile_revisions": 3}
	for _, table := range profileTables() {
		if got := sum.RowCounts[table]; got != want[table] {
			t.Errorf("exported rows of %q = %d, want %d", table, got, want[table])
		}
	}
}

// checkRestoredProfiles checks that dst holds the kept profile with both
// revisions, byte for byte, and no live deleted profile, after a restore.
func checkRestoredProfiles(t *testing.T, dst RestoreStore, acme namespace.Namespace, seeded *seededProfiles) {
	t.Helper()

	seeder, ok := dst.(ProfileSeeder)
	if !ok || seeded == nil {
		return
	}

	store := seeder.ProfileStore()
	ctx := t.Context()

	kept, err := store.Get(ctx, acme.Name, profile.RefByName(profile.Name(seeded.kept)))
	if err != nil || kept.ID != seeded.id[seeded.kept] || kept.Latest != 2 {
		t.Fatalf("restored profile %s = %+v, %v, want its ID and revision 2", seeded.kept, kept, err)
	}

	for number := 1; number <= 2; number++ {
		r, revErr := store.Revision(ctx, acme.Name, kept.ID, profile.At{Number: number})
		if revErr != nil || string(r.Document) != profileDocument || r.Number != number {
			t.Fatalf("restored revision %d = %+v, %v, want the document as written", number, r, revErr)
		}
	}

	if _, err = store.Get(ctx, acme.Name, profile.RefByName(profile.Name(seeded.gone))); err == nil {
		t.Errorf("restored deleted profile %s is live", seeded.gone)
	}
}
