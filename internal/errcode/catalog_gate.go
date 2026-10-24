package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// GateDenied means that a gate refused the request.
	GateDenied = "gate.denied"
	// GateUnavailable means that the gates could not decide the request, so the server refuses it.
	GateUnavailable = "gate.unavailable"
)

// gateSpecs are the codes of a request that a gate or an access limit
// refuses.
func gateSpecs() []Spec {
	return []Spec{
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
