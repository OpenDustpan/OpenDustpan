package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/setting"
)

// TestJobTxReadsTheInstallationSetting proves that the read of a setting in the
// transaction of the job stores returns the stored value, and that a key with
// no row reports no row.
func TestJobTxReadsTheInstallationSetting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)
	ctx := t.Context()

	if err := sqlite.SetSettingRow(ctx, s, setting.SettingFreshAuthEnabled, "false"); err != nil {
		t.Fatalf("write the setting row: %v", err)
	}

	value, found, err := sqlite.ReadSettingInTx(ctx, s, setting.SettingFreshAuthEnabled)
	if err != nil {
		t.Fatalf("read the setting in the transaction: %v", err)
	}

	if !found || value != "false" {
		t.Fatalf("read the setting = %q, found %v, want \"false\", found true", value, found)
	}

	const missing = "jobtx-test-missing"

	value, found, err = sqlite.ReadSettingInTx(ctx, s, missing)
	if err != nil {
		t.Fatalf("read a missing setting in the transaction: %v", err)
	}

	if found || value != "" {
		t.Fatalf("read a missing setting = %q, found %v, want no row", value, found)
	}
}
