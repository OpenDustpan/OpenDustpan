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
	}
}
