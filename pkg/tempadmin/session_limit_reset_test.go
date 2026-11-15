package tempadmin_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/setting"
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

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
				Mode: tc.mode, Reason: tc.reason, Entry: resetEntry(), Now: nil,
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
		})
	}
}
