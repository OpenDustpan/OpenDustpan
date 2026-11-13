package dustpan

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
)

// serverHandlerInputs holds the values that newServerHandler reads. The
// assembly fills each field; the installation area reads no other value.
type serverHandlerInputs struct {
	authenticator identity.Authenticator
	authorizer    identity.Authorizer
	// store is the namespace store. The schema, journal, temporary
	// administrator and preferences ports come from it when it keeps them.
	store      namespace.Store
	namespaces *namespace.Service
	access     *access.Service
	// reach reads the reach of a group for the protected flag of the group
	// routes.
	reach      access.ReachConfig
	tokens     *accesstoken.Service
	deleted    *deletedSweeper
	stateWrite stategate.Effective
	// cursors seals the cursor of each page of the namespace list.
	cursors secret.Wrapper

	// backups is the backup service and restores the restore service, both
	// nil when no backup target is configured. checks is the store check
	// service, or nil.
	backups     *backup.Service
	restores    *backup.RestoreService
	checks      *storecheck.Service
	maintenance *maintenance.Service
	observed    *metrics.ServerMetrics
	chain       *auditChain
	// policies is the session policy service, nil for a store with no
	// session policy. signIn is the console sign-in, nil with no provider.
	policies *session.Policies
	signIn   *serverapi.SignIn

	// The parts that the assembly builds after the principals.
	principals   *principal.Service
	gates        *gate.ConfigService
	forgeService *forge.Service
	idempotency  *idempotent.Middleware
	extras       serverExtras

	// shipper ships the audit entries to the audit sinks, or is nil.
	shipper          *audit.Shipper
	workers          *workerWatch
	instances        installation.InstanceStore
	installationKind installation.Kind
	denialLimiter    *denylimit.Limiter
	version          string
	logger           *slog.Logger
	now              func() time.Time
}

// newServerHandler builds the tool-neutral installation API. It carries the
// namespace service that creates namespaces, the backup service when a
// backup target is configured, else nil, the restore service when a backup
// target is configured, else nil, the maintenance service, the registry
// that the metrics route writes, the server version, the schema store that
// the upgrade check reads, and the principal service when the store keeps
// principals.
func newServerHandler(in serverHandlerInputs) (http.Handler, error) {
	handler, err := serverapi.NewHandler(&serverapi.Config{
		GitHost:          in.forgeService,
		Idempotency:      in.idempotency,
		Authenticator:    in.authenticator,
		Authorizer:       in.authorizer,
		Logger:           in.logger,
		Namespaces:       in.namespaces,
		Backups:          in.backups,
		Restores:         in.restores,
		Maintenance:      in.maintenance,
		Metrics:          in.observed,
		Version:          in.version,
		Schema:           schemaReporterOf(in.store),
		Journal:          journalReporterOf(in.store),
		Principals:       in.principals,
		Access:           in.access,
		Reach:            in.reach,
		Tokens:           in.tokens,
		IPAllowlists:     in.extras.allow,
		Gates:            gatesPort(in.gates),
		SessionPolicy:    in.policies,
		Audit:            in.extras.audit,
		CheckpointEpochs: checkpointEpochsOf(in.chain),
		OnAuditFailure:   in.observed.IncAuditAppendFailure,
		DenialLimiter:    in.denialLimiter,
		DeletedUnits:     in.deleted,
		TempAdmins:       tempReaderOf(in.store),
		Now:              in.now,
		AuditChain:       auditChainPort(in.chain),
		AuditSinks:       auditSinksOf(in.shipper),
		Workers:          in.workers,
		SignIn:           withPasskeys(in.signIn, in.extras.passkeys),
		Preferences:      newPreferencesService(in.store, in.now),
		GitHostLinks:     newForgeLinkService(in.store, in.now),
		LocalUsers:       in.extras.localUsers,
		Settings:         in.extras.settings,
		InstallationKind: in.installationKind, StateWriteGates: in.stateWrite,
		Instances: in.instances, StoreChecks: in.checks, Cursors: in.cursors,
	})
	if err != nil {
		return nil, fmt.Errorf("server api: %w", err)
	}

	return handler, nil
}

// withPasskeys returns a copy of the console sign-in signIn that holds the
// passkey service passkeys, or signIn as it is when the server has no console
// sign-in or no passkey service. The copy keeps the shared sign-in unchanged.
// A nil *mfasvc.Service never goes into the interface: the handler would
// mount the passkey routes on a service that is not there.
func withPasskeys(signIn *serverapi.SignIn, passkeys *mfasvc.Service) *serverapi.SignIn {
	if signIn == nil || passkeys == nil {
		return signIn
	}

	withService := *signIn
	withService.Passkeys = passkeys
	withService.PasskeySignIn = passkeys

	return &withService
}

// auditSinksOf returns the source of the audit sink list, or nil when no audit
// shipper is set.
//
//nolint:ireturn // The route reads the list through its own port.
func auditSinksOf(shipper *audit.Shipper) serverapi.AuditSinkSource {
	if shipper == nil {
		return nil
	}

	return shipper
}

// newPreferencesService builds the preferences service from the namespace
// store, or returns nil when the store keeps no preferences or no principals.
// Both SQL adapters keep both. A nil service makes the routes answer 501.
func newPreferencesService(st namespace.Store, now func() time.Time) *preferences.Service {
	store, ok := st.(preferences.Store)
	if !ok {
		return nil
	}

	people, ok := st.(preferences.People)
	if !ok {
		return nil
	}

	return preferences.NewService(store, people, now)
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
