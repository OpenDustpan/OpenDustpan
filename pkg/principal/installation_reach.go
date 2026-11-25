package principal

import "context"

// InstallationReach reports whether a grant on the installation reaches the
// actions inside a namespace. principal.Service meets it through the
// authorizer.
type InstallationReach interface {
	// GrantsReachNamespaces returns the effective value of the setting. A
	// failed read returns the error.
	GrantsReachNamespaces(ctx context.Context) (bool, error)
}
