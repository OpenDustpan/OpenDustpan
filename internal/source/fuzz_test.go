package source_test

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/source"
	portsource "opendustpan.org/opendustpan/pkg/source"
)

// lowerHex64 is a SHA-256 in lowercase hex.
var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FuzzResolveURL checks the URL of a submodule that ResolveURL answers for
// any superproject URL and any .gitmodules URL: it fails only with
// ErrNotAllowed, the answer is an https URL with a host and a path that
// SplitHTTPS takes as it is, and a relative URL never leaves the host of the
// superproject.
func FuzzResolveURL(f *testing.F) {
	f.Add("https://git.example.com/platform/app", "../modules")
	f.Add("https://git.example.com/platform/app", "./sub/")
	f.Add("https://git.example.com/a", "../../../etc")
	f.Add("https://git.example.com/a/b", "https://other.example.com/x.git")
	f.Add("https://git.example.com/a/b", "git@other.example.com:x.git")
	f.Add("https://git.example.com/a/b", "../%2e%2e/x")
	f.Add("https://git.example.com/a/b", "./../")
	f.Add("http://git.example.com/a", "../b")

	f.Fuzz(func(t *testing.T, base, ref string) {
		got, err := source.ResolveURL(base, ref)
		if err != nil {
			if !errors.Is(err, source.ErrNotAllowed) {
				t.Fatalf("ResolveURL(%q, %q) = %v, want ErrNotAllowed", base, ref, err)
			}

			return
		}

		u, ok := portsource.SplitHTTPS(got)
		if !ok || u.Host == "" || len(u.Parts) == 0 || u.String() != got {
			t.Fatalf("ResolveURL(%q, %q) = %q, want an https URL with a host and a path", base, ref, got)
		}

		relative := strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../")
		if relative && !source.SameHost(got, base) {
			t.Fatalf("ResolveURL(%q, %q) = %q, which leaves the host", base, ref, got)
		}
	})
}

// FuzzLFSEndpoint checks that each Git LFS endpoint that LFSEndpoint answers
// has the host of the repository URL and ends with ".git/info/lfs".
func FuzzLFSEndpoint(f *testing.F) {
	f.Add("https://git.example.com/a/b")
	f.Add("https://git.example.com/a/b.git")
	f.Add("https://git.example.com/a/b.git.git/")
	f.Add("https://git.example.com")
	f.Add("https://user@git.example.com/a")

	f.Fuzz(func(t *testing.T, repo string) {
		got, err := source.LFSEndpoint(repo)
		if err != nil {
			if !errors.Is(err, source.ErrNotAllowed) {
				t.Fatalf("LFSEndpoint(%q) = %v, want ErrNotAllowed", repo, err)
			}

			return
		}

		if !strings.HasSuffix(got, ".git/info/lfs") {
			t.Fatalf("LFSEndpoint(%q) = %q, want .git/info/lfs at the end", repo, got)
		}

		if _, ok := portsource.SplitHTTPS(got); !ok || !source.SameHost(got, repo) {
			t.Fatalf("LFSEndpoint(%q) = %q, want an https URL on the host of the repository", repo, got)
		}
	})
}

// FuzzParsePointer checks that each Git LFS pointer that ParsePointer takes
// has at most 1024 bytes, names a SHA-256 in lowercase hex and a size of 0 or
// more, and that the pointer text of the object parses to the same object.
func FuzzParsePointer(f *testing.F) {
	const version = "version https://git-lfs.github.com/spec/v1\n"

	oid := strings.Repeat("ab", 32)

	for _, s := range []string{
		version + "oid sha256:" + oid + "\nsize 12345\n",
		version + "ext-0-foo sha256:" + oid + "\noid sha256:" + oid + "\nsize 1\n",
		version + "size 1\noid sha256:" + oid + "\n",
		version + "oid sha256:" + strings.ToUpper(oid) + "\nsize 1\n",
		version + "oid sha256:" + oid + "\nsize -1\n",
		version + "oid sha256:" + oid + "\nsize 01\n",
		version + "oid sha256:" + oid + "\nsize 1",
		"not a pointer\n",
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		got, ok := source.ParsePointer(b)
		if !ok {
			if got != (source.LargeFile{OID: "", Size: 0}) {
				t.Fatalf("ParsePointer(%q) = %+v, false, want the zero object", b, got)
			}

			return
		}

		if len(b) > 1024 || !lowerHex64.MatchString(got.OID) || got.Size < 0 {
			t.Fatalf("ParsePointer(%q) = %+v, want 1024 bytes at most, a SHA-256 and a size of 0 or more", b, got)
		}

		canonical := version + "oid sha256:" + got.OID + "\nsize " + strconv.FormatInt(got.Size, 10) + "\n"
		if again, ok := source.ParsePointer([]byte(canonical)); !ok || again != got {
			t.Fatalf("ParsePointer(%q) = %+v, %v, want %+v", canonical, again, ok, got)
		}
	})
}

// FuzzLargeFileKey checks that each blob key of a Git LFS object is the
// snapshot ID and the object ID under "source/", as given, and that the pack
// keys of the same snapshot ID are keys under the same "source/" path.
func FuzzLargeFileKey(f *testing.F) {
	f.Add("src-1", strings.Repeat("ab", 32))
	f.Add("src-1/../x", "a")
	f.Add("", "")
	f.Add("src-1", "../../pack")

	f.Fuzz(func(t *testing.T, id, oid string) {
		key, err := source.LargeFileKey(id, oid)
		if err == nil && key.String() != "source/"+id+"/lfs/"+oid {
			t.Fatalf("LargeFileKey(%q, %q) = %q, want the IDs as given", id, oid, key.String())
		}

		pack, err := source.PackKey(id)
		if err == nil && pack.String() != "source/"+id+"/pack" {
			t.Fatalf("PackKey(%q) = %q, want the ID as given", id, pack.String())
		}

		sub, err := source.SubmodulePackKey(id, len(oid))
		if err == nil && sub.String() != "source/"+id+"/submodules/"+strconv.Itoa(len(oid))+"/pack" {
			t.Fatalf("SubmodulePackKey(%q, %d) = %q, want the ID as given", id, len(oid), sub.String())
		}
	})
}
