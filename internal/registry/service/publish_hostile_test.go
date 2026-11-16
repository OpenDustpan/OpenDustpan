package service_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_serviceProtocolHostile checks that the service matches stated
// protocols against the manifest as an exact set and fails closed without a
// manifest. It fails on main: false protocols store.
func TestPublish_serviceProtocolHostile(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		manifest []string
		stated   []string
	}{
		"subset":       {manifest: []string{"5.0", "6.0"}, stated: []string{"5.0"}},
		"superset":     {manifest: []string{"5.0"}, stated: []string{"5.0", "6.0"}},
		"other":        {manifest: []string{"6.0"}, stated: []string{"5.0"}},
		"stated empty": {manifest: []string{"5.0"}, stated: nil},
		"unknown":      {manifest: []string{"7.0"}, stated: []string{"7.0"}},
		"no manifest":  {manifest: nil, stated: []string{"5.0"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, acme, sg)

			var data []byte
			if tc.manifest == nil {
				data = makeZip(t, []zipFile{fileEntry("terraform-provider-payments", []byte("x"))})
			} else {
				data = providerZip(t, tc.manifest...)
			}

			s := buildZipSet(t, sg, data)
			req := e.stage(t, acme, s)
			req.Protocols = tc.stated

			_, err := e.svc.Publish(t.Context(), e.caller, req)
			if !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("publish with stated %q and manifest %q: got %v, want %v",
					tc.stated, tc.manifest, err, registry.ErrInvalid)
			}
		})
	}
}
