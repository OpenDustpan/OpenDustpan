package service_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_refusesProtocolMismatch checks that a publish with a manifest
// file refuses a stated protocol list that differs from the manifest. It
// fails on main: the publish has no manifest file role, and stated protocols
// are stored as they come.
func TestPublish_refusesProtocolMismatch(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	req := e.stage(t, acme, manifestSet(t, sg, "6.0"))
	req.Protocols = []string{"5.0"}

	if _, err := e.svc.Publish(t.Context(), e.caller, req); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("publish with stated 5.0 and manifest 6.0: got %v, want %v", err, registry.ErrInvalid)
	}

	requireNoRelease(t, e)
}

// TestPublish_storesManifestProtocols checks that stated protocols that match
// the manifest file as a set are stored as the manifest protocols, and that
// the manifest file is not stored as a file of the release.
func TestPublish_storesManifestProtocols(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := manifestSet(t, sg, "5.0", "6.0")
	req := e.stage(t, acme, s)
	req.Protocols = []string{"6.0", "5.0", "6.0"}

	rel, err := e.svc.Publish(t.Context(), e.caller, req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if !slices.Equal(rel.Protocols, []string{"5.0", "6.0"}) {
		t.Fatalf("stored protocols: got %q, want the manifest protocols", rel.Protocols)
	}

	if slices.ContainsFunc(rel.Files, func(f registry.StoredFile) bool { return f.SHA256 == hashOf(s.manifest) }) {
		t.Fatal("the manifest file is stored as a file of the release")
	}
}

// TestPublish_acceptsUnlistedManifestFile checks that a manifest file that
// the checksums file does not list is read: its protocols are stored.
func TestPublish_acceptsUnlistedManifestFile(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := withManifestFile(t, sg, buildZipSet(t, sg, binaryZip(t)), manifestJSON("6.0"), nil)
	req := e.stage(t, acme, s)
	req.Protocols = []string{"6.0"}

	rel, err := e.svc.Publish(t.Context(), e.caller, req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if !slices.Equal(rel.Protocols, []string{"6.0"}) {
		t.Fatalf("stored protocols: got %q, want the manifest protocols [6.0]", rel.Protocols)
	}
}
