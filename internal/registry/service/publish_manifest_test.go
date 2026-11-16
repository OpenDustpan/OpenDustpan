package service_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_refusesStatedProtocolsWithoutManifest checks that a publish of
// an archive without terraform-registry-manifest.json is refused with
// registry.ErrInvalid, with or without stated protocols. It fails on main:
// stated protocols store without a manifest.
func TestPublish_refusesStatedProtocolsWithoutManifest(t *testing.T) {
	t.Parallel()

	for _, protocols := range [][]string{{"5.0"}, nil} {
		e := newEnv(t, false)
		sg := newSigner(t)
		e.trust(t, acme, sg)

		bare := makeZip(t, []zipFile{fileEntry("terraform-provider-payments", []byte("fake provider binary"))})
		s := buildZipSet(t, sg, bare)
		req := e.stage(t, acme, s)
		req.Protocols = protocols

		_, err := e.svc.Publish(t.Context(), e.caller, req)
		if !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("publish without manifest with protocols %q: got %v, want %v", protocols, err, registry.ErrInvalid)
		}

		requireNoRelease(t, e, "1.0.0")
	}
}

// TestPublish_refusesMalformedManifest checks that a publish of an archive
// with malformed JSON in terraform-registry-manifest.json is refused with
// registry.ErrBadArchive. It fails on main: the manifest is never read.
func TestPublish_refusesMalformedManifest(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	bad := makeZip(t, []zipFile{
		fileEntry("terraform-provider-payments", []byte("fake provider binary")),
		fileEntry("terraform-registry-manifest.json", []byte(`{"version":1,"metadata":{`)),
	})
	s := buildZipSet(t, sg, bad)

	_, err := e.publish(t, acme, s)
	if !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("publish with malformed manifest: got %v, want %v", err, registry.ErrBadArchive)
	}
}
