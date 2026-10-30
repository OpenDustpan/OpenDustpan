package storetest

import (
	"bytes"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// IssuerSeeder is an optional BackupStore capability: a store that keeps the
// issuer of each namespace gives its port, so the round trip fills and
// compares the two issuer tables. A store with no issuer port skips them: the
// fake of this package's own tests is one.
type IssuerSeeder interface {
	IssuerPorts() IssuerPorts
}

// IssuerPorts holds the issuer port of a store.
type IssuerPorts struct {
	Store profile.IssuerStore
}

// issuerTables returns the issuer tables in backup.Tables.
func issuerTables() []string {
	return []string{"issuer_keys", "issuer_settings"}
}

// seededIssuer names what seedBackupIssuer wrote.
type seededIssuer struct {
	keys     []profile.Key
	settings profile.IssuerSettings
}

// seedBackupIssuer writes an active, a next and a retired key and the
// settings with a copy check in the namespace acme. It returns nil for a
// store with no issuer port.
func seedBackupIssuer(t *testing.T, s BackupStore, acme namespace.Namespace) *seededIssuer {
	t.Helper()

	seeder, ok := s.(IssuerSeeder)
	if !ok {
		return nil
	}

	store := seeder.IssuerPorts().Store
	ctx := t.Context()
	at := now()

	key := func(state profile.KeyState) profile.Key {
		k := profile.Key{
			CreatedAt: at, ActivatedAt: time.Time{}, RetiredAt: time.Time{}, DeletedAt: time.Time{},
			ID: profile.NewKeyID(), Namespace: acme.Name, State: state, Public: []byte("public-" + string(state)),
			Sealed: nil,
		}
		if state.HoldsPrivate() {
			k.Sealed = []byte("sealed-" + string(state) + "\x00\xff")
		}

		if state == profile.KeyActive {
			k.ActivatedAt = at
		}

		if state == profile.KeyRetired {
			k.RetiredAt = at
		}

		return k
	}

	keys := []profile.Key{key(profile.KeyActive), key(profile.KeyNext), key(profile.KeyRetired)}
	entry := lifecycleEntry(acme.Name, "namespace:manage-issuer")

	err := store.Apply(ctx, acme.Name, profile.KeyChange{Add: keys, Move: nil, Initial: true}, acme.Fence(),
		[]audit.Entry{entry})
	if err != nil {
		t.Fatalf("seed issuer keys: %v", err)
	}

	set := profile.DefaultIssuerSettings(acme.Name)
	set.IssuerURL = "https://files.example.com/acme"
	set.IncludeActorName = true
	set.UpdatedAt = at
	set.UpdatedBy = "alice"
	set.SubjectTemplate = []string{"namespace", "unit", "access"}

	set, err = store.PutIssuerSettings(ctx, set, 0, acme.Fence(), []audit.Entry{entry})
	if err != nil {
		t.Fatalf("seed issuer settings: %v", err)
	}

	check := profile.CopyCheck{
		At: at, URL: set.IssuerURL, OK: true, Steps: []profile.CopyStep{{Name: "jwks", OK: true, Detail: "read"}},
		Verified: []profile.KeyID{keys[0].ID},
	}
	if err = store.RecordCopyCheck(ctx, acme.Name, check, acme.Fence(), []audit.Entry{entry}); err != nil {
		t.Fatalf("seed copy check: %v", err)
	}

	set.CopyCheck = &check

	return &seededIssuer{keys: keys, settings: set}
}

// checkIssuerRowCounts checks that sum holds the rows of the issuer tables,
// when the store keeps issuers: three keys and one settings row.
func checkIssuerRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	if _, ok := s.(IssuerSeeder); !ok {
		return
	}

	want := map[string]int64{"issuer_keys": 3, "issuer_settings": 1}
	for _, table := range issuerTables() {
		if got := sum.RowCounts[table]; got != want[table] {
			t.Errorf("exported rows of %q = %d, want %d", table, got, want[table])
		}
	}
}

// checkRestoredIssuer checks that dst holds the keys and the settings of the
// seed byte for byte after a restore of the same installation.
func checkRestoredIssuer(t *testing.T, dst RestoreStore, acme namespace.Namespace, seeded *seededIssuer) {
	t.Helper()

	seeder, ok := dst.(IssuerSeeder)
	if !ok || seeded == nil {
		return
	}

	store := seeder.IssuerPorts().Store

	live, err := store.Live(t.Context(), acme.Name)
	if err != nil || len(live) != len(seeded.keys) {
		t.Fatalf("restored issuer keys = %d, %v, want %d", len(live), err, len(seeded.keys))
	}

	for i := range seeded.keys {
		checkRestoredKey(t, live, &seeded.keys[i])
	}

	set, err := store.GetIssuerSettings(t.Context(), acme.Name)
	if err != nil || !sameSettings(&set, seeded) {
		t.Errorf("restored issuer settings = %+v, %v, want %+v", set, err, seeded.settings)
	}
}

// templateKeys is the count of keys of the seeded subject template.
const templateKeys = 3

func sameSettings(set *profile.IssuerSettings, seeded *seededIssuer) bool {
	return set.Version == seeded.settings.Version && set.IssuerURL == seeded.settings.IssuerURL &&
		set.IncludeActorName && len(set.SubjectTemplate) == templateKeys && set.CopyCheck != nil &&
		set.CopyCheck.Holds(set.IssuerURL, seeded.keys[0].ID)
}

// checkRestoredKey checks that live holds the key want byte for byte.
func checkRestoredKey(t *testing.T, live []profile.Key, want *profile.Key) {
	t.Helper()

	var got *profile.Key

	for i := range live {
		if live[i].ID == want.ID {
			got = &live[i]
		}
	}

	if got == nil || got.State != want.State || !bytes.Equal(got.Public, want.Public) ||
		!bytes.Equal(got.Sealed, want.Sealed) || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("restored issuer key %s = %+v, want %+v", want.ID, got, want)
	}
}
