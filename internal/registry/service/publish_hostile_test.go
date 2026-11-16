package service_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_serviceProtocolHostile sends hostile manifest files and
// protocol lists through the publish. Each is refused with
// registry.ErrInvalid, names the failed rule, and stores nothing. It fails on
// main: the publish has no manifest file role.
func TestPublish_serviceProtocolHostile(t *testing.T) {
	t.Parallel()

	listed := func(body []byte) func(*testing.T, pgpfixture.Signer) *set {
		return func(t *testing.T, sg pgpfixture.Signer) *set {
			t.Helper()

			return withManifestFile(t, sg, buildZipSet(t, sg, binaryZip(t)), body, body)
		}
	}

	// The manifest bound is 64 KiB, and the staging bound of the test service
	// is 1 MiB, so the body reaches the publish.
	over := append(manifestJSON("5.0"), bytes.Repeat([]byte{' '}, 64<<10)...)

	for name, tc := range map[string]struct {
		build  func(*testing.T, pgpfixture.Signer) *set
		stated []string
		want   string
	}{
		"over the size limit": {build: listed(over), stated: []string{"5.0"}, want: "larger than"},
		"not JSON": {
			build: listed([]byte(`{"version":1,"metadata":{`)), stated: []string{"5.0"}, want: "not JSON",
		},
		"version 2": {
			build:  listed([]byte(`{"version":2,"metadata":{"protocol_versions":["5.0"]}}`)),
			stated: []string{"5.0"}, want: "not manifest version 1",
		},
		"empty list":    {build: listed(manifestJSON()), stated: []string{"5.0"}, want: "lists no protocol"},
		"unknown value": {build: listed(manifestJSON("7.0")), stated: []string{"7.0"}, want: "is not known"},
		"subset":        {build: listed(manifestJSON("5.0", "6.0")), stated: []string{"5.0"}, want: "differ"},
		"superset":      {build: listed(manifestJSON("5.0")), stated: []string{"5.0", "6.0"}, want: "differ"},
		"stated empty":  {build: listed(manifestJSON("5.0")), stated: nil, want: "differ"},
		"digest differs from the checksums file": {
			build: func(t *testing.T, sg pgpfixture.Signer) *set {
				t.Helper()

				return withManifestFile(t, sg, buildZipSet(t, sg, binaryZip(t)), manifestJSON("6.0"), manifestJSON("5.0"))
			},
			stated: []string{"6.0"}, want: "another digest",
		},
		"manifest in the zip and no manifest file": {
			build: func(t *testing.T, sg pgpfixture.Signer) *set {
				t.Helper()

				return buildZipSet(t, sg, providerZip(t, "5.0"))
			},
			stated: nil, want: "protocol",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, acme, sg)

			req := e.stage(t, acme, tc.build(t, sg))
			req.Protocols = tc.stated

			_, err := e.svc.Publish(t.Context(), e.caller, req)
			if !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("publish: got %v, want %v", err, registry.ErrInvalid)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("publish: got %q, want the rule %q named", err, tc.want)
			}

			requireNoRelease(t, e)

			if got := e.listing(t, e.ids[acme], "sha256"); len(got) != 0 {
				t.Fatalf("a refused publish left content keys: %v", got)
			}
		})
	}
}
