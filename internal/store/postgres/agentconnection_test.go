package postgres_test

import (
	"bytes"
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/secret"
)

// openAgentStore opens a fresh schema and the sealer of its secret items.
func openAgentStore(t *testing.T, base string) (*postgres.Store, *secret.InstallationSecrets) {
	t.Helper()

	s := open(t, base)

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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	agenttest.RunSecretItems(t, func(t *testing.T) agenttest.SecretItemsHarness {
		t.Helper()

		s, secrets := openAgentStore(t, base)

		return agenttest.SecretItemsHarness{Items: s.AgentSecretItems(secrets)}
	})
}

func TestAgentConnectionStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	agenttest.RunConnectionStore(t, func(t *testing.T) agenttest.ConnectionStoreHarness {
		t.Helper()

		s, secrets := openAgentStore(t, base)

		return agenttest.ConnectionStoreHarness{Store: s.AgentConnections(), Items: s.AgentSecretItems(secrets)}
	})
}
