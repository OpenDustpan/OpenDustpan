package backup_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
			setup.live.backupSetting = tc.next
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

// afterSwapReader returns a reader of the setting that answers before at its
// first call, the read of the decision, and then runs later: the reads after
// the swap. A change entry that does not depend on a later read gives the
// same entry whatever later does.
func afterSwapReader(before string, later func() (string, error)) func(context.Context) (string, error) {
	var calls atomic.Int32

	return func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return before, nil
		}

		return later()
	}
}

// TestRestoreSettingChangeEntryComesFromTheSwapRecord proves that the entry of
// a taken value comes from the record of the swap and not from a read after
// the swap: a read error, a change by a patch between the swap and the
// after-apply step, and a value set back to the old one leave the entry that
// the record gives.
func TestRestoreSettingChangeEntryComesFromTheSwapRecord(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		later func() (string, error)
	}{
		{name: "a read error after the swap", later: func() (string, error) { return "", errAccessSettingRead }},
		{name: "a patch changes the value after the swap", later: func() (string, error) { return "maybe", nil }},
		{name: "a patch sets the value back to the old one", later: func() (string, error) { return "false", nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := afterSwapReader("false", tc.later)
			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AccessSetting = read })
			setup.live.backupSetting = "true"

			runStartedRestore(t, setup, false)

			checkSettingChange(t, settingChangeEntries(setup), "false", "true")
		})
	}
}

// TestRestoreSettingChangeEntryNeedsAnEffectiveChange proves that a restore
// writes no setting-change entry when the static layer sets the key: the row
// that the swap took does not change the value that the installation uses. The
// same restore without the static layer writes the entry.
func TestRestoreSettingChangeEntryNeedsAnEffectiveChange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		static  func(context.Context) (bool, error)
		entries int
	}{
		{name: "the static layer sets the key", static: func(context.Context) (bool, error) { return true, nil }, entries: 0},
		{name: "the static layer does not set the key", static: func(context.Context) (bool, error) { return false, nil }, entries: 1},
		{name: "the read of the static layer fails", static: func(context.Context) (bool, error) {
			return true, errAccessSettingRead
		}, entries: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := func(context.Context) (string, error) { return "false", nil }
			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
				cfg.AccessSetting, cfg.AccessSettingStatic = read, tc.static
			})
			setup.live.backupSetting = "true"

			runStartedRestore(t, setup, false)

			if got := len(settingChangeEntries(setup)); got != tc.entries {
				t.Fatalf("setting-change entries = %d, want %d", got, tc.entries)
			}
		})
	}
}

// TestRestoreSettingChangeEntryWhenTheLiveValueWasNotRead is the hostile table
// of the setting-change entry when the restore did not read the live value
// before the swap (a read error, or no reader). A restore by a caller with
// server:restore-access that takes a new value from the backup writes one
// entry that names the new value and marks the old value unknown. Each other
// guard of the entry still holds when the old value is unknown: a kept live
// row, the static layer, and a backup with no value write none.
func TestRestoreSettingChangeEntryWhenTheLiveValueWasNotRead(t *testing.T) {
	t.Parallel()

	failing := func(context.Context) (string, error) { return "", errAccessSettingRead }
	static := func(context.Context) (bool, error) { return true, nil }

	for _, tc := range []struct {
		name   string
		read   func(context.Context) (string, error)
		static func(context.Context) (bool, error)
		keep   bool
		next   string
		want   bool
	}{
		{name: "a read error, new value on", read: failing, static: nil, keep: false, next: "true", want: true},
		{name: "a read error, new value off", read: failing, static: nil, keep: false, next: "false", want: true},
		{name: "no reader, new value on", read: nil, static: nil, keep: false, next: "true", want: true},
		{name: "a read error, live row kept", read: failing, static: nil, keep: true, next: "true", want: false},
		{name: "a read error, the static layer sets the key", read: failing, static: static, keep: false, next: "true", want: false},
		{name: "a read error, the backup has no value", read: failing, static: nil, keep: false, next: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
				cfg.AccessSetting, cfg.AccessSettingStatic = tc.read, tc.static
			})
			setup.live.backupSetting = tc.next

			runStartedRestore(t, setup, tc.keep)

			found := settingChangeEntries(setup)
			if tc.want {
				checkSettingChange(t, found, backup.SettingValueUnknown, tc.next)
			} else if len(found) != 0 {
				t.Fatalf("setting-change entries = %+v, want none", found)
			}
		})
	}
}

// restoreEntryObjects returns the object text of each restore entry that the
// restore appended.
func restoreEntryObjects(setup *restoreServiceSetup) []string {
	var found []string

	appends := setup.live.devSnapshot().appends
	for i := range appends {
		if appends[i].Action == string(identity.ActionServerRestore) {
			found = append(found, appends[i].Object)
		}
	}

	return found
}

