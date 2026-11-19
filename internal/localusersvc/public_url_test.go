package localusersvc_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
)

// noPublicURLService returns a service over the store of the fixture for a
// server that has no public URL.
func (f *fixture) noPublicURLService(t *testing.T) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		NoPublicURL: true,
		Log:         nil,
		Store:       f.store,
		Principals:  f.principals,
		Hasher:      f.hasher,
		Blocklist:   blocklist.Embedded(),
		Audit:       f.store,
		Locks:       f.store,
		Keys:        f.store,
		Settings:    f.settings,
		SSO:         func(context.Context) (bool, error) { return false, nil },
		Reach:       f.storeReach(),
		Subjects:    f.store,
		Kind:        installation.KindProduction,
		Now:         nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestPolicyShowsThePublicURLCause proves that a server with no public URL
// shows the password sign-in as off with the source public-url, whatever the
// setting and the identity provider say. Each case sends the hostile value
// directly to the policy.
func TestPolicyShowsThePublicURLCause(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		setting string
	}{
		{name: "setting empty", setting: ""},
		{name: "setting on", setting: "true"},
		{name: "setting off", setting: "false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.settings.values[localuser.SettingEnabled] = tc.setting

			p, err := f.noPublicURLService(t).Policy(t.Context())
			if err != nil {
				t.Fatalf("policy: %v", err)
			}

			if p.Enabled || p.EnabledSource != localusersvc.SourcePublicURL {
				t.Fatalf(
					"policy = enabled %v from %q, want off from %q",
					p.Enabled,
					p.EnabledSource,
					localusersvc.SourcePublicURL,
				)
			}
		})
	}

	// A server with a public URL keeps the old values: the guard is the only
	// reason for the new source.
	f := newFixture(t)

	p, err := f.svc.Policy(t.Context())
	if err != nil || !p.Enabled || p.EnabledSource != localusersvc.SourceDefault {
		t.Fatalf("policy with a public URL = %+v, %v; want on from the default", p, err)
	}
}

// TestPublicURLRefusesEachPasswordCheck proves that the service of a server
// with no public URL refuses the sign-in, the redeem and the reset with
// ErrPublicURLRequired, changes nothing and writes a denied entry. The setting
// is on: it does not turn the check back on. The refusal comes before the
// look-up of the code and of the user name, so a hostile code or name gets the
// same answer.
func TestPublicURLRefusesEachPasswordCheck(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.settings.values[localuser.SettingEnabled] = "true"
	c := f.mustCreate(t, "alice", "")
	off := f.noPublicURLService(t)

	checks := []struct {
		run  func() error
		name string
	}{
		{name: "redeem a good code", run: func() error {
			return off.Redeem(t.Context(), base(""), c.SetUp.Text, goodPassword)
		}},
		{name: "redeem an unknown code", run: func() error {
			return off.Redeem(t.Context(), base(""), "not-a-code", goodPassword)
		}},
		{name: "sign in", run: func() error {
			_, err := off.SignIn(t.Context(), base(""), "alice", goodPassword)

			return err
		}},
		{name: "sign in with an unknown name", run: func() error {
			_, err := off.SignIn(t.Context(), base(""), "no-such-name", goodPassword)

			return err
		}},
		{name: "reset", run: func() error {
			_, err := off.StartReset(t.Context(), admin(), c.Principal.ID, localusersvc.ResetInput{
				Reason: "e2e", RevokeAccessTokens: false,
			})

			return err
		}},
	}

	for _, tc := range checks {
		if err := tc.run(); !errors.Is(err, localuser.ErrPublicURLRequired) {
			t.Fatalf("%s = %v, want ErrPublicURLRequired", tc.name, err)
		}
	}

	// The refusals did not use the code: the same code still sets a password
	// on a server that has a public URL.
	if err := f.svc.Redeem(t.Context(), base(""), c.SetUp.Text, goodPassword); err != nil {
		t.Fatalf("redeem on the server with a public URL: %v", err)
	}

	refused := 0

	for _, action := range []string{
		localusersvc.ActionResetRefused, localusersvc.ActionSignInPasswordFails, localusersvc.ActionResetCreated,
	} {
		for _, e := range f.entriesWith(t, action) {
			if e.Outcome == audit.OutcomeDenied {
				refused++
			}
		}
	}

	if refused != len(checks) {
		t.Fatalf("denied entries = %d, want %d", refused, len(checks))
	}
}
