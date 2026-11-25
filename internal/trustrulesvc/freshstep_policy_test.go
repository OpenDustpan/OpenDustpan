package trustrulesvc_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// policyService returns a service that holds the options opts, over the fakes
// of the tests and the bundle store bundles.
func policyService(t *testing.T, bundles *guardBundleStore, opts ...trustrulesvc.Option) *trustrulesvc.Service {
	t.Helper()

	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	people := fakePrincipals{"prn-worker": principalOf("prn-worker", principal.KindService, "acme")}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: &fakeStore{created: nil, entries: nil, deleted: nil}, Namespaces: fakeNamespaces{},
		Principals: people, Authorizer: authz, Reach: &fakeReach{checked: nil, err: nil},
		InstallationURLs: []string{"https://dustpan.example.com"}, OwnIssuer: nil, CheckURL: nil,
		Bundles: bundles, Sessions: guardSessionPolicy{window: session.DefaultTokenFreshness},
		Now: exchangeNow,
	}, opts...)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestFreshPolicyGatesTheWriteStep proves that, with the setting off, a rule
// create and a bundle write pass for a caller with no console session, and
// that the setting on, or no policy, keeps the refusal. The caller is a
// person with no fresh authentication.
func TestFreshPolicyGatesTheWriteStep(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for name, tc := range map[string]struct {
		opts    []trustrulesvc.Option
		wantErr error
	}{
		"off":                 {opts: []trustrulesvc.Option{trustrulesvc.WithFreshPolicy(off)}, wantErr: nil},
		"on":                  {opts: []trustrulesvc.Option{trustrulesvc.WithFreshPolicy(on)}, wantErr: trustrulesvc.ErrFreshRequired},
		"no policy":           {opts: nil, wantErr: trustrulesvc.ErrFreshRequired},
		"nil policy":          {opts: []trustrulesvc.Option{trustrulesvc.WithFreshPolicy(nil)}, wantErr: trustrulesvc.ErrFreshRequired},
		"nil option":          {opts: []trustrulesvc.Option{nil}, wantErr: trustrulesvc.ErrFreshRequired},
		"nil option then off": {opts: []trustrulesvc.Option{nil, trustrulesvc.WithFreshPolicy(off)}, wantErr: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bundles := &guardBundleStore{saved: 0, rotated: 0, deleted: nil, entries: nil}
			svc := policyService(t, bundles, tc.opts...)

			_, err := svc.Create(context.Background(), person(), emptyEntry(), "acme", input())
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("Create with no console session = %v, want %v", err, tc.wantErr)
			}

			err = svc.DeleteBundleVersion(context.Background(), person(), emptyEntry(), "acme", 1)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("DeleteBundleVersion with no console session = %v, want %v", err, tc.wantErr)
			}

			if reached := len(bundles.deleted) == 1; reached != (tc.wantErr == nil) {
				t.Errorf("bundle delete reached the store = %v, want %v", reached, tc.wantErr == nil)
			}
		})
	}
}

// With the setting off, a caller with a stored access token keeps the
// refusal: the setting turns off the fresh step only.
func TestFreshPolicyOffKeepsTheTokenRefusal(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	svc := policyService(t, &guardBundleStore{saved: 0, rotated: 0, deleted: nil, entries: nil},
		trustrulesvc.WithFreshPolicy(off))

	token := person()
	token.TokenID, token.Credential = "tok-1", identity.CredentialAccessToken

	if _, err := svc.Create(context.Background(), token, emptyEntry(), "acme", input()); !errors.Is(
		err, trustrulesvc.ErrSignInRequired,
	) {
		t.Fatalf("Create with an access token = %v, want ErrSignInRequired", err)
	}
}
