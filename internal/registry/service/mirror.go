package service

import (
	"context"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// MirrorIndex returns the servable releases of the provider name in ns, from
// the lowest version to the highest, for the index document of the provider
// network mirror. A version is listed only while the namespace trusts the
// signing key that made it, so a client that reads the index never picks a
// version it cannot download. It returns registry.ErrNotFound for a namespace
// that does not exist, for a provider with no servable release, and for a
// provider of another namespace, so a caller cannot tell the three apart. It
// reads committed rows only: trust is checked at publish, and a removed key
// only leaves later versions out of the index. The mirror routes of the TFE
// adapter read the same set through ServableReleases, which this method pins
// with the tests of the mirror.
func (s *Service) MirrorIndex(ctx context.Context, ns namespace.Name, name string) ([]registry.Release, error) {
	return s.ServableReleases(ctx, ns, name)
}
