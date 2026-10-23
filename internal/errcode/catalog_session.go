package errcode

// sessionSpecs are the codes of a request with the session cookie that the
// session checks refuse.
func sessionSpecs() []Spec {
	return []Spec{
		{
			Code:    SessionCSRF,
			Meaning: "the request has the session cookie and a missing or wrong CSRF header",
			Args:    nil,
		},
		{Code: SessionOtherSite, Meaning: "the request has the session cookie and comes from another site", Args: nil},
	}
}
