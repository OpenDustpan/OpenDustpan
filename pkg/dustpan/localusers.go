package dustpan

import (
	"context"
	"fmt"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// serverExtras holds the parts of the installation API that newServerHandler
// builds after the principals: the IP allowlists, the audit store of the
// installation, the settings service and the local user service.
type serverExtras struct {
	allow      *ipallow.Service
	audit      audit.ServerStore
	settings   *setting.Service
	localUsers *localusersvc.Service
}

// newServerExtras builds the serverExtras of cfg.
func newServerExtras(cfg *settings, deps installationDeps, principals *principal.Service) (serverExtras, error) {
	none := serverExtras{allow: nil, audit: nil, settings: nil, localUsers: nil}

	allow, err := newIPAllowlistService(cfg, deps.services.namespaces)
	if err != nil {
		return none, err
	}

	serverAudit, err := serverAuditOf(cfg.namespaceStore)
	if err != nil {
		return none, err
	}

	settingsSvc := newSettingService(cfg, cfg.namespaceStore)

	localUsers, err := newLocalUserService(cfg, principals, settingsSvc, deps.signIn)
	if err != nil {
		return none, err
	}

	return serverExtras{allow: allow, audit: serverAudit, settings: settingsSvc, localUsers: localUsers.service}, nil
}

// localUserWiring holds the service of the local users. The service is nil
// when the store keeps no local users, no principals or no audit log: then no
// local user route is mounted.
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
		Store: store, Principals: principals, Hasher: hasher, Blocklist: blocklist.Embedded(), Audit: entries,
		Settings: settingsPort, SSO: identityProviders(cfg, signIn), Reach: reach, Kind: cfg.installationKind,
		Now: cfg.now,
	})
	if err != nil {
		return none, fmt.Errorf("local users: %w", err)
	}

	return localUserWiring{service: svc}, nil
}

// reachConfig returns the reach config of the store. A store that keeps no
// grants gives an empty config, and each reset then fails closed.
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
