package registry_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/registry"
)

func addr(kind registry.Kind, name, system string) registry.Address {
	return registry.Address{Namespace: "acme", Kind: kind, Name: name, System: system}
}

const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func provider() registry.Release {
	return registry.Release{
		Address:        addr(registry.KindProvider, "payments", ""),
		Version:        "1.2.3",
		SumsFile:       "terraform-provider-payments_1.2.3_SHA256SUMS",
		SumsSHA256:     sum,
		KeyFingerprint: "",
		PublishedBy:    "user-1",
		PublishedAt:    time.UnixMilli(1_800_000_000_000).UTC(),
		Archives: []registry.Archive{
			{
				Platform: "linux_amd64",
				FileName: "terraform-provider-payments_1.2.3_linux_amd64.zip",
				SHA256:   sum,
				Size:     10,
			},
			{
				Platform: "darwin_arm64",
				FileName: "terraform-provider-payments_1.2.3_darwin_arm64.zip",
				SHA256:   sum,
				Size:     10,
			},
		},
	}
}

func TestAddressValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr registry.Address
		ok   bool
	}{
		{"provider", addr(registry.KindProvider, "payments", ""), true},
		{"module", addr(registry.KindModule, "vpc", "aws"), true},
		{
			"provider with system",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "p", System: "aws"},
			false,
		},
		{
			"module without system",
			registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "vpc", System: ""},
			false,
		},
		{"unknown kind", registry.Address{Namespace: "acme", Kind: "plugin", Name: "p", System: ""}, false},
		{
			"path in name",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "../x", System: ""},
			false,
		},
		{
			"slash in name",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a/b", System: ""},
			false,
		},
		{
			"upper case",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "Payments", System: ""},
			false,
		},
		{"newline", registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a\nb", System: ""}, false},
		{
			"leading dash",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "-a", System: ""},
			false,
		},
		{
			"too long",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: strings.Repeat("a", 65), System: ""},
			false,
		},
		{"bad namespace", registry.Address{Namespace: "", Kind: registry.KindProvider, Name: "p", System: ""}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.addr.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, registry.ErrInvalid)) {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	t.Parallel()

	for v, ok := range map[string]bool{
		"1.2.3": true, "0.0.1": true, "1.0.0-rc.1": true,
		"1.2": false, "v1.2.3": false, "01.2.3": false, "1.2.3+build": false, "": false, "1.2.3\n": false, "../1.2.3": false,
	} {
		err := registry.ValidateVersion(v)
		if ok != (err == nil) {
			t.Errorf("ValidateVersion(%q) = %v, want ok=%v", v, err, ok)
		}
	}
}

func TestReleaseValidate(t *testing.T) {
	t.Parallel()

	mod := func() registry.Release {
		return registry.Release{
			Address:        addr(registry.KindModule, "vpc", "aws"),
			Version:        "1.0.0",
			SumsFile:       "",
			SumsSHA256:     "",
			KeyFingerprint: "",
			PublishedBy:    "user-1",
			PublishedAt:    time.UnixMilli(1_800_000_000_000).UTC(),
			Archives:       []registry.Archive{{Platform: "", FileName: "vpc-1.0.0.zip", SHA256: sum, Size: 5}},
		}
	}

	tests := []struct {
		mutate func(r *registry.Release)
		name   string
		ok     bool
	}{
		{func(*registry.Release) {}, "provider", true},
		{func(r *registry.Release) { *r = mod() }, "module", true},
		{func(r *registry.Release) { r.Archives = nil }, "no archive", false},
		{func(r *registry.Release) { r.SumsFile = "" }, "no sums file", false},
		{func(r *registry.Release) { r.SumsSHA256 = "ABC" }, "bad sums hash", false},
		{func(r *registry.Release) { r.Archives[0].FileName = "../a.zip" }, "path in file name", false},
		{func(r *registry.Release) { r.Archives[0].SHA256 = strings.ToUpper(sum) }, "upper case hash", false},
		{func(r *registry.Release) { r.Archives[0].Size = 0 }, "zero size", false},
		{func(r *registry.Release) { r.Archives[1].Platform = "linux_amd64" }, "duplicate platform", false},
		{func(r *registry.Release) { r.Archives[1].FileName = r.Archives[0].FileName }, "duplicate file", false},
		{func(r *registry.Release) { r.Archives[0].Platform = "linux" }, "bad platform", false},
		{func(r *registry.Release) { r.PublishedBy = "" }, "no publisher", false},
		{func(r *registry.Release) { r.Version = "latest" }, "bad version", false},
		{
			func(r *registry.Release) { *r = mod(); r.Archives[0].Platform = "linux_amd64" },
			"module with platform",
			false,
		},
		{func(r *registry.Release) { *r = mod(); r.SumsFile = "x" }, "module with sums", false},
		{func(r *registry.Release) {
			r.Archives = make([]registry.Archive, registry.MaxArchives+1)
		}, "too many archives", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := provider()
			r.Archives = append([]registry.Archive(nil), r.Archives...)
			tc.mutate(&r)

			err := r.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, registry.ErrInvalid)) {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestBlobKey(t *testing.T) {
	t.Parallel()

	key, keyErr := registry.BlobKey("acme", sum)
	if keyErr != nil || key.String() != "registry/acme/sha256/"+sum {
		t.Fatalf("BlobKey = %q, %v", key, keyErr)
	}

	for _, bad := range []string{"", "abc", sum + "0", strings.ToUpper(sum), "../" + sum[3:]} {
		if _, badErr := registry.BlobKey("acme", bad); !errors.Is(badErr, registry.ErrInvalid) {
			t.Errorf("BlobKey(%q) = %v, want ErrInvalid", bad, badErr)
		}
	}

	if _, nsErr := registry.BlobKey("", sum); !errors.Is(nsErr, registry.ErrInvalid) {
		t.Errorf("BlobKey with no namespace = %v, want ErrInvalid", nsErr)
	}

	// Two namespaces never share a key.
	other, otherErr := registry.BlobKey("beta", sum)
	if otherErr != nil || other == key {
		t.Fatal("the keys of two namespaces are equal")
	}
}

func TestSameContent(t *testing.T) {
	t.Parallel()

	a, b := provider(), provider()
	b.PublishedBy, b.PublishedAt = "user-2", b.PublishedAt.Add(time.Hour)
	b.Archives[0], b.Archives[1] = b.Archives[1], b.Archives[0]

	if !a.SameContent(b) {
		t.Fatal("same hashes in another order and by another publisher must be the same content")
	}

	b.Archives[0].SHA256 = strings.Repeat("a", 64)

	if a.SameContent(b) {
		t.Fatal("a changed hash must be other content")
	}
}
