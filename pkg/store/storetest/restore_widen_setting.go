package storetest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// widenSettingCase is one live value of the restore access setting under a
// swap that widens.
type widenSettingCase struct {
	name string
	// live is the stored text. absent leaves the live row out, and raw stores
	// the text without the check of the declaration.
	live        string
	absent, raw bool
	wantRefused bool
	wantMarker  bool
	wantSwapped bool
}

// widenSettingCases are the hostile live values. The decision of each case is
// the one that a bug in the service could send: it widens and keeps the
// setting key. Only the live row "true" lets the swap run.
func widenSettingCases() []widenSettingCase {
	refused := func(name, live string, absent, raw bool) widenSettingCase {
		return widenSettingCase{
			name: name, live: live, absent: absent, raw: raw, wantRefused: true, wantMarker: true, wantSwapped: false,
		}
	}

	return []widenSettingCase{
		refused("the live setting is false", "false", false, false),
		refused("the live setting is empty", "", false, true),
		refused("the live setting is TRUE", "TRUE", false, true),
		refused("the live setting is 1", "1", false, true),
		refused("the live setting is the longest value", strings.Repeat("x", setting.MaxValueBytes), false, true),
		refused("the live setting has no row", "", true, false),
		{
			name: "the live setting is true", live: "true", absent: false, raw: false, wantRefused: false,
			wantMarker: false, wantSwapped: true,
		},
	}
}

// testRestoreWidenedSwapNeedsSetting proves that the store alone refuses a
// swap that widens when the live row of the restore access setting is not
// true: the access tables stay and the swap step is not recorded. The
// decision is the one that a bug in the service could send.
func testRestoreWidenedSwapNeedsSetting(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	cases := widenSettingCases()

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runWidenSettingCase(t, newStore, tc)
		})
	}
}

// runWidenSettingCase runs one case of widenSettingCases.
func runWidenSettingCase(t *testing.T, newStore RestoreFactory, tc *widenSettingCase) {
	t.Helper()

	r := newStagedRestoreWith(t, newStore, nil)

	dst, ok := r.dst.(settingKeepStore)
	if !ok {
		t.Skip("the store keeps no installation settings")
	}

	groups, ok := r.dst.(principal.Store)
	if !ok {
		t.Skip("the store keeps no groups")
	}

	// The marker is a group that only the live store holds. A swap that
	// replaces the access tables removes it.
	marker := principal.Group{
		CreatedAt: now(), UpdatedAt: now(), ID: "grp-live-marker", Name: "Live marker", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := groups.CreateGroup(t.Context(), marker, ServerChange(now(), "group:grp-live-marker")); err != nil {
		t.Fatalf("create the marker group: %v", err)
	}

	seedLiveSetting(t, dst.SettingPorts().Store, tc)

	area := r.stage(t, r.held.Claim.Token)

	if err := r.dst.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	decision := backup.PlainSwap(false)
	decision.Widened = true
	decision.KeepSettingKeys = backup.KeptSettingKeys()

	swap := r.held.Claim.Step(backup.RestoreStepSwap)
	_, err := r.staging.ApplyRestoreArea(t.Context(), swap, area, decision)

	if tc.wantRefused != errors.Is(err, backup.ErrCorrupt) || (!tc.wantRefused && err != nil) {
		t.Fatalf("swap error = %v, want refused %v", err, tc.wantRefused)
	}

	_, markerErr := groups.GetGroup(t.Context(), "grp-live-marker")
	if tc.wantMarker != (markerErr == nil) {
		t.Fatalf("marker group kept = %v, want %v (error %v)", markerErr == nil, tc.wantMarker, markerErr)
	}

	if got := swapRecorded(t, r.dst.DurableOperationsPort(), swap.Operation); got != tc.wantSwapped {
		t.Fatalf("swap step recorded = %v, want %v", got, tc.wantSwapped)
	}
}

// seedLiveSetting writes the live row of the restore access setting that tc
// names: none, text that the declaration refuses, or a value that the
// settings service accepts.
func seedLiveSetting(t *testing.T, store setting.Store, tc *widenSettingCase) {
	t.Helper()

	switch {
	case tc.absent:
	case tc.raw:
		setRawRestoreAccessSetting(t, store, tc.live)
	default:
		setRestoreAccessSetting(t, store, tc.live)
	}
}

// swapRecorded reports whether the swap step of operation id is recorded.
func swapRecorded(t *testing.T, ops durable.Store, id durable.ID) bool {
	t.Helper()

	op, err := ops.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	_, recorded := op.Step(backup.RestoreStepSwap)

	return recorded
}
