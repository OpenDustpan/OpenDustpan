package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
)

// writeFreshAuthSetting sets the installation setting freshAuthEnabled to on.
// The change from off to on needs no fresh step.
func writeFreshAuthSetting(ctx context.Context, t *testing.T, b *dustpan.BuiltinStore) {
	t.Helper()

	registry, err := setting.NewRegistry([]setting.Declaration{setting.FreshAuthEnabledDeclaration()})
	if err != nil {
		t.Fatalf("setting registry: %v", err)
	}

	svc := setting.NewService(b.SettingStore(), registry, nil, nil, func() time.Time { return time.Unix(0, 0) })

	entry := hostEntry("set-setting")
	entry.Object = "settings"

	_, err = svc.Patch(ctx, setting.InstallationScope(),
		setting.Patch{{Key: setting.SettingFreshAuthEnabled, Value: "true", Delete: false}},
		setting.PatchRequest{Entry: entry, IfMatch: 0, Fresh: false})
	if err != nil {
		t.Fatalf("set %s: %v", setting.SettingFreshAuthEnabled, err)
	}
}

// hostStepCase is one row of TestHostTokenServiceFollowsFreshAuthSetting.
type hostStepCase struct {
	name string
	// on writes freshAuthEnabled as on before the change.
	on bool
	// breakRead closes the store before the change, so the setting cannot be read.
	breakRead bool
	// wantStep is whether the change must be refused for want of a fresh step.
	wantStep bool
}

// checkHostTokenStep runs one token change of the host command and fails the
// test when the refusal for want of a fresh step is not tc.wantStep.
func checkHostTokenStep(t *testing.T, tc hostStepCase) {
	t.Helper()

	ctx := t.Context()

	b, err := dustpan.OpenStore(ctx, "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if !tc.breakRead {
		t.Cleanup(func() {
			if closeErr := b.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})
	}

	if tc.on {
		writeFreshAuthSetting(ctx, t, b)
	}

	tokens, err := hostTokenService(openedStore{b})
	if err != nil {
		t.Fatalf("host token service: %v", err)
	}

	if tc.breakRead {
		if closeErr := b.Close(); closeErr != nil {
			t.Fatalf("close store: %v", closeErr)
		}
	}

	// The change names a principal that does not exist. A refused change
	// stops before that lookup; a change that passes the guard fails there.
	admin := identity.Principal{
		Subject: "host", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	_, _, err = tokens.Create(ctx, admin, hostEntry("create-token"), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "missing", Name: "host"})

	if got := errors.Is(err, identity.ErrDenied); got != tc.wantStep {
		t.Fatalf("create: refused for want of a fresh step = %t, want %t: err = %v", got, tc.wantStep, err)
	}
}

// TestHostTokenServiceFollowsFreshAuthSetting checks that a token change of
// the host command takes the fresh step policy of the installation setting
// freshAuthEnabled. With the setting on, the change needs a fresh step and is
// refused. With the setting off, the guard asks for no step. A setting that
// cannot be read requires the step (fail closed).
func TestHostTokenServiceFollowsFreshAuthSetting(t *testing.T) {
	t.Parallel()

	cases := []hostStepCase{
		{name: "setting off asks no step", on: false, breakRead: false, wantStep: false},
		{name: "setting on needs the step", on: true, breakRead: false, wantStep: true},
		{name: "setting read error requires the step", on: false, breakRead: true, wantStep: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkHostTokenStep(t, tc)
		})
	}
}
