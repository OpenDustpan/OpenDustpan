package backup

import (
	"errors"
	"strings"
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
		noEntry("an old value with a line break", takenRecord("true\r\nforged line", "true", false)),
		noEntry("a new value with a line break", takenRecord("false", "true\r\nforged line", false)),
		noEntry("an old value of 1 MiB", takenRecord(strings.Repeat("A", 1<<20), "true", false)),
		noEntry("a new value of 1 MiB", takenRecord("false", strings.Repeat("A", 1<<20), false)),
		noEntry("an old value that is not lower case", takenRecord("TRUE", "true", false)),
		noEntry("a new value that is not lower case", takenRecord("false", "TRUE", false)),
		noEntry("an old value that is the unknown marker", takenRecord(SettingValueUnknown, "true", false)),
		noEntry("a new value that is the unknown marker", takenRecord("false", SettingValueUnknown, false)),
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

// hostileTexts are the texts that a step record, which a restore of another
// version or a person can change, must never put in an audit entry or a log
// line: a line break, 1 MiB, a value that is not lower case, and the marker of
// an unknown value.
func hostileTexts() map[string]string {
	return map[string]string{
		"a line break":       "true\r\nforged line",
		"1 MiB":              strings.Repeat("A", 1<<20),
		"not lower case":     "TRUE",
		"the unknown marker": SettingValueUnknown,
	}
}

// TestSettingChangeEntryHostileDecisions sends a decision with hostile text
// straight to settingChangeEntry, as if the decoder and the store did not
// check it. Only the check of the service layer decides: it must fail closed
// and write no entry. The test fails when that check is removed.
func TestSettingChangeEntryHostileDecisions(t *testing.T) {
	t.Parallel()

	svc := new(RestoreService)
	svc.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }

	for name, text := range hostileTexts() {
		for _, field := range []string{"old", "new"} {
			t.Run(field+" value with "+name, func(t *testing.T) {
				t.Parallel()

				d := SwapDecision{
					KeepAccessTables: false, Widened: false, KeepSettingKeys: nil,
					SettingBefore: "false", SettingAfter: "true", SettingStatic: false,
				}

				if field == "old" {
					d.SettingBefore = text
				} else {
					d.SettingAfter = text
				}

				var in restoreInput

				if entry, ok := svc.settingChangeEntry(d, in); ok {
					t.Fatalf(
						"settingChangeEntry wrote %d bytes of entry for a hostile %s value",
						len(entry.Object),
						field,
					)
				}
			})
		}
	}
}

// TestSettingModeTextHostileDecisions sends a decision with hostile text
// straight to settingModeText. The text of the restore entry and of the log
// line must hold none of it. The test fails when the check is removed.
func TestSettingModeTextHostileDecisions(t *testing.T) {
	t.Parallel()

	for name, text := range hostileTexts() {
		for _, field := range []string{"old", "new"} {
			t.Run(field+" value with "+name, func(t *testing.T) {
				t.Parallel()

				d := SwapDecision{
					KeepAccessTables: false, Widened: false, KeepSettingKeys: nil,
					SettingBefore: "false", SettingAfter: "true", SettingStatic: false,
				}

				if field == "old" {
					d.SettingBefore = text
				} else {
					d.SettingAfter = text
				}

				got := settingModeText(d)
				if strings.ContainsAny(got, "\r\n") || len(got) > 512 || strings.Contains(got, text) {
					t.Fatalf("settingModeText holds the hostile %s value: %d bytes", field, len(got))
				}
			})
		}
	}
}

// TestDecodeRestoreSwapDecisionHostileTexts proves the decoder, alone, refuses
// a record with hostile text in either value of the setting, and accepts the
// known texts.
func TestDecodeRestoreSwapDecisionHostileTexts(t *testing.T) {
	t.Parallel()

	for name, text := range hostileTexts() {
		for _, record := range [][]byte{takenRecord(text, "true", false), takenRecord("false", text, false)} {
			if _, err := DecodeRestoreSwapDecision(record); !errors.Is(err, ErrCorrupt) {
				t.Errorf("decode of a record with %s = %v, want ErrCorrupt", name, err)
			}

			if _, err := DecodeRestoreSwap(record); !errors.Is(err, ErrCorrupt) {
				t.Errorf("decode of the counts of a record with %s = %v, want ErrCorrupt", name, err)
			}
		}
	}

	for _, text := range []string{"", "true", "false"} {
		if _, err := DecodeRestoreSwapDecision(takenRecord(text, text, false)); err != nil {
			t.Errorf("decode of a record with %q = %v, want nil", text, err)
		}
	}
}
