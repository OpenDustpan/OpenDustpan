package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// GitHostAppBudgetSpent means that the calls that the key of the GitHub App signed in the current window
	// reached the limit of the budget.
	GitHostAppBudgetSpent = "git-host.app-budget-spent"
	// GitHostAppNotRegistered means that the operator has not registered the GitHub App.
	GitHostAppNotRegistered = "git-host.app-not-registered"
	// GitHostInstallationNotAssigned means that the operator did not assign the installation of the Git host to the
	// namespace.
	GitHostInstallationNotAssigned = "git-host.installation-not-assigned"
	// GitHostLinkAccountInvalid means that the kind, the host or the ID of the Git host account in the path is not
	// valid.
	GitHostLinkAccountInvalid = "git-host.link-account-invalid"
	// GitHostLinkBodyInvalid means that the body of the request has no valid link token, or has a field that the route
	// does not take.
	GitHostLinkBodyInvalid = "git-host.link-body-invalid"
	// GitHostLinkBodyTooLarge means that the body of the request is over the size limit.
	GitHostLinkBodyTooLarge = "git-host.link-body-too-large"
	// GitHostLinkConflict means that the Git host account has a link that is not the link that the person saw, so the
	// accept replaces nothing.
	GitHostLinkConflict = "git-host.link-conflict"
	// GitHostLinkLimit means that the person has too many link requests that can still be accepted.
	GitHostLinkLimit = "git-host.link-limit"
	// GitHostLinkNotFound means that no link of this Git host account exists for the principal.
	GitHostLinkNotFound = "git-host.link-not-found"
	// GitHostLinkNotProved means that no Git host account proved itself for this link request yet.
	GitHostLinkNotProved = "git-host.link-not-proved"
	// GitHostLinkPersonRequired means that only a person in a console session can start, show or accept a link, and a
	// break-glass account cannot.
	GitHostLinkPersonRequired = "git-host.link-person-required"
	// GitHostLinkRequestExpired means that the link request expired.
	GitHostLinkRequestExpired = "git-host.link-request-expired"
	// GitHostLinkRequestNotFound means that no link request of this person has this token.
	GitHostLinkRequestNotFound = "git-host.link-request-not-found"
	// GitHostLinkRequestUsed means that the link request was accepted already.
	GitHostLinkRequestUsed = "git-host.link-request-used"
	// GitHostLinkUnavailable means that the store of this server keeps no Git host account links.
	GitHostLinkUnavailable = "git-host.link-unavailable"
)

// gitHostSpecs are the codes of the Git host connections.
func gitHostSpecs() []Spec {
	return []Spec{
		{
			Code:    GitHostAppBudgetSpent,
			Meaning: "the calls that the key of the GitHub App signed in the current window reached the limit of the budget",
			Args:    nil,
		},
		{Code: GitHostAppNotRegistered, Meaning: "the operator has not registered the GitHub App", Args: nil},
		{
			Code:    GitHostInstallationNotAssigned,
			Meaning: "the operator did not assign the installation of the Git host to the namespace",
			Args:    nil,
		},
		{
			Code:    GitHostLinkAccountInvalid,
			Meaning: "the kind, the host or the ID of the Git host account in the path is not valid",
			Args:    nil,
		},
		{
			Code:    GitHostLinkBodyInvalid,
			Meaning: "the body of the request has no valid link token, or has a field that the route does not take",
			Args:    nil,
		},
		{Code: GitHostLinkBodyTooLarge, Meaning: "the body of the request is over the size limit", Args: nil},
		{
			Code:    GitHostLinkConflict,
			Meaning: "the Git host account has a link that is not the link that the person saw, so the accept replaces nothing",
			Args:    nil,
		},
		{
			Code:    GitHostLinkLimit,
			Meaning: "the person has too many link requests that can still be accepted",
			Args:    nil,
		},
		{Code: GitHostLinkNotFound, Meaning: "no link of this Git host account exists for the principal", Args: nil},
		{Code: GitHostLinkNotProved, Meaning: "no Git host account proved itself for this link request yet", Args: nil},
		{
			Code:    GitHostLinkPersonRequired,
			Meaning: "only a person in a console session can start, show or accept a link, and a break-glass account cannot",
			Args:    nil,
		},
		{Code: GitHostLinkRequestExpired, Meaning: "the link request expired", Args: nil},
		{Code: GitHostLinkRequestNotFound, Meaning: "no link request of this person has this token", Args: nil},
		{Code: GitHostLinkRequestUsed, Meaning: "the link request was accepted already", Args: nil},
		{Code: GitHostLinkUnavailable, Meaning: "the store of this server keeps no Git host account links", Args: nil},
	}
}
