package backup_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
)

var errSwapAttempt = errors.New("fake swap failure before any change")

// settingBox is a setting value that a test changes while a restore runs.
type settingBox struct {
	mu   sync.Mutex
	text string
}

func (b *settingBox) set(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.text = text
}

func (b *settingBox) read(context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.text, nil
}

// decisionCase is one caller and one stored text of the decision table.
type decisionCase struct {
	name         string
	keep         bool
	read         func(context.Context) (string, error)
	wantKeepAcc  bool
	wantWidened  bool
	wantKeptKeys []string
}

// decisionsOf returns the decisions of the applies of the fake live store.
func decisionsOf(setup *restoreServiceSetup) []backup.SwapDecision {
	setup.live.mu.Lock()
	defer setup.live.mu.Unlock()

	return slices.Clone(setup.live.decisions)
}

// TestRestoreSwapDecisionByCaller is the hostile table of the service layer:
// the decision that the service gives the store, for each caller and each
// stored text. A caller without server:restore-access always keeps the live
// row of the setting. Only a caller with the action takes it from the backup.
func TestRestoreSwapDecisionByCaller(t *testing.T) {
	t.Parallel()

	keys := backup.KeptSettingKeys()
	failing := func(context.Context) (string, error) { return "true", errAccessSettingRead }

	cases := []decisionCase{
		{
			name: "no restore-access, setting on", keep: true, read: fixedSetting("true"),
			wantKeepAcc: false, wantWidened: true, wantKeptKeys: keys,
		},
		{
			name: "no restore-access, setting off", keep: true, read: fixedSetting("false"),
			wantKeepAcc: true, wantWidened: false, wantKeptKeys: keys,
		},
		{
			name: "no restore-access, unknown text", keep: true, read: fixedSetting("maybe"),
			wantKeepAcc: true, wantWidened: false, wantKeptKeys: keys,
		},
		{
			name: "no restore-access, read error", keep: true, read: failing,
			wantKeepAcc: true, wantWidened: false, wantKeptKeys: keys,
		},
		{
			name: "no restore-access, no reader", keep: true, read: nil,
			wantKeepAcc: true, wantWidened: false, wantKeptKeys: keys,
		},
		{
			name: "restore-access, setting on", keep: false, read: fixedSetting("true"),
			wantKeepAcc: false, wantWidened: false, wantKeptKeys: nil,
		},
		{
			name: "restore-access, setting off", keep: false, read: fixedSetting("false"),
			wantKeepAcc: false, wantWidened: false, wantKeptKeys: nil,
		},
	}

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			decisions := decisionsOf(runKeepRestore(t, tc.keep, tc.read))
			if len(decisions) != 1 {
				t.Fatalf("decisions = %d, want 1", len(decisions))
			}

			got := decisions[0]
			if got.KeepAccessTables != tc.wantKeepAcc || got.Widened != tc.wantWidened ||
				!slices.Equal(got.KeepSettingKeys, tc.wantKeptKeys) {
				t.Fatalf("decision = %+v, want keepAccess %v widened %v keys %v",
					got, tc.wantKeepAcc, tc.wantWidened, tc.wantKeptKeys)
			}
		})
	}
}

// settingChangeEntries returns the appended audit entries that hold changes.
func settingChangeEntries(setup *restoreServiceSetup) []audit.Entry {
	var found []audit.Entry

	appends := setup.live.devSnapshot().appends
	for i := range appends {
		if len(appends[i].Changes) > 0 {
			found = append(found, appends[i])
		}
	}

	return found
}

// checkSettingChange fails the test unless entries is one change entry of the
// restore access setting from before to next.
func checkSettingChange(t *testing.T, entries []audit.Entry, before, next string) {
	t.Helper()

	if len(entries) != 1 || entries[0].Action != string(identity.ActionServerManageSettings) {
		t.Fatalf("setting-change entries = %+v, want one with the action server:manage-settings", entries)
	}

	change := entries[0].Changes[0]
	if change.Key != backup.SettingRestoreAccessTables || change.Old == nil || *change.Old != before ||
		change.New != next {
		t.Fatalf("change = %+v, want %s from %q to %q", change, backup.SettingRestoreAccessTables, before, next)
	}
}

