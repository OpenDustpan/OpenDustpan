package releasecheck_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/registry"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	linuxZip = "terraform-provider-payments_1.0.0_linux_amd64.zip"
	armZip   = "terraform-provider-payments_1.0.0_darwin_arm64.zip"
)

// addr is the address of the provider that the tests check.
func addr() registry.Address {
	return registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "payments", System: ""}
}

func goodSet() releasecheck.Set {
	return releasecheck.Set{
		Sums:      releasecheck.File{Name: "terraform-provider-payments_1.0.0_SHA256SUMS", SHA256: hashC, Size: 10},
		Signature: releasecheck.File{Name: "terraform-provider-payments_1.0.0_SHA256SUMS.sig", SHA256: hashC, Size: 10},
		Archives: []releasecheck.File{
			{Name: linuxZip, SHA256: hashA, Size: 5},
			{Name: armZip, SHA256: hashB, Size: 6},
		},
	}
}

func goodSums() map[string]string {
	return map[string]string{linuxZip: hashA, armZip: hashB}
}

// sumsLine writes one line of a checksums file.
func sumsLine(hash, name string) string { return hash + "  " + name + "\n" }

func TestParseSumsAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{name: "two lines", raw: sumsLine(hashA, linuxZip) + sumsLine(hashB, armZip), want: goodSums()},
		{
			name: "no final newline",
			raw:  strings.TrimSuffix(sumsLine(hashA, linuxZip), "\n"),
			want: map[string]string{linuxZip: hashA},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := releasecheck.ParseSums("sums", []byte(tc.raw))
			if err != nil || !maps.Equal(got, tc.want) {
				t.Fatalf("ParseSums = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestParseSumsRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "only a newline", raw: "\n"},
		{name: "blank line between", raw: sumsLine(hashA, linuxZip) + "\n" + sumsLine(hashB, armZip)},
		{name: "one space", raw: hashA + " " + linuxZip + "\n"},
		{name: "binary marker", raw: hashA + " *" + linuxZip + "\n"},
		{name: "uppercase hash", raw: sumsLine(strings.ToUpper(hashA), linuxZip)},
		{name: "short hash", raw: sumsLine(hashA[:63], linuxZip)},
		{name: "path in the name", raw: sumsLine(hashA, "../"+linuxZip)},
		{name: "slash in the name", raw: sumsLine(hashA, "dir/"+linuxZip)},
		{name: "absolute path", raw: sumsLine(hashA, "/etc/passwd")},
		{name: "duplicate name", raw: sumsLine(hashA, linuxZip) + sumsLine(hashB, linuxZip)},
		{name: "carriage return", raw: hashA + "  " + linuxZip + "\r\n"},
		{name: "NUL byte", raw: hashA + "  " + linuxZip + "\x00\n"},
		{name: "trailing space", raw: hashA + "  " + linuxZip + " \n"},
		{name: "leading dot name", raw: sumsLine(hashA, ".hidden")},
		{name: "two newlines at the end", raw: sumsLine(hashA, linuxZip) + "\n"},
		{name: "too large", raw: strings.Repeat(sumsLine(hashA, linuxZip), releasecheck.MaxSumsBytes/10)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := releasecheck.ParseSums("sums", []byte(tc.raw))
			if !errors.Is(err, releasecheck.ErrRefused) {
				t.Fatalf("ParseSums = %v, %v; want ErrRefused", got, err)
			}

			if !strings.Contains(err.Error(), "sums") {
				t.Fatalf("error %q does not name the file", err)
			}
		})
	}
}

