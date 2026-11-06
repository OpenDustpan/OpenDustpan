package dustpan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	triggersapi "opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/jobstarter"
)

// WithTriggerStore sets the store of run triggers. With it, the server serves
// the trigger API under /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/
// triggers. A trigger starts a plan job through the job service, so the server
// serves the routes only when the job service is set up too (see
// WithJobStore).
func WithTriggerStore(st trigger.Store) Option {
	return func(o *settings) { o.triggerStore = st }
}

// triggerPorts are the ports of the server that the triggers need besides the
// trigger store.
type triggerPorts struct {
	records principal.RecordStore
	entries audit.Store
}

// triggerPortsOf returns the ports that the triggers need, and false when the
// server lacks one. Fail closed: a server without them saves no trigger that
// could never start. A namespace store that keeps no audit entries is an
// error, not a missing feature: the trigger API must not run without the
// entries of its refusals.
func triggerPortsOf(svc services, cfg *settings) (triggerPorts, bool, error) {
	entries, err := auditStoreOf(cfg)
	if err != nil {
		return triggerPorts{records: nil, entries: nil}, false, err
	}

	records, hasRecords := cfg.namespaceStore.(principal.RecordStore)

	ok := cfg.triggerStore != nil && hasRecords && cfg.inventoryReader != nil &&
		svc.namespaces != nil && cfg.authorizer != nil

	return triggerPorts{records: records, entries: entries}, ok, nil
}

// newTriggersHandler builds the trigger API, or returns nil when the server
// has no trigger store or lacks a port that the triggers need: the job service,
// the inventory and the principal records. It answers ErrMissingOption when the
// namespace store keeps no audit entries.
func newTriggersHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	ports, ok, err := triggerPortsOf(svc, cfg)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, nil //nolint:nilnil // No trigger API: the server lacks a port that it needs.
	}

	jobs, err := newJobService(svc, cfg)
	if err != nil {
		return nil, err
	}

	if jobs == nil {
		return nil, nil //nolint:nilnil // No trigger API: there is no job service to start a plan.
	}

	manager, err := newTriggerManager(svc, cfg, &ports, jobs, trigger.NewDispatcher)
	if err != nil {
		return nil, err
	}

	handler, err := triggersapi.NewHandler(triggersapi.Config{
		Namespaces:     svc.namespaces,
		Units:          cfg.inventoryReader,
		Store:          cfg.triggerStore,
		Manager:        manager,
		Workers:        cfg.watch,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          ports.entries,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("triggers api: %w", err)
	}

	return handler, nil
}

// finalVersions gives the dispatcher the last final state version of a unit,
// read from the job store.
type finalVersions struct {
	reader job.FinalVersionReader
}

// LastFinal implements trigger.Versions.
func (f finalVersions) LastFinal(ctx context.Context, unit graph.Unit) (string, bool, error) {
	row, ok, err := f.reader.FinalVersion(ctx, unit.Namespace, unit.ID)
	if err != nil {
		return "", false, fmt.Errorf("final state version of %s: %w", unit, err)
	}

	return row.VersionID, ok, nil
}

