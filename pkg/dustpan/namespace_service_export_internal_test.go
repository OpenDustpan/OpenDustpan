package dustpan

import (
	"context"

	"opendustpan.org/opendustpan/pkg/identity"
)

// AuthorizeWired runs wireTokens on the default settings, with authz as the
// configured authorizer and no token store, and then checks the action with
// the authorizer that the server uses.
func AuthorizeWired(
	authz identity.Authorizer, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	cfg := defaultSettings()
	cfg.authorizer = authz

	if err := wireTokens(cfg, nil); err != nil {
		return err
	}

	return cfg.authorizer.Authorize(context.Background(), p, action, obj)
}
