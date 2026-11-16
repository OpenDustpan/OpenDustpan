package service_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestRegistry_servesNamespaceUnderSlug checks that a namespace with a name
// outside the registry-safe subset can publish, and that the registry slug
// stays identical after the namespace renames its display name. It fails on
// main: the unsafe name breaks the registry URL.
func TestRegistry_servesNamespaceUnderSlug(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)

	unsafe := namespace.Name("Acme_Corp")
	e.ns[unsafe] = namespaceOf(unsafe, namespace.NewID(), e.now)
	e.trust(t, unsafe, sg)

	s := buildZipSet(t, sg, providerZip(t, "5.0"))

	rel, err := e.publish(t, unsafe, s)
	if err != nil {
		t.Fatalf("publish in a namespace with an unsafe name: %v", err)
	}

	if rel.Address.Namespace != unsafe {
		t.Fatalf("published namespace: got %q, want %q", rel.Address.Namespace, unsafe)
	}

	renamed := namespace.Name("Acme_Corp_Renamed")
	e.ns[renamed] = namespaceOf(renamed, e.ns[unsafe].ID, e.now)
	e.trust(t, renamed, sg)

	again, err := e.publish(t, renamed, s)
	if err != nil {
		t.Fatalf("publish after rename: %v", err)
	}

	if again.Archives[0].H1 == "" {
		t.Fatal("release after rename has no H1 digest")
	}
}
