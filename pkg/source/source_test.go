package source_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/source"
)

const commit1 = "0123456789abcdef0123456789abcdef01234567"

// request returns a valid request with an empty policy.
func request() source.Request {
	return source.Request{
		Namespace: "acme", Connection: "github", Repository: "acme/infra", Commit: commit1,
		Format: source.FormatSHA1, Policy: source.Policy{Submodules: nil, LargeFiles: nil, FailOnDenied: false},
	}
}

func TestValidCommit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		f    source.ObjectFormat
		c    string
		want bool
	}{
		{source.FormatSHA1, commit1, true},
		{source.FormatSHA1, strings.ToUpper(commit1), false},
		{source.FormatSHA1, commit1[:39], false},
		{source.FormatSHA256, commit1 + commit1[:24], true},
		{source.FormatSHA256, commit1, false},
		{"md5", commit1, false},
		{source.FormatSHA1, "main", false},
	}

	for _, tc := range cases {
		if got := source.ValidCommit(tc.f, tc.c); got != tc.want {
			t.Errorf("ValidCommit(%s, %q) = %v, want %v", tc.f, tc.c, got, tc.want)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(r *source.Request)
		ok     bool
	}{
		{"valid", func(*source.Request) {}, true},
		{"no namespace", func(r *source.Request) { r.Namespace = "" }, false},
		{"no connection", func(r *source.Request) { r.Connection = "" }, false},
		{"a branch, not a commit", func(r *source.Request) { r.Commit = "main" }, false},
		{"bad policy", func(r *source.Request) { r.Policy.Submodules = []source.URLPattern{"git@host:a/b"} }, false},
	}

	for _, tc := range cases {
		r := request()
		tc.change(&r)

		err := r.Validate()
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, source.ErrInvalid)) {
			t.Errorf("%s: Validate = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestSnapshotKeyChangesWithThePolicy(t *testing.T) {
	t.Parallel()

	a, b := request(), request()
	b.Policy.Submodules = []source.URLPattern{"https://git.example.com/modules/*"}

	if source.SnapshotKey(&a) == source.SnapshotKey(&b) {
		t.Fatal("two policies give one snapshot key")
	}

	c := request()
	c.Policy.FailOnDenied = true

	if source.SnapshotKey(&a) == source.SnapshotKey(&c) {
		t.Fatal("the switch does not change the snapshot key")
	}

	if source.SnapshotKey(&a) != source.SnapshotKey(new(request())) {
		t.Fatal("one request gives two snapshot keys")
	}
}

func TestBlobKeys(t *testing.T) {
	t.Parallel()

	for _, f := range []func() (string, error){
		func() (string, error) { k, err := source.PackKey("src-1"); return string(k), err },
		func() (string, error) { k, err := source.SubmodulePackKey("src-1", 3); return string(k), err },
		func() (string, error) {
			k, err := source.LargeFileKey("src-1", strings.Repeat("a", 64))

			return string(k), err
		},
	} {
		if k, err := f(); err != nil || !strings.HasPrefix(k, "source/src-1/") {
			t.Errorf("key %q, %v", k, err)
		}
	}
}

func TestURLPattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern source.URLPattern
		target  string
		valid   bool
		match   bool
	}{
		{"https://git.example.com/platform/modules", "https://git.example.com/platform/modules", true, true},
		{"https://git.example.com/platform/modules", "https://git.example.com/platform/modules.git", true, true},
		{"https://git.example.com/platform/modules.git", "https://git.example.com/platform/modules", true, true},
		{"https://git.example.com/platform/modules", "https://GIT.example.com/platform/modules", true, true},
		{"https://git.example.com/platform/modules", "https://git.example.com/Platform/modules", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/network", true, true},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/network.git", true, true},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/a/b", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/..", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform", true, false},
		{"https://git.example.com/platform/*", "https://evil.example.com/platform/network", true, false},
		{"https://git.example.com/platform/*", "http://git.example.com/platform/network", true, false},
		{"https://git.example.com/platform/*", "https://user@git.example.com/platform/network", true, false},
		{"https://git.example.com/*/modules", "https://git.example.com/platform/modules", false, false},
		{"https://git.example.com/platform/mod*", "https://git.example.com/platform/modules", false, false},
		{"https://git.example.com", "https://git.example.com", false, false},
		{
			"https://git.example.com:8443/platform/modules",
			"https://git.example.com:8443/platform/modules",
			false,
			false,
		},
		{"git@git.example.com:platform/modules", "git@git.example.com:platform/modules", false, false},
		{"https://git.example.com//modules", "https://git.example.com//modules", false, false},
	}

	for _, tc := range cases {
		err := tc.pattern.Validate()
		if tc.valid != (err == nil) {
			t.Errorf("Validate(%s) = %v, want valid %v", tc.pattern, err, tc.valid)
		}

		if got := tc.pattern.Match(tc.target); tc.valid && got != tc.match {
			t.Errorf("Match(%s, %s) = %v, want %v", tc.pattern, tc.target, got, tc.match)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	t.Parallel()

	var none source.Policy
	if none.AllowsSubmodule("https://git.example.com/a/b") || none.AllowsLargeFiles("https://git.example.com/a/b") {
		t.Fatal("an empty policy allows a URL")
	}

	p := source.Policy{
		Submodules:   []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:   []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied: true,
	}

	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if !p.AllowsSubmodule("https://git.example.com/platform/vpc") ||
		p.AllowsSubmodule("https://git.example.com/acme/infra") {
		t.Error("submodule allowlist")
	}

	if !p.AllowsLargeFiles("https://git.example.com/acme/infra.git") ||
		p.AllowsLargeFiles("https://git.example.com/platform/vpc") {
		t.Error("large file allowlist")
	}

	big := source.Policy{
		Submodules:   make([]source.URLPattern, source.MaxPatterns+1),
		LargeFiles:   nil,
		FailOnDenied: false,
	}
	for i := range big.Submodules {
		big.Submodules[i] = "https://git.example.com/a/b"
	}

	if err := big.Validate(); !errors.Is(err, source.ErrInvalid) {
		t.Errorf("Validate of %d patterns = %v, want ErrInvalid", len(big.Submodules), err)
	}
}

func TestParsePointer(t *testing.T) {
	t.Parallel()

	oid := strings.Repeat("4d", 32)
	valid := "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + "\nsize 12345\n"

	cases := []struct {
		name string
		in   string
		size int64
		ok   bool
	}{
		{"valid", valid, 12345, true},
		{"extra key in order", "version https://git-lfs.github.com/spec/v1\next-0-foo bar\noid sha256:" + oid +
			"\nsize 1\n", 1, true},
		{"no final newline", strings.TrimSuffix(valid, "\n"), 0, false},
		{"keys out of order", "version https://git-lfs.github.com/spec/v1\nsize 1\noid sha256:" + oid + "\n", 0, false},
		{"no size", "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + "\nzz 1\n", 0, false},
		{"short oid", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 1\n", 0, false},
		{"upper case oid", "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.ToUpper(oid) +
			"\nsize 1\n", 0, false},
		{"negative size", "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + "\nsize -1\n", 0, false},
		{"size with plus", "version https://git-lfs.github.com/spec/v1\noid sha256:" + oid + "\nsize +1\n", 0, false},
		{"other version", "version https://example.com/v2\noid sha256:" + oid + "\nsize 1\n", 0, false},
		{"ordinary file", "variable \"region\" {}\n", 0, false},
		{"too large", valid + strings.Repeat("x", 1024), 0, false},
	}

	for _, tc := range cases {
		f, ok := source.ParsePointer([]byte(tc.in))
		if ok != tc.ok || (ok && (f.OID != oid || f.Size != tc.size)) {
			t.Errorf("%s: ParsePointer = %+v, %v, want ok %v", tc.name, f, ok, tc.ok)
		}
	}
}
