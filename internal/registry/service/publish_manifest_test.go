package service_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_withoutManifestFile checks a publish with no manifest file: the
// stated protocols are stored, also when the zip holds an entry named
// terraform-registry-manifest.json with other protocols, and a publish that
// states no protocols is refused with registry.ErrInvalid. It fails on main:
// the zip manifest decides the protocols.
func TestPublish_withoutManifestFile(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]func(t *testing.T) []byte{
		"binary only":           binaryZip,
		"manifest entry in zip": func(t *testing.T) []byte { t.Helper(); return providerZip(t, "6.0") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, acme, sg)

			req := e.stage(t, acme, buildZipSet(t, sg, data(t)))
			req.Protocols = []string{"5.0"}

			rel, err := e.svc.Publish(t.Context(), e.caller, req)
			if err != nil {
				t.Fatalf("publish with stated 5.0 and no manifest file: %v", err)
			}

			if !slices.Equal(rel.Protocols, []string{"5.0"}) {
				t.Fatalf("stored protocols: got %q, want the stated [5.0]", rel.Protocols)
			}
		})
	}

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	req := e.stage(t, acme, buildZipSet(t, sg, binaryZip(t)))
	req.Protocols = nil

	if _, err := e.svc.Publish(t.Context(), e.caller, req); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("publish with no manifest file and no stated protocols: got %v, want %v", err, registry.ErrInvalid)
	}

	requireNoRelease(t, e)
}
