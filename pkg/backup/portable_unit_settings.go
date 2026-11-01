package backup

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// Tables of the unit settings.
const (
	tableUnitSettings = "unit_settings"
	tableUnitDefaults = "unit_defaults"
)

// portableUnitSettingsTables is the portable description of the unit settings
// tables, in the order of Tables.
func portableUnitSettingsTables() []PortableTable {
	return []PortableTable{
		{
			Table:   tableUnitSettings,
			OrderBy: []string{portColUnitID},
			Columns: []Column{
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColBody, Kind: KindText},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   tableUnitDefaults,
			OrderBy: []string{portColNamespaceID},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "pool", Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
	}
}

// checkUnitSettingsValue runs the check of the column of a unit settings table
// that has one. A column of another table has none.
func checkUnitSettingsValue(table, column string, value any) error {
	switch {
	case table == tableUnitSettings && column == portColBody:
		return checkUnitSettingsBody(value)
	case table == tableUnitDefaults && column == "pool":
		return checkUnitDefaultsPool(value)
	case (table == tableUnitSettings || table == tableUnitDefaults) && column == portColVersion:
		return checkUnitSettingsVersion(table, value)
	default:
		return nil
	}
}

// checkUnitSettingsBody refuses a unit settings row whose body is not a
// document that passes the checks of the settings. It returns ErrCorrupt,
// wrapped with unitsettings.ErrInvalid. The error does not hold the body.
//
// Defense in depth: the store parses the body again at each read, and the
// write checks the settings before it stores them. This check stays so that a
// restored backup fails before it reaches the database.
func checkUnitSettingsBody(value any) error {
	body, isText := value.(string)
	if !isText {
		return fmt.Errorf("unit settings row has no body: %w: %w", unitsettings.ErrInvalid, ErrCorrupt)
	}

	if _, err := unitsettings.Parse([]byte(body)); err != nil {
		return fmt.Errorf("unit settings row has an invalid body: %w: %w", unitsettings.ErrInvalid, ErrCorrupt)
	}

	return nil
}

// checkUnitDefaultsPool refuses a unit defaults row whose pool is neither empty
// nor a runner pool ID. It returns ErrCorrupt, wrapped with
// unitsettings.ErrInvalid.
//
// Defense in depth: the store checks the pool at each read and each write.
// This check stays so that a restored backup fails before it reaches the
// database.
func checkUnitDefaultsPool(value any) error {
	pool, isText := value.(string)
	if !isText {
		return fmt.Errorf("unit defaults row has no pool: %w: %w", unitsettings.ErrInvalid, ErrCorrupt)
	}

	if err := unitsettings.CheckPool(pool); err != nil {
		return fmt.Errorf("unit defaults row has an invalid pool: %w: %w", unitsettings.ErrInvalid, ErrCorrupt)
	}

	return nil
}

// checkUnitSettingsVersion refuses a version below one. It returns ErrCorrupt.
func checkUnitSettingsVersion(table string, value any) error {
	version, isNumber := value.(int64)
	if !isNumber || version < 1 {
		return fmt.Errorf("%s row has a version below one: %w", table, ErrCorrupt)
	}

	return nil
}
