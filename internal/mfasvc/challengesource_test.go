package mfasvc_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/mfa"
)

// sourceRecorder is a passkey store that records the challenges that the
// service writes.
type sourceRecorder struct {
	mfa.PasskeyStore

	mu   sync.Mutex
	seen []mfa.Challenge
}

func (r *sourceRecorder) CreateChallenge(ctx context.Context, c mfa.Challenge, d mfa.Digest) error {
	r.mu.Lock()
	r.seen = append(r.seen, c)
	r.mu.Unlock()

	if err := r.PasskeyStore.CreateChallenge(ctx, c, d); err != nil {
		return fmt.Errorf("create the challenge: %w", err)
	}

	return nil
}

// TestSignInChallengeSource sends source addresses straight to the start of a
// sign-in, as if the HTTP adapter had checked nothing. The service turns the
// address into the budget key of the gate, and an address that has no valid key
// gets the one shared source, so no start has an open budget. Defense in depth:
// the store checks the source again.
func TestSignInChallengeSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		want    string
		group   string
	}{
		{"IPv4 address", "198.51.100.7", "198.51.100.7", "198.51.100.7"},
		{"IPv6 address is its /64 and /48", "2001:db8:0:1:ffff::2", "2001:db8:0:1::/64", "2001:db8::/48"},
		{"IPv4-mapped IPv6 address is the IPv4 address", "::ffff:198.51.100.7", "198.51.100.7", "198.51.100.7"},
		{"empty", "", mfa.UnknownSource, mfa.UnknownSource},
		{"line break", "198.51.100.7\r\nX-Injected: 1", mfa.UnknownSource, mfa.UnknownSource},
		{"control character", "198.51.100.7\x00", mfa.UnknownSource, mfa.UnknownSource},
		{"non-ASCII", "198.51.100.é", mfa.UnknownSource, mfa.UnknownSource},
		{"over the length", strings.Repeat("a", mfa.MaxSourceBytes+1), mfa.UnknownSource, mfa.UnknownSource},
		{"scope separator", "a|b", mfa.UnknownSource, mfa.UnknownSource},
		{"printable text that is no address", "not-an-address", mfa.UnknownSource, mfa.UnknownSource},
		{"another printable text", "x1", mfa.UnknownSource, mfa.UnknownSource},
		{
			"printable text of exactly 64 bytes",
			strings.Repeat("a", mfa.MaxSourceBytes),
			mfa.UnknownSource,
			mfa.UnknownSource,
		},
		{"IPv6 address with a zone", "fe80::1%eth0", mfa.UnknownSource, mfa.UnknownSource},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			rec := &sourceRecorder{PasskeyStore: f.store, mu: sync.Mutex{}, seen: nil}

			rp := counting(t, false)

			svc, err := mfasvc.New(mfasvc.Config{Store: rec, RP: rp, Audit: f.store, Now: f.clock.now, Log: nil})
			if err != nil {
				t.Fatalf("service: %v", err)
			}

			entry := base(f.clock.now(), "passkey:unknown")
			entry.SourceIP = tc.address

			_, err = svc.BeginAssertion(
				t.Context(), mfasvc.Actor{Entry: entry, PrincipalID: "", SessionID: ""}, mfa.CeremonySignIn,
				mfasvc.Binding{PersonID: "", PendingID: ""},
			)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}

			if len(rec.seen) != 1 || rec.seen[0].Source != tc.want || rec.seen[0].SourceGroup != tc.group {
				t.Fatalf(
					"stored challenges = %+v, want one with the source %q and the group %q",
					rec.seen,
					tc.want,
					tc.group,
				)
			}
		})
	}
}

// TestSignInChallengeSourceRefusal fills the budget of one address and checks
// that the service refuses the next start with mfa.ErrSourceChallengeLimit,
// and that another address still starts.
func TestSignInChallengeSourceRefusal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	begin := func(address string) error {
		entry := base(f.clock.now(), "passkey:unknown")
		entry.SourceIP = address

		_, err := f.svc.BeginAssertion(
			t.Context(), mfasvc.Actor{Entry: entry, PrincipalID: "", SessionID: ""}, mfa.CeremonySignIn,
			mfasvc.Binding{PersonID: "", PendingID: ""},
		)

		return err
	}

	for i := range mfa.MaxOpenSignInChallengesPerSource {
		if err := begin("2001:db8:0:1::" + fmt.Sprintf("%x", i+1)); err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
	}

	if err := begin("2001:db8:0:1:ffff::9"); !errors.Is(err, mfa.ErrSourceChallengeLimit) {
		t.Fatalf("begin over the count of the /64 = %v, want ErrSourceChallengeLimit", err)
	}

	if err := begin("2001:db8:0:2::1"); err != nil {
		t.Fatalf("begin of another /64: %v", err)
	}
}
