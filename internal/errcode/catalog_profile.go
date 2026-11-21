package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// ProfileConflict means that the profile changed since the caller read it.
	ProfileConflict = "profile.conflict"
	// ProfileInvalid means that the profile is not valid.
	ProfileInvalid = "profile.invalid"
	// ProfileLimit means that the profile is over a limit.
	ProfileLimit = "profile.limit"
	// ProfileNotFound means that the profile does not exist.
	ProfileNotFound = "profile.not-found"
	// ProfileRoutingChangeNeedsOpen means that a change of the routing of the profile needs the open action.
	ProfileRoutingChangeNeedsOpen = "profile.routing-change-needs-open"
	// ProfileSchemaVersion means that the profile has a schema version that the server does not know.
	ProfileSchemaVersion = "profile.schema-version"
	// ProfileProvider means that the profile holds a provider block that the
	// open cannot run yet.
	ProfileProvider = "profile.provider"
	// ProfileNeverStore means that the open asks a value marked never store.
	ProfileNeverStore = "profile.never-store"
	// ProfileFinal means that the open replaces or removes a final value.
	ProfileFinal = "profile.final"
	// ProfileReference means that an expression reads a name other than
	// values.
	ProfileReference = "profile.reference"
	// ProfileImportCycle means that the imports of the profile make a cycle.
	ProfileImportCycle = "profile.import-cycle"
)

// profileSpecs are the codes of the profiles.
func profileSpecs() []Spec {
	return []Spec{
		{Code: ProfileConflict, Meaning: "the profile changed since the caller read it", Args: nil},
		{Code: ProfileInvalid, Meaning: "the profile is not valid", Args: nil},
		{Code: ProfileLimit, Meaning: "the profile is over a limit", Args: nil},
		{Code: ProfileNotFound, Meaning: "the profile does not exist", Args: nil},
		{
			Code:    ProfileRoutingChangeNeedsOpen,
			Meaning: "a change of the routing of the profile needs the open action",
			Args:    nil,
		},
		{
			Code:    ProfileSchemaVersion,
			Meaning: "the profile has a schema version that the server does not know",
			Args:    nil,
		},
		{Code: ProfileProvider, Meaning: "the profile holds a provider block that the open cannot run", Args: nil},
		{Code: ProfileNeverStore, Meaning: "the open asks a value marked never store", Args: nil},
		{Code: ProfileFinal, Meaning: "the open replaces or removes a final value", Args: nil},
		{Code: ProfileReference, Meaning: "an expression reads a name other than values", Args: nil},
		{Code: ProfileImportCycle, Meaning: "the imports of the profile make a cycle", Args: nil},
	}
}
