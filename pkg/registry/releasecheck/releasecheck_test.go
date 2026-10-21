package releasecheck_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/releasecheck"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	linuxZip = "terraform-provider-payments_1.0.0_linux_amd64.zip"
	armZip   = "terraform-provider-payments_1.0.0_darwin_arm64.zip"
)

var addr = registry.Address{Namespace: "acme", Kind: registry.KindProvider, Name: "payments", System: ""}

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

func TestParseSums(t *testing.T) {
	t.Parallel()

	line := func(hash, name string) string { return hash + "  " + name + "\n" }

	tests := []struct {
		name    string
		raw     string
		wantErr bool
		want    map[string]string
	}{
		{"two lines", line(hashA, linuxZip) + line(hashB, armZip), false, goodSums()},
		{"no final newline", strings.TrimSuffix(line(hashA, linuxZip), "\n"), false, map[string]string{linuxZip: hashA}},
		{"empty", "", true, nil},
		{"only a newline", "\n", true, nil},
		{"blank line between", line(hashA, linuxZip) + "\n" + line(hashB, armZip), true, nil},
		{"one space", hashA + " " + linuxZip + "\n", true, nil},
		{"binary marker", hashA + " *" + linuxZip + "\n", true, nil},
		{"uppercase hash", line(strings.ToUpper(hashA), linuxZip), true, nil},
		{"short hash", line(hashA[:63], linuxZip), true, nil},
		{"path in the name", line(hashA, "../"+linuxZip), true, nil},
		{"slash in the name", line(hashA, "dir/"+linuxZip), true, nil},
		{"absolute path", line(hashA, "/etc/passwd"), true, nil},
		{"duplicate name", line(hashA, linuxZip) + line(hashB, linuxZip), true, nil},
		{"carriage return", hashA + "  " + linuxZip + "\r\n", true, nil},
		{"NUL byte", hashA + "  " + linuxZip + "\x00\n", true, nil},
		{"trailing space", hashA + "  " + linuxZip + " \n", true, nil},
		{"leading dot name", line(hashA, ".hidden"), true, nil},
		{"two newlines at the end", line(hashA, linuxZip) + "\n", true, nil},
		{"too large", strings.Repeat(line(hashA, linuxZip), releasecheck.MaxSumsBytes/10), true, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := releasecheck.ParseSums("sums", []byte(tc.raw))
			if tc.wantErr {
				if !errors.Is(err, releasecheck.ErrRefused) {
					t.Fatalf("ParseSums = %v, %v; want ErrRefused", got, err)
				}

				if !strings.Contains(err.Error(), "sums") {
					t.Fatalf("error %q does not name the file", err)
				}

				return
			}

			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("ParseSums = %v, %v; want %v", got, err, tc.want)
			}

			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("ParseSums[%s] = %q, want %q", k, got[k], v)
				}
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
		{"duplicate archive", func(s *releasecheck.Set, _ map[string]string) { s.Archives[1].Name = linuxZip }, linuxZip},
		{"wrong sums file name", func(s *releasecheck.Set, _ map[string]string) { s.Sums.Name = "SHA256SUMS" }, "SHA256SUMS"},
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

			rel, err := releasecheck.Check(addr, "1.0.0", set, sums)
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

	if _, err := releasecheck.Check(addr, "../1", goodSet(), goodSums()); !errors.Is(err, releasecheck.ErrRefused) {
		t.Fatalf("Check = %v, want ErrRefused", err)
	}
}
