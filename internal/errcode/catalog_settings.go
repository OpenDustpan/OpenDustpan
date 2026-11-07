package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// SettingsKeyUnknown means that the change names a setting that the scope does not declare.
	SettingsKeyUnknown = "settings.key-unknown"
	// SettingsLocked means that the static layer sets the setting, so the API cannot set it.
	SettingsLocked = "settings.locked"
	// SettingsPatchInvalid means that the change is not one JSON object of settings with a string, a boolean, a
	// whole number or null for each value.
	SettingsPatchInvalid = "settings.patch-invalid"
	// SettingsRetentionInvalid means that a retention setting is not a duration from one day to ten years, so the
	// server does not start.
	SettingsRetentionInvalid = "settings.retention-invalid"
	// SettingsStaticOnly means that only the static layer can set the setting.
	SettingsStaticOnly = "settings.static-only"
	// SettingsUnavailable means that the store of the server keeps no settings.
	SettingsUnavailable = "settings.unavailable"
	// SettingsValueInvalid means that the value of a setting does not fit its kind or its rules.
	SettingsValueInvalid = "settings.value-invalid"
)

// argKey is the name of the argument of a code for the key of a setting.
const argKey = "key"

// settingsSpecs are the codes of the settings resource.
func settingsSpecs() []Spec {
	return []Spec{
		{Code: SettingsKeyUnknown, Meaning: "the change names a setting that the scope does not declare", Args: nil},
		{
			Code:    SettingsLocked,
			Meaning: "the static layer sets the setting, so the API cannot set it",
			Args:    []Arg{{Name: argKey, Kind: KindIdentifier}, {Name: "source", Kind: KindIdentifier}},
		},
		{
			Code: SettingsPatchInvalid,
			Meaning: "the change is not one JSON object of settings with a string, a boolean, a whole number " +
				"or null for each value",
			Args: nil,
		},
		{
			Code: SettingsRetentionInvalid,
			Meaning: "a retention setting is not a duration from one day to ten years, " +
				"so the server does not start",
			Args: []Arg{{Name: "setting", Kind: KindIdentifier}},
		},
		{
			Code:    SettingsStaticOnly,
			Meaning: "only the static layer can set the setting",
			Args:    []Arg{{Name: argKey, Kind: KindIdentifier}},
		},
		{Code: SettingsUnavailable, Meaning: "the store of the server keeps no settings", Args: nil},
		{
			Code:    SettingsValueInvalid,
			Meaning: "the value of a setting does not fit its kind or its rules",
			Args:    []Arg{{Name: argKey, Kind: KindIdentifier}},
		},
	}
}
