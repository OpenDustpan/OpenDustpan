package dustpan

import (
	"fmt"
	"net/http"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/preferences"
)

// newServerHandler builds the tool-neutral installation API. It carries the
// namespace service that creates namespaces, the backup service when a
// backup target is configured, else nil, the restore service when a backup
// target is configured, else nil, the maintenance service, the registry
// that the metrics route writes, the server version, the schema store that
// the upgrade check reads, and the principal service when the store keeps
// principals (ADR 52).
func newServerHandler(
	cfg *settings, deps installationDeps, restores *backup.RestoreService, checks *storecheck.Service,
) (http.Handler, error) {
	grants := deps.services.access

	principals, err := newPrincipalService(cfg, grants)
	if err != nil {
		return nil, err
	}

	gates, err := newGateConfigService(cfg, deps.services.namespaces, principals)
	if err != nil {
		return nil, err
	}

	gitHost, idem, err := newGitHostParts(cfg, deps)
	if err != nil {
		return nil, err
	}

	extras, err := newServerExtras(cfg, deps, principals)
	if err != nil {
		return nil, err
	}

	handler, err := serverapi.NewHandler(serverapi.Config{
		GitHost:          gitHost,
		Idempotency:      idem,
		Authenticator:    cfg.authenticator,
		Authorizer:       cfg.authorizer,
		Logger:           cfg.logger,
		Namespaces:       deps.services.namespaces,
		Backups:          deps.backups,
		Restores:         restores,
		Maintenance:      deps.maint,
		Metrics:          deps.observed,
		Version:          cfg.version,
		Schema:           schemaReporterOf(cfg.namespaceStore),
		Journal:          journalReporterOf(cfg.namespaceStore),
		Principals:       principals,
		Access:           grants,
		Tokens:           deps.services.tokens,
		IPAllowlists:     extras.allow,
		Gates:            gatesPort(gates),
		SessionPolicy:    deps.policies,
		Audit:            extras.audit,
		CheckpointEpochs: checkpointEpochsOf(deps.chain),
		OnAuditFailure:   deps.observed.IncAuditAppendFailure,
		DenialLimiter:    cfg.denialLimiter,
		DeletedUnits:     deps.services.deleted,
		TempAdmins:       tempReaderOf(cfg.namespaceStore),
		Now:              cfg.now,
		AuditChain:       auditChainPort(deps.chain),
		AuditSinks:       auditSinksOf(cfg),
		Workers:          cfg.watch,
		SignIn:           deps.signIn,
		Preferences:      newPreferencesService(cfg),
		LocalUsers:       extras.localUsers,
		Settings:         extras.settings,
		InstallationKind: cfg.installationKind, StateWriteGates: deps.services.stateWrite.effective(),
		Instances: cfg.instanceStore, StoreChecks: checks,
	})
	if err != nil {
		return nil, fmt.Errorf("server api: %w", err)
	}

	return handler, nil
}

// auditSinksOf returns the source of the audit sink list, or nil when no audit
// shipper is set.
//
//nolint:ireturn // The route reads the list through its own port.
func auditSinksOf(cfg *settings) serverapi.AuditSinkSource {
	if cfg.auditShipper == nil {
		return nil
	}

	return cfg.auditShipper
}

// newPreferencesService builds the preferences service from the namespace
// store, or returns nil when the store keeps no preferences or no principals.
// Both SQL adapters keep both. A nil service makes the routes answer 501.
func newPreferencesService(cfg *settings) *preferences.Service {
	store, ok := cfg.namespaceStore.(preferences.Store)
	if !ok {
		return nil
	}

	people, ok := cfg.namespaceStore.(preferences.People)
	if !ok {
		return nil
	}

	return preferences.NewService(store, people, cfg.now)
}

// schemaReporterOf returns the schema reporter of the namespace store, or
// nil when the store does not report applied migrations. Both SQL adapters
// report them.
//
// caller keeps the interface the installation API consumes.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the
func schemaReporterOf(store namespace.Store) serverapi.SchemaReporter {
	reporter, ok := store.(serverapi.SchemaReporter)
	if !ok {
		return nil
	}

	return reporter
}

// journalReporterOf returns the journal reporter of the namespace store, or
// nil when the store does not count running journaled updates. Both SQL
// adapters count them.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the
func journalReporterOf(store namespace.Store) serverapi.JournalReporter {
	reporter, ok := store.(serverapi.JournalReporter)
	if !ok {
		return nil
	}

	return reporter
}
