package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingRestoreAccessTables is the key of the installation setting that lets
// each holder of server:restore replace the access tables in a restore. The
// default is off. Only the settings API writes it, and a change needs the
// actions server:manage-settings and server:restore-access, with a fresh
// authentication.
const SettingRestoreAccessTables = "restore.replace-access-tables"

// RestoreAccessSettingDefault is the effective text of the restore access
// setting when no row stores it. A store records it as the value after a swap
// whose restored settings hold no row for the key.
const RestoreAccessSettingDefault = "false"

// restoreAccessSettingOn is the text of the restore access setting that turns
// it on.
const restoreAccessSettingOn = "true"

// ErrRestoreAccessSetting reports a stored value of the restore access setting
// that is not true or false.
var ErrRestoreAccessSetting = errors.New("invalid restore access setting")

// restoreAccessSetting is the value of the restore access setting. Only
// newRestoreAccessSetting makes one with the value on.
type restoreAccessSetting struct {
	on bool
}

// newRestoreAccessSetting maps text to a value: empty and "false" mean off,
// "true" means on. Any other text returns ErrRestoreAccessSetting, and the
// caller then keeps the access tables.
func newRestoreAccessSetting(text string) (restoreAccessSetting, error) {
	switch text {
	case "", "false":
		return restoreAccessSetting{on: false}, nil
	case restoreAccessSettingOn:
		return restoreAccessSetting{on: true}, nil
	default:
		return restoreAccessSetting{
			on: false,
		}, fmt.Errorf(
			"%w: the value is not true or false",
			ErrRestoreAccessSetting,
		)
	}
}

// EffectiveRestoreAccessSettingText returns the text that the settings
// resource shows as the effective value of the restore access setting for the
// stored text: "true" and "false" as they are, empty as "false", and
// RestoreAccessSettingDefault for any other text. The settings resource treats
// a stored value that the declaration refuses as no value, so a store records
// this text, not the raw row, as the value after a swap.
func EffectiveRestoreAccessSettingText(text string) string {
	value, err := newRestoreAccessSetting(text)
	if err != nil {
		return RestoreAccessSettingDefault
	}

	if value.on {
		return restoreAccessSettingOn
	}

	return RestoreAccessSettingDefault
}

// KnownSettingText reports whether text is one of the texts that a swap
// decision records for the restore access setting: "" (no value), "true" or
// "false". Every other text, for example one with a line break, a long text,
// "TRUE" or the marker SettingValueUnknown, is not a value that a swap records.
func KnownSettingText(text string) bool {
	return text == "" || text == restoreAccessSettingOn || text == RestoreAccessSettingDefault
}

// RestoreAccessSettingDeclarations returns the declaration of the restore
// access setting for the settings resource of the installation. The database
// layer holds it, and no flag, variable or file sets it. A change to true is
// weaker, so it needs a fresh authentication.
func RestoreAccessSettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingRestoreAccessTables, Level: setting.LevelInstallation, Kind: setting.KindBool,
			Default: RestoreAccessSettingDefault, Secret: false, Database: true,
			StaticCredentialHost: false, Apply: setting.ApplyLive,
			Check: func(value string) error {
				_, err := newRestoreAccessSetting(value)

				return err
			},
			ReducesSecurity: func(old, next string) bool {
				before, beforeErr := newRestoreAccessSetting(old)
				after, afterErr := newRestoreAccessSetting(next)

				return beforeErr != nil || afterErr != nil || (after.on && !before.on)
			},
			Doc: "When true, each holder of server:restore replaces the access tables in a restore. " +
				"When false, only a holder of server:restore-access does. Only a holder of " +
				"server:manage-settings and server:restore-access changes it, with a fresh authentication.",
		},
	}
}

