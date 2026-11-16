package service

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// TestManifestProtocols_hostile sends each hostile manifest body straight to
// manifestProtocols, as if the measure step had failed. Each case names the
// text of the error of its own guard, so a case fails when its guard is
// removed.
func TestManifestProtocols_hostile(t *testing.T) {
	t.Parallel()

	valid := `{"version":1,"metadata":{"protocol_versions":["5.0"]}}`

	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "over the bound", body: append([]byte(valid), bytes.Repeat([]byte{' '}, maxManifestBytes)...), want: "larger than"},
		{name: "not JSON", body: []byte(`{"version":1,"metadata":{`), want: "not JSON"},
		{name: "a JSON array", body: []byte(`["5.0"]`), want: "not JSON"},
		{name: "two values", body: []byte(valid + ` {"version":1}`), want: "more than one value"},
		{name: "no version", body: []byte(`{"metadata":{"protocol_versions":["5.0"]}}`), want: "not manifest version 1"},
		{name: "version 2", body: []byte(`{"version":2,"metadata":{"protocol_versions":["5.0"]}}`), want: "not manifest version 1"},
		{name: "version as text", body: []byte(`{"version":"1","metadata":{"protocol_versions":["5.0"]}}`), want: "not JSON"},
		{name: "no metadata", body: []byte(`{"version":1}`), want: "lists no protocol"},
		{name: "empty list", body: []byte(`{"version":1,"metadata":{"protocol_versions":[]}}`), want: "lists no protocol"},
		{name: "unknown value", body: []byte(`{"version":1,"metadata":{"protocol_versions":["7.0"]}}`), want: "is not known"},
		{name: "known and unknown", body: []byte(`{"version":1,"metadata":{"protocol_versions":["5.0","4.0"]}}`), want: "is not known"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := manifestProtocols("manifest.json", tc.body)
			if !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("manifest %q: got %v, want %v", tc.name, err, registry.ErrInvalid)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("manifest %q: got %q, want the error of the guard %q", tc.name, err, tc.want)
			}
		})
	}
}

// TestManifestProtocols_valid checks that a valid manifest gives its
// protocols sorted and without duplicates, also at the bound, so the hostile
// cases test the guard and not another fault of the body.
func TestManifestProtocols_valid(t *testing.T) {
	t.Parallel()

	body := []byte(`{"version":1,"metadata":{"protocol_versions":["6.0","5.0","6.0"]}}`)
	body = append(body, bytes.Repeat([]byte{' '}, maxManifestBytes-len(body))...)

	got, err := manifestProtocols("manifest.json", body)
	if err != nil {
		t.Fatalf("manifest at the bound: %v", err)
	}

	if !slices.Equal(got, []string{"5.0", "6.0"}) {
		t.Fatalf("protocols: got %q, want [5.0 6.0]", got)
	}
}

// manifestFile returns a measured manifest file with the given hash.
func manifestFile(sum string) *measured {
	f := archiveFile("payments_1.0.0_manifest.json")
	f.Role, f.sum = RoleManifest, sum

	return &f
}

// TestCheckManifestSum checks the manifest file against the lines of the
// checksums file: another digest is refused, the same digest removes the line
// so the archive check sees archives only, and an unlisted manifest passes.
// It fails when the digest check is removed.
func TestCheckManifestSum(t *testing.T) {
	t.Parallel()

	sums := map[string]string{"archive.zip": "aa", "payments_1.0.0_manifest.json": "bb"}

	if _, err := checkManifestSum(manifestFile("cc"), sums); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("manifest with another digest: got %v, want %v", err, registry.ErrInvalid)
	}

	got, err := checkManifestSum(manifestFile("bb"), sums)
	if err != nil {
		t.Fatalf("manifest with the listed digest: %v", err)
	}

	if _, ok := got["payments_1.0.0_manifest.json"]; ok || len(got) != 1 || len(sums) != 2 {
		t.Fatalf("lines after the check: got %v, want the archive line only and the input unchanged", got)
	}

	unlisted := map[string]string{"archive.zip": "aa"}
	if got, err := checkManifestSum(manifestFile("cc"), unlisted); err != nil || len(got) != 1 {
		t.Fatalf("unlisted manifest: got %v, %v, want the lines unchanged", got, err)
	}
}

// TestReleaseProtocols checks the protocols to store: the manifest protocols
// when the stated list matches as a set, a refusal when it differs, and the
// stated list without a manifest.
func TestReleaseProtocols(t *testing.T) {
	t.Parallel()

	m := manifestFile("bb")
	m.bytes = []byte(`{"version":1,"metadata":{"protocol_versions":["5.0","6.0"]}}`)

	got, err := releaseProtocols([]string{"6.0", "5.0", "5.0"}, m)
	if err != nil || !slices.Equal(got, []string{"5.0", "6.0"}) {
		t.Fatalf("matching set: got %q, %v, want [5.0 6.0]", got, err)
	}

	for _, stated := range [][]string{{"5.0"}, {"5.0", "6.0", "7.0"}, nil} {
		if _, refuseErr := releaseProtocols(stated, m); !errors.Is(refuseErr, registry.ErrInvalid) {
			t.Fatalf("stated %q: got %v, want %v", stated, refuseErr, registry.ErrInvalid)
		}
	}

	got, err = releaseProtocols([]string{"5.0"}, nil)
	if err != nil || !slices.Equal(got, []string{"5.0"}) {
		t.Fatalf("no manifest: got %q, %v, want the stated [5.0]", got, err)
	}
}

// TestCheckFileList_manifestRole checks that the manifest role is accepted
// once and refused two times. It fails when the role count check is removed.
func TestCheckFileList_manifestRole(t *testing.T) {
	t.Parallel()

	base := []StagedFile{
		{Role: RoleSums, Name: "sums", Token: "t1"},
		{Role: RoleSignature, Name: "sig", Token: "t2"},
		{Role: RoleArchive, Name: "a.zip", Token: "t3"},
	}

	one := append(slices.Clone(base), StagedFile{Role: RoleManifest, Name: "m1.json", Token: "t4"})
	if err := checkFileList(one); err != nil {
		t.Fatalf("one manifest file: %v", err)
	}

	two := append(slices.Clone(one), StagedFile{Role: RoleManifest, Name: "m2.json", Token: "t5"})
	if err := checkFileList(two); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("two manifest files: got %v, want %v", err, ErrBadRequest)
	}
}