// TestRestoreSettingChangeEntry proves that a restore by a caller with
// server:restore-access, which takes the value of the setting from the backup,
// writes a setting-change entry with the old and the new value, and that a
// restore that keeps the live row or changes no value writes none.
func TestRestoreSettingChangeEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		keep         bool
		before, next string
		want         bool
	}{
		{name: "restore-access, value changes", keep: false, before: "false", next: "true", want: true},
		{name: "restore-access, same value", keep: false, before: "false", next: "false", want: false},
		{name: "no restore-access, live row kept", keep: true, before: "false", next: "false", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			box := &settingBox{mu: sync.Mutex{}, text: tc.before}
			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AccessSetting = box.read })
			// The swap takes the value of the backup.
			setup.live.swapHook = func(int) error {
				box.set(tc.next)

				return nil
			}

			runStartedRestore(t, setup, tc.keep)

			found := settingChangeEntries(setup)
			if tc.want {
				checkSettingChange(t, found, tc.before, tc.next)
			} else if len(found) != 0 {
				t.Fatalf("setting-change entries = %+v, want none", found)
			}
		})
	}
}

// ownRestoreEntries returns the objects of the restore entries that name what
// the swap did to the access tables.
func ownRestoreEntries(setup *restoreServiceSetup) []string {
	var own []string

	appends := setup.live.devSnapshot().appends
	for i := range appends {
		if strings.Contains(appends[i].Object, "(access tables") {
			own = append(own, appends[i].Object)
		}
	}

	return own
}

// checkOwnEntry fails the test unless own is one restore entry that holds has
// and does not hold hasNot.
func checkOwnEntry(t *testing.T, own []string, has, hasNot string) {
	t.Helper()

	if len(own) != 1 {
		t.Fatalf("restore entries = %v, want one", own)
	}

	if !strings.Contains(own[0], has) || strings.Contains(own[0], hasNot) {
		t.Fatalf("restore entry = %q, want it to hold %q and not %q", own[0], has, hasNot)
	}
}

// TestRestoreDecisionSurvivesStepRetry proves that the decision and the record
// agree when the setting changes between two attempts of the swap step: the
// first attempt fails before it changes anything, the setting changes, and the
// second attempt decides again. The swap records its decision, and the audit
// text comes from that record, so there is one entry and it names the action
// that happened.
func TestRestoreDecisionSurvivesStepRetry(t *testing.T) {
	t.Parallel()

	t.Run("turned off between attempts", func(t *testing.T) {
		t.Parallel()
		checkRetryDecision(t, "true", "false")
	})
	t.Run("turned on between attempts", func(t *testing.T) {
		t.Parallel()
		checkRetryDecision(t, "false", "true")
	})
}

// checkRetryDecision runs a restore of a caller without server:restore-access.
// The setting holds first at the first attempt of the swap, and that attempt
// fails and sets later. The check proves two attempts, one applied decision
// that follows later, and one restore entry that names that decision.
func checkRetryDecision(t *testing.T, first, later string) {
	t.Helper()

	wantWidened := later == "true"

	box := &settingBox{mu: sync.Mutex{}, text: first}
	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AccessSetting = box.read })
	setup.live.swapHook = func(attempt int) error {
		if attempt > 1 {
			return nil
		}

		box.set(later)

		return errSwapAttempt
	}

	runStartedRestore(t, setup, true)

	setup.live.mu.Lock()
	attempts := setup.live.swapAttempts
	setup.live.mu.Unlock()

	decisions := decisionsOf(setup)
	if attempts != 2 || len(decisions) != 1 {
		t.Fatalf("attempts = %d, applied decisions = %d, want 2 and 1", attempts, len(decisions))
	}

	if decisions[0].Widened != wantWidened || decisions[0].KeepAccessTables == wantWidened {
		t.Fatalf("decision = %+v, want widened %v from the value at the second attempt", decisions[0], wantWidened)
	}

	widenedText := "because " + backup.SettingRestoreAccessTables + " is on"
	if wantWidened {
		checkOwnEntry(t, ownRestoreEntries(setup), widenedText, "access tables kept")
	} else {
		checkOwnEntry(t, ownRestoreEntries(setup), "access tables kept", widenedText)
	}
}

// runStartedRestore starts a restore of setup and waits for its success.
func runStartedRestore(t *testing.T, setup *restoreServiceSetup, keep bool) {
	t.Helper()

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
}