// KeptSettingKeys returns the keys of the installation settings whose live
// rows a restore keeps when its caller does not hold server:restore-access.
// Each of these keys widens a later restore, so a backup must not set it
// through the restore of the settings table. A key that needs an extra
// action to change is in this list.
func KeptSettingKeys() []string {
	return []string{SettingRestoreAccessTables}
}

// IsKeptSettingKey reports whether key is one of KeptSettingKeys. The stores
// check each key of a swap with it, so a swap cannot keep or replace a row
// that this package did not name.
func IsKeptSettingKey(key string) bool {
	return key == SettingRestoreAccessTables
}

// readAccessSetting returns the stored text of the restore access setting.
// ok is false when the service has no reader or the read fails: the caller
// then fails closed.
func (s *RestoreService) readAccessSetting(ctx context.Context) (string, bool) {
	if s.accessSetting == nil {
		return "", false
	}

	text, err := s.accessSetting(ctx)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "restore keeps the access tables: the setting cannot be read",
			slog.String("setting", SettingRestoreAccessTables))

		return "", false
	}

	return text, true
}

// accessSettingIsStatic reports whether the static layer sets the restore
// access setting. A missing reader or a read error answers false, so the audit
// entry of the restore shows the change that the swap made to the row.
func (s *RestoreService) accessSettingIsStatic(ctx context.Context) bool {
	if s.accessSettingStatic == nil {
		return false
	}

	static, err := s.accessSettingStatic(ctx)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "restore cannot read whether the static layer sets the setting",
			slog.String("setting", SettingRestoreAccessTables))

		return false
	}

	return static
}

// widenedAccess reports whether the restore access setting is on in text. It
// fails closed: text that is not true or false answers false, so the restore
// keeps the access tables.
func (s *RestoreService) widenedAccess(ctx context.Context, text string) bool {
	value, err := newRestoreAccessSetting(text)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "restore keeps the access tables: the setting value is not valid",
			slog.String("setting", SettingRestoreAccessTables))

		return false
	}

	return value.on
}

// swapDecision makes the one decision of the apply of in. A request that keeps
// the access tables came from a caller without server:restore-access. That
// caller replaces the access tables only when the setting is on in text. Only
// a caller with server:restore-access takes the row of the setting from the
// backup, so every other caller keeps the live row of each kept key.
func (s *RestoreService) swapDecision(ctx context.Context, in restoreInput) SwapDecision {
	text, ok := s.readAccessSetting(ctx)
	d := PlainSwap(in.KeepAccessTables)

	if ok {
		// The decision records the effective value, never the raw text of the
		// reader: the audit entries and the log line of the restore name it.
		// An empty text or a text that the declaration refuses gives "false",
		// which is the value that the installation uses, so an empty
		// SettingBefore always means that the live value was not read.
		d.SettingBefore = EffectiveRestoreAccessSettingText(text)
	}

	d.SettingStatic = s.accessSettingIsStatic(ctx)

	if !in.KeepAccessTables {
		return d
	}

	d.KeepSettingKeys = KeptSettingKeys()

	if ok && s.widenedAccess(ctx, text) {
		d.KeepAccessTables, d.Widened = false, true
	}

	return d
}

// accessModeText names what the swap did to the access tables, for the audit
// entry and the log line, from the decision that the swap recorded. Without a
// recorded swap the restore did not change them.
func accessModeText(op durable.Operation) string {
	rec, ok := op.Step(RestoreStepSwap)
	if !ok {
		return "unchanged"
	}

	d, err := DecodeRestoreSwapDecision(rec.Result)

	var mode string

	switch {
	case err != nil:
		return "unknown"
	case d.KeepAccessTables:
		mode = "kept"
	case d.Widened:
		mode = "replaced because " + SettingRestoreAccessTables + " is on and the caller lacks server:restore-access"
	default:
		mode = "replaced"
	}

	return mode + "; setting " + SettingRestoreAccessTables + " " + settingModeText(d)
}

