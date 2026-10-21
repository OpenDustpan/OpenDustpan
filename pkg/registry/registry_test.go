package registry_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

func addr(kind registry.Kind, name, system string) registry.Address {
	return registry.Address{Namespace: "acme", Kind: kind, Name: name, System: system}
}

const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func provider() registry.Release {
	return registry.Release{
		Address:         addr(registry.KindProvider, "payments", ""),
		Version:         "1.2.3",
		SumsFile:        "terraform-provider-payments_1.2.3_SHA256SUMS",
		SumsSHA256:      sum,
		SignatureFile:   "terraform-provider-payments_1.2.3_SHA256SUMS.sig",
		SignatureSHA256: sum,
		KeyFingerprint:  strings.Repeat("CD", 20),
		Protocols:       []string{"5.0"},
		PublishedBy:     "user-1",
		PublishedAt:     time.UnixMilli(1_800_000_000_000).UTC(),
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
		{
			"namespace with dashes",
			registry.Address{Namespace: "acme-co", Kind: registry.KindProvider, Name: "a-b", System: ""},
			true,
		},
		{
			"namespace upper case",
			registry.Address{Namespace: "Acme", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"namespace with dot",
			registry.Address{Namespace: "acme.io", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"namespace with underscore",
			registry.Address{Namespace: "acme_co", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"namespace with double dash",
			registry.Address{Namespace: "acme--co", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"namespace leading dash",
			registry.Address{Namespace: "-acme", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"namespace trailing dash",
			registry.Address{Namespace: "acme-", Kind: registry.KindProvider, Name: "p", System: ""},
			false,
		},
		{
			"name with dot",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a.b", System: ""},
			false,
		},
		{
			"name with underscore",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a_b", System: ""},
			false,
		},
		{
			"module name with underscore",
			registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "a_b", System: "aws"},
			false,
		},
		{
			"name with double dash",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a--b", System: ""},
			false,
		},
		{
			"name trailing dash",
			registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "a-", System: ""},
			false,
		},
		{
			"system with dash",
			registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "vpc", System: "a-b"},
			false,
		},
		{
			"system upper case",
			registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "vpc", System: "AWS"},
			false,
		},
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

// TestAddressRuleInError checks that the refusal of a namespace that cannot
// publish names the rule.
func TestAddressRuleInError(t *testing.T) {
	t.Parallel()

	err := registry.Address{Namespace: "Acme", Kind: registry.KindProvider, Name: "p", System: ""}.Validate()
	if err == nil || !strings.Contains(err.Error(), "lowercase letters, digits and single dashes") {
		t.Fatalf("Validate = %v, want the rule in the text", err)
	}
}

func TestValidateVersion(t *testing.T) {
	t.Parallel()

	for v, ok := range map[string]bool{
		"1.2.3": true, "0.0.1": true, "1.0.0-rc.1": true, "1.0.0-0": true, "1.0.0-rc.0": true, "1.0.0-a01": true,
		"1.0.0-01": false, "1.0.0-rc.01": false, "1.0.0-00": false,
		"1.2": false, "99999999999999999999.0.0": false, "v1.2.3": false, "01.2.3": false, "1.2.3+build": false, "": false, "1.2.3\n": false, "../1.2.3": false,
	} {
		err := registry.ValidateVersion(v)
		if ok != (err == nil) {
			t.Errorf("ValidateVersion(%q) = %v, want ok=%v", v, err, ok)
		}
	}
}

// TestValidateVersionPreReleaseBits proves that a numeric pre-release
// identifier fits 64 bits, as a version number does, and that an identifier
// with a letter has no such limit.
func TestValidateVersionPreReleaseBits(t *testing.T) {
	t.Parallel()

	for v, ok := range map[string]bool{
		"1.0.0-18446744073709551615":     true,
		"1.0.0-18446744073709551616":     false,
		"1.0.0-99999999999999999999":     false,
		"1.0.0-rc.99999999999999999999":  false,
		"1.0.0-rc.99999999999999999999x": true,
	} {
		err := registry.ValidateVersion(v)
		if ok != (err == nil) {
			t.Errorf("ValidateVersion(%q) = %v, want ok=%v", v, err, ok)
		}
	}
}

// signed adds a verified signature to a provider release.
func signed(r *registry.Release) {
	r.SignatureFile = r.SumsFile + ".sig"
	r.SignatureSHA256 = sum
	r.KeyFingerprint = strings.Repeat("AB", 20)
}

func TestReleaseValidate(t *testing.T) {
	t.Parallel()

	mod := func() registry.Release {
		return registry.Release{
			Address:         addr(registry.KindModule, "vpc", "aws"),
			Version:         "1.0.0",
			SumsFile:        "",
			SumsSHA256:      "",
			SignatureFile:   "",
			SignatureSHA256: "",
			KeyFingerprint:  "",
			Protocols:       nil,
			PublishedBy:     "user-1",
			PublishedAt:     time.UnixMilli(1_800_000_000_000).UTC(),
			Archives:        []registry.Archive{{Platform: "", FileName: "vpc-1.0.0.zip", SHA256: sum, Size: 5}},
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
		{func(r *registry.Release) { r.Archives[1].FileName = r.SumsFile }, "archive named as the sums file", false},
		{
			func(r *registry.Release) { signed(r); r.Archives[1].FileName = r.SignatureFile },
			"archive named as the signature file",
			false,
		},
		{signed, "signed", true},
		{
			func(r *registry.Release) { r.SignatureFile, r.SignatureSHA256, r.KeyFingerprint = "", "", "" },
			"unsigned provider",
			false,
		},
		{func(r *registry.Release) { signed(r); r.SignatureFile = "" }, "fingerprint without signature file", false},
		{func(r *registry.Release) { signed(r); r.SignatureSHA256 = "" }, "fingerprint without signature hash", false},
		{func(r *registry.Release) { signed(r); r.KeyFingerprint = "" }, "signature without fingerprint", false},
		{func(r *registry.Release) { signed(r); r.KeyFingerprint = "abcd" }, "bad fingerprint", false},
		{
			func(r *registry.Release) { signed(r); r.SignatureFile = r.SumsFile },
			"signature file named as the sums file",
			false,
		},
		{
			func(r *registry.Release) { *r = mod(); r.KeyFingerprint = strings.Repeat("A", 40) },
			"module with fingerprint",
			false,
		},
		{func(r *registry.Release) { r.Version = "latest" }, "bad version", false},
		{
			func(r *registry.Release) { *r = mod(); r.Archives[0].Platform = "linux_amd64" },
			"module with platform",
			false,
		},
		{func(r *registry.Release) { *r = mod(); r.SumsFile = "x" }, "module with sums", false},
		{func(r *registry.Release) { *r = mod(); r.Protocols = []string{"5.0"} }, "module with protocols", false},
		{func(r *registry.Release) { r.Protocols = nil }, "provider with no protocols", false},
		{func(r *registry.Release) { r.Protocols = []string{"5.0", "6.0"} }, "provider with two protocols", true},
		{func(r *registry.Release) { r.Protocols = []string{"5.10", "6.0"} }, "protocol with two digits", true},
		{func(r *registry.Release) { r.Protocols = []string{"6.0", "5.0"} }, "falling protocols", false},
		{func(r *registry.Release) { r.Protocols = []string{"5.0", "5.0"} }, "repeated protocol", false},
		{func(r *registry.Release) { r.Protocols = []string{"5.9", "5.10"} }, "protocols compare as numbers", true},
		{func(r *registry.Release) { r.Protocols = []string{"5"} }, "protocol with no minor", false},
		{func(r *registry.Release) { r.Protocols = []string{"5.0\n"} }, "protocol with a newline", false},
		{
			func(r *registry.Release) { r.Protocols = []string{"1.0", "2.0", "3.0", "4.0", "5.0"} },
			"five protocols",
			false,
		},
		{func(r *registry.Release) {
			r.Archives = make([]registry.Archive, registry.MaxArchives+1)
		}, "too many archives", false},
		{func(r *registry.Release) {
			r.Archives = make([]registry.Archive, registry.MaxArchives)
			for i := range r.Archives {
				r.Archives[i] = registry.Archive{
					Platform: "os" + strconv.Itoa(
						i,
					) + "_amd64",
					FileName: "p" + strconv.Itoa(i) + ".zip",
					SHA256:   sum,
					Size:     1,
				}
			}
		}, "64 platforms", true},
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

const nsID namespace.ID = "ns-abcdefghij0123456789"

func TestBlobKey(t *testing.T) {
	t.Parallel()

	key, keyErr := registry.BlobKey(nsID, sum)
	if keyErr != nil || key.String() != "namespaces/"+string(nsID)+"/registry/sha256/"+sum {
		t.Fatalf("BlobKey = %q, %v", key, keyErr)
	}

	// Defense in depth: the scoped view of the blob port refuses a key outside
	// the prefix of the namespace too. This check does not rely on it.
	if !strings.HasPrefix(key.String(), "namespaces/"+string(nsID)+"/") {
		t.Fatal("the key is outside the prefix of the namespace")
	}

	for _, bad := range []string{"", "abc", sum + "0", strings.ToUpper(sum), "../" + sum[3:]} {
		if _, badErr := registry.BlobKey(nsID, bad); !errors.Is(badErr, registry.ErrInvalid) {
			t.Errorf("BlobKey(%q) = %v, want ErrInvalid", bad, badErr)
		}
	}

	for _, bad := range []namespace.ID{"", "acme", "ns-", "ns-../x", "ns-a/b"} {
		if _, idErr := registry.BlobKey(bad, sum); !errors.Is(idErr, registry.ErrInvalid) {
			t.Errorf("BlobKey with ID %q = %v, want ErrInvalid", bad, idErr)
		}
	}

	// Two namespaces never share a key.
	other, otherErr := registry.BlobKey("ns-zzzzzzzzzz0123456789", sum)
	if otherErr != nil || other == key {
		t.Fatal("the keys of two namespaces are equal")
	}
}

func TestStagingKey(t *testing.T) {
	t.Parallel()

	const upload = "abcdefghij0123456789"

	key, err := registry.StagingKey(nsID, upload)
	if err != nil || key.String() != "namespaces/"+string(nsID)+"/registry/staging/"+upload {
		t.Fatalf("StagingKey = %q, %v", key, err)
	}

	content, contentErr := registry.BlobKey(nsID, sum)
	if contentErr != nil || key == content {
		t.Fatal("a staging key equals a content key")
	}

	for _, bad := range []string{"", "short", "../" + upload, upload + "/x", strings.ToUpper(upload), strings.Repeat("a", 65)} {
		if _, badErr := registry.StagingKey(nsID, bad); !errors.Is(badErr, registry.ErrInvalid) {
			t.Errorf("StagingKey(%q) = %v, want ErrInvalid", bad, badErr)
		}
	}

	if _, idErr := registry.StagingKey("", upload); !errors.Is(idErr, registry.ErrInvalid) {
		t.Errorf("StagingKey with no ID = %v, want ErrInvalid", idErr)
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	ordered := []string{
		"0.9.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.2.0", "1.9.0", "1.10.0", "2.0.0",
	}
	for i, a := range ordered {
		for j, b := range ordered {
			got := registry.CompareVersions(a, b)
			if (i < j && got >= 0) || (i == j && got != 0) || (i > j && got <= 0) {
				t.Errorf("CompareVersions(%s, %s) = %d", a, b, got)
			}
		}
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

	c := b
	c.Archives = append([]registry.Archive(nil), b.Archives...)
	c.Archives[0].SHA256 = strings.Repeat("a", 64)

	if a.SameContent(c) {
		t.Fatal("a changed hash must be other content")
	}

	for name, mutate := range map[string]func(r *registry.Release){
		"signature file": func(r *registry.Release) { r.SignatureFile = "x.sig" },
		"signature hash": func(r *registry.Release) { r.SignatureSHA256 = strings.Repeat("a", 64) },
		"fingerprint":    func(r *registry.Release) { r.KeyFingerprint = strings.Repeat("AB", 20) },
		"protocols":      func(r *registry.Release) { r.Protocols = []string{"6.0"} },
	} {
		d := b
		d.Archives = append([]registry.Archive(nil), b.Archives...)
		mutate(&d)

		if a.SameContent(d) {
			t.Errorf("a changed %s must be other content", name)
		}
	}
}
