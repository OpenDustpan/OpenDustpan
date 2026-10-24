package errcode

// accessSpecs are the codes of a request that the access rules refuse.
func accessSpecs() []Spec {
	return []Spec{
		{Code: AccessDenied, Meaning: "the caller has no permission for the request", Args: nil},
		{
			Code:    AccessIPNotAllowed,
			Meaning: "the request comes from an address outside the IP allowlist of the namespace",
			Args:    nil,
		},
		{
			Code:    AccessNoAction,
			Meaning: "the route declares no authorization action for the object of the request, so the server refuses it",
			Args:    nil,
		},
	}
}
