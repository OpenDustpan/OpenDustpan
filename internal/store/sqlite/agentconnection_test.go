package sqlite_test

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/secret"
)

// openAgentStore opens a fresh store and the sealer of its secret items.
func openAgentStore(t *testing.T) (*sqlite.Store, *secret.InstallationSecrets) {
	t.Helper()

	s := open(t)

	wrapper, err := secret.NewBuiltinWrapper(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: s, Wrapper: wrapper, Now: nil})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	return s, secrets
}

func TestAgentSecretItemsConformance(t *testing.T) {
	t.Parallel()

	agenttest.RunSecretItems(t, func(t *testing.T) agenttest.SecretItemsHarness {
		t.Helper()

		s, secrets := openAgentStore(t)

		return agenttest.SecretItemsHarness{Items: s.AgentSecretItems(secrets)}
	})
}

func TestAgentConnectionStoreConformance(t *testing.T) {
	t.Parallel()

	agenttest.RunConnectionStore(t, func(t *testing.T) agenttest.ConnectionStoreHarness {
		t.Helper()

		s, secrets := openAgentStore(t)

		return agenttest.ConnectionStoreHarness{Store: s.AgentConnections(), Items: s.AgentSecretItems(secrets)}
	})
}
