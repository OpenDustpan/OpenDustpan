package storetest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ProfileReadInterleaver is an optional RestoreStore capability for the
// swap-during-read case. InterleaveProfileRead makes the next call of
// Revision on the profile port run fn once, after the store has read the
// profile and before it reads the revision. A store that cannot run a write
// there, because its reads hold the lock that a restore needs, does not
// implement it: the case then restores before the read.
type ProfileReadInterleaver interface {
	RestoreStore
	// InterleaveProfileRead sets fn for the next Revision call.
	InterleaveProfileRead(fn func())
}

// swapWait is how long the interleaved read waits for the swap to end. A
// swap that waits for the read to end, because the read holds one snapshot,
// does not end in this time. The read goes on, and the case waits for the
// swap after the read.
const swapWait = 2 * time.Second

// movingProfile is a profile that the swap case writes: its revisions grow
// between the backup and the restore.
type movingProfile struct {
	store profile.Store
	acme  namespace.Namespace
	id    profile.ID
}

// save saves revisions first to last, each on top of the one before.
func (m movingProfile) save(t *testing.T, first, last int) {
	t.Helper()

	for n := first; n <= last; n++ {
		at := profile.Anchor{Namespace: m.acme.Name, ID: m.id, Latest: n - 1}
		entry := []audit.Entry{lifecycleEntry(m.acme.Name, "profile:write")}

		rev := backupRevision(m.id, n, "again")

		_, err := m.store.Save(t.Context(), at, rev, m.acme.Fence(), entry)
		if err != nil {
			t.Fatalf("save revision %d: %v", n, err)
		}
	}
}

// createMovingProfile creates the profile with its first revision.
func createMovingProfile(t *testing.T, store profile.Store, acme namespace.Namespace) movingProfile {
	t.Helper()

	id := profile.NewID()
	p := profile.Profile{
		ID: id, Namespace: acme.Name, Name: "team/moving", Latest: 1, CreatedAt: now(), CreatedBy: "alice",
		UpdatedAt: now(),
	}

	entry := []audit.Entry{lifecycleEntry(acme.Name, "profile:write")}

	if _, err := store.Create(t.Context(), p, backupRevision(id, 1, "first"), acme.Fence(), entry); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	return movingProfile{store: store, acme: acme, id: id}
}

// testRestoreProfileRevisionDuringSwap restores a backup that is older than
// the live data while a read of the newest revision of a profile runs. The
// profile exists in both states, so the read must answer a revision of one
// of them: the newest revision of the live data (7) or of the backup (5).
// It must not answer that the revision does not exist. A store without a
// profile port skips the case.
func testRestoreProfileRevisionDuringSwap(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ctx := t.Context()
	s := newStore(t)
	acme := newNamespace(t, s, "acme")

	seeder, ok := s.(ProfileSeeder)
	if !ok {
		t.Skip("the store has no profile port")
	}

	ps := portableRestoreStores(t, s, s)
	moving := createMovingProfile(t, seeder.ProfileStore(), acme)

	moving.save(t, 2, 5)

	sink, sum, files := exportPortable(t, ps.src)

	moving.save(t, 6, 7)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage the older backup: %v", err)
	}

	swapped := make(chan error, 1)
	apply := func() { swapped <- ps.dst.ApplyStaging(ctx) }

	if inter, can := s.(ProfileReadInterleaver); can {
		inter.InterleaveProfileRead(func() {
			go apply()

			select {
			case err := <-swapped:
				swapped <- err
			case <-time.After(swapWait):
			}
		})
	} else {
		apply()
	}

	got, err := moving.store.Revision(ctx, acme.Name, moving.id, profile.At{Number: 0})
	if err != nil {
		t.Fatalf("newest revision during the restore: %v, want revision 5 or 7", err)
	}

	if got.Number != 5 && got.Number != 7 {
		t.Fatalf("newest revision during the restore = %d, want 5 or 7", got.Number)
	}

	if err = <-swapped; err != nil {
		t.Fatalf("apply the older backup: %v", err)
	}

	after, err := moving.store.Revision(ctx, acme.Name, moving.id, profile.At{Number: 0})
	if err != nil || after.Number != 5 {
		t.Fatalf("newest revision after the restore = %+v, %v, want revision 5", after, err)
	}
}
