package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// retentionRegistry holds one declaration with the check and the reduction
// rule of the decision record retention setting.
func retentionRegistry(t *testing.T) *setting.Registry {
	t.Helper()

	reg, err := setting.NewRegistry([]setting.Declaration{{
		Key: gate.RetentionKey, Level: setting.LevelInstallation, Kind: setting.KindInt, Default: "0",
		Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive,
		Check: gate.RetentionCheck, ReducesSecurity: gate.RetentionReducesSecurity, Doc: "Retention.",
	}})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return reg
}

// TestSettingsPatchRetentionFreshStep checks that a change to a shorter
// period, or from keep-forever to a period, needs a fresh authentication and
// that a longer period needs none.
func TestSettingsPatchRetentionFreshStep(t *testing.T) {
	t.Parallel()

	e := newKeyedSettingsEnvWith(t, retentionRegistry(t))
	stale := session.DefaultTokenFreshness + time.Minute

	set := func(_, value string) string { return `{"` + gate.RetentionKey + `":` + value + `}` }

	// From keep-forever to a period: refused with a token and an old session.
	for name, p := range map[string]keyedPatch{
		"token":       tokenPatch(settingsURL, `"0"`, "key-ret-token", set("0", "720")),
		"old session": sessionPatch(`"0"`, set("0", "720"), stale),
	} {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusForbidden)

		if !strings.Contains(r.body, settingsFreshCode) {
			t.Fatalf("%s: body = %s, want the code mfa.fresh-step-required", name, r.body)
		}
	}

	wantSettingsStatus(t, e.patch(t, sessionPatch(`"0"`, set("0", "8760"), 0)), http.StatusOK)

	// A shorter period is refused without the step.
	wantSettingsStatus(t, e.patch(t, tokenPatch(settingsURL, `"1"`, "key-ret-shorter", set("1", "720"))),
		http.StatusForbidden)

	if got := e.read(t); got.header.Get("ETag") != `"1"` {
		t.Fatalf("a refused change moved the settings: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	// A longer period and keep-forever need no step.
	wantSettingsStatus(t, e.patch(t, tokenPatch(settingsURL, `"1"`, "key-ret-longer", set("1", "17520"))),
		http.StatusOK)
	wantSettingsStatus(t, e.patch(t, tokenPatch(settingsURL, `"2"`, "key-ret-forever", set("2", "0"))),
		http.StatusOK)
}
