package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/gate/limitgroups"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// gateService is the service that checks the writes of state. The alias keeps
// the name of the package out of the files that name gate for the body gate.
type gateService = gate.Service

// reasonNoGateStore is the reason of the effective value "state.write gates:
// off". It is the only case where the gates are off.
const reasonNoGateStore = "the namespace store keeps no gates: it does not implement gate.Store"

// errGateParts means that the store keeps gates and the server lacks a part
// that the gate service needs. The server does not start: it never runs with
// a store that keeps gates and no check of them.
var errGateParts = errors.New("the namespace store keeps gates, so the gate service needs the inventory reader, " +
	"the namespace service and the principal store")

// stateWriteGates is the rule of the state.write gates of a server, which the
// tool adapters and the claimer of the runner protocol share: a store that
// keeps gates gives the gate service, and a store that keeps no gates gives
// the explicit guard of stategate.Off with its reason. A store that keeps
// gates with a part missing stops the start.
type stateWriteGates struct {
	// svc is the gate service. It is nil only when the gates are off.
	svc *gate.Service
	// guard is svc, or the guard of stategate.Off. It is never nil.
	guard stategate.Guard
}

// effective returns the effective value of the state.write gates.
func (g stateWriteGates) effective() stategate.Effective {
	return stategate.EffectiveOf(g.guard)
}

// newCollectionAndGateServices returns the collection service and the
// state.write gates. The collection service is nil when the store holds no
// collections.
func newCollectionAndGateServices(
	cfg *settings, namespaces *namespace.Service,
) (*collection.Service, stateWriteGates, error) {
	collections, err := newCollectionService(cfg)
	if err != nil {
		return nil, stateWriteGates{svc: nil, guard: nil}, err
	}

	gates, err := newStateWriteGates(cfg, namespaces)
	if err != nil {
		return nil, stateWriteGates{svc: nil, guard: nil}, err
	}

	return collections, gates, nil
}

// accessLimitGroups returns the reader through which the guard of a group
// reads the access limits of every scope, or nil when the store keeps none.
// An access limit of a scope that names a group holds for the members of
// that group, so a change of its members that can lift the limit needs a
// site administrator.
func accessLimitGroups(cfg *settings, groups access.Directory) *limitgroups.Reader {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		return nil
	}

	return limitgroups.New(store, groups)
}

// newStateWriteGates returns the state.write gates of the server: the gate
// service when the store keeps gates, and the guard of stategate.Off when it
// keeps none. Both SQL adapters keep gates. A store that keeps gates on a
// server with no inventory reader, no namespace service or no principal store
// is an error, so the server does not start: it fails closed.
func newStateWriteGates(cfg *settings, namespaces *namespace.Service) (stateWriteGates, error) {
	none := stateWriteGates{svc: nil, guard: nil}

	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		return stateWriteGates{svc: nil, guard: stategate.Off(reasonNoGateStore)}, nil
	}

	principals := principalStoreOf(cfg)

	// Defense in depth: the option check refuses a server with no inventory
	// reader, and the namespace service and the principal store are always
	// built before this call. This check stays so that a new path to this call
	// cannot run a store that keeps gates with no gate service. It does not
	// rely on the option check.
	if cfg.inventoryReader == nil || namespaces == nil || principals == nil {
		return none, fmt.Errorf("%w: %w", ErrMissingOption, errGateParts)
	}

	facts := newInventoryFacts(cfg)

	log := cfg.logger
	if log == nil {
		log = slog.Default()
	}

	svc, err := gate.NewService(gate.ServiceConfig{
		Store: store, Engine: newGateEngine(), Producers: nil,
		Units: facts, Subjects: principalSubjects{principals: principals},
		Fences: namespaceFences{namespaces: namespaces},
		Now:    cfg.now, Log: log,
	})
	if err != nil {
		return none, fmt.Errorf("gate service: %w", err)
	}

	return stateWriteGates{svc: svc, guard: svc}, nil
}

