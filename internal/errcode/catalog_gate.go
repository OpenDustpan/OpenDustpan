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
	// GateApproversUserInvalid means that an approver user name is not a valid name.
	GateApproversUserInvalid = "gate.approvers-user-invalid"
	// GateDenied means that a gate refused the request.
	GateDenied = "gate.denied"
	// GateUnavailable means that the gates could not decide the request, so the server refuses it.
	GateUnavailable = "gate.unavailable"
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
		{Code: GateApproversUserInvalid, Meaning: "an approver user name is not a valid name", Args: nil},
		{Code: GateDenied, Meaning: "a gate refused the request", Args: nil},
		{Code: GateLimited, Meaning: "an access limit forbids the request", Args: nil},
		{
			Code:    GateRuleFailed,
			Meaning: "a gate or an access limit refused the request because it failed to evaluate",
			Args:    nil,
		},
		{Code: GateUnavailable, Meaning: "the gates could not decide the request, so the server refuses it", Args: nil},
	}
}
