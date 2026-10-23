package errcode

// AuditReasonInvalid is a reason for an audited action that is empty, too
// long or not plain text.
const AuditReasonInvalid = "audit.reason-invalid"

// auditSpecs are the codes of the audit log.
func auditSpecs() []Spec {
	return []Spec{
		{Code: AuditReasonInvalid, Meaning: "the reason of the audited action is not valid", Args: nil},
	}
}
