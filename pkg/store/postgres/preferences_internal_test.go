package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// prefsEntry returns a valid audit entry of a preferences change.
func openPreferencesStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), membershipURL(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

func prefsEntry(id string, at time.Time) audit.Entry {
	return audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "alice@example.com", IDPSubject: "", TokenID: "",
		Action: preferences.AuditAction, Object: principal.Ref(id), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

func prefsPerson(t *testing.T, s *Store) string {
	t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: principal.IDPrefix + "alice",
		Subject: "alice@example.com", DisplayName: "Alice", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	e := prefsEntry(p.ID, at)
	e.Action = "server:manage-principals"

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}

	return p.ID
}

func countPreferenceRows(t *testing.T, s *Store, id string) int {
	t.Helper()

	var n int

	row := s.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM person_preferences WHERE principal_id = $1`, id)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}

	return n
}

// TestPreferencesRowGoesWithThePerson proves that the end of a person deletes
// its row in the same transaction, and that a no-op patch writes no row.
func TestPreferencesRowGoesWithThePerson(t *testing.T) {
	t.Parallel()

	s := openPreferencesStore(t)
	id := prefsPerson(t, s)
	at := time.UnixMilli(1_700_000_100_000).UTC()

	if _, err := s.Update(t.Context(), id, preferences.Patch{}, prefsEntry(id, at)); err != nil {
		t.Fatalf("Update no-op: %v", err)
	}

	if n := countPreferenceRows(t, s, id); n != 0 {
		t.Fatalf("rows after a no-op = %d, want 0", n)
	}

	patch := preferences.Patch{preferences.FieldLanguage: preferences.Text("ar")}
	if _, err := s.Update(t.Context(), id, patch, prefsEntry(id, at)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if n := countPreferenceRows(t, s, id); n != 1 {
		t.Fatalf("rows after a change = %d, want 1", n)
	}

	if err := s.DeletePrincipal(t.Context(), id, prefsEntry(id, at)); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}

	if n := countPreferenceRows(t, s, id); n != 0 {
		t.Errorf("rows after the end of the person = %d, want 0", n)
	}
}

// TestPreferencesStoredRowIsChecked puts rows into the table that a person
// could not have set. The store must refuse to read them and must not write
// over them, so a restore or an older version never gives the console a
// value that fails the checks.
func TestPreferencesStoredRowIsChecked(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"an unknown key":                        `{"nope":"x"}`,
		"a bad language":                        `{"language":"ar_EG"}`,
		"a formatting locale with an extension": `{"formattingLocale":"ar-u-nu-latn"}`,
		"bad digits":                            `{"digits":"deva"}`,
		"a bad zone":                            `{"timeZone":"../../etc/passwd"}`,
		"a bad theme":                           `{"theme":"neon"}`,
		"a number":                              `{"language":1}`,
		"a repeated key":                        `{"theme":"terminal","theme":"terminal"}`,
		"a nested scheme":                       `{"terminalSchemes":{"light":"a"}}`,
		"an html scheme":                        `{"terminalSchemes.light":"<script>"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := openPreferencesStore(t)
			id := prefsPerson(t, s)

			params := db.UpsertPersonPreferencesParams{PrincipalID: id, Document: doc, UpdatedAtMs: 1}
			if err := s.queries.UpsertPersonPreferences(t.Context(), params); err != nil {
				t.Fatalf("write row: %v", err)
			}

			if _, err := s.Get(t.Context(), id); !errors.Is(err, preferences.ErrInvalidStored) {
				t.Errorf("Get error = %v, want ErrInvalidStored", err)
			}

			at := time.UnixMilli(1_700_000_100_000).UTC()
			patch := preferences.Patch{preferences.FieldColorMode: preferences.Text("dark")}

			_, updateErr := s.Update(t.Context(), id, patch, prefsEntry(id, at))
			if !errors.Is(updateErr, preferences.ErrInvalidStored) {
				t.Errorf("Update error = %v, want ErrInvalidStored", updateErr)
			}

			row, err := s.queries.GetPersonPreferences(t.Context(), id)
			if err != nil || row.Document.String != doc {
				t.Errorf("row = %+v, %v; want the stored text unchanged", row, err)
			}
		})
	}
}

// TestPreferencesTableRefusesAHugeDocument proves the schema holds a limit of
// its own, so a path that skips the checks of the package still cannot store
// a document over 4 KiB or text that is not a JSON object.
func TestPreferencesTableRefusesAHugeDocument(t *testing.T) {
	t.Parallel()

	s := openPreferencesStore(t)
	id := prefsPerson(t, s)

	for name, doc := range map[string]string{
		"over 4 KiB": `{"language":"` + strings.Repeat("a", 4096) + `"}`,
		"not JSON":   `not json`,
		"empty":      ``,
		"an array":   `[]`,
		"a string":   `"x"`,
	} {
		err := s.queries.UpsertPersonPreferences(t.Context(), db.UpsertPersonPreferencesParams{
			PrincipalID: id, Document: doc, UpdatedAtMs: 1,
		})
		if err == nil {
			t.Errorf("%s: the table took the document", name)
		}
	}
}

// TestPreferencesTableRefusesAMissingPerson proves the foreign key: a row
// names a principal that exists.
func TestPreferencesTableRefusesAMissingPerson(t *testing.T) {
	t.Parallel()

	s := openPreferencesStore(t)

	err := s.queries.UpsertPersonPreferences(t.Context(), db.UpsertPersonPreferencesParams{
		PrincipalID: "prn-nobody", Document: `{}`, UpdatedAtMs: 1,
	})
	if err == nil {
		t.Error("the table took a row for a principal that does not exist")
	}
}
