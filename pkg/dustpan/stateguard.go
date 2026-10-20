package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// newGateService returns the service that checks each write of state against
// the access limits and the gates of state.write, or nil when the store
// keeps no gates or the server has no inventory. Both SQL adapters keep gates.
// The tool adapters then check no write.
func newGateService(cfg *settings, namespaces *namespace.Service) (*gate.Service, error) {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok || cfg.inventoryReader == nil || namespaces == nil {
		return nil, nil //nolint:nilnil // A store without gates is a valid setup: no write is checked.
	}

	facts := inventoryFacts{reader: cfg.inventoryReader}

	var actions []string
	for _, family := range []access.Family{
		access.FamilyOperator, access.FamilyAdministration, access.FamilyData,
		access.FamilyDelegation, access.FamilySelf,
	} {
		for _, a := range access.Actions(family) {
			actions = append(actions, string(a))
		}
	}

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	svc, err := gate.NewService(gate.ServiceConfig{
		Store: store, Engine: cedargo.New(cedargo.WithAccessActions(actions...)), Producers: nil,
		Members: facts, Units: facts, Subjects: principalSubjects{}, Fences: namespaceFences{namespaces: namespaces},
		Now: cfg.now, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("gate service: %w", err)
	}

	return svc, nil
}

// inventoryFacts gives the collection and the labels of a unit from the
// inventory. One read gives both.
type inventoryFacts struct {
	reader inventory.Reader
}

// entry returns the inventory entry of unit. A unit that is not in the
// inventory, for example a deleted unit, gives gate.ErrNotFound.
func (f inventoryFacts) entry(ctx context.Context, unit identity.Object) (inventory.Entry, error) {
	if id := unit.UnitID(); id != "" {
		e, err := f.reader.GetUnit(ctx, id)
		if errors.Is(err, inventory.ErrUnitNotFound) {
			return inventory.Entry{}, gate.ErrNotFound
		}

		if err != nil {
			return inventory.Entry{}, fmt.Errorf("inventory unit %s: %w", id, err)
		}

		return e, nil
	}

	entries, err := f.reader.ListUnits(ctx, unit.Namespace())
	if err != nil {
		return inventory.Entry{}, fmt.Errorf("inventory units of %s: %w", unit.Namespace(), err)
	}

	for i := range entries {
		if entries[i].Tool == unit.Tool() && entries[i].Name == unit.UnitName() {
			return entries[i], nil
		}
	}

	return inventory.Entry{}, gate.ErrNotFound
}

// Collection implements gate.Membership.
func (f inventoryFacts) Collection(ctx context.Context, unit identity.Object) (collection.Name, error) {
	e, err := f.entry(ctx, unit)
	if err != nil {
		return "", err
	}

	if e.Collection == "" {
		return "", gate.ErrNotFound
	}

	return collection.Name(e.Collection), nil
}

// Labels implements gate.UnitSource. The labels are sorted key=value strings.
func (f inventoryFacts) Labels(ctx context.Context, unit identity.Object) ([]string, error) {
	e, err := f.entry(ctx, unit)
	if err != nil {
		return nil, err
	}

	labels := make([]string, 0, len(e.Labels))
	for k, v := range e.Labels {
		labels = append(labels, k+"="+v)
	}

	slices.Sort(labels)

	return labels, nil
}

// principalSubjects gives the Subject of a principal from the facts that its
// credential carries. It resolves no group and no authentication time yet, so
// a limit or a gate that reads a group does not match, and a limit that asks
// for a recent authentication forbids the call.
type principalSubjects struct{}

// Subject implements gate.SubjectSource.
func (principalSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	if strings.TrimSpace(p.Subject) == "" {
		return gate.Subject{}, fmt.Errorf("%w: the principal has no subject", identity.ErrDenied)
	}

	kind := gate.PrincipalPerson
	if p.Kind == identity.KindService || p.Kind == identity.KindWorkload {
		kind = gate.PrincipalService
	}

	credential := gate.CredentialAccessToken

	switch p.Credential {
	case identity.CredentialSession:
		credential = gate.CredentialSession
	case identity.CredentialJobToken:
		credential = gate.CredentialJobToken
	case identity.CredentialTrustToken:
		credential = gate.CredentialTrustToken
	default:
	}

	return gate.Subject{
		ID: p.Subject, Groups: nil, Kind: kind, Credential: credential, AuthAt: time.Time{}, BreakGlassAccount: false,
		Job: nil,
	}, nil
}

// namespaceFences gives the fence of a namespace from the namespace service.
type namespaceFences struct {
	namespaces *namespace.Service
}

// Fence implements gate.FenceSource.
func (f namespaceFences) Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := f.namespaces.Get(ctx, ns)
	if errors.Is(err, namespace.ErrNotFound) {
		return namespace.Fence{}, gate.ErrNotFound
	}

	if err != nil {
		return namespace.Fence{}, fmt.Errorf("namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// stateGuardOf returns svc as the guard of the TFE-compatible API, and an
// untyped nil for a nil service: a typed nil in the interface would call a
// nil service.
//
//nolint:ireturn // The adapter takes the port, and nil must stay an untyped nil.
func stateGuardOf(svc *gate.Service) tfe.StateGuard {
	if svc == nil {
		return nil
	}

	return svc
}

// inventoryGuardOf is stateGuardOf for the tool-neutral inventory API.
//
//nolint:ireturn // The adapter takes the port, and nil must stay an untyped nil.
func inventoryGuardOf(svc *gate.Service) inventoryapi.StateGuard {
	if svc == nil {
		return nil
	}

	return svc
}
