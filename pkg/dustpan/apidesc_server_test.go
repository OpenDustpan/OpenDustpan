package dustpan_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
)

// newEveryModuleServer builds a server with every optional module turned on:
// jobs, runners, profiles, saved views, durable operations and the stores of
// idempotency. The description of the API holds all of them.
func newEveryModuleServer(t *testing.T) *dustpan.Server {
	t.Helper()

	return newModuleServer(t)
}

// newDevModuleServer builds the server of newEveryModuleServer with the
// installation kind dev and the dev sign-in, which adds the route of the
// one-time code.
func newDevModuleServer(t *testing.T) *dustpan.Server {
	t.Helper()

	code, _, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make code: %v", err)
	}

	return newModuleServer(t, dustpan.WithInstallationKind(installation.KindDev), devSignInOption(t, code))
}

// newModuleServer builds the server of newEveryModuleServer with more
// options.
func newModuleServer(t *testing.T, extra ...dustpan.Option) *dustpan.Server {
	t.Helper()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithDurableStore(p.st.DurableOperations()),
		dustpan.WithJobStore(p.st.Jobs()),
		dustpan.WithRunners(p.st.Runners()),
		dustpan.WithSavedViewStore(p.st.SavedViews()),
		dustpan.WithProfileStore(p.st.Profiles()),
		dustpan.WithGitHostStore(p.st.GitHost()),
		dustpan.WithSinkStore(p.st.NotificationSinks()),
		dustpan.WithIdempotencyStore(p.st.Idempotency(testWrapper(t))),
		dustpan.WithPublicURL("https://dustpan.example"),
	)
	opts = append(opts, extra...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}
