package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// TestBreakGlassLockSecondsInSettingsResource proves that the settings
// resource shows the lock timeout with its effective value and its source.
func TestBreakGlassLockSecondsInSettingsResource(t *testing.T) {
	t.Parallel()

	registry, err := setting.NewRegistry(principal.BreakGlassSettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	e := newSettingsEnvWith(t, registry, nil)

	r := e.do(t, getCall(settingsURL))
	wantSettingsStatus(t, r, http.StatusOK)

	item, ok := decodeSettings(t, r)[principal.SettingBreakGlassLockSeconds]
	if !ok {
		t.Fatalf("no setting %s in %s", principal.SettingBreakGlassLockSeconds, r.body)
	}

	if item["value"] != float64(5) || item["source"] != string(setting.SourceDefault) {
		t.Fatalf("setting = %v, want the value 5 from the default", item)
	}
}
