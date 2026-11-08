package dustpan

import (
	"context"
	"fmt"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/removalalertsvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// serverExtras holds the parts of the installation API that newServerHandler
// builds after the principals: the IP allowlists, the audit store of the
// installation, the settings service, the local user service and the
// passkey service. passkeys is nil when the server keeps no passkeys.
type serverExtras struct {
	allow      *ipallow.Service
	audit      audit.ServerStore
	settings   *setting.Service
	localUsers *localusersvc.Service
	passkeys   *mfasvc.Service
}

// newServerExtras builds the serverExtras of cfg.
func newServerExtras(cfg *settings, deps installationDeps, principals *principal.Service) (serverExtras, error) {
	none := serverExtras{allow: nil, audit: nil, settings: nil, localUsers: nil, passkeys: nil}

	allow, err := newIPAllowlistService(cfg.ipAllowlists, deps.services.namespaces, cfg.now)
	if err != nil {
		return none, err
	}

	serverAudit, err := serverAuditOf(cfg.namespaceStore)
	if err != nil {
		return none, err
	}

	settingsSvc := newSettingService(cfg, cfg.namespaceStore)

	if deps.signIn != nil && settingsSvc != nil {
		deps.signIn.Sessions.SetBreakGlassLimit(breakGlassLimitOf(settingsSvc))
	}

	localUsers, err := newLocalUserService(cfg, principals, settingsSvc, deps.signIn)
	if err != nil {
		return none, err
	}

	if deps.signIn != nil {
		if wireErr := wireSignIn(cfg, deps, principals, settingsSvc, serverAudit); wireErr != nil {
			return none, wireErr
		}
	}

	passkeys, err := newPasskeyService(cfg.namespaceStore, cfg.publicURL, serverAudit, cfg.now, cfg.logger)
	if err != nil {
		return none, err
	}

	return serverExtras{
		allow: allow, audit: serverAudit, settings: settingsSvc, localUsers: localUsers.service,
		passkeys: passkeys.service,
	}, nil
}

// wireSignIn builds the removal alert service and the sign-in service over
// the store of cfg.
func wireSignIn(
	cfg *settings, deps installationDeps, principals *principal.Service, settingsSvc *setting.Service,
	serverAudit audit.ServerStore,
) error {
	wiring, err := removalAlertServiceOf(cfg, settingsSvc, serverAudit)
	if err != nil {
		return err
	}

	deps.signIn.Service, err = newSignInService(signInServiceInputs{
		principals: principals, store: cfg.namespaceStore, sessions: deps.signIn.Sessions, audit: serverAudit,
		logger: cfg.logger, onAuditFailure: deps.observed.IncAuditAppendFailure, removals: wiring.service,
	})

	return err
}

// removalAlertWiring holds the service of the removal alert. The service is
// nil when the store keeps no removals, no member count or no notifications:
// the sign-in service then records no removal.
type removalAlertWiring struct {
	service *removalalertsvc.Service
}

// removalAlertServiceOf builds the removal alert service of the store of
// cfg. The limits reader is nil when the server keeps no settings: the
// service then applies the default limits.
func removalAlertServiceOf(
	cfg *settings, settingsSvc *setting.Service, serverAudit audit.ServerStore,
) (removalAlertWiring, error) {
	none := removalAlertWiring{service: nil}

	log, logOK := cfg.namespaceStore.(principal.RemovalLog)
	members, membersOK := cfg.namespaceStore.(removalalertsvc.MemberCounter)
	outbox, outboxOK := cfg.sinks.store.(notify.Outbox)

	if !logOK || !membersOK || !outboxOK {
		return none, nil
	}

	svc, err := removalalertsvc.New(removalalertsvc.Config{
		Log: log, Members: members, Outbox: outbox, Audit: serverAudit,
		Logger: cfg.logger, Now: cfg.now, Limits: removalAlertLimitsOf(settingsSvc),
	})
	if err != nil {
		return none, fmt.Errorf("removal alert service: %w", err)
	}

	return removalAlertWiring{service: svc}, nil
}

// breakGlassLimitOf returns the reader of the limit of a break-glass session
// from the settings service: the effective value of the setting, whatever
// layer holds it. A reader that fails makes the sessions use the default.
func breakGlassLimitOf(settingsSvc *setting.Service) session.BreakGlassLimit {
	return func(ctx context.Context) (time.Duration, error) {
		item, err := settingsSvc.Effective(ctx, setting.InstallationScope(), session.SettingBreakGlassMaxLength)
		if err != nil {
			return 0, fmt.Errorf("read the break-glass session limit: %w", err)
		}

		return session.ParseBreakGlassMaxLength(item.Value)
	}
}

// removalAlertLimitsOf returns the reader of the removal alert limits from
// the settings service: the effective value of each setting, whatever layer
// holds it. A reader that fails makes the service use the default limits. A
// nil settings service gives a nil reader, and the defaults apply.
func removalAlertLimitsOf(settingsSvc *setting.Service) removalalertsvc.Limits {
	if settingsSvc == nil {
		return nil
	}

	return func(ctx context.Context) (principal.RemovalAlertLimits, error) {
		count, err := settingsSvc.Effective(ctx, setting.InstallationScope(), principal.SettingRemovalAlertCount)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("read the removal alert count: %w", err)
		}

		percent, err := settingsSvc.Effective(ctx, setting.InstallationScope(), principal.SettingRemovalAlertPercent)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("read the removal alert percent: %w", err)
		}

		window, err := settingsSvc.Effective(ctx, setting.InstallationScope(), principal.SettingRemovalAlertWindow)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("read the removal alert window: %w", err)
		}

		c, err := principal.ParseRemovalAlertCount(count.Value)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("parse the removal alert count: %w", err)
		}

		p, err := principal.ParseRemovalAlertPercent(percent.Value)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("parse the removal alert percent: %w", err)
		}

		w, err := principal.ParseRemovalAlertWindow(window.Value)
		if err != nil {
			return principal.RemovalAlertLimits{}, fmt.Errorf("parse the removal alert window: %w", err)
		}

		return principal.RemovalAlertLimits{Count: c, Percent: p, Window: w}, nil
	}
}

