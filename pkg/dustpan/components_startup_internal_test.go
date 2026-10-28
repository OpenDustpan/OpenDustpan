package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestApiOnlyStartLeavesTheMigrationPending proves that the start of a
// process without the worker component claims no durable operation: a
// migration of the audit chain that an earlier instance left stays pending,
// and the start reads the stored record. The control case shows that the same
// start with the worker component runs it.
func TestApiOnlyStartLeavesTheMigrationPending(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		runs        bool
		wantPending bool
	}{
		{name: "api only", runs: false, wantPending: true},
		{name: "with the worker", runs: true, wantPending: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain, _, op := setupTakeoverChain(t)
			chain.runsMigrations = tc.runs

			if _, err := chain.startUpRecord(t.Context()); err != nil {
				t.Fatalf("start-up record: %v", err)
			}

			got, err := chain.durableOps.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatalf("get operation: %v", err)
			}

			if pending := got.State == durable.StatePending; pending != tc.wantPending {
				t.Fatalf("operation state %q after the start, pending = %v, want %v",
					got.State, pending, tc.wantPending)
			}

			if !tc.runs && got.Attempt != 0 {
				t.Fatalf("operation attempt = %d after an api-only start, want 0: nothing claimed it", got.Attempt)
			}
		})
	}
}

// failingBindings is a provider store that fails each bind, so that the
// result of the bind step shows whether it ran.
type failingBindings struct {
	session.ProviderStore
}

func (failingBindings) BindProvider(context.Context, string, string, time.Time) (string, error) {
	return "", errBindFailed
}

var errBindFailed = errors.New("bind failed")

// TestWorkerOnlyStartBindsNoSignInProviders proves that a process without
// the api component neither binds the sign-in providers nor fails at one.
func TestWorkerOnlyStartBindsNoSignInProviders(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  Components
		want bool
	}{
		{name: "worker only", set: workerOnly(), want: false},
		{name: "api only", set: apiOnly(), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := testServer(nil, "a")
			srv.components, srv.now = tc.set, time.Now
			srv.signInProviders = providerBinding{
				store:     failingBindings{ProviderStore: nil},
				providers: []session.Provider{{ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: ""}},
			}

			err := srv.bindSignInProviders(t.Context())
			if ran := err != nil; ran != tc.want {
				t.Fatalf("bind step error = %v, want an error = %v", err, tc.want)
			}
		})
	}
}
