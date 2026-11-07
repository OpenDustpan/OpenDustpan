package render

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// TestContainment is the containment layer: checkFiles gets hostile paths
// directly, as if every other check had failed.
func TestContainment(t *testing.T) {
	t.Parallel()

	allowed := []string{fileTofuProgram, fileTofuValues}
	bad := []string{
		"../main.tf",
		"/etc/passwd",
		"/main.tf",
		"a/b",
		"./main.tf",
		"main.tf/",
		"main.tf\x00",
		"..",
		".",
		"",
		"main.tf/../../x",
		`..\main.tf`,
		"other.tf",
		"MAIN.TF",
		"main.tf ",
		"terraform.tfvars.json.bak",
		"Pulumi.x.yaml",
	}

	for _, p := range bad {
		err := checkFiles([]selfservice.File{{Path: p, Content: []byte("x")}}, allowed)
		if !errors.Is(err, selfservice.ErrInvalid) {
			t.Errorf("path %q: error = %v, want ErrInvalid", p, err)
		}
	}

	twice := []selfservice.File{{Path: fileTofuProgram, Content: nil}, {Path: fileTofuProgram, Content: nil}}
	if err := checkFiles(twice, allowed); !errors.Is(err, selfservice.ErrInvalid) {
		t.Errorf("a path twice: error = %v, want ErrInvalid", err)
	}

	big := []selfservice.File{{Path: fileTofuProgram, Content: make([]byte, maxFileBytes+1)}}
	if err := checkFiles(big, allowed); !errors.Is(err, selfservice.ErrLimit) {
		t.Errorf("a file over the limit: error = %v, want ErrLimit", err)
	}

	both := []selfservice.File{{Path: fileTofuProgram, Content: nil}, {Path: fileTofuValues, Content: nil}}
	if err := checkFiles(both, allowed); err != nil {
		t.Errorf("the allowed files: %v", err)
	}
}

// TestAllowedText sends hostile text to the allowlist of the program text.
func TestAllowedText(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"", "a b", "a\"b", "a\\b", "a$b", "a{b", "a%b", "a\nb", "a\x00b", "é"} {
		if allowedText(s, tofuVersionExtra, maxSourceBytes) {
			t.Errorf("allowedText(%q) = true, want false", s)
		}
	}

	if allowedText("aaa", tofuVersionExtra, 2) {
		t.Error("allowedText over the limit = true, want false")
	}

	if !allowedText("1.2.3-rc.1+b5", tofuVersionExtra, maxVersionBytes) {
		t.Error("allowedText of a version = false, want true")
	}
}

// TestRegistrySource sends hostile module sources to the registry check
// directly, as if every other check had failed.
func TestRegistrySource(t *testing.T) {
	t.Parallel()

	for _, s := range hostileSources() {
		if registrySource(s) {
			t.Errorf("registrySource(%q) = true, want false", s)
		}
	}

	for _, s := range []string{
		"registry.example.com/platform/postgres/aws", "hashicorp/consul/aws", "ns_1/my-name/aws2",
		"a-b.example.com/ns/name/sys", "gitlab.com/ns/name/sys", "githubx.com/ns/name/sys", "git.hub.com/ns/name/sys",
		"registry.example.com/ns/name/sys",
	} {
		if !registrySource(s) {
			t.Errorf("registrySource(%q) = false, want true", s)
		}
	}
}

// hostileSources are module sources that OpenTofu reads as a local path, a
// go-getter address or a shorthand for a Git host, or that break the form of
// a registry address.
func hostileSources() []string {
	return []string{
		"",
		"../../../../etc/x",
		"../x",
		"./x",
		"/abs/x",
		"/",
		"//",
		"a//b",
		"a/b/",
		"/a/b/c",
		"git::https://example.com/r.git",
		"git::https://example.com/r.git?ref=main",
		"s3::https://bucket.example.com/m.zip",
		"gcs::https://example.com/m",
		"hg::https://example.com/m",
		"http://example.com/m.zip",
		"https://example.com/m.zip",
		"file:///etc/x",
		"github.com/x/y",
		"github.com/x/y/z/w",
		"bitbucket.org/x/y",
		"github.com/x/y/z",
		"GitHub.com/x/y/z",
		"GITHUB.COM/x/y/z",
		"bitbucket.org/x/y/z",
		"BitBucket.Org/x/y/z",
		"BITBUCKET.ORG/x/y/z",
		"GitHub.com/x/y",
		"BITBUCKET.ORG/x/y",
		"10.0.0.1/x/y/z",
		"127.0.0.1/x/y/z",
		"1.2.3.4/ns/name/sys",
		"0.0.0.0/ns/name/sys",
		"2130706433/ns/name/sys",
		"[::1]/x/y/z",
		"::1/x/y/z",
		"[2001:db8::1]/ns/name/sys",
		"2001:db8::1/ns/name/sys",
		"a/b",
		"a/b/c/d/e",
		"ns/name/sys?ref=x",
		"ns/name/sys#x",
		"ns@x/name/sys",
		"ns/name/sys@v1",
		"ns/./sys",
		"ns/name/..",
		"ns/../sys",
		"../ns/name/sys",
		"host/ns/name/sys",
		"-bad.example.com/ns/name/sys",
		"bad-.example.com/ns/name/sys",
		"a..b.example.com/ns/name/sys",
		"reg.example.com/../a/b",
		"reg.example.com/./a/b",
		"ns/name/sys\n",
		"ns/na me/sys",
		"ns/name/sys\x00",
		"ns/name/s_ys",
		"ns/name/s-ys",
		"ns/na.me/sys",
		"n.s/name/sys",
		"reg.example.com:443/ns/name/sys",
		"ns/name/${sys}",
		"ns/name/%{sys}",
		"ns/\"name/sys",
		"ns/name\\x/sys",
		"ns/name/é",
		"ns/name/" + strings.Repeat("a", maxRegistryPartBytes+1),
		strings.Repeat("a", maxSourceBytes+1) + "/b/c",
	}
}
