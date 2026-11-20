package githoststore_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/internal/store/githoststore"
)

const testTimeMs = 1_700_000_000_000

// noInt and noText return the NULL of a nullable column.
func noInt() sql.NullInt64 { return sql.NullInt64{Int64: 0, Valid: false} }

func noText() sql.NullString { return sql.NullString{String: "", Valid: false} }

func validRow() githoststore.RegistrationRow {
	const (
		appID   = "12345"
		baseURL = "https://api.github.com"
	)

	return githoststore.RegistrationRow{
		Kind: string(forge.KindGitHub), AppID: appID, BaseURL: baseURL, HookID: forge.NewHookID(),
		SealedKey: []byte("sealed-key"), KeySetID: forge.NewSetID(), KeyCreatedAtMs: testTimeMs,
		SealedEventSecret: []byte("sealed-event"), EventSecretSetID: forge.NewSetID(),
		EventSecretCreatedAtMs: testTimeMs, AddressDigest: forge.AddressDigest(forge.KindGitHub, baseURL, appID),
		Version: 1, CreatedAtMs: testTimeMs, UpdatedAtMs: testTimeMs, OldEventSecretEndsAtMs: noInt(),
		OldEventSecretSetID: noText(), OldSealedEventSecret: nil,
	}
}

func withWindow(row githoststore.RegistrationRow) githoststore.RegistrationRow {
	row.OldEventSecretEndsAtMs = sql.NullInt64{Int64: testTimeMs + 1000, Valid: true}
	row.OldEventSecretSetID = sql.NullString{String: forge.NewSetID(), Valid: true}
	row.OldSealedEventSecret = []byte("sealed-old-event")

	return row
}

// TestRegistrationOldEventSecret proves that the row mapper reads a row with no
// window and a row with a whole window, and that it refuses a row that holds a
// part of the old set, as another version of the server could have written it.
func TestRegistrationOldEventSecret(t *testing.T) {
	t.Parallel()

	none, err := githoststore.Registration(validRow())
	if err != nil || !none.OldEventSecretEndsAt.IsZero() || none.OldEventSecretSetID != "" ||
		len(none.OldSealedEventSecret) != 0 {
		t.Fatalf("row with NULL old columns = %+v, %v; want no window", none, err)
	}

	whole, err := githoststore.Registration(withWindow(validRow()))
	if err != nil {
		t.Fatalf("row with a window: %v", err)
	}

	if !whole.OldEventSecretEndsAt.Equal(time.UnixMilli(testTimeMs+1000)) || whole.OldEventSecretSetID == "" ||
		string(whole.OldSealedEventSecret) != "sealed-old-event" {
		t.Fatalf("row with a window = %+v, want the three old fields", whole)
	}

	full := withWindow(validRow())

	for name, edit := range map[string]func(r *githoststore.RegistrationRow){
		"only the end": func(r *githoststore.RegistrationRow) {
			r.OldEventSecretSetID, r.OldSealedEventSecret = noText(), nil
		},
		"only the set ID": func(r *githoststore.RegistrationRow) {
			r.OldEventSecretEndsAtMs, r.OldSealedEventSecret = noInt(), nil
		},
		"only the sealed value": func(r *githoststore.RegistrationRow) {
			r.OldEventSecretEndsAtMs, r.OldEventSecretSetID = noInt(), noText()
		},
		"no end":                func(r *githoststore.RegistrationRow) { r.OldEventSecretEndsAtMs = noInt() },
		"no set ID":             func(r *githoststore.RegistrationRow) { r.OldEventSecretSetID = noText() },
		"no sealed value":       func(r *githoststore.RegistrationRow) { r.OldSealedEventSecret = nil },
		"an empty sealed value": func(r *githoststore.RegistrationRow) { r.OldSealedEventSecret = []byte{} },
	} {
		row := full
		edit(&row)

		_, rowErr := githoststore.Registration(row)
		if !errors.Is(rowErr, forge.ErrInvalid) || !errors.Is(rowErr, githoststore.ErrCorrupt) {
			t.Errorf("row with %s = %v, want ErrInvalid and ErrCorrupt", name, rowErr)
		}
	}
}
