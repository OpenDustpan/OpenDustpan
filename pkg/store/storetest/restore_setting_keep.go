package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/setting"
)

// SettingSeeder is an optional BackupStore capability: a store that keeps the
// installation settings returns their port. The swap cases of the setting rows
// skip a store without it.
type SettingSeeder interface {
	SettingPorts() SettingPorts
}

// SettingPorts holds the ports of the installation settings of a store.
type SettingPorts struct {
	Store setting.Store
}

// settingKeepStore is a store that also keeps the installation settings.
type settingKeepStore interface {
	PortableRestoreStore
	SettingSeeder
}

// settingKeepCase is one swap of a backup whose restore access setting has
// one value onto a live installation that has another.
type settingKeepCase struct {
	name string
	// backupValue and liveValue are the text of the setting in the backup and
	// in the live installation. An empty liveValue leaves the live row absent.
	backupValue, liveValue string
	// decision is the decision of the swap.
	decision backup.SwapDecision
	// want is the effective value after the swap.
	want string
	// wantErr is the error of the swap, or nil.
	wantErr error
}

// settingKeepCases are the hostile cases of the swap of the setting rows. The
// cases with a kept key prove the guard of the store alone: the store keeps
// the live row whatever the service and the route decided.
func settingKeepCases() []settingKeepCase {
	kept := func(keepAccess bool) backup.SwapDecision {
		d := backup.PlainSwap(keepAccess)
		d.KeepSettingKeys = backup.KeptSettingKeys()

		return d
	}

	unlisted := backup.SwapDecision{
		KeepAccessTables: true, Widened: false, KeepSettingKeys: []string{"settings.freshAuthSeconds"},
		SettingBefore: "",
	}

	return []settingKeepCase{
		{
			name: "a backup with true keeps the live false when the key is kept", backupValue: "true",
			liveValue: "false", decision: kept(true), want: "false", wantErr: nil,
		},
		{
			name: "a backup with true keeps the absent live row when the key is kept", backupValue: "true",
			liveValue: "", decision: kept(true), want: "false", wantErr: nil,
		},
		{
			name: "a backup with true keeps the live false when the access tables are replaced", backupValue: "true",
			liveValue: "false", decision: kept(false), want: "false", wantErr: nil,
		},
		{
			name: "a backup with false keeps the live true when the key is kept", backupValue: "false",
			liveValue: "true", decision: kept(true), want: "true", wantErr: nil,
		},
		{
			name: "a backup with true takes the value when no key is kept", backupValue: "true",
			liveValue: "false", decision: backup.PlainSwap(false), want: "true", wantErr: nil,
		},
		{
			name: "a key that is not on the list is refused and changes nothing", backupValue: "true",
			liveValue: "false", decision: unlisted, want: "false", wantErr: backup.ErrCorrupt,
		},
	}
}

// testRestoreSwapKeepsSettingRow proves, for each case, the row of the restore
// access setting after the swap of the staged backup.
func testRestoreSwapKeepsSettingRow(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := settingKeepCases()

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runSettingKeepCase(t, newStore, tc)
		})
	}
}

// runSettingKeepCase runs one case of settingKeepCases.
func runSettingKeepCase(t *testing.T, newStore RestoreFactory, tc *settingKeepCase) {
	t.Helper()

	var src settingKeepStore

	r := newStagedRestoreWith(t, newStore, func(t *testing.T, s BackupStore) {
		t.Helper()

		store, ok := s.(settingKeepStore)
		if !ok {
			t.Skip("the store keeps no installation settings")
		}

		src = store

		setRestoreAccessSetting(t, src.SettingPorts().Store, tc.backupValue)
	})

	dst, ok := r.dst.(settingKeepStore)
	if !ok {
		t.Skip("the store keeps no installation settings")
	}

	if tc.liveValue != "" {
		setRestoreAccessSetting(t, dst.SettingPorts().Store, tc.liveValue)
	}

	area := r.stage(t, r.held.Claim.Token)

	if err := r.dst.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	swap := r.held.Claim.Step(backup.RestoreStepSwap)
	_, err := r.staging.ApplyRestoreArea(t.Context(), swap, area, tc.decision)

	if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
		t.Fatalf("swap error = %v, want %v", err, tc.wantErr)
	}

	if got := restoreAccessSettingValue(t, dst.SettingPorts().Store); got != tc.want {
		t.Fatalf("restore access setting after the swap = %q, want %q", got, tc.want)
	}
}

// settingServiceOf returns the settings service over store with the
// declaration of the restore access setting.
func settingServiceOf(t *testing.T, store setting.Store) *setting.Service {
	t.Helper()

	reg, err := setting.NewRegistry(backup.RestoreAccessSettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return setting.NewService(store, reg, nil, nil, func() time.Time { return time.UnixMilli(1_700_000_000_000) })
}

// setRestoreAccessSetting sets the restore access setting of store to value
// through the settings service, with a fresh authentication.
func setRestoreAccessSetting(t *testing.T, store setting.Store, value string) {
	t.Helper()

	entry := audit.Entry{
		At: time.UnixMilli(1), Namespace: audit.ServerScope, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: "", Object: audit.ServerScope.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}

	patch := setting.Patch{{Key: backup.SettingRestoreAccessTables, Value: value, Delete: false}}

	_, err := settingServiceOf(t, store).Patch(
		t.Context(), setting.InstallationScope(), patch, setting.PatchRequest{Entry: entry, IfMatch: 0, Fresh: true},
	)
	if err != nil {
		t.Fatalf("set the restore access setting to %q: %v", value, err)
	}
}

// restoreAccessSettingValue returns the effective value of the restore access
// setting of store.
func restoreAccessSettingValue(t *testing.T, store setting.Store) string {
	t.Helper()

	item, err := settingServiceOf(t, store).Effective(
		t.Context(), setting.InstallationScope(), backup.SettingRestoreAccessTables,
	)
	if err != nil {
		t.Fatalf("read the restore access setting: %v", err)
	}

	return item.Value
}
