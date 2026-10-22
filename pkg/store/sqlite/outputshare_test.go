package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// openOutputShare returns a fresh store and the path of its database.
func openOutputShare(t *testing.T) (*sqlite.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s, path
}

// outputShareHarness returns the harness of the output sharing suite over a
// fresh store. The store serves the setting and no rule yet.
func outputShareHarness(t *testing.T) outputsharetest.Harness {
	t.Helper()

	s, path := openOutputShare(t)

	return outputsharetest.Harness{
		Store: nil, Checker: nil, Attachments: nil, States: nil,
		Settings: s.OutputSharingSettings(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Unit: nil, UnitFault: nil, Authority: nil, Profile: nil, Attach: nil, SaveState: nil, Grant: nil, Job: nil,
		Disable: nil,
		Audited: func(t *testing.T, ns string) []string {
			t.Helper()

			var actions []string
			for _, e := range allAuditEntries(t, s, namespace.Name(ns)) {
				actions = append(actions, e.Action)
			}

			return actions
		},
		Calls: nil, StoreRaw: nil,
		SettingRaw: func(t *testing.T, ns namespace.Name, value string) {
			t.Helper()

			setSettingRow(t, path, ns, value, true)
		},
	}
}

// setSettingRow writes value as the output sharing setting of ns with direct
// SQL. With ignoreCheck, the connection turns the CHECK constraints off, as
// a row of another version could hold such a value. It returns the error of
// the write.
func setSettingRow(t *testing.T, path string, ns namespace.Name, value string, ignoreCheck bool) error {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	// A pragma holds for one connection only.
	conn.SetMaxOpenConns(1)

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	if ignoreCheck {
		if _, execErr := conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); execErr != nil {
			t.Fatalf("ignore check constraints: %v", execErr)
		}
	}

	_, err = conn.ExecContext(t.Context(),
		`UPDATE namespaces SET output_sharing_default = ? WHERE name = ?`, value, ns.String())
	if ignoreCheck && err != nil {
		t.Fatalf("plant the output sharing setting %q: %v", value, err)
	}

	return err
}

func TestOutputShareConformance(t *testing.T) {
	t.Parallel()

	outputsharetest.Run(t, outputShareHarness)
}

// TestOutputSharingDefaultColumnRefusesAnotherValue proves the layer of the
// table alone: the CHECK of the column refuses a value that is not a value of
// the setting, also when the store code is skipped.
func TestOutputSharingDefaultColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	s, path := openOutputShare(t)
	ensureNamespace(t, s, "acme")

	for _, bad := range []string{"", "everyone", "Nobody", "namespace "} {
		err := setSettingRow(t, path, "acme", bad, false)
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("store the setting %q = %v, want the refusal of the CHECK of the column", bad, err)
		}
	}

	for _, good := range []string{"namespace", "nobody"} {
		if err := setSettingRow(t, path, "acme", good, false); err != nil {
			t.Fatalf("store the setting %q: %v", good, err)
		}
	}
}