// newTriggerManager builds the manager of triggers over the ports of the
// server and the job service. It builds the dispatcher with newDispatcher, which
// the server passes as trigger.NewDispatcher.
func newTriggerManager(svc services, cfg *settings, ports *triggerPorts, jobs job.Starter,
	newDispatcher func(trigger.DispatcherConfig) (trigger.Dispatcher, error),
) (*trigger.Manager, error) {
	units := triggerUnits{reader: cfg.inventoryReader}

	starter, err := jobstarter.New(jobs, units)
	if err != nil {
		return nil, fmt.Errorf("trigger starter: %w", err)
	}

	savers, err := trigger.NewSaverCheck(trigger.SaverCheckConfig{
		Resolver: saverRecords{records: ports.records}, Authorizer: cfg.authorizer, Units: units,
	})
	if err != nil {
		return nil, fmt.Errorf("trigger saver check: %w", err)
	}

	fences := jobFences{namespaces: svc.namespaces}

	parts := triggerParts{starter: starter, savers: savers, units: units, fences: fences}

	dispatcher, err := newDispatcher(triggerDispatcherConfig(cfg, ports, parts))
	if err != nil {
		return nil, fmt.Errorf("trigger dispatcher: %w", err)
	}

	manager, err := trigger.NewManager(trigger.ManagerConfig{
		Store: cfg.triggerStore, Dispatcher: dispatcher, Authorizer: cfg.authorizer, Savers: savers, Units: units,
		Fences: fences, Now: cfg.now, NewID: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("trigger manager: %w", err)
	}

	return manager, nil
}

// triggerParts are the ports of the trigger wiring that the server builds
// before the dispatcher.
type triggerParts struct {
	starter trigger.Starter
	savers  trigger.SaverCheck
	units   trigger.Units
	fences  trigger.Fences
}

// triggerDispatcherConfig returns the ports of the dispatcher of triggers. The
// versions come from the job store when it has the final version reader. A
// store without the port gives no versions, so Dispatcher.Upstream fails closed
// with trigger.ErrVersionMismatch.
func triggerDispatcherConfig(cfg *settings, ports *triggerPorts, parts triggerParts) trigger.DispatcherConfig {
	var versions trigger.Versions

	// Defense in depth: the job store interfaces of the SQL adapters embed the
	// reader, which is the first layer. The assertion stays for job stores of
	// other builds, so a new caller cannot skip it. The log below is the third
	// layer: it makes a missing reader visible at start.
	if r, ok := cfg.jobStore.(job.FinalVersionReader); ok {
		versions = finalVersions{reader: r}
	} else {
		// Runs at server construction, which has no context.
		cfg.logger.Warn("the job store has no final version reader: run triggers refuse every upstream check")
	}

	return trigger.DispatcherConfig{
		Store: cfg.triggerStore, Starter: parts.starter, Savers: parts.savers, Units: parts.units,
		Audit: ports.entries, Fences: parts.fences, Versions: versions, Now: cfg.now,
	}
}

// triggerUnits is the trigger.Units over the inventory.
type triggerUnits struct {
	reader inventory.Reader
}

// Object implements trigger.Units.
func (u triggerUnits) Object(ctx context.Context, unit graph.Unit) (identity.Object, error) {
	e, err := u.entry(ctx, unit.Namespace, unit.ID)
	if err != nil {
		return identity.Object{}, err
	}

	// Defense in depth: the inventory looks a unit up by its ID alone. A
	// trigger row that names another tool for the ID must not pass for it.
	if e.Tool != unit.Tool {
		return identity.Object{}, trigger.ErrUnitGone
	}

	obj, err := identity.UnitObject(e.Tool, e.Namespace, e.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", trigger.ErrUnitGone, err)
	}

	return obj.WithUnitID(e.UnitID), nil
}

// Find implements trigger.Units.
func (u triggerUnits) Find(ctx context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	e, err := u.entry(ctx, ns, unitID)
	if err != nil {
		return graph.Unit{}, err
	}

	return graph.Unit{Namespace: ns, Tool: e.Tool, ID: e.UnitID}, nil
}

// entry returns the live unit unitID of the namespace ns.
func (u triggerUnits) entry(ctx context.Context, ns namespace.Name, unitID string) (inventory.Entry, error) {
	e, err := u.reader.GetUnit(ctx, unitID)

	switch {
	case errors.Is(err, inventory.ErrUnitNotFound):
		return inventory.Entry{}, trigger.ErrUnitGone
	case err != nil:
		return inventory.Entry{}, fmt.Errorf("get unit: %w", err)
	}

	// Defense in depth: a unit of another namespace does not answer for this one.
	if e.Namespace != ns || e.UnitID != unitID {
		return inventory.Entry{}, trigger.ErrUnitGone
	}

	return e, nil
}

// saverRecords is the trigger.SaverResolver over the principal records.
type saverRecords struct {
	records principal.RecordStore
}

// Saver implements trigger.SaverResolver. It finds the principal record with
// the subject now. A record that is missing, deleted or disabled is
// trigger.ErrSaverGone. The answer never carries the administrator flag, a
// token or a session: the grants of the record decide.
func (r saverRecords) Saver(ctx context.Context, subject string) (trigger.SaverRecord, error) {
	// Defense in depth: the form of a subject that belongs to the server never
	// names a saver, whatever the records hold.
	if identity.IsNamespaceServiceSubject(subject) {
		return trigger.SaverRecord{}, trigger.ErrSaverGone
	}

	p, err := r.records.GetPrincipalBySubject(ctx, subject)

	switch {
	case errors.Is(err, principal.ErrNotFound):
		return trigger.SaverRecord{}, trigger.ErrSaverGone
	case err != nil:
		return trigger.SaverRecord{}, fmt.Errorf("saver %q: %w", subject, err)
	case !p.DisabledAt.IsZero():
		return trigger.SaverRecord{}, trigger.ErrSaverGone
	case p.Subject != subject:
		return trigger.SaverRecord{}, trigger.ErrSaverGone
	}

	return trigger.SaverRecord{
		Principal: identity.Principal{
			Subject: p.Subject, DisplayName: p.DisplayName, Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.Kind(p.Kind),
			Credential: identity.CredentialAccessToken, JobID: "",
		},
		ID: p.ID,
	}, nil
}
