package backup

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/preferences"
)

// portColDocument is the column that holds a stored document.
const portColDocument = "document"

// portablePreferenceTables is the portable description of the table of the
// preferences of a person, in the order of Tables.
func portablePreferenceTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TablePersonPreferences,
			OrderBy: []string{portColPrincipalID},
			Columns: []Column{
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColDocument, Kind: KindText},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
	}
}

// checkPreferencesDocument refuses a preferences row whose document a person
// could not have set: text that is not one JSON object of strings, a field
// that the document does not have, or a value that the checks of the
// preferences package refuse. The console reads these values into the page,
// so a restore must not bring in a value that skips the checks of the route.
// It returns ErrCorrupt, wrapped with preferences.ErrInvalidStored.
//
// Defense in depth: the route and the store check each value when a person
// sets it, and the store checks the document again when it reads the row.
// This check stays so that a restored backup cannot bring in a value that
// skips them.
func checkPreferencesDocument(value any) error {
	text, isText := value.(string)
	if !isText {
		return fmt.Errorf("preferences row has no document: %w", ErrCorrupt)
	}

	if _, err := preferences.ParseSettings(text); err != nil {
		return fmt.Errorf("preferences row has an invalid document: %w: %w", err, ErrCorrupt)
	}

	return nil
}
