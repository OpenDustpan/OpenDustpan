package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// UnitSettingsBulkSize means that a bulk call names no unit, or more units than the limit.
	UnitSettingsBulkSize = "unit-settings.bulk-size"
	// UnitSettingsConnectionLacksCapability means that the Git host connection cannot do what the settings use it for.
	UnitSettingsConnectionLacksCapability = "unit-settings.connection-lacks-capability"
	// UnitSettingsConnectionUnknown means that the namespace has no Git host connection with the ID.
	UnitSettingsConnectionUnknown = "unit-settings.connection-unknown"
	// UnitSettingsInvalid means that a field of the settings of a unit is not valid.
	UnitSettingsInvalid = "unit-settings.invalid"
	// UnitSettingsNotFound means that the namespace or the unit of the request does not exist or is not readable by the
	// caller.
	UnitSettingsNotFound = "unit-settings.not-found"
	// UnitSettingsPoolUnknown means that the namespace has no runner pool for jobs with the ID.
	UnitSettingsPoolUnknown = "unit-settings.pool-unknown"
)

// unitSettingsSpecs are the codes of the settings of a unit.
func unitSettingsSpecs() []Spec {
	return []Spec{
		{
			Code:    UnitSettingsBulkSize,
			Meaning: "a bulk call names no unit, or more units than the limit",
			Args:    nil,
		},
		{
			Code:    UnitSettingsConnectionLacksCapability,
			Meaning: "the Git host connection cannot do what the settings use it for",
			Args:    nil,
		},
		{
			Code:    UnitSettingsConnectionUnknown,
			Meaning: "the namespace has no Git host connection with the ID",
			Args:    nil,
		},
		{Code: UnitSettingsInvalid, Meaning: "a field of the settings of a unit is not valid", Args: nil},
		{
			Code: UnitSettingsNotFound,
			Meaning: "the namespace or the unit of the request does not exist, " +
				"or the caller cannot read it",
			Args: nil,
		},
		{
			Code:    UnitSettingsPoolUnknown,
			Meaning: "the namespace has no runner pool for jobs with the ID",
			Args:    nil,
		},
	}
}
