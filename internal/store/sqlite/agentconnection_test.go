package sqlite_test

import (
	"bytes"
	"net/netip"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/secret"
)

// openAgentStore opens a fresh store at path and the sealer of its secret
// items.
func openAgentStore(t *testing.T, path string) (*sqlite.Store, *secret.InstallationSecrets) {
	t.Helper()

	s := openPath(t, path)

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

// ciphertextAtRest reads the stored ciphertext of an item through a second
// handle on the database at path.
func ciphertextAtRest(t *testing.T, path string) func(t *testing.T, ref agent.SecretRef) []byte {
	t.Helper()

	return func(t *testing.T, ref agent.SecretRef) []byte {
		t.Helper()

		conn, err := openRawDB(path)
		if err != nil {
			t.Fatalf("open raw database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close raw database: %v", closeErr)
			}
		}()

		var sealed []byte

		err = conn.QueryRowContext(
			t.Context(), "SELECT ciphertext FROM agent_secret_items WHERE ref = ?", string(ref),
		).Scan(&sealed)
		if err != nil {
			t.Fatalf("select the ciphertext of the item %s: %v", ref, err)
		}

		return sealed
	}
}

func TestAgentSecretItemsConformance(t *testing.T) {
	t.Parallel()

	agenttest.RunSecretItems(t, func(t *testing.T) agenttest.SecretItemsHarness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, secrets := openAgentStore(t, path)

		return agenttest.SecretItemsHarness{
			Items:            s.AgentSecretItems(secrets),
			Store:            s.AgentConnections(),
			CiphertextAtRest: ciphertextAtRest(t, path),
		}
	})
}

func TestAgentConnectionStoreConformance(t *testing.T) {
	t.Parallel()

	agenttest.RunConnectionStore(t, func(t *testing.T) agenttest.ConnectionStoreHarness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, secrets := openAgentStore(t, path)

		return agenttest.ConnectionStoreHarness{
			Store:     s.AgentConnections(),
			Items:     s.AgentSecretItems(secrets),
			Allowlist: s.AgentEgressAllowlist(),
		}
	})
}

// TestAgentEgressAllowlistGetFailsClosedOnStoredCIDR stores a rule with Set,
// corrupts its CIDR text in the row, past the checks of Set and of the
// service, and reads the allowlist. The read fails closed: a stored text that
// no parse can read must not come back as a rule of the list.
func TestAgentEgressAllowlistGetFailsClosedOnStoredCIDR(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, _ := openAgentStore(t, path)

	entry := agenttest.TestEntry(audit.ServerScope, "admin", "server:manage-settings")
	allow := egress.Allowlist{
		Hosts: []string{"models.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 443, HTTP: false}},
	}

	if err := s.AgentEgressAllowlist().Set(t.Context(), allow, entry); err != nil {
		t.Fatalf("Set() = %v, want nil", err)
	}

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	_, err = conn.ExecContext(
		t.Context(), "UPDATE agent_egress_allowlist_rules SET cidr = 'not-a-prefix'",
	)
	if err != nil {
		t.Fatalf("corrupt the stored rule: %v", err)
	}

	if _, err = s.AgentEgressAllowlist().Get(t.Context()); err == nil {
		t.Fatal("Get() = nil, want an error for the stored rule that does not parse")
	}
}
