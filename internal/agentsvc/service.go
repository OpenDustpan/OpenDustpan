// Package agentsvc is the service of the agent connection and secret items.
// It validates documents, checks the egress allowlist, stores sealed secret
// items and keeps the current and pending connection in one transaction.
// Each change appends an audit entry of the kind change on @server.
package agentsvc

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
)

// ErrUnknownAction reports an action the service does not know.
var ErrUnknownAction = errors.New("agentsvc: unknown action")

// ErrForbidden reports that the caller has no permission for the action.
var ErrForbidden = errors.New("agentsvc: forbidden")

// Service holds the ports of the agent connection and secret items.
type Service struct {
	// Secrets keeps the sealed secret items of @server.
	Secrets agent.SecretItems
	// Connection keeps the current and pending connection document.
	Connection agent.ConnectionStore
	// Egress holds the egress rules for checking model URLs.
	Egress *egress.Rules
	// Authorizer checks authorization at the point of use.
	Authorizer identity.Authorizer
}

// Authorize checks the action at the point of use, for @server.
func (s *Service) Authorize(ctx context.Context, p identity.Principal, action identity.Action) error {
	if action != identity.ActionServerManageAgent && action != identity.ActionServerManageSettings {
		return ErrUnknownAction
	}

	if err := s.Authorizer.Authorize(ctx, p, action, identity.ServerObject()); err != nil {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}
