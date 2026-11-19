package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

var errAccessSettingRead = errors.New("fake setting read failure")

// runKeepRestore runs a restore of a caller without server:restore-access
// (KeepAccessTables true) against a service with the setting reader read, and
// returns the setup after the restore succeeded.
func runKeepRestore(
	t *testing.T, keep bool, read func(context.Context) (string, error),
) *restoreServiceSetup {
	t.Helper()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AccessSetting = read })
	id := backup.MintRestoreID()

	err := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID: setup.id, PreBackupID: "", CheckOnly: false, Force: false,
		RequestedBy: "tester", KeepAccessTables: keep,
	})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
	}

	return setup
}

func fixedSetting(text string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return text, nil }
}

// TestRestoreAccessSettingDefaultOff proves that the setting is off by
// default: the declaration says false, and a restore with no reader, an empty
// value or false keeps the access tables.
func TestRestoreAccessSettingDefaultOff(t *testing.T) {
	t.Parallel()

	decls := backup.RestoreAccessSettingDeclarations()
	if len(decls) != 1 || decls[0].Key != backup.SettingRestoreAccessTables || decls[0].Default != "false" {
		t.Fatalf("declarations = %+v, want one key with default false", decls)
	}

	if !decls[0].Database {
		t.Error("the setting is not a database setting")
	}

	cases := map[string]func(context.Context) (string, error){
		"no reader": nil, "empty": fixedSetting(""), "false": fixedSetting("false"),
	}

	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			setup := runKeepRestore(t, true, read)
			if got := setup.live.counts(); got.kept != 1 {
				t.Fatalf("kept = %d, want 1: the access tables stay", got.kept)
			}
		})
	}
}

// TestRestoreAccessSettingOnReplaces proves that a restore of a caller
// without server:restore-access replaces the access tables when the setting
// is on, and that the audit entry names the setting.
func TestRestoreAccessSettingOnReplaces(t *testing.T) {
	t.Parallel()

	setup := runKeepRestore(t, true, fixedSetting("true"))

	if got := setup.live.counts(); got.kept != 0 || got.applied != 1 {
		t.Fatalf("kept = %d, applied = %d, want a replacing apply", got.kept, got.applied)
	}

	objects := auditObjects(setup.live.devSnapshot().appends)
	want := "replaced because " + backup.SettingRestoreAccessTables + " is on and the caller lacks server:restore-access"
	if !strings.Contains(objects, want) {
		t.Fatalf("no restore entry names the widened path: %v", objects)
	}
}

// TestRestoreAccessSettingFailsClosed proves that unknown text and a failed
// read keep the access tables, even though the setting may be on.
func TestRestoreAccessSettingFailsClosed(t *testing.T) {
	t.Parallel()

	cases := map[string]func(context.Context) (string, error){
		"unknown text": fixedSetting("maybe"),
		"upper case":   fixedSetting("TRUE"),
		"sql text":     fixedSetting("true'; DROP TABLE grants;--"),
		"read error": func(context.Context) (string, error) {
			return "true", errAccessSettingRead
		},
	}

	for name, read := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			setup := runKeepRestore(t, true, read)
			if got := setup.live.counts(); got.kept != 1 {
				t.Fatalf("kept = %d, want 1: the access tables stay", got.kept)
			}
		})
	}
}

// TestRestoreAccessSettingChecks proves the check of the declaration and the
// rule for a weaker change.
func TestRestoreAccessSettingChecks(t *testing.T) {
	t.Parallel()

	decl := backup.RestoreAccessSettingDeclarations()[0]

	for _, bad := range []string{"maybe", "1", "TRUE", "true ", "x'; --"} {
		if err := decl.Check(bad); err == nil {
			t.Errorf("Check(%q) = nil, want an error", bad)
		}
	}

	for _, good := range []string{"true", "false"} {
		if err := decl.Check(good); err != nil {
			t.Errorf("Check(%q) = %v, want nil", good, err)
		}
	}

	if !decl.ReducesSecurity("false", "true") || decl.ReducesSecurity("true", "false") ||
		decl.ReducesSecurity("true", "true") || !decl.ReducesSecurity("maybe", "false") {
		t.Error("ReducesSecurity does not follow: only a change to true, or from a value that is not valid, is weaker")
	}
}
