package errcode

// The codes of the audit log. A code is never removed and never changes its
// meaning.
const (
	// AuditEntryNotFound is a sequence at which the scope holds no audit
	// entry.
	AuditEntryNotFound = "audit.entry-not-found"
	// AuditEntryReadUnavailable is a read of one audit entry on a server whose
	// store cannot read one entry.
	AuditEntryReadUnavailable = "audit.entry-read-unavailable"
	// AuditReasonInvalid is a reason for an audited action that is empty, too
	// long or not plain text.
	AuditReasonInvalid = "audit.reason-invalid"
)

// auditSpecs are the codes of the audit log.
func auditSpecs() []Spec {
	return []Spec{
		{Code: AuditEntryNotFound, Meaning: "the scope holds no audit entry at the sequence", Args: nil},
		{
			Code:    AuditEntryReadUnavailable,
			Meaning: "the store of the server cannot read one audit entry",
			Args:    nil,
		},
		{Code: AuditReasonInvalid, Meaning: "the reason of the audited action is not valid", Args: nil},
	}
}