// TestRestoreSettingChangeEntryRecordsTheEffectiveLiveValue is the hostile
// table of the live value that the reader gives. The decision records the
// effective value, so an empty text, a text that the declaration refuses, one
// with a line break and one of 1 MiB all give the old value false in the change
// entry, and the restore still writes its own entry, which holds no line break.
// An empty text from a reader that answers without an error is a value that
// was read, not an unknown one. The test fails when the source of the decision
// records the raw text.
func TestRestoreSettingChangeEntryRecordsTheEffectiveLiveValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "an empty text", text: ""},
		{name: "a text that is not lower case", text: "TRUE"},
		{name: "a text with a line break", text: "true\r\nforged line"},
		{name: "a text of 1 MiB", text: strings.Repeat("A", 1<<20)},
		{name: "the unknown marker", text: backup.SettingValueUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := fixedSetting(tc.text)
			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) { cfg.AccessSetting = read })
			setup.live.backupSetting = "true"

			runStartedRestore(t, setup, false)

			checkSettingChange(t, settingChangeEntries(setup), "false", "true")

			objects := restoreEntryObjects(setup)
			if len(objects) == 0 {
				t.Fatal("the restore wrote no restore entry")
			}

			for _, object := range objects {
				if strings.ContainsAny(object, "\r\n") || len(object) > 2048 {
					t.Fatalf("the restore entry holds raw text: %d bytes", len(object))
				}
			}
		})
	}
}

// TestRestoreSwapRecordHostileDecode proves the decoder of the swap record
// refuses a record that shows a swap that took the restore setting while it
// kept or widened the access tables, and a record that is both kept and
// widened. Each case sends the bytes to the decoder alone.
func TestRestoreSwapRecordHostileDecode(t *testing.T) {
	t.Parallel()

	const (
		head = `{"expiredUpdateLeases":0,`
		tail = `"settingBefore":"false","settingAfter":""}`
	)

	for _, tc := range []struct {
		name, record string
	}{
		{"kept and widened", head + `"keepAccessTables":true,"widened":true,"keepSettingKeys":["restore.replace-access-tables"],` + tail},
		{"kept without the setting key", head + `"keepAccessTables":true,"widened":false,"keepSettingKeys":[],` + tail},
		{"widened without the setting key", head + `"keepAccessTables":false,"widened":true,"keepSettingKeys":[],` + tail},
		{"kept with another key", head + `"keepAccessTables":true,"widened":false,"keepSettingKeys":["other"],` + tail},
		{"kept with a null key list", head + `"keepAccessTables":true,"widened":false,"keepSettingKeys":null,` + tail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := backup.DecodeRestoreSwapDecision([]byte(tc.record)); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("decode %s = %v, want ErrCorrupt", tc.name, err)
			}

			if _, err := backup.DecodeRestoreSwap([]byte(tc.record)); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("decode kept result of %s = %v, want ErrCorrupt", tc.name, err)
			}
		})
	}

	for _, ok := range []string{
		head + `"keepAccessTables":false,"widened":false,"keepSettingKeys":[],` + tail,
		head + `"keepAccessTables":true,"widened":false,"keepSettingKeys":["restore.replace-access-tables"],` + tail,
		head + `"keepAccessTables":false,"widened":true,"keepSettingKeys":["restore.replace-access-tables"],` + tail,
	} {
		if _, err := backup.DecodeRestoreSwapDecision([]byte(ok)); err != nil {
			t.Fatalf("decode %s = %v, want nil", ok, err)
		}
	}
}

// decisionOf returns a decision with the three fields that the rule checks.
func decisionOf(keepAccess, widened bool, keys []string) backup.SwapDecision {
	return backup.SwapDecision{
		KeepAccessTables: keepAccess, Widened: widened, KeepSettingKeys: keys, SettingBefore: "", SettingAfter: "",
		SettingStatic: false,
	}
}

// TestValidateSwapDecisionHostile proves the check of the store layer: it
// refuses a decision that is both kept and widened, and one that keeps or
// widens the access tables without the live row of each kept setting key.
func TestValidateSwapDecisionHostile(t *testing.T) {
	t.Parallel()

	keys := backup.KeptSettingKeys()

	for _, tc := range []struct {
		name string
		d    backup.SwapDecision
		ok   bool
	}{
		{"plain replace", backup.PlainSwap(false), true},
		{"plain keep", backup.PlainSwap(true), true},
		{"widened with the key", decisionOf(false, true, keys), true},
		{"kept and widened", decisionOf(true, true, keys), false},
		{"kept without the key", decisionOf(true, false, nil), false},
		{"widened without the key", decisionOf(false, true, nil), false},
		{"widened with another key", decisionOf(false, true, []string{"x"}), false},
		{"widened with a duplicate key", decisionOf(false, true, append(slices.Clone(keys), keys...)), true},
		{"widened with the key in another case", decisionOf(false, true, []string{"Restore.Replace-Access-Tables"}), false},
		{"kept with an empty list", decisionOf(true, false, []string{}), false},
		{"widened with an empty list", decisionOf(false, true, []string{}), false},
	} {
		err := backup.ValidateSwapDecision(tc.d)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("%s: error = %v, want ok %v", tc.name, err, tc.ok)
		}
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
