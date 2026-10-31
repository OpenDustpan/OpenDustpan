package backup_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreDecidesTheIssuerKeysForEachNamespace proves that a restore hands
// its own installation name to the drop of the issuer keys, so the store
// decides for each namespace, and not once for the whole backup. A server with
// no installation name hands the empty name, which keeps no namespace.
func TestRestoreDecidesTheIssuerKeysForEachNamespace(t *testing.T) {
	t.Parallel()

	for name, installation := range map[string]string{
		"the installation that made the backup": "eu-west",
		"another installation":                  "us-east",
		"an installation with no name":          "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkDropDecision(t, installation)
		})
	}
}

// checkDropDecision runs a restore on the installation and checks the name that
// the drop got and the event of each dropped namespace.
func checkDropDecision(t *testing.T, installation string) {
	t.Helper()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.Installation = installation })

	setup.live.mu.Lock()
	setup.live.dropReport = []backup.IssuerKeysDropped{{Namespace: "acme", Keys: 3}, {Namespace: "globex", Keys: 2}}
	setup.live.mu.Unlock()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	waitRestoreEnd(t, setup.regs, id)

	setup.live.mu.Lock()
	keeps := append([]string(nil), setup.live.keeps...)
	events := setup.live.events[id]
	setup.live.mu.Unlock()

	if len(keeps) == 0 {
		t.Fatal("the restore never dropped the issuer keys of other installations")
	}

	for _, keep := range keeps {
		if keep != installation {
			t.Errorf("the drop got the installation %q, want %q", keep, installation)
		}
	}

	// Each dropped namespace has its own event.
	for _, ns := range []string{"acme", "globex"} {
		if !strings.Contains(events, "OIDC issuer of namespace "+ns+" ") {
			t.Errorf("no event for namespace %s in %q", ns, events)
		}
	}
}

// TestRestoreRecordsNoDropWhenNothingWasDropped proves that a backup with no
// issuer key of another installation leaves no event.
func TestRestoreRecordsNoDropWhenNothingWasDropped(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.Installation = "eu-west" })

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	waitRestoreEnd(t, setup.regs, id)

	setup.live.mu.Lock()
	events := setup.live.events[id]
	setup.live.mu.Unlock()

	if strings.Contains(events, "OIDC issuer") {
		t.Errorf("events = %q, want no event about the issuer", events)
	}
}
