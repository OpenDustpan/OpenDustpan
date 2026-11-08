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
	// PrincipalNoWorkingBreakGlass means that the change leaves too few working break-glass accounts.
	PrincipalNoWorkingBreakGlass = "principal.no-working-break-glass"
	// PrincipalManagedByDirectory means that a directory keeps the group, so the request cannot change it.
	PrincipalManagedByDirectory = "principal.managed-by-directory"
	// PrincipalNotDisabled means that the principal is not disabled, so the request cannot enable it.
	PrincipalNotDisabled = "principal.not-disabled"
	// PrincipalNotFound means that the principal, the group or the membership does not exist.
	PrincipalNotFound = "principal.not-found"
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
		noWorkingBreakGlassSpec(),
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
		{Code: PrincipalReservedSubject, Meaning: "the subject is one that the server reserves", Args: nil},
		{Code: PrincipalUnsupported, Meaning: "the store of the server keeps no principals", Args: nil},
	}
}

// noWorkingBreakGlassSpec is the code of a change that leaves too few
// working break-glass accounts.
func noWorkingBreakGlassSpec() Spec {
	return Spec{
		Code:    PrincipalNoWorkingBreakGlass,
		Meaning: "the change leaves too few working break-glass accounts",
		Args:    nil,
	}
}
