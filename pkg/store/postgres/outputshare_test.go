package postgres_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// outputShareHarness returns the factory of the output sharing suite over
// fresh schemas of the database at base. The store serves the setting and no
// rule yet.
func outputShareHarness(base string) outputsharetest.Factory {
	return func(t *testing.T) outputsharetest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)
		s := openURL(t, schemaURL)

		return outputsharetest.Harness{
			Store: nil, Checker: nil, Attachments: nil, States: nil,
			Settings: s.OutputSharingSettings(),
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				return ensureNamespace(t, s, namespace.Name(name))
			},
			Unit: nil, UnitFault: nil, Authority: nil, Profile: nil, Attach: nil, SaveState: nil, Grant: nil,
			Job: nil, Disable: nil,
			Audited: func(t *testing.T, ns string) []string {
				t.Helper()

				return auditActionsOf(t, s, namespace.Name(ns))
			},
			Calls: nil, StoreRaw: nil,
			SettingRaw: func(t *testing.T, ns namespace.Name, value string) {
				t.Helper()

				if err := setSettingRow(t, schemaURL, ns, value, true); err != nil {
					t.Fatalf("plant the output sharing setting %q: %v", value, err)
				}
			},
		}
	}
}

// auditActionsOf returns the action of each audit entry of ns, oldest first.
func auditActionsOf(t *testing.T, s *postgres.Store, ns namespace.Name) []string {
	t.Helper()

	var actions []string
	for _, e := range allAuditEntries(t, s, ns) {
		actions = append(actions, e.Action)
	}

	return actions
}

// setSettingRow writes value as the output sharing setting of ns in the
// schema that schemaURL names, with direct SQL. With dropCheck, it first
// drops the CHECK of the column in that schema, as a row of another version
// could hold such a value. It returns the error of the write.
func setSettingRow(t *testing.T, schemaURL string, ns namespace.Name, value string, dropCheck bool) error {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if dropCheck {
		const drop = `ALTER TABLE namespaces DROP CONSTRAINT IF EXISTS namespaces_output_sharing_default_check`
		if _, execErr := conn.Exec(t.Context(), drop); execErr != nil {
			t.Fatalf("drop the check of the output sharing setting: %v", execErr)
		}
	}

	_, err = conn.Exec(t.Context(),
		`UPDATE namespaces SET output_sharing_default = $1 WHERE name = $2`, value, ns.String())

	return err
}

func TestOutputShareConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	outputsharetest.Run(t, outputShareHarness(base))
}

// TestOutputSharingDefaultColumnRefusesAnotherValue proves the layer of the
// table alone: the CHECK of the column refuses a value that is not a value of
// the setting, also when the store code is skipped.
func TestOutputSharingDefaultColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	ensureNamespace(t, s, "acme")

	for _, bad := range []string{"", "everyone", "Nobody", "namespace "} {
		err := setSettingRow(t, schemaURL, "acme", bad, false)
		if err == nil || !strings.Contains(err.Error(), "namespaces_output_sharing_default_check") {
			t.Fatalf("store the setting %q = %v, want the refusal of the CHECK of the column", bad, err)
		}
	}

	for _, good := range []string{"namespace", "nobody"} {
		if err := setSettingRow(t, schemaURL, "acme", good, false); err != nil {
			t.Fatalf("store the setting %q: %v", good, err)
		}
	}
}
