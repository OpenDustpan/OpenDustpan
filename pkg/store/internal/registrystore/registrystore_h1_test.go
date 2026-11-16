package registrystore_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
)

const testSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestArchives_roundTripH1 is hostile: the store must not drop the H1 digest
// of an archive. It fails when EncodeArchives leaves H1 out.
func TestArchives_roundTripH1(t *testing.T) {
	t.Parallel()

	archives := []registry.Archive{{
		Platform: "linux_amd64",
		FileName: "terraform-provider-payments_1.0.0_linux_amd64.zip",
		SHA256:   testSum,
		Size:     3,
		H1:       "h1:stored-digest-value",
	}}

	encoded, err := registrystore.EncodeArchives(archives)
	if err != nil {
		t.Fatalf("encode archives: %v", err)
	}

	got, err := registrystore.Release(registrystore.Row{
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
		ArchivesJSON:    encoded,
		PublishedAtMs:   time.UnixMilli(1_800_000_000_000).UTC().UnixMilli(),
		StoredFilesJSON: "",
	})
	if err != nil {
		t.Fatalf("read release: %v", err)
	}

	if len(got.Archives) != 1 || got.Archives[0].H1 != "h1:stored-digest-value" {
		t.Fatalf("H1 did not survive the store round trip: %+v", got.Archives)
	}
}
