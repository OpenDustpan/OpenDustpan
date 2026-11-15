package service_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestRegistryMirror_IndexListsActiveVersions holds the rule of the provider
// network mirror: the index lists the active, servable versions of the
// namespace, an unknown provider answers ErrNotFound, and a read of another
// namespace answers ErrNotFound with no data of that namespace.
func TestRegistryMirror_IndexListsActiveVersions(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	for _, version := range []string{"1.0.0", "2.0.0"} {
		if _, err := e.publish(t, acme, buildSet(t, sg, "payments", version, "linux_amd64")); err != nil {
			t.Fatalf("Publish %s: %v", version, err)
		}
	}

	rels, err := e.svc.MirrorIndex(t.Context(), acme, "payments")
	if err != nil {
		t.Fatalf("MirrorIndex: %v", err)
	}

	if len(rels) != 2 || rels[0].Version != "1.0.0" || rels[1].Version != "2.0.0" {
		t.Fatalf("MirrorIndex = %v, want 1.0.0 and 2.0.0", rels)
	}

	if _, err := e.svc.MirrorIndex(t.Context(), acme, "unknown"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("MirrorIndex of an unknown provider = %v, want ErrNotFound", err)
	}

	if _, err := e.svc.MirrorIndex(t.Context(), beta, "payments"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("MirrorIndex of another namespace = %v, want ErrNotFound", err)
	}
}
