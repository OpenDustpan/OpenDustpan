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
// not decide it. In the variant "other sum" the checksums file lists a hash
// that is not the hash of the body, so only a zip check before the hash can
// give registry.ErrBadArchive. It fails if the zip check of the measure step
// is removed.
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

	variants := []struct {
		name     string
		otherSum bool
	}{
		{name: "same sum", otherSum: false},
		{name: "other sum", otherSum: true},
	}

	for _, tc := range cases {
		for _, v := range variants {
			t.Run(tc.name+"/"+v.name, func(t *testing.T) {
				t.Parallel()

				var listed []byte
				if v.otherSum {
					listed = good
				}

				refuseNonZip(t, tc.body, listed)
			})
		}
	}
}

// refuseNonZip publishes body as the archive and fails the test unless the
// publish is refused with registry.ErrBadArchive, with no release and no
// staged file left. When listed is not nil, the checksums file lists the hash
// of listed in place of the hash of body.
func refuseNonZip(t *testing.T, body, listed []byte) {
	t.Helper()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := buildZipSet(t, sg, body)

	if listed != nil {
		other := buildZipSet(t, sg, listed)
		s.sums, s.sig = other.sums, other.sig
	}

	_, err := e.publish(t, acme, s)
	if !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("publish: got %v, want %v", err, registry.ErrBadArchive)
	}

	requireNoRelease(t, e)

	for _, left := range e.listing(t, e.ids[acme], "staging") {
		if !strings.HasSuffix(left, "/") {
			t.Fatalf("staged file left after refused publish: %s", left)
		}
	}
}
