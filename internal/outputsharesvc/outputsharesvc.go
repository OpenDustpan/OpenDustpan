// Package outputsharesvc is the service of output sharing: the read and the
// change of the output sharing setting of a namespace. The ports stay in
// package outputshare.
package outputsharesvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// SettingKey is the key of the setting in the audit entry of a change.
const SettingKey = "outputSharing.newUnitDefault"

// Authorizer decides whether a principal may act on an object.
// identity.Authorizer satisfies it. The service asks it for each call, so a
// caller that skips the check of its own route still cannot read or change
// the setting without the action.
type Authorizer interface {
	// Authorize returns nil if allowed and an error wrapping identity.ErrDenied
	// otherwise.
	Authorize(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) error
}

// Config holds the dependencies of a Service.
type Config struct {
	// Settings keeps the setting of each namespace.
	Settings outputshare.Settings
	// Namespaces gives the current fence of a namespace before each change.
	Namespaces namespace.Store
	// Authorizer checks the action of each call.
	Authorizer Authorizer
	// Now is the clock of the audit entries. Nil means time.Now.
	Now func() time.Time
}

// Service reads and changes the output sharing setting of a namespace. It
// reads the fence of the namespace before each change, so that the change
// carries the failover version that the namespace has now.
type Service struct {
	settings   outputshare.Settings
	namespaces namespace.Store
	authorizer Authorizer
	now        func() time.Time
}

// New returns a Service. It returns ErrMissingDependency when a port is
// missing.
func New(cfg Config) (*Service, error) {
	if cfg.Settings == nil || cfg.Namespaces == nil || cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: output sharing settings, namespace store and authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{settings: cfg.Settings, namespaces: cfg.Namespaces, authorizer: cfg.Authorizer, now: now}, nil
}

// Setting returns the setting of the namespace ns for the principal p, who
// needs namespace:read on the namespace. A namespace that never set it reads
// outputshare.DefaultNobody.
func (s *Service) Setting(ctx context.Context, p identity.Principal, ns namespace.Name) (outputshare.Default, error) {
	if err := s.authorize(ctx, p, identity.ActionNamespaceRead, ns); err != nil {
		return "", err
	}

	d, err := s.settings.Setting(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("read the output sharing setting of %s: %w", ns, err)
	}

	return d, nil
}

// SetSetting stores d as the setting of the namespace ns for the principal
// p, who needs unit:manage-sharing on the namespace. The audit entry by
// names the caller and the request. The service sets its time, its
// namespace, its object, its action and the change of the setting. A value
// that is not a value of the setting returns an error that wraps
// outputshare.ErrInvalid and changes nothing. The change applies to the
// units that the namespace makes later. It changes no rule of a unit that
// exists.
func (s *Service) SetSetting(
	ctx context.Context, p identity.Principal, ns namespace.Name, d outputshare.Default, by audit.Entry,
) error {
	if err := s.authorize(ctx, p, identity.ActionUnitManageSharing, ns); err != nil {
		return err
	}

	// Defense in depth: the handler decodes the value against the same list,
	// and the store validates it again. This check stays so that a new
	// caller cannot skip either of them.
	if err := d.Validate(); err != nil {
		return fmt.Errorf("set the output sharing setting of %s: %w", ns, err)
	}

	old, err := s.settings.Setting(ctx, ns)
	if err != nil {
		return fmt.Errorf("read the output sharing setting of %s: %w", ns, err)
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("read namespace %s: %w", ns, err)
	}

	prev := string(old)

	by.At = s.now().UTC()
	by.Namespace = ns
	by.Object = identity.NamespaceObject(ns).String()
	by.Action = string(identity.ActionUnitManageSharing)
	by.Kind = audit.KindChange
	by.Outcome = audit.OutcomeSuccess
	by.Changes = []audit.Change{{Key: SettingKey, Old: &prev, New: string(d), Layer: "", TakesEffect: ""}}

	if err := s.settings.SetSetting(ctx, ns, d, n.Fence(), by); err != nil {
		return fmt.Errorf("set the output sharing setting of %s: %w", ns, err)
	}

	return nil
}

// authorize checks that p may do action on the namespace ns. It fails
// closed: an error of the Authorizer denies.
func (s *Service) authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	ns namespace.Name,
) error {
	if err := s.authorizer.Authorize(ctx, p, action, identity.NamespaceObject(ns)); err != nil {
		return fmt.Errorf("authorize %s on %s: %w", action, ns, err)
	}

	return nil
}