// newGateEngine returns the linked policy engine. It knows each action of the
// access families, so an access limit that names an action of the server
// checks, and one that names another action does not.
func newGateEngine() *cedargo.Engine {
	var actions []string
	for _, family := range []access.Family{
		access.FamilyOperator, access.FamilyAdministration, access.FamilyData,
		access.FamilyDelegation, access.FamilySelf,
	} {
		for _, a := range access.Actions(family) {
			actions = append(actions, string(a))
		}
	}

	return cedargo.New(cedargo.WithAccessActions(actions...))
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

// Unit implements gate.UnitSource: the collection and the labels of unit
// from one read of its inventory entry.
func (f inventoryFacts) Unit(ctx context.Context, unit identity.Object) (gate.UnitInfo, error) {
	e, err := f.entry(ctx, unit)
	if err != nil {
		return gate.UnitInfo{Collection: "", Labels: nil}, err
	}

	return gate.UnitInfo{Collection: collection.Name(e.Collection), Labels: entryLabels(e)}, nil
}

// entryLabels returns the labels of e as sorted key=value strings.
func entryLabels(e inventory.Entry) []string {
	labels := make([]string, 0, len(e.Labels))
	for k, v := range e.Labels {
		labels = append(labels, k+"="+v)
	}

	slices.Sort(labels)

	return labels
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
		LastRelease: nil,
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

// principalReads reads the record and the active groups of a principal. The
// principal store of the server meets it.
type principalReads interface {
	// GetPrincipalBySubject returns the principal with the subject subject.
	// It returns principal.ErrNotFound when no such principal exists or it
	// was deleted.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
	// ListActiveGroupNamesOf returns the names of the groups in which the
	// principal with the ID principalID has an active membership that is not
	// removed, ordered by name without regard to case. It returns
	// principal.ErrNotFound when no such principal exists or it was deleted.
	ListActiveGroupNamesOf(ctx context.Context, principalID string) ([]string, error)
}

// principalSubjects gives the Subject of a principal from its credential and
// its principal record. A session of a break-glass account is outside the
// access limits, and the active groups of the record are the groups of the
// subject.
type principalSubjects struct {
	principals principalReads
}

// Subject implements gate.SubjectSource.
func (s principalSubjects) Subject(ctx context.Context, p identity.Principal) (gate.Subject, error) {
	if strings.TrimSpace(p.Subject) == "" {
		return gate.Subject{}, fmt.Errorf("%w: the principal has no subject", identity.ErrDenied)
	}

	kind := gate.PrincipalPerson
	if p.Kind == identity.KindService || p.Kind == identity.KindWorkload {
		kind = gate.PrincipalService
	}

	credential, err := credentialKind(p.Credential)
	if err != nil {
		return gate.Subject{}, err
	}

	record, err := s.principals.GetPrincipalBySubject(ctx, p.Subject)
	if errors.Is(err, principal.ErrNotFound) {
		// A principal with no record, for example the bootstrap admin token,
		// has no group and is no break-glass account. Its kind is empty. A
		// principal with a kind must have a record, so it fails closed.
		if p.Kind != "" {
			return gate.Subject{}, fmt.Errorf(
				"%w: the principal %q has a kind but no record", identity.ErrDenied, p.Subject,
			)
		}

		return gate.Subject{
			ID: p.Subject, Groups: nil, Kind: kind, Credential: credential, AuthAt: authAt(ctx, credential),
			BreakGlassAccount: false, Job: nil,
		}, nil
	}

	if err != nil {
		return gate.Subject{}, fmt.Errorf("principal %q: %w", p.Subject, err)
	}

	groups, err := groupsOf(ctx, s.principals, record.ID)
	if err != nil {
		return gate.Subject{}, err
	}

	// Only a session of a break-glass account is outside the access limits. An
	// access token or another credential of that account is not: it meets the
	// limits as each other credential does.
	breakGlass := record.BreakGlass && credential == gate.CredentialSession

	return gate.Subject{
		ID: p.Subject, Groups: groups, Kind: kind, Credential: credential, AuthAt: authAt(ctx, credential),
		BreakGlassAccount: breakGlass, Job: nil,
	}, nil
}

// credentialKind returns the credential of the gates for c. The empty
// credential counts as an access token. A value that it does not know is
// refused: the gates must not treat a new kind of credential as an access
// token.
func credentialKind(c identity.Credential) (gate.CredentialKind, error) {
	switch c {
	case identity.CredentialSession:
		return gate.CredentialSession, nil
	case identity.CredentialJobToken:
		return gate.CredentialJobToken, nil
	case identity.CredentialTrustToken:
		return gate.CredentialTrustToken, nil
	case identity.CredentialAccessToken, "":
		return gate.CredentialAccessToken, nil
	case identity.CredentialServer:
		// The namespace service principal has no kind of credential in the
		// gates yet. Until a gate names it, the gates deny it: fail closed.
		return "", fmt.Errorf("%w: the gates do not know the server credential", identity.ErrDenied)
	default:
		return "", fmt.Errorf("%w: the principal has an unknown kind of credential", identity.ErrDenied)
	}
}

// groupsOf returns the sorted names of the active groups of the principal
// with the ID principalID, each once.
func groupsOf(ctx context.Context, reads principalReads, principalID string) ([]string, error) {
	groups, err := reads.ListActiveGroupNamesOf(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("active groups of principal %q: %w", principalID, err)
	}

	seen := make(map[string]struct{}, len(groups))
	var names []string

	for _, name := range groups {
		if _, ok := seen[name]; ok {
			continue
		}

		seen[name] = struct{}{}

		names = append(names, name)
	}

	slices.Sort(names)

	return names, nil
}

// authAt returns the time of the last authentication of a session, or the zero
// time for every other credential and for a session with no sign-in. The zero
// time is an unknown age, so a limit that asks for a recent authentication
// forbids the call.
func authAt(ctx context.Context, credential gate.CredentialKind) time.Time {
	if credential != gate.CredentialSession {
		return time.Time{}
	}

	signIn, ok := sessionauth.SignInFrom(ctx)
	if !ok || signIn.At.IsZero() {
		return time.Time{}
	}

	return signIn.At
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