func TestParseSumsTooManyLines(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := range registry.MaxArchives + 1 {
		b.WriteString(hashA + "  terraform-provider-payments_1.0.0_os" + strings.Repeat("a", i%5) + "_arch.zip\n")
	}

	// The names repeat, so the parser may refuse for either reason. It must refuse.
	if _, err := releasecheck.ParseSums("sums", []byte(b.String())); !errors.Is(err, releasecheck.ErrRefused) {
		t.Fatalf("ParseSums = %v, want ErrRefused", err)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(s *releasecheck.Set, sums map[string]string)
		// want is a part of the error text that names the file, or "" for success.
		want string
	}{
		{"good set", func(*releasecheck.Set, map[string]string) {}, ""},
		{"wrong hash", func(s *releasecheck.Set, _ map[string]string) { s.Archives[0].SHA256 = hashC }, linuxZip},
		{"archive with no line", func(_ *releasecheck.Set, m map[string]string) { delete(m, armZip) }, armZip},
		{"line for a file not uploaded", func(_ *releasecheck.Set, m map[string]string) {
			m["terraform-provider-payments_1.0.0_windows_amd64.zip"] = hashA
		}, "windows_amd64"},
		{"no archive", func(s *releasecheck.Set, m map[string]string) {
			s.Archives = nil

			clear(m)
		}, "no archive"},
		{"archive named as the sums file", func(s *releasecheck.Set, _ map[string]string) {
			s.Archives[0].Name = s.Sums.Name
		}, "SHA256SUMS"},
		{"archive named as the signature file", func(s *releasecheck.Set, _ map[string]string) {
			s.Archives[0].Name = s.Signature.Name
		}, ".sig"},
		{
			"duplicate archive",
			func(s *releasecheck.Set, _ map[string]string) { s.Archives[1].Name = linuxZip },
			linuxZip,
		},
		{
			"wrong sums file name",
			func(s *releasecheck.Set, _ map[string]string) { s.Sums.Name = "SHA256SUMS" },
			"SHA256SUMS",
		},
		{"wrong signature file name", func(s *releasecheck.Set, _ map[string]string) {
			s.Signature.Name = "terraform-provider-payments_1.0.0_SHA256SUMS.asc"
		}, ".asc"},
		{"archive of another provider", func(s *releasecheck.Set, m map[string]string) {
			other := "terraform-provider-ledger_1.0.0_linux_amd64.zip"
			s.Archives[0].Name = other
			delete(m, linuxZip)
			m[other] = hashA
		}, "ledger"},
		{"archive of another version", func(s *releasecheck.Set, m map[string]string) {
			other := "terraform-provider-payments_1.0.1_linux_amd64.zip"
			s.Archives[0].Name = other
			delete(m, linuxZip)
			m[other] = hashA
		}, "1.0.1"},
		{"archive with no platform", func(s *releasecheck.Set, m map[string]string) {
			other := "terraform-provider-payments_1.0.0.zip"
			s.Archives[0].Name = other
			delete(m, linuxZip)
			m[other] = hashA
		}, "payments_1.0.0.zip"},
		{"archive that is not a zip", func(s *releasecheck.Set, m map[string]string) {
			other := "terraform-provider-payments_1.0.0_linux_amd64.tar.gz"
			s.Archives[0].Name = other
			delete(m, linuxZip)
			m[other] = hashA
		}, "tar.gz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			set := goodSet()
			set.Archives = append([]releasecheck.File(nil), set.Archives...)
			sums := goodSums()
			tc.mutate(&set, sums)

			rel, err := releasecheck.Check(addr(), "1.0.0", set, sums)
			if tc.want == "" {
				if err != nil || len(rel.Archives) != 2 || rel.Archives[0].Platform != "linux_amd64" ||
					rel.Archives[1].Platform != "darwin_arm64" || rel.SumsFile != set.Sums.Name {
					t.Fatalf("Check = %+v, %v", rel, err)
				}

				return
			}

			if !errors.Is(err, releasecheck.ErrRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v, want ErrRefused naming %q", err, tc.want)
			}
		})
	}
}

func TestCheckRefusesAModule(t *testing.T) {
	t.Parallel()

	mod := registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "vpc", System: "aws"}

	if _, err := releasecheck.Check(mod, "1.0.0", goodSet(), goodSums()); !errors.Is(err, releasecheck.ErrRefused) {
		t.Fatalf("Check = %v, want ErrRefused", err)
	}
}

func TestCheckRefusesABadVersion(t *testing.T) {
	t.Parallel()

	if _, err := releasecheck.Check(addr(), "../1", goodSet(), goodSums()); !errors.Is(err, releasecheck.ErrRefused) {
		t.Fatalf("Check = %v, want ErrRefused", err)
	}
}
