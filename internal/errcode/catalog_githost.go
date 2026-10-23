package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// GitHostAppNotRegistered means that the operator has not registered the GitHub App.
	GitHostAppNotRegistered = "git-host.app-not-registered"
	// GitHostInstallationNotAssigned means that the operator did not assign the installation of the Git host to the
	// namespace.
	GitHostInstallationNotAssigned = "git-host.installation-not-assigned"
)

// gitHostSpecs are the codes of the Git host connections.
func gitHostSpecs() []Spec {
	return []Spec{
		{Code: GitHostAppNotRegistered, Meaning: "the operator has not registered the GitHub App", Args: nil},
		{
			Code:    GitHostInstallationNotAssigned,
			Meaning: "the operator did not assign the installation of the Git host to the namespace",
			Args:    nil,
		},
	}
}
