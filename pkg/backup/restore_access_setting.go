package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingRestoreAccessTables is the key of the installation setting that lets
// each holder of server:restore replace the access tables in a restore. The
// default is off. Only the settings API writes it, and a change needs the
// actions server:manage-settings and server:restore-access, with a fresh
// authentication.
const SettingRestoreAccessTables = "restore.replace-access-tables"

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
	case "true":
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

// RestoreAccessSettingDeclarations returns the declaration of the restore
// access setting for the settings resource of the installation. The database
// layer holds it, and no flag, variable or file sets it. A change to true is
// weaker, so it needs a fresh authentication.
func RestoreAccessSettingDeclarations() []setting.Declaration {
	return []setting.Declaration{
		{
			Key: SettingRestoreAccessTables, Level: setting.LevelInstallation, Kind: setting.KindBool,
			Default: "false", Secret: false, Database: true,
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

// widenedAccess reports whether the restore access setting is on now. It
// fails closed: no reader, a read error, or a stored value that is not true or
// false all answer false, so the restore keeps the access tables.
func (s *RestoreService) widenedAccess(ctx context.Context) bool {
	if s.accessSetting == nil {
		return false
	}

	text, err := s.accessSetting(ctx)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "restore keeps the access tables: the setting cannot be read",
			slog.String("setting", SettingRestoreAccessTables))

		return false
	}

	value, err := newRestoreAccessSetting(text)
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "restore keeps the access tables: the setting value is not valid",
			slog.String("setting", SettingRestoreAccessTables))

		return false
	}

	return value.on
}

// widenedAccessAuditObject names the widened path of restore in for its audit
// entry: the access tables are replaced for a caller without
// server:restore-access, because the setting is on.
func widenedAccessAuditObject(in restoreInput) string {
	return "restore " + in.ID.String() + " of backup " + in.BackupID.String() +
		" replaced the access tables because the setting " + SettingRestoreAccessTables +
		" is on (the caller did not hold server:restore-access)"
}
