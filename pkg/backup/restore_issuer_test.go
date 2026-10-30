package backup_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreDropsIssuerKeysOfAnotherInstallation proves that a restore stages
// the signing keys of the OIDC issuers only from a backup of the installation
// that restores it. A server with another installation name, and a server
// with none, cannot say that the backup is its own, so they drop the keys.
func TestRestoreDropsIssuerKeysOfAnotherInstallation(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		installation string
		drop         bool
	}{
		"the installation that made the backup": {"eu-west", false},
		"another installation":                  {"us-east", true},
		"an installation with no name":          {"", true},
		"a name that only starts the same":      {"eu-west-2", true},
		"another case":                          {"EU-WEST", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetupWith(
				t,
				func(cfg *backup.RestoreConfig) { cfg.Installation = tc.installation },
			)

			id, err := startRestoreOf(t, setup, plainRestore())
			if err != nil {
				t.Fatalf("start: %v", err)
			}

			waitRestoreEnd(t, setup.regs, id)

			setup.live.mu.Lock()
			staged := append([]backup.RestoreStageInput(nil), setup.live.staged...)
			setup.live.mu.Unlock()

			if len(staged) == 0 {
				t.Fatal("the restore staged nothing")
			}

			for _, in := range staged {
				if in.DropIssuerKeys != tc.drop {
					t.Errorf("DropIssuerKeys = %v, want %v", in.DropIssuerKeys, tc.drop)
				}
			}
		})
	}
}