// localUserWiring holds the service of the local users. The service is nil
// when the store keeps no local users, no principals, no audit log or no
// lockout by name: then no local user route is mounted, so the password
// sign-in never runs without its lockout.
type localUserWiring struct {
	service *localusersvc.Service
}

// newLocalUserService builds the service of the local users. The settings
// service is nil when the server keeps no settings: the service then applies
// the default of each password setting.
func newLocalUserService(
	cfg *settings, principals *principal.Service, settingsSvc *setting.Service, signIn *serverapi.SignIn,
) (localUserWiring, error) {
	none := localUserWiring{service: nil}

	store, ok := cfg.namespaceStore.(localusersvc.Stores)
	if !ok || principals == nil {
		return none, nil
	}

	entries, ok := cfg.namespaceStore.(localusersvc.Auditor)
	if !ok {
		return none, nil
	}

	locks, ok := cfg.namespaceStore.(authgate.NameLockCounter)
	if !ok {
		return none, nil
	}

	keys, ok := cfg.namespaceStore.(localusersvc.NameKeys)
	if !ok {
		return none, nil
	}

	// A store that cannot find a principal by its subject leaves Subjects
	// nil: the unlock and the read of the counts then refuse each caller
	// with an access token, which fails closed.
	var subjects localusersvc.Subjects
	if found, isSubjects := cfg.namespaceStore.(localusersvc.Subjects); isSubjects {
		subjects = found
	}

	budget, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		return none, fmt.Errorf("password hash budget: %w", err)
	}

	hasher, err := argon2id.New(budget)
	if err != nil {
		return none, fmt.Errorf("password hasher: %w", err)
	}

	var settingsPort localusersvc.Settings
	if settingsSvc != nil {
		settingsPort = settingsSvc
	}

	reach, err := reachConfig(cfg)
	if err != nil {
		return none, err
	}

	svc, err := localusersvc.New(localusersvc.Config{
		Store:      store,
		Principals: principals,
		Hasher:     hasher,
		Blocklist:  blocklist.Embedded(),
		Audit:      entries,
		Locks:      locks,
		Keys:       keys,
		Settings:   settingsPort,
		SSO:        identityProviders(cfg, signIn), Reach: reach, Subjects: subjects,
		Kind: cfg.installationKind,
		Now:  cfg.now,
		Log:  cfg.logger,
	})
	if err != nil {
		return none, fmt.Errorf("local users: %w", err)
	}

	return localUserWiring{service: svc}, nil
}

// reachConfig returns the reach config of the store. A store that keeps no
// grants gives an empty config: a reset of a person and a token change then
// fail closed.
func reachConfig(cfg *settings) (access.ReachConfig, error) {
	none := access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil}

	stores, ok := cfg.namespaceStore.(accessStores)
	if !ok {
		return none, nil
	}

	collections, err := unitCollections(cfg)
	if err != nil {
		return none, err
	}

	return access.ReachConfig{
		Grants: stores, Roles: stores, Callers: stores, Principals: stores, Collections: collections, Units: stores,
	}, nil
}

// identityProviders returns the function that tells whether the installation
// has an identity provider: a provider of the configuration, or an enabled
// stored provider. A store that cannot list the providers returns its error,
// and the password sign-in then fails closed.
func identityProviders(cfg *settings, signIn *serverapi.SignIn) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		if len(cfg.oidcProviders) > 0 {
			return true, nil
		}

		if signIn == nil || signIn.Admin == nil {
			return false, nil
		}

		list, err := signIn.Admin.List(ctx)
		if err != nil {
			return false, fmt.Errorf("list the sign-in providers: %w", err)
		}

		for i := range list {
			if list[i].Enabled {
				return true, nil
			}
		}

		return false, nil
	}
}
