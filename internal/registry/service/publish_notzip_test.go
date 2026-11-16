package service_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestPublish_refusesNonZipArchive checks that a publish refuses staged bytes
// that are not a zip with registry.ErrBadArchive, stores no release and leaves
// no staged file. The check reads the bytes: the media type of the upload does
// not decide it. It fails if the zip check before the hash is removed.
func TestPublish_refusesNonZipArchive(t *testing.T) {
	t.Parallel()

	good := makeZip(t, []zipFile{fileEntry("terraform-provider-x", []byte("binary"))})

	cases := []struct {
		name string
		body []byte
	}{
		{name: "plain text", body: []byte("this is not a zip file")},
		{name: "gzip header", body: []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{name: "zip magic only", body: []byte("PK\x03\x04")},
		{name: "truncated zip", body: good[:len(good)/2]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, false)
			sg := newSigner(t)
			e.trust(t, acme, sg)

			s := buildZipSet(t, sg, tc.body)

			_, err := e.publish(t, acme, s)
			if !errors.Is(err, registry.ErrBadArchive) {
				t.Fatalf("publish of %s: got %v, want %v", tc.name, err, registry.ErrBadArchive)
			}

			requireNoRelease(t, e)

			for _, left := range e.listing(t, e.ids[acme], "staging") {
				if !strings.HasSuffix(left, "/") {
					t.Fatalf("staged file left after refused publish: %s", left)
				}
			}
		})
	}
}
