package service_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_refusesProtocolMismatch checks that a publish derives the
// stored protocols from terraform-registry-manifest.json and refuses a stated
// value that differs. It fails on main: stated protocols win over the
// manifest.
func TestPublish_refusesProtocolMismatch(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildZipSet(t, sg, providerZip(t, "6.0"))
	req := e.stage(t, acme, s)
	req.Protocols = []string{"5.0"}

	if _, err := e.svc.Publish(t.Context(), e.caller, req); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("publish with stated 5.0 and manifest 6.0: got %v, want %v", err, registry.ErrInvalid)
	}

	requireNoRelease(t, e)
}

// TestPublish_storesManifestProtocols checks that stated protocols that match
// the manifest as a set are stored as the manifest protocols.
func TestPublish_storesManifestProtocols(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildZipSet(t, sg, providerZip(t, "5.0", "6.0"))
	req := e.stage(t, acme, s)
	req.Protocols = []string{"6.0", "5.0"}

	rel, err := e.svc.Publish(t.Context(), e.caller, req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if !slices.Equal(rel.Protocols, []string{"5.0", "6.0"}) {
		t.Fatalf("stored protocols: got %q, want the manifest protocols", rel.Protocols)
	}
}
