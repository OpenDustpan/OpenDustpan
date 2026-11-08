package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// GateApproversCountAbove means that the approval count is above the number of people who can approve.
	GateApproversCountAbove = "gate.approvers-count-above"
	// GateApproversGroupInvalid means that an approver group name is not a valid group name.
	GateApproversGroupInvalid = "gate.approvers-group-invalid"
	// GateApproversGroupMissing means that an approver group does not exist.
	GateApproversGroupMissing = "gate.approvers-group-missing"
	// GateApproversUnmet means that no one can meet the approver list, and the caller may not read the principals.
	GateApproversUnmet = "gate.approvers-unmet"
	// GateApproversUserInvalid means that an approver user name is not a valid name.
	GateApproversUserInvalid = "gate.approvers-user-invalid"
	// GateDenied means that a gate refused the request.
	GateDenied = "gate.denied"
	// GateFinal means that a final gate forbids the change to the gate configuration, or that the change needs a
	// reason.
	GateFinal = "gate.final"
	// GateInvalid means that a gate, an access limit or another object of the gate configuration is not valid.
	GateInvalid = "gate.invalid"
	// GateLimit means that a write to the gate configuration is over a limit of its scope.
	GateLimit = "gate.limit"
	// GateLimitMembership means that the right side of a membership test of principal in an access limit is not a
	// named group.
	GateLimitMembership = "gate.limit-membership"
	// GateNotFound means that the gate configuration has no such object at the scope.
	GateNotFound = "gate.not-found"
	// GateUnavailable means that the gates could not decide the request, so the server refuses it.
	GateUnavailable = "gate.unavailable"
	// GateVersionConflict means that the stored version of a gate configuration object is not the version of the
	// request.
	GateVersionConflict = "gate.version-conflict"
)

// gateSpecs are the codes of a request that a gate or an access limit
// refuses.
func gateSpecs() []Spec {
	return []Spec{
		{
			Code:    GateApproversCountAbove,
			Meaning: "the approval count is above the number of people who can approve",
			Args:    []Arg{{Name: "people", Kind: KindQuantity}},
		},
		{Code: GateApproversGroupInvalid, Meaning: "an approver group name is not a valid group name", Args: nil},
		{Code: GateApproversGroupMissing, Meaning: "an approver group does not exist", Args: nil},
		{
			Code:    GateApproversUnmet,
			Meaning: "no one can meet the approver list, and the caller may not read the principals",
			Args:    nil,
		},
		{Code: GateApproversUserInvalid, Meaning: "an approver user name is not a valid name", Args: nil},
		{Code: GateDenied, Meaning: "a gate refused the request", Args: nil},
		{
			Code:    GateFinal,
			Meaning: "a final gate forbids the change to the gate configuration, or the change needs a reason",
			Args:    nil,
		},
		{
			Code:    GateInvalid,
			Meaning: "a gate, an access limit or another object of the gate configuration is not valid",
			Args:    nil,
		},
		{Code: GateLimit, Meaning: "a write to the gate configuration is over a limit of its scope", Args: nil},
		{
			Code:    GateLimitMembership,
			Meaning: "the right side of a membership test of principal in an access limit is not a named group",
			Args:    nil,
		},
		{Code: GateNotFound, Meaning: "the gate configuration has no such object at the scope", Args: nil},
		{Code: GateLimited, Meaning: "an access limit forbids the request", Args: nil},
		{
			Code:    GateRuleFailed,
			Meaning: "a gate or an access limit refused the request because it failed to evaluate",
			Args:    nil,
		},
		{Code: GateUnavailable, Meaning: "the gates could not decide the request, so the server refuses it", Args: nil},
		{
			Code:    GateVersionConflict,
			Meaning: "the stored version of a gate configuration object is not the version of the request",
			Args:    nil,
		},
	}
}
