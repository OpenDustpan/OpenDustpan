package backup

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// entryCase is one record of the swap step for settingAuditEntry. A case with
// an empty want must write no entry. A case with a want must write one entry
// with the old and the new value.
type entryCase struct {
	name    string
	result  []byte
	wantOld string
	want    string
}

func noEntry(name string, result []byte) entryCase {
	return entryCase{name: name, result: result, wantOld: "", want: ""}
}

func oneEntry(name string, result []byte, wantOld, want string) entryCase {
	return entryCase{name: name, result: result, wantOld: wantOld, want: want}
}

// takenRecord returns the record of a swap that took the row of the setting
// from the backup, with the live value before and the value after.
func takenRecord(before, after string, static bool) []byte {
	return EncodeRestoreSwap(KeepAccessResult{DroppedTempAdmins: 0, ExpiredUpdateLeases: 0}, SwapDecision{
		KeepAccessTables: false, Widened: false, KeepSettingKeys: nil,
		SettingBefore: before, SettingAfter: after, SettingStatic: static,
	})
}

// TestSettingAuditEntryHostileRecords sends a record of the swap step straight
// to settingAuditEntry, so that only its own guards decide. Each case that
// must write no entry fails when the guard for it is removed. The cases with
// an unknown old value write one entry that names the new value and marks the
// old value unknown.
func TestSettingAuditEntryHostileRecords(t *testing.T) {
	t.Parallel()

	const (
		keptRecord = `{"droppedTempAdmins":0,"expiredUpdateLeases":0,"keepAccessTables":true,"widened":false,` +
			`"keepSettingKeys":["restore.replace-access-tables"],"settingBefore":"false","settingAfter":"true",` +
			`"settingStatic":false}`
		corrupt = `{"keepAccessTables":"yes"`
	)

	for _, tc := range []entryCase{
		noEntry("a record that does not decode", []byte(corrupt)),
		noEntry("a kept row with a new value in the record", []byte(keptRecord)),
		noEntry("the static layer sets the key", takenRecord("false", "true", true)),
		noEntry("the static layer sets the key and the old value is unknown", takenRecord("", "true", true)),
		noEntry("the backup has no value", takenRecord("false", "", false)),
		noEntry("the same value", takenRecord("false", "false", false)),
		oneEntry("the old value is unknown, new value on", takenRecord("", "true", false), SettingValueUnknown, "true"),
		oneEntry("the old value is unknown, new value off", takenRecord("", "false", false), SettingValueUnknown, "false"),
		oneEntry("the old value is known", takenRecord("false", "true", false), "false", "true"),
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkEntryCase(t, &tc)
		})
	}
}

// TestSettingAuditEntryWithoutSwapRecord proves that an operation that has not
// recorded the swap step writes no entry.
func TestSettingAuditEntryWithoutSwapRecord(t *testing.T) {
	t.Parallel()

	svc := new(RestoreService)

	var (
		op durable.Operation
		in restoreInput
	)

	if entry, ok := svc.settingAuditEntry(op, in); ok {
		t.Fatalf("settingAuditEntry wrote %+v, want no entry", entry)
	}
}

// checkEntryCase runs settingAuditEntry on the record of tc and compares the
// answer with the case.
func checkEntryCase(t *testing.T, tc *entryCase) {
	t.Helper()

	svc := new(RestoreService)
	svc.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }

	var op durable.Operation

	op.Steps = []durable.StepRecord{{Name: RestoreStepSwap, Result: tc.result, DoneAt: time.Time{}}}

	var in restoreInput

	in.RequestedBy = "carol"

	entry, ok := svc.settingAuditEntry(op, in)
	if tc.want == "" {
		if ok {
			t.Fatalf("settingAuditEntry wrote %+v, want no entry", entry)
		}

		return
	}

	if !ok || len(entry.Changes) != 1 {
		t.Fatalf("settingAuditEntry = %+v, %v; want one change", entry, ok)
	}

	change := entry.Changes[0]
	if change.Key != SettingRestoreAccessTables || change.Old == nil || *change.Old != tc.wantOld ||
		change.New != tc.want {
		t.Fatalf("change = %+v, want old %q new %q", change, tc.wantOld, tc.want)
	}
}

// TestSettingModeTextNamesTheNewValue proves that the text of the restore
// entry names the value that the swap took from the backup, and the live value
// before the swap when it was read.
func TestSettingModeTextNamesTheNewValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, before, after, want string
		static                    bool
	}{
		{"read before, taken", "false", "true", "taken from the backup (the live value was false before the swap, the new value is true)", false},
		{"not read, taken", "", "true", "taken from the backup (the live value was not read before the swap, the new value is true)", false},
		{"static layer", "false", "true", "taken from the backup (the live value was false before the swap, the new value is true); the static layer sets the effective value", true},
	} {
		d := SwapDecision{
			KeepAccessTables: false, Widened: false, KeepSettingKeys: nil,
			SettingBefore: tc.before, SettingAfter: tc.after, SettingStatic: tc.static,
		}

		if got := settingModeText(d); got != tc.want {
			t.Errorf("%s: settingModeText = %q, want %q", tc.name, got, tc.want)
		}
	}
}
