package registrystore_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
)

const testSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testH1 is an H1 digest in the stored form: "h1:" and the base64 of 32 bytes.
const testH1 = "h1:ASNFZ4mrze8BI0VniavN7wEjRWeJq83vASNFZ4mrze8="

// providerRow returns a row of a provider release with the archives JSON
// archivesJSON.
func providerRow(archivesJSON string) registrystore.Row {
	return registrystore.Row{
		Namespace:       "acme",
		Kind:            string(registry.KindProvider),
		Name:            "payments",
		System:          "",
		Version:         "1.0.0",
		SumsFile:        "terraform-provider-payments_1.0.0_SHA256SUMS",
		SumsSHA256:      testSum,
		SignatureFile:   "terraform-provider-payments_1.0.0_SHA256SUMS.sig",
		SignatureSHA256: testSum,
		KeyFingerprint:  "CDCDCDCDCDCDCDCDCDCDCDCDCDCDCDCDCDCDCDCD",
		Protocols:       "5.0",
		PublishedBy:     "user-1",
		ArchivesJSON:    archivesJSON,
		PublishedAtMs:   time.UnixMilli(1_800_000_000_000).UTC().UnixMilli(),
		StoredFilesJSON: "",
	}
}

// TestArchives_roundTripH1 is hostile: the store must not drop the H1 digest
// of an archive. It fails when EncodeArchives leaves H1 out.
func TestArchives_roundTripH1(t *testing.T) {
	t.Parallel()

	for _, h1 := range []string{testH1, ""} {
		archives := []registry.Archive{{
			Platform: "linux_amd64",
			FileName: "terraform-provider-payments_1.0.0_linux_amd64.zip",
			SHA256:   testSum,
			Size:     3,
			H1:       h1,
		}}

		encoded, err := registrystore.EncodeArchives(archives)
		if err != nil {
			t.Fatalf("encode archives: %v", err)
		}

		got, err := registrystore.Release(providerRow(encoded))
		if err != nil {
			t.Fatalf("read release with H1 %q: %v", h1, err)
		}

		if len(got.Archives) != 1 || got.Archives[0].H1 != h1 {
			t.Fatalf("H1 did not survive the store round trip: %+v", got.Archives)
		}
	}
}

// hostileH1Case is one stored H1 value of another form.
type hostileH1Case struct {
	name string
	h1   string
}

// hostileH1 returns stored H1 values of another form. Each one must be
// refused.
func hostileH1() []hostileH1Case {
	return []hostileH1Case{
		{name: "no prefix", h1: strings.TrimPrefix(testH1, "h1:")},
		{name: "other prefix", h1: "h2:" + strings.TrimPrefix(testH1, "h1:")},
		{name: "upper prefix", h1: "H1:" + strings.TrimPrefix(testH1, "h1:")},
		{name: "short", h1: "h1:stored-digest-value"},
		{name: "long", h1: "h1:A" + strings.TrimPrefix(testH1, "h1:")},
		{name: "no padding", h1: strings.TrimSuffix(testH1, "=")},
		{name: "url alphabet", h1: "h1:-_" + strings.TrimPrefix(testH1, "h1:AS")},
		{name: "newline", h1: testH1 + "\n"},
		{name: "line break inside", h1: "h1:ASNFZ4mrze8BI0VniavN7wEjRWeJq83v\nSNFZ4mrze8="},
		{name: "space", h1: " " + testH1},
		{name: "second value", h1: testH1 + "," + testH1},
	}
}

// TestDecodeArchives_refusesHostileH1 sends each hostile H1 straight to the
// decode of the archives. It fails when the H1 check of the decode is removed:
// Release.Validate does not run here.
func TestDecodeArchives_refusesHostileH1(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileH1() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := registrystore.DecodeArchivesJSON(archivesJSON(t, tc.h1))
			if !errors.Is(err, registrystore.ErrCorrupt) {
				t.Fatalf("decode H1 %q: got %v, want %v", tc.h1, err, registrystore.ErrCorrupt)
			}
		})
	}
}

// TestRelease_refusesHostileH1 reads a row with each hostile H1, as a restored
// backup or a row of another version can hold. The row is refused as corrupt.
func TestRelease_refusesHostileH1(t *testing.T) {
	t.Parallel()

	for _, tc := range hostileH1() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := registrystore.Release(providerRow(archivesJSON(t, tc.h1)))
			if !errors.Is(err, registrystore.ErrCorrupt) {
				t.Fatalf("read release with H1 %q: got %v, want %v", tc.h1, err, registrystore.ErrCorrupt)
			}
		})
	}
}

// archivesJSON returns the archives JSON of one archive with the H1 h1. It
// writes the JSON by hand, so no check of EncodeArchives can stop the value.
func archivesJSON(t *testing.T, h1 string) string {
	t.Helper()

	b, err := json.Marshal([]map[string]any{{
		"platform": "linux_amd64",
		"fileName": "terraform-provider-payments_1.0.0_linux_amd64.zip",
		"sha256":   testSum,
		"size":     3,
		"h1":       h1,
	}})
	if err != nil {
		t.Fatalf("marshal archives: %v", err)
	}

	return string(b)
}
