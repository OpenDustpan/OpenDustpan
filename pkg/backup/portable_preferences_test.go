package backup_test

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/preferences"
)

// readPreferencesRow writes one person_preferences row with the document value
// document to a table file and reads the file back. It returns the number of
// rows that reached the caller and the error of the read. The test sends the
// row to the import check as if no other check had run.
func readPreferencesRow(t *testing.T, document any) (int, error) {
	t.Helper()

	var table backup.PortableTable

	for _, candidate := range backup.PortableTables() {
		if candidate.Table == backup.TablePersonPreferences {
			table = candidate
		}
	}

	if table.Table == "" {
		t.Fatal("portable tables have no person_preferences table")
	}

	var buf bytes.Buffer

	tw, err := backup.NewTableWriter(&buf, table)
	if err != nil {
		t.Fatalf("new table writer: %v", err)
	}

	if writeErr := tw.WriteRow([]any{"prn-alice", document, int64(1000)}); writeErr != nil {
		t.Fatalf("write row: %v", writeErr)
	}

	file, err := tw.Close()
	if err != nil {
		t.Fatalf("close table writer: %v", err)
	}

	open := func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	}

	rows := 0
	err = backup.ReadPortableTable(t.Context(), open, file, table, func([]any) error {
		rows++

		return nil
	})

	return rows, err
}

// TestReadPortablePreferencesDocuments proves that a restored preferences row
// with a document that the route would refuse fails with ErrCorrupt, and that
// a valid document reaches the caller.
func TestReadPortablePreferencesDocuments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document any
		valid    bool
	}{
		{name: "an empty object", document: `{}`, valid: true},
		{
			name:     "a plain document",
			document: `{"language":"ar","theme":"terminal","timeZone":"Asia/Riyadh"}`,
			valid:    true,
		},
		{name: "the flat names of the schemes", document: `{"terminalSchemes.dark":"nord"}`, valid: true},
		{name: "a value that is the default", document: `{"theme":"default"}`, valid: true},
		{name: "null", document: nil, valid: false},
		{name: "empty", document: ``, valid: false},
		{name: "not JSON", document: `not json`, valid: false},
		{name: "an array", document: `[]`, valid: false},
		{name: "a string", document: `"theme"`, valid: false},
		{name: "text after the object", document: `{} {}`, valid: false},
		{name: "a repeated key", document: `{"theme":"terminal","theme":"terminal"}`, valid: false},
		{name: "an unknown key", document: `{"nope":"x"}`, valid: false},
		{name: "updatedAt", document: `{"updatedAt":"1"}`, valid: false},
		{name: "a nested scheme object", document: `{"terminalSchemes":{"light":"a"}}`, valid: false},
		{name: "a number", document: `{"language":1}`, valid: false},
		{name: "a null value", document: `{"language":null}`, valid: false},
		{name: "a language with an underscore", document: `{"language":"ar_EG"}`, valid: false},
		{name: "an extension in the formatting locale", document: `{"formattingLocale":"ar-u-nu-latn"}`, valid: false},
		{name: "digits that are not allowed", document: `{"digits":"deva"}`, valid: false},
		{name: "a zone with a path", document: `{"timeZone":"../../etc/passwd"}`, valid: false},
		{name: "the zone of the host", document: `{"timeZone":"Local"}`, valid: false},
		{name: "a script as a scheme", document: `{"terminalSchemes.light":"<script>"}`, valid: false},
		{name: "a theme that is not allowed", document: `{"theme":"neon"}`, valid: false},
		{name: "a hidden character", document: "{\"language\":\"en\x00\"}", valid: false},
		{
			name:     "too long",
			document: `{"language":"` + strings.Repeat("a", preferences.MaxBodyBytes) + `"}`,
			valid:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rows, err := readPreferencesRow(t, tc.document)
			if tc.valid {
				if err != nil || rows != 1 {
					t.Fatalf("read: rows = %d, err = %v; want 1 row and no error", rows, err)
				}

				return
			}

			if !errors.Is(err, backup.ErrCorrupt) || rows != 0 {
				t.Fatalf("read: rows = %d, err = %v; want ErrCorrupt and no row", rows, err)
			}
		})
	}
}

// TestPreferencesTableIsBackedUp proves the table is in the backup list, that
// it follows the principals table (a row names a principal), and that it moves
// with the access tables: a restore that keeps the live principals keeps the
// live preferences too.
func TestPreferencesTableIsBackedUp(t *testing.T) {
	t.Parallel()

	tables := backup.TablesCopy()

	at := slices.Index(tables, backup.TablePersonPreferences)
	if at < 0 {
		t.Fatalf("Tables = %v, want %s in the list", tables, backup.TablePersonPreferences)
	}

	if from := slices.Index(tables, backup.TablePrincipals); from < 0 || from > at {
		t.Errorf("the principals table is at %d and the preferences table at %d, want principals first", from, at)
	}

	if !backup.IsAccessTable(backup.TablePersonPreferences) {
		t.Errorf("%s is no access table, want it kept with the principals of a restore", backup.TablePersonPreferences)
	}
}
