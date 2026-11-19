package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
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
		SettingBefore: "", SettingAfter: "",
	}

	// The decisions below break the rule of the decision: the swap keeps or
	// widens the access tables and does not keep the setting row. The store
	// refuses each of them alone, whatever the service decided.
	keepsNoKey := backup.SwapDecision{
		KeepAccessTables: true, Widened: false, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
	}
	widensNoKey := backup.SwapDecision{
		KeepAccessTables: false, Widened: true, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
	}
	keptAndWidened := backup.SwapDecision{
		KeepAccessTables: true, Widened: true, KeepSettingKeys: backup.KeptSettingKeys(), SettingBefore: "",
		SettingAfter: "",
	}

	return []settingKeepCase{
		{
			name: "a swap that keeps the access tables without the setting key is refused", backupValue: "true",
			liveValue: "false", decision: keepsNoKey, want: "false", wantErr: backup.ErrCorrupt,
		},
		{
			name: "a swap that widens the access tables without the setting key is refused", backupValue: "true",
			liveValue: "false", decision: widensNoKey, want: "false", wantErr: backup.ErrCorrupt,
		},
		{
			name: "a swap that is both kept and widened is refused", backupValue: "true",
			liveValue: "false", decision: keptAndWidened, want: "false", wantErr: backup.ErrCorrupt,
		},
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

	if tc.wantErr == nil {
		checkSwapRecord(t, r.dst.DurableOperationsPort(), swap.Operation, tc.decision, tc.want)
	}
}

// checkSwapRecord proves that the step record of the swap holds the value of
// the setting after the swap when the swap took the row from the backup, and
// no value when the swap kept the live row.
func checkSwapRecord(t *testing.T, ops durable.Store, id durable.ID, d backup.SwapDecision, after string) {
	t.Helper()

	op, err := ops.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}

	rec, ok := op.Step(backup.RestoreStepSwap)
	if !ok {
		t.Fatal("the swap step is not recorded")
	}

	got, err := backup.DecodeRestoreSwapDecision(rec.Result)
	if err != nil {
		t.Fatalf("decode the swap record: %v", err)
	}

	want := after
	if len(d.KeepSettingKeys) > 0 {
		want = ""
	}

	if got.SettingAfter != want {
		t.Fatalf("setting after the swap in the record = %q, want %q", got.SettingAfter, want)
	}
}

// errSwapAfterKeep is the failure that the swap retry case injects after the
// keeps of the swap.
var errSwapAfterKeep = errors.New("injected failure after the keeps of the swap")

// SwapRetryCase is one retry of a swap that decides another way than its
// failed first attempt.
type SwapRetryCase struct {
	Name string
	// First is the decision of the attempt that fails after its keeps.
	First backup.SwapDecision
	// Second is the decision of the retry.
	Second backup.SwapDecision
	// Want is the effective value of the setting after the retry.
	Want string
}

// SwapRetryCases returns the retries that CheckSwapRetryAfterKeep runs, one
// on each new store.
func SwapRetryCases() []SwapRetryCase {
	return []SwapRetryCase{
		{Name: "keep, then take", First: backup.PlainSwap(true), Second: backup.PlainSwap(false), Want: "true"},
		{Name: "take, then keep", First: backup.PlainSwap(false), Second: backup.PlainSwap(true), Want: "false"},
	}
}

// CheckSwapRetryAfterKeep proves that a retry of a swap that decides another
// way leaves data and record in agreement. The first attempt keeps or takes the
// setting row and fails after its keeps; the retry decides the other way. The
// live setting and the record of the retry must follow the retry alone: a keep
// of the failed attempt must not stay in the data that the retry swaps in.
// install arms a store hook that fails the next swap once after its keeps. A
// store whose swap is one transaction passes because the failure rolls
// everything back. Each call needs a new store.
func CheckSwapRetryAfterKeep(
	t *testing.T, s StagedAreaStore, settings setting.Store, install func(hook func(context.Context) error),
	tc SwapRetryCase,
) {
	t.Helper()

	// The backup holds true and the live row holds false, so the value after
	// the retry tells which attempt the data follows.
	setRestoreAccessSetting(t, settings, "true")

	staged := StageRestoreArea(t, s)

	// The settings API refuses a change in maintenance mode, so the live row
	// changes in normal operation, before the swap.
	if err := s.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	setRestoreAccessSetting(t, settings, "false")

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	failed := false

	install(func(context.Context) error {
		if failed {
			return nil
		}

		failed = true

		return errSwapAfterKeep
	})

	_, firstErr := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, tc.First)
	if !errors.Is(firstErr, errSwapAfterKeep) {
		t.Fatalf("%s: first attempt = %v, want the injected failure", tc.Name, firstErr)
	}

	if got := restoreAccessSettingValue(t, settings); got != "false" {
		t.Fatalf("%s: setting after the failed attempt = %q, want false", tc.Name, got)
	}

	if _, err := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, tc.Second); err != nil {
		t.Fatalf("%s: retry: %v", tc.Name, err)
	}

	if got := restoreAccessSettingValue(t, settings); got != tc.Want {
		t.Fatalf("%s: setting after the retry = %q, want %q", tc.Name, got, tc.Want)
	}

	checkSwapRecord(t, s.DurableOperationsPort(), staged.Swap.Operation, tc.Second, tc.Want)
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

	svc := settingServiceOf(t, store)

	current, err := svc.Get(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("read the settings revision: %v", err)
	}

	_, err = svc.Patch(
		t.Context(), setting.InstallationScope(), patch,
		setting.PatchRequest{Entry: entry, IfMatch: current.Revision, Fresh: true},
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
