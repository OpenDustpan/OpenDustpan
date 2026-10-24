package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// PreferencesBodyInvalid means that the merge patch of the preferences is not valid.
	PreferencesBodyInvalid = "preferences.body-invalid"
	// PreferencesBodyTooLarge means that the merge patch of the preferences is over the size limit.
	PreferencesBodyTooLarge = "preferences.body-too-large"
	// PreferencesFieldInvalid means that a field of the merge patch has a value that is not valid.
	PreferencesFieldInvalid = "preferences.field-invalid"
	// PreferencesFieldReadOnly means that the merge patch changes a field that only the server sets.
	PreferencesFieldReadOnly = "preferences.field-read-only"
	// PreferencesFieldUnknown means that the merge patch has a field that the preferences do not have.
	PreferencesFieldUnknown = "preferences.field-unknown"
	// PreferencesIfMatchInvalid means that the If-Match header is not one version in quotes or *.
	PreferencesIfMatchInvalid = "preferences.if-match-invalid"
	// PreferencesMediaTypeUnsupported means that the content type of the request is not the merge patch type.
	PreferencesMediaTypeUnsupported = "preferences.media-type-unsupported"
	// PreferencesPersonNotFound means that the person of the request does not exist.
	PreferencesPersonNotFound = "preferences.person-not-found"
	// PreferencesPersonRequired means that the caller is not a person, and only a person has preferences.
	PreferencesPersonRequired = "preferences.person-required"
	// PreferencesPreconditionFailed means that the preferences changed since the version of the If-Match header.
	PreferencesPreconditionFailed = "preferences.precondition-failed"
	// PreferencesUnavailable means that the store of the server keeps no preferences.
	PreferencesUnavailable = "preferences.unavailable"
)

// preferencesSpecs are the codes of the preferences of a person.
func preferencesSpecs() []Spec {
	return []Spec{
		{Code: PreferencesBodyInvalid, Meaning: "the merge patch of the preferences is not valid", Args: nil},
		{
			Code:    PreferencesBodyTooLarge,
			Meaning: "the merge patch of the preferences is over the size limit",
			Args:    nil,
		},
		{
			Code:    PreferencesFieldInvalid,
			Meaning: "a field of the merge patch has a value that is not valid",
			Args:    []Arg{{Name: "field", Kind: KindIdentifier}},
		},
		{
			Code:    PreferencesFieldReadOnly,
			Meaning: "the merge patch changes a field that only the server sets",
			Args:    []Arg{{Name: "field", Kind: KindIdentifier}},
		},
		{
			Code:    PreferencesFieldUnknown,
			Meaning: "the merge patch has a field that the preferences do not have",
			Args:    nil,
		},
		{Code: PreferencesIfMatchInvalid, Meaning: "the If-Match header is not one version in quotes or *", Args: nil},
		{
			Code:    PreferencesMediaTypeUnsupported,
			Meaning: "the content type of the request is not the merge patch type",
			Args:    nil,
		},
		{Code: PreferencesPersonNotFound, Meaning: "the person of the request does not exist", Args: nil},
		{
			Code:    PreferencesPersonRequired,
			Meaning: "the caller is not a person, and only a person has preferences",
			Args:    nil,
		},
		{
			Code:    PreferencesPreconditionFailed,
			Meaning: "the preferences changed since the version of the If-Match header",
			Args:    nil,
		},
		{Code: PreferencesUnavailable, Meaning: "the store of the server keeps no preferences", Args: nil},
	}
}
