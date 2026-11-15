package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// PrincipalAlreadyExists means that a principal or a group with the same subject or name exists.
	PrincipalAlreadyExists = "principal.already-exists"
	// PrincipalDisabled means that the principal is disabled, so the request cannot change it.
	PrincipalDisabled = "principal.disabled"
	// PrincipalInvalid means that the principal or the group is not valid.
	PrincipalInvalid = "principal.invalid"
	// PrincipalLastBreakGlass means that the request would end or disable the last enabled break-glass account.
	PrincipalLastBreakGlass = "principal.last-break-glass"
	// PrincipalManagedByDirectory means that a directory keeps the group, so the request cannot change it.
	PrincipalManagedByDirectory = "principal.managed-by-directory"
	// PrincipalNotDisabled means that the principal is not disabled, so the request cannot enable it.
	PrincipalNotDisabled = "principal.not-disabled"
	// PrincipalNotFound means that the principal, the group or the membership does not exist.
	PrincipalNotFound = "principal.not-found"
	// PrincipalRemovalAlertFailed means that the removal alert of a group claim sync could not be written; the sign-in
	// went on.
	PrincipalRemovalAlertFailed = "principal.removal-alert-failed"
	// PrincipalRemovalAlertMarkerInvalid means that the removal alert marker of a source was after the time of the
	// removal, so the server counted it as no marker.
	PrincipalRemovalAlertMarkerInvalid = "principal.removal-alert-marker-invalid"
	// PrincipalRemovalAlertUnrecorded means that the removal alert and the audit entry of its failure could not be
	// written, so the sign-in was refused.
	PrincipalRemovalAlertUnrecorded = "principal.removal-alert-unrecorded"
	// PrincipalReservedSubject means that the subject is one that the server reserves.
	PrincipalReservedSubject = "principal.reserved-subject"
	// PrincipalUnsupported means that the store of the server keeps no principals.
	PrincipalUnsupported = "principal.unsupported"
)

// principalSpecs are the codes of the principals and the groups.
func principalSpecs() []Spec {
	return []Spec{
		{
			Code:    PrincipalAlreadyExists,
			Meaning: "a principal or a group with the same subject or name exists",
			Args:    nil,
		},
		{Code: PrincipalDisabled, Meaning: "the principal is disabled, so the request cannot change it", Args: nil},
		{Code: PrincipalInvalid, Meaning: "the principal or the group is not valid", Args: nil},
		lastBreakGlassSpec(),
		{
			Code:    PrincipalManagedByDirectory,
			Meaning: "a directory keeps the group, so the request cannot change it",
			Args:    nil,
		},
		{
			Code:    PrincipalNotDisabled,
			Meaning: "the principal is not disabled, so the request cannot enable it",
			Args:    nil,
		},
		{Code: PrincipalNotFound, Meaning: "the principal, the group or the membership does not exist", Args: nil},
		{
			Code:    PrincipalRemovalAlertFailed,
			Meaning: "the removal alert of a group claim sync could not be written; the sign-in went on",
			Args:    nil,
		},
		{
			Code: PrincipalRemovalAlertMarkerInvalid,
			Meaning: "the removal alert marker of a source was after the time of the removal, so the server counted it " +
				"as no marker",
			Args: nil,
		},
		{
			Code:    PrincipalRemovalAlertUnrecorded,
			Meaning: "the removal alert and the audit entry of its failure could not be written, so the sign-in was refused",
			Args:    nil,
		},
		{Code: PrincipalReservedSubject, Meaning: "the subject is one that the server reserves", Args: nil},
		{Code: PrincipalUnsupported, Meaning: "the store of the server keeps no principals", Args: nil},
	}
}

// lastBreakGlassSpec is the code of a request that would end or disable the
// last enabled break-glass account.
func lastBreakGlassSpec() Spec {
	return Spec{
		Code:    PrincipalLastBreakGlass,
		Meaning: "the request would end or disable the last enabled break-glass account of the installation",
		Args:    nil,
	}
}
