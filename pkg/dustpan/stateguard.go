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

// gateService is the service that checks the writes of state. The alias keeps
// the name of the package out of the files that name gate for the body gate.
type gateService = gate.Service

// newCollectionAndGateServices returns the collection service and the gate
// service. Each one is nil when the store does not hold its data.
func newCollectionAndGateServices(
	cfg *settings, namespaces *namespace.Service,
) (*collection.Service, *gateService, error) {
	collections, err := newCollectionService(cfg)
	if err != nil {
		return nil, nil, err
	}

	gates, err := newGateService(cfg, namespaces)
	if err != nil {
		return nil, nil, err
	}

	return collections, gates, nil
}

// newGateService returns the service that checks each write of state against
// the access limits and the gates of state.write, or nil when the store
// keeps no gates or the server has no inventory. Both SQL adapters keep gates.
// The tool adapters then check no write.
func newGateService(cfg *settings, namespaces *namespace.Service) (*gate.Service, error) {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok || cfg.inventoryReader == nil || namespaces == nil {
		return nil, nil //nolint:nilnil // A store without gates is a valid setup: no write is checked.
	}

	facts := newInventoryFacts(cfg)

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
	// ids finds the stable ID of a unit by its reference. It is nil when the
	// store has no such lookup, and the reader then lists the namespace.
	ids unitIDs
	// deleted gives the collection and the labels of a deleted unit. It is
	// nil when the store keeps no deleted units.
	deleted inventory.DeletedFactsReader
}

// unitIDs finds the stable ID of a live unit by its reference with one
// indexed read. The SQL stores implement it.
type unitIDs interface {
	UnitID(ctx context.Context, obj identity.Object) (string, bool, error)
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

// entry returns the inventory entry of unit. A unit that is not in the
// inventory, for example a deleted unit, gives gate.ErrNotFound.
func (f inventoryFacts) entry(ctx context.Context, unit identity.Object) (inventory.Entry, error) {
	id, err := f.unitID(ctx, unit)
	if err != nil {
		return inventory.Entry{}, err
	}

	if id == "" {
		return f.listed(ctx, unit)
	}

	e, err := f.reader.GetUnit(ctx, id)
	if errors.Is(err, inventory.ErrUnitNotFound) {
		if unit.UnitID() != "" {
			return f.deletedEntry(ctx, unit)
		}

		return inventory.Entry{}, gate.ErrNotFound
	}

	if err != nil {
		return inventory.Entry{}, fmt.Errorf("inventory unit %s: %w", id, err)
	}

	// Defense in depth: the caller names the unit by its stable ID, and the ID
	// alone selects the row. This check stays so that a reference with the ID of
	// a unit in another namespace or of another tool never reads the facts of
	// that unit. It does not rely on the authorization of the route or on the
	// stores, which key the units by namespace and tool too.
	if e.Namespace != unit.Namespace() || e.Tool != unit.Tool() {
		return inventory.Entry{}, fmt.Errorf("%w: unit %s", errUnitMismatch, unit)
	}

	return e, nil
}

// errUnitMismatch reports a stable ID whose inventory entry belongs to another
// namespace or another tool than the reference. It is not gate.ErrNotFound,
// because that error reads as a unit with no collection.
var errUnitMismatch = errors.New("the stable ID names a unit in another namespace or of another tool")

// deletedEntry returns the collection and the labels of a deleted unit that
// the reference names by its stable ID. The membership of a collection stays
// until the purge, so the facts are the real ones. A unit that is neither live
// nor deleted fails closed: the error is not gate.ErrNotFound, because that
// error reads as a unit with no collection.
func (f inventoryFacts) deletedEntry(ctx context.Context, unit identity.Object) (inventory.Entry, error) {
	if f.deleted == nil {
		return inventory.Entry{}, fmt.Errorf("%w: unit %s", errNoDeletedFacts, unit)
	}

	facts, err := f.deleted.GetDeletedUnitFacts(ctx, unit.Namespace(), unit.Tool(), unit.UnitID())
	if err != nil {
		return inventory.Entry{}, fmt.Errorf("facts of deleted unit %s: %w", unit, err)
	}

	return inventory.Entry{
		LastChanged: time.Time{}, Namespace: unit.Namespace(), Tool: unit.Tool(), Name: unit.UnitName(),
		UnitID: unit.UnitID(), DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, ManualLock: nil,
		Collection: facts.Collection, Labels: facts.Labels,
	}, nil
}

// errNoDeletedFacts reports a unit that is not live, in a store that keeps no
// facts of deleted units.
var errNoDeletedFacts = errors.New("the unit is not live and the store keeps no facts of deleted units")

// unitID returns the stable ID of unit: the one the reference carries, or the
// one that the store finds with one indexed read. It returns the empty ID when
// the store has no such lookup, and gate.ErrNotFound for a unit it does not
// know.
func (f inventoryFacts) unitID(ctx context.Context, unit identity.Object) (string, error) {
	if id := unit.UnitID(); id != "" || f.ids == nil {
		return id, nil
	}

	id, ok, err := f.ids.UnitID(ctx, unit)
	if err != nil {
		return "", fmt.Errorf("unit ID of %s: %w", unit, err)
	}

	if !ok {
		return "", gate.ErrNotFound
	}

	return id, nil
}

// listed finds unit in the list of its namespace. It is the way for a store
// with no indexed lookup.
func (f inventoryFacts) listed(ctx context.Context, unit identity.Object) (inventory.Entry, error) {
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

	var credential gate.CredentialKind

	// The empty credential counts as an access token. A value that this
	// switch does not know is refused: the gates must not treat a new kind
	// of credential as an access token.
	switch p.Credential {
	case identity.CredentialSession:
		credential = gate.CredentialSession
	case identity.CredentialJobToken:
		credential = gate.CredentialJobToken
	case identity.CredentialTrustToken:
		credential = gate.CredentialTrustToken
	case identity.CredentialAccessToken, identity.CredentialServer, "":
		// The server credential is the one of the namespace service principal
		// of a start that no person makes. No client can present it. The facts
		// of a gate know it as a service principal with an access token, so a
		// limit for services covers it, and no rule can treat it as a person
		// or a session.
		credential = gate.CredentialAccessToken
	default:
		return gate.Subject{}, fmt.Errorf("%w: the principal has an unknown kind of credential", identity.ErrDenied)
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
func stateGuardOf(svc *gateService) tfe.StateGuard {
	if svc == nil {
		return nil
	}

	return svc
}

// inventoryGuardOf is stateGuardOf for the tool-neutral inventory API.
//
//nolint:ireturn // The adapter takes the port, and nil must stay an untyped nil.
func inventoryGuardOf(svc *gateService) inventoryapi.StateGuard {
	if svc == nil {
		return nil
	}

	return svc
}
