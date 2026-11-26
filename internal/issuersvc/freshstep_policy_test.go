package issuersvc_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/profile"
)

// withFreshPolicy returns a harness whose service holds the options opts.
func withFreshPolicy(t *testing.T, opts ...issuersvc.Option) *harness {
	t.Helper()

	h := newHarness(t)

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store: h.store.Issuer(), Namespaces: h.store, Sealer: scopedSealer{}, Notifier: h.outbox, Fetcher: nil,
		Authorizer: grants{
			alice: allActions(), "rita": {},
		},
		IssuerBaseURL: issuerHost, PublicURL: "https://api.example.com", Now: h.clock.Now, Log: nil,
	}, opts...)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	h.svc = svc

	return h
}

// TestFreshPolicyGatesThePatchStep proves that the setting off lets a change
// that reduces security pass with no step, and that the setting on, or no
// policy, keeps the refusal.
func TestFreshPolicyGatesThePatchStep(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	for name, tc := range map[string]struct {
		opts    []issuersvc.Option
		wantErr error
	}{
		"off":        {opts: []issuersvc.Option{issuersvc.WithFreshPolicy(off)}, wantErr: nil},
		"on":         {opts: []issuersvc.Option{issuersvc.WithFreshPolicy(on)}, wantErr: profile.ErrFreshRequired},
		"no policy":  {opts: nil, wantErr: profile.ErrFreshRequired},
		"nil policy": {opts: []issuersvc.Option{issuersvc.WithFreshPolicy(nil)}, wantErr: profile.ErrFreshRequired},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := withFreshPolicy(t, tc.opts...)

			// A change of the issuerURL reduces security, and the caller has no
			// fresh authentication.
			_, err := h.patch(h.admin, patchURL("https://files.example.com/acme"), 0, nil)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("patch with the setting off: %v", err)
				}

				return
			}

			wantErr(t, "patch with no fresh check", err, tc.wantErr)
		})
	}
}
