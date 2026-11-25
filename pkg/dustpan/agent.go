package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agentsvc"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
)

// newAgentService builds the service of the model connection from the
// namespace store. The second result is false when the store is none of the
// built-in SQL adapters or the server has no key wrapper or authorizer.
// Without the service, the agent routes stay unmounted.
func newAgentService(cfg *settings, authz identity.Authorizer) (*agentsvc.Service, bool, error) {
	if cfg.secretWrapper == nil || authz == nil {
		return nil, false, nil
	}

	// The two adapters return their own store structs, which share no Go
	// type, so one interface cannot name them both: each branch of the
	// switch hands the builder the ports of one adapter.
	switch store := cfg.namespaceStore.(type) {
	case *sqlite.Store:
		return buildAgentService(cfg, authz, agentPortSet{
			keys: store,
			items: func(sealer agent.SecretSealer) agent.SecretItems {
				return store.AgentSecretItems(sealer)
			},
			connection: func() agent.ConnectionStore { return store.AgentConnections() },
			allowlist:  func() agent.EgressAllowlistStore { return store.AgentEgressAllowlist() },
		})
	case *postgres.Store:
		return buildAgentService(cfg, authz, agentPortSet{
			keys: store,
			items: func(sealer agent.SecretSealer) agent.SecretItems {
				return store.AgentSecretItems(sealer)
			},
			connection: func() agent.ConnectionStore { return store.AgentConnections() },
			allowlist:  func() agent.EgressAllowlistStore { return store.AgentEgressAllowlist() },
		})
	default:
		return nil, false, nil
	}
}

// agentPortSet is the ports of one built-in SQL adapter for the model
// connection.
type agentPortSet struct {
	keys       secret.InstallationKeyStore
	items      func(agent.SecretSealer) agent.SecretItems
	connection func() agent.ConnectionStore
	allowlist  func() agent.EgressAllowlistStore
}

// buildAgentService seals the secret items of one adapter with the
// installation keys and builds the service of the model connection.
func buildAgentService(
	cfg *settings, authz identity.Authorizer, ports agentPortSet,
) (*agentsvc.Service, bool, error) {
	sealer, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys: ports.keys, Wrapper: cfg.secretWrapper, Now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("installation secrets: %w", err)
	}

	svc, err := agentsvc.New(agentsvc.Config{
		Secrets:    ports.items(sealer),
		Connection: ports.connection(),
		Allowlist:  ports.allowlist(),
		Authorizer: authz,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("agent service: %w", err)
	}

	return svc, true, nil
}
