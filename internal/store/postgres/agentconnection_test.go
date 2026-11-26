package postgres_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/secret"
)

// openAgentStore opens a fresh schema and the sealer of its secret items. It
// returns the URL of the schema, so the ciphertext hook can read its rows.
func openAgentStore(t *testing.T, base string) (*postgres.Store, *secret.InstallationSecrets, string) {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s := openURL(t, schemaURL)

	wrapper, err := secret.NewBuiltinWrapper(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: s, Wrapper: wrapper, Now: nil})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	return s, secrets, schemaURL
}

// ciphertextAtRest reads the stored ciphertext of an item through a second
// handle on the schema at schemaURL.
func ciphertextAtRest(t *testing.T, schemaURL string) func(t *testing.T, ref agent.SecretRef) []byte {
	t.Helper()

	return func(t *testing.T, ref agent.SecretRef) []byte {
		t.Helper()

		conn, err := pgx.Connect(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("connect to the schema: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(t.Context()); closeErr != nil {
				t.Errorf("close the schema connection: %v", closeErr)
			}
		}()

		var sealed []byte

		err = conn.QueryRow(
			t.Context(), "SELECT ciphertext FROM agent_secret_items WHERE ref = $1", string(ref),
		).Scan(&sealed)
		if err != nil {
			t.Fatalf("select the ciphertext of the item %s: %v", ref, err)
		}

		return sealed
	}
}

func TestAgentSecretItemsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	agenttest.RunSecretItems(t, func(t *testing.T) agenttest.SecretItemsHarness {
		t.Helper()

		s, secrets, schemaURL := openAgentStore(t, base)

		return agenttest.SecretItemsHarness{
			Items:            s.AgentSecretItems(secrets),
			Store:            s.AgentConnections(),
			CiphertextAtRest: ciphertextAtRest(t, schemaURL),
		}
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

		s, secrets, _ := openAgentStore(t, base)

		return agenttest.ConnectionStoreHarness{Store: s.AgentConnections(), Items: s.AgentSecretItems(secrets)}
	})
}