// SettingValueUnknown is the old value of the change entry of the restore
// access setting when the restore did not read the live value before the swap.
// The entry names the new value and marks the old value with this text, so a
// reader never takes a value that was not read for an empty value.
const SettingValueUnknown = "unknown"

// settingModeText says what the swap did to the row of the restore access
// setting, with the live value before the swap and, when the swap took the row
// from the backup, the value after it.
func settingModeText(d SwapDecision) string {
	// Defense in depth: the decoder and the store allow only known texts too.
	// This check stays so that a decision that did not come through them never
	// puts raw text in the restore entry or the log line. It does not rely on
	// either of them.
	before, after := d.SettingBefore, d.SettingAfter
	if !KnownSettingText(before) {
		before = ""
	}

	if !KnownSettingText(after) {
		after = ""
	}

	value := "the live value was not read"
	if before != "" {
		value = "the live value was " + before
	}

	if len(d.KeepSettingKeys) > 0 {
		return "kept (" + value + ")"
	}

	if after != "" {
		value += " before the swap, the new value is " + after
	} else {
		value += " before the swap"
	}

	if d.SettingStatic {
		return "taken from the backup (" + value + "); the static layer sets the effective value"
	}

	return "taken from the backup (" + value + ")"
}

// settingAuditEntry returns the setting-change entry of a restore that took a
// new value of the restore access setting from its backup, or false when the
// swap kept the live row or the value is the same. The entry has the form of a
// change by the settings API: the key, the old and the new value. It reads
// both values from the record of the swap step, which the store wrote in the
// swap transaction, and never reads the live setting, so a change after the
// swap, or an error of a read, cannot change or drop the entry. When the live
// value was not read before the swap, the entry names the new value and marks
// the old value as SettingValueUnknown: the setting can have changed, and the
// entry must say so.
func (s *RestoreService) settingAuditEntry(op durable.Operation, in restoreInput) (audit.Entry, bool) {
	var none audit.Entry

	rec, ok := op.Step(RestoreStepSwap)
	if !ok {
		return none, false
	}

	d, err := DecodeRestoreSwapDecision(rec.Result)
	// Defense in depth: the decoder returns a kept decision with no new value
	// for a record that does not decode, and the checks of settingChangeEntry
	// refuse that decision too (the kept setting keys and the empty new value).
	// This check stays so that a change of the decoder cannot let a corrupt
	// record write an entry. It does not rely on those checks.
	if err != nil {
		return none, false
	}

	return s.settingChangeEntry(d, in)
}

// settingChangeEntry returns the setting-change entry for the decision d, or
// false when the swap wrote none. d comes from the record of the swap step.
func (s *RestoreService) settingChangeEntry(d SwapDecision, in restoreInput) (audit.Entry, bool) {
	var none audit.Entry

	// Defense in depth: the decoder and the store allow only known texts too.
	// This check stays so that a decision that did not come through them never
	// puts raw text, for example a line break, a very long text, "TRUE" or the
	// marker SettingValueUnknown, in the audit entry. It fails closed. It does
	// not rely on the decoder or on the store.
	if !KnownSettingText(d.SettingBefore) || !KnownSettingText(d.SettingAfter) {
		return none, false
	}

	// A value from the static layer shadows the row that the swap took, so the
	// effective value did not change and the restore writes no change entry.
	if len(d.KeepSettingKeys) > 0 || d.SettingStatic || d.SettingAfter == "" ||
		d.SettingAfter == d.SettingBefore {
		return none, false
	}

	old := d.SettingBefore
	if old == "" {
		old = SettingValueUnknown
	}

	entry := s.restoreAuditEntry(in, "installation settings")
	entry.Action = string(identity.ActionServerManageSettings)
	entry.Reason = "restore " + in.ID.String() + " took the value from backup " + in.BackupID.String()
	entry.Changes = []audit.Change{{
		Key: SettingRestoreAccessTables, Old: &old, New: d.SettingAfter, Layer: "database", TakesEffect: "",
	}}

	return entry, true
}
