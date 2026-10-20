package dustpan_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// newEveryModuleServer builds a server with every optional module turned on:
// jobs, runners, profiles, saved views, durable operations and the stores of
// idempotency. The description of the API holds all of them.
func newEveryModuleServer(t *testing.T) *dustpan.Server {
	t.Helper()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithDurableStore(p.st.DurableOperations()),
		dustpan.WithJobStore(p.st.Jobs()),
		dustpan.WithRunners(p.st.Runners()),
		dustpan.WithSavedViewStore(p.st.SavedViews()),
		dustpan.WithProfileStore(p.st.Profiles()),
		dustpan.WithIdempotencyStore(p.st.Idempotency(testWrapper(t))),
		dustpan.WithPublicURL("https://dustpan.example"),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}
