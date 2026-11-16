package service_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_refusesUnsafeProviderNamespace checks that a provider publish in
// a namespace whose name is outside the registry-safe subset is refused with
// registry.ErrInvalid. The registry serves providers under the namespace name,
// so the name must stay in the subset until the registry serves a slug. The
// slug, its stored index and its rename stability are not built yet.
func TestPublish_refusesUnsafeProviderNamespace(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)

	unsafe := namespace.Name("Acme_Corp")
	e.ns[unsafe] = namespaceOf(unsafe, namespace.NewID(), e.now)
	e.trust(t, unsafe, sg)

	_, err := e.publish(t, unsafe, buildZipSet(t, sg, providerZip(t, "5.0")))
	if !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("publish in a namespace with an unsafe name: got %v, want %v", err, registry.ErrInvalid)
	}
}
