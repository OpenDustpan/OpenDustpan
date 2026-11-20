package tempadmin_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

func TestHostResetSessionLimitHostile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mode   string
		reason string
	}{
		{"no reason", "bootstrap", ""},
		{"a blank reason", "bootstrap", "   "},
		{"a reason with a control character", "bootstrap", "bad\x00reason"},
		{"recovery mode none", tempadmin.RecoveryModeNone, "the stored limit is wrong"},
		{"an empty recovery mode", "", "the stored limit is wrong"},
		{"a recovery mode with a space", " none", "the stored limit is wrong"},
		{"a recovery mode in capitals", "NONE", "the stored limit is wrong"},
		{"an unknown recovery mode", "anything", "the stored limit is wrong"},
		{"a recovery mode in capitals that names a real one", "BOOTSTRAP", "the stored limit is wrong"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkResetRefused(t, tc.mode, tc.reason)
		})
	}
}

// checkResetRefused stores a limit, calls the reset and checks that it
// refuses and that the stored limit stays.
func checkResetRefused(t *testing.T, mode, reason string) {
	t.Helper()

	st := openSettingsStore(t)

	registry, err := setting.NewRegistry(tempadmin.SessionLimitSettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	svc := setting.NewService(st.SettingStore(), registry, nil, nil, nil)

	_, err = svc.Patch(t.Context(), setting.InstallationScope(),
		setting.Patch{{Key: tempadmin.SettingSessionLimit, Value: "2h", Delete: false}},
		setting.PatchRequest{Entry: resetEntry(), IfMatch: 0, Fresh: true})
	if err != nil {
		t.Fatalf("store the limit: %v", err)
	}

	err = tempadmin.ResetSessionLimit(t.Context(), st.SettingStore(), tempadmin.ResetInput{
		Mode: mode, Reason: reason, Audit: st, Entry: resetEntry(), Now: nil,
	})
	if !errors.Is(err, tempadmin.ErrResetRefused) {
		t.Fatalf("reset = %v, want ErrResetRefused", err)
	}

	stored, err := st.SettingStore().Load(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if stored.Values[tempadmin.SettingSessionLimit] != "2h" {
		t.Fatalf("the stored limit = %q, want 2h", stored.Values[tempadmin.SettingSessionLimit])
	}

	checkResetFailureEntry(t, st)
}

// checkResetFailureEntry checks that the refused reset wrote one failure entry
// with no reason that ValidateReason refuses.
func checkResetFailureEntry(t *testing.T, st *sqlite.Store) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	failures := 0

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Outcome != audit.OutcomeFailure || e.Action != tempadmin.Action {
			continue
		}

		failures++

		if tempadmin.ValidateReason(e.Reason) != nil && e.Reason != "" {
			t.Fatalf("the failure entry holds the reason %q that ValidateReason refuses", e.Reason)
		}
	}

	if failures != 1 {
		t.Fatalf("failure entries after the refused reset = %d, want 1 in %+v", failures, listing.Entries)
	}
}

func TestResetSessionLimitNeedsAnAuditStore(t *testing.T) {
	t.Parallel()

	st := openSettingsStore(t)

	err := tempadmin.ResetSessionLimit(t.Context(), st.SettingStore(), tempadmin.ResetInput{
		Mode: tempadmin.RecoveryModeBootstrap, Reason: "the stored limit is wrong", Audit: nil,
		Entry: resetEntry(), Now: nil,
	})
	if !errors.Is(err, tempadmin.ErrMissingDependency) {
		t.Fatalf("reset with no audit store = %v, want ErrMissingDependency", err)
	}
}
