package source_test

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/source"
)

// withUserInfo returns an HTTPS URL of git.example.com with a user name and a
// password. The test builds it at run time, so that no file holds a literal URL
// with user info.
func withUserInfo(path string) string {
	u := url.URL{
		Scheme: "https",
		User:   url.UserPassword("user", "pass"),
		Host:   "git.example.com",
		Path:   "/" + path,
	}

	return u.String()
}

func TestEncodeFilterIsCanonical(t *testing.T) {
	t.Parallel()

	empty, err := source.EncodeFilter(
		source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: nil},
	)
	if err != nil {
		t.Fatalf("EncodeFilter(empty): %v", err)
	}

	const wantEmpty = `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`
	if empty != wantEmpty {
		t.Errorf("EncodeFilter(empty) = %s, want %s", empty, wantEmpty)
	}

	full, err := source.EncodeFilter(source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{"main", "release/1"},
	})
	if err != nil {
		t.Fatalf("EncodeFilter(full): %v", err)
	}

	const wantFull = `{"submodules":["https://git.example.com/platform/*"],` +
		`"largeFiles":["https://git.example.com/acme/infra"],"failOnDenied":true,` +
		`"protectedBranches":["main","release/1"]}`
	if full != wantFull {
		t.Errorf("EncodeFilter(full) = %s, want %s", full, wantFull)
	}
}

func TestEncodeFilterRefusesAnInvalidFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		f    source.Filter
	}{
		{
			"a bad pattern",
			source.Filter{
				Submodules:        []source.URLPattern{"git@host:a/b"},
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: nil,
			},
		},
		{
			"a glob branch",
			source.Filter{
				Submodules:        nil,
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: []string{"release/*"},
			},
		},
		{
			"a branch twice",
			source.Filter{
				Submodules:        nil,
				LargeFiles:        nil,
				FailOnDenied:      false,
				ProtectedBranches: []string{"main", "main"},
			},
		},
	}

	for _, tc := range cases {
		if _, err := source.EncodeFilter(tc.f); !errors.Is(err, source.ErrInvalid) {
			t.Errorf("%s: EncodeFilter = %v, want ErrInvalid", tc.name, err)
		}
	}
}

func TestDecodeFilterRoundTrip(t *testing.T) {
	t.Parallel()

	in := source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{"main"},
	}

	text, err := source.EncodeFilter(in)
	if err != nil {
		t.Fatalf("EncodeFilter: %v", err)
	}

	out, err := source.DecodeFilter(text)
	if err != nil {
		t.Fatalf("DecodeFilter(%s): %v", text, err)
	}

	again, err := source.EncodeFilter(out)
	if err != nil || again != text {
		t.Errorf("a round trip gives %q, %v, want %q", again, err, text)
	}

	none, err := source.DecodeFilter("")
	if err != nil || none.FailOnDenied || len(none.Submodules)+len(none.LargeFiles)+len(none.ProtectedBranches) != 0 {
		t.Errorf("DecodeFilter(empty) = %+v, %v, want the empty filter", none, err)
	}
}

func TestDecodeFilterRefusesHostileText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want error
	}{
		{"not JSON", `not json`, source.ErrInvalid},
		{"an array", `[]`, source.ErrInvalid},
		{"null", `null`, source.ErrInvalid},
		{"an unknown key", `{"submodules":[],"extra":1}`, source.ErrInvalid},
		{"a wrong type", `{"failOnDenied":"yes"}`, source.ErrInvalid},
		{"a second document", `{} {}`, source.ErrInvalid},
		{"data after the document", `{}x`, source.ErrInvalid},
		{"a bad pattern", `{"submodules":["http://git.example.com/a/b"]}`, source.ErrInvalid},
		{"a glob branch", `{"protectedBranches":["a/*"]}`, source.ErrInvalid},
		{"a branch with a newline", `{"protectedBranches":["a\nb"]}`, source.ErrInvalid},
		{
			"too long",
			`{"protectedBranches":["` + strings.Repeat("a", source.MaxFilterJSON) + `"]}`,
			source.ErrFilterTooLarge,
		},
	}

	for _, tc := range cases {
		f, err := source.DecodeFilter(tc.text)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: DecodeFilter = %v, want %v", tc.name, err, tc.want)
		}

		if err != nil && strings.Contains(err.Error(), "git.example.com") {
			t.Errorf("%s: the error holds the text of the filter: %v", tc.name, err)
		}

		if err != nil && (f.FailOnDenied || len(f.Submodules)+len(f.LargeFiles)+len(f.ProtectedBranches) != 0) {
			t.Errorf("%s: DecodeFilter gives a value %+v with the error", tc.name, f)
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
		{"https://git.example.com/platform%2Fmodules", "https://git.example.com/platform%2Fmodules", false, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/a%2Fb", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/%2e%2e", true, false},
		{"https://git.example.com/platform/modules", "https://git.example.com/platform/modul%65s", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/./network", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/a/../platform/network", true, false},
		{"https://git.example.com/platform/../modules", "https://git.example.com/modules", false, false},
		{"https://git-1.example.com/platform/*", "https://GIT-1.example.com/platform/network", true, true},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/network/", true, true},
		{"https://git.example.com/platform/*/", "https://git.example.com/platform/network", true, true},
		{"https://git.ex\u0430mple.com/platform/*", "https://git.ex\u0430mple.com/platform/network", false, false},
		{"https://git.example.com/platform/*", "https://git.ex\u0430mple.com/platform/network", true, false},
		{"https://git_host.example.com/platform/*", "https://git_host.example.com/platform/a", false, false},
		{"https://git..example.com/platform/*", "https://git..example.com/platform/a", false, false},
		{"https://.example.com/platform/*", "https://.example.com/platform/a", false, false},
		{"https://git.example.com/platform/*", "https://git.example.com./platform/network", true, false},
		{"https://git.example.com/platform/*", "https://git.example.com/platform/network//", true, false},
		{"https://git.example.com/platform//", "https://git.example.com/platform//", false, false},
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

func TestFilterAllows(t *testing.T) {
	t.Parallel()

	var none source.Filter
	if none.AllowsSubmodule("https://git.example.com/a/b") || none.AllowsLargeFiles("https://git.example.com/a/b") {
		t.Fatal("an empty filter allows a URL")
	}

	p := source.Filter{
		Submodules:        []source.URLPattern{"https://git.example.com/platform/*"},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: nil,
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

	big := source.Filter{
		Submodules:        make([]source.URLPattern, source.MaxPatterns+1),
		LargeFiles:        nil,
		FailOnDenied:      false,
		ProtectedBranches: nil,
	}
	for i := range big.Submodules {
		big.Submodules[i] = "https://git.example.com/a/b"
	}

	if err := big.Validate(); !errors.Is(err, source.ErrInvalid) {
		t.Errorf("Validate of %d patterns = %v, want ErrInvalid", len(big.Submodules), err)
	}
}

func TestValidBranchName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "main", true},
		{"with a slash", "release/1.2", true},
		{"with dot, underscore and dash", "feature/a_b-c.d", true},
		{"longest", strings.Repeat("a", source.MaxBranchName), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", source.MaxBranchName+1), false},
		{"starts with a dash", "-main", false},
		{"double dot", "a..b", false},
		{"empty part", "a//b", false},
		{"leading slash", "/main", false},
		{"trailing slash", "main/", false},
		{"part starts with a dot", "a/.hidden", false},
		{"part ends with .lock", "a/b.lock", false},
		{"glob", "release/*", false},
		{"space", "my branch", false},
		{"newline", "main\nevil", false},
		{"NUL", "main\x00", false},
		{"percent", "a%2Fb", false},
		{"colon", "refs:heads", false},
		{"backslash", `a\b`, false},
		{"non-ASCII", "größe", false},
	}

	for _, tc := range cases {
		if got := source.ValidBranchName(tc.in); got != tc.want {
			t.Errorf("%s: ValidBranchName(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestFilterValidateProtectedBranches(t *testing.T) {
	t.Parallel()

	many := make([]string, source.MaxPatterns+1)
	for i := range many {
		many[i] = "b" + strconv.Itoa(i)
	}

	cases := []struct {
		name     string
		branches []string
		ok       bool
	}{
		{"none", nil, true},
		{"two names", []string{"main", "release/1"}, true},
		{"at the limit", many[:source.MaxPatterns], true},
		{"above the limit", many, false},
		{"a glob", []string{"main", "release/*"}, false},
		{"a name twice", []string{"main", "main"}, false},
		{"an empty name", []string{""}, false},
	}

	for _, tc := range cases {
		f := source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: tc.branches}

		err := f.Validate()
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, source.ErrInvalid)) {
			t.Errorf("%s: Validate = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestFilterHashIgnoresProtectedBranches(t *testing.T) {
	t.Parallel()

	a := source.Filter{
		Submodules: []source.URLPattern{"https://git.example.com/m/*"}, LargeFiles: nil, FailOnDenied: false,
		ProtectedBranches: nil,
	}
	b := a
	b.ProtectedBranches = []string{"main"}

	if a.Hash() != b.Hash() {
		t.Error("the protected branches change the hash of the filter")
	}
}

func TestSplitHTTPS(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		host string
		path string
		ok   bool
	}{
		{"repository", "https://git.example.com/a/b", "git.example.com", "a/b", true},
		{"slash at the end", "https://git.example.com/a/b/", "git.example.com", "a/b", true},
		{"host only", "https://git.example.com", "git.example.com", "", true},
		{"http", "http://git.example.com/a", "", "", false},
		{"user", "https://u@git.example.com/a", "", "", false},
		{"port", "https://git.example.com:8443/a", "", "", false},
		{"query", "https://git.example.com/a?x=1", "", "", false},
		{"percent", "https://git.example.com/a%2Fb", "", "", false},
		{"dot part", "https://git.example.com/a/../b", "", "", false},
		{"empty part", "https://git.example.com/a//b", "", "", false},
		{"backslash", "https://git.example.com/a\\b", "", "", false},
		{"underscore host", "https://git_x.example.com/a", "", "", false},
		{"empty label", "https://git..example.com/a", "", "", false},
		{"uppercase scheme", "HTTPS://git.example.com/a", "", "", false},
		{"IPv6 host", "https://[::1]/a", "", "", false},
		{"trailing dot host", "https://h.example./a", "", "", false},
		{"user and password", withUserInfo("a"), "", "", false},
		{"punycode host", "https://xn--gt-9ja.example.com/a", "xn--gt-9ja.example.com", "a", true},
		{"non-ASCII host", "https://gіt.example.com/a", "", "", false},
		{"NUL", "https://git.example.com/a\x00b", "", "", false},
		{"IPv4 host", "https://169.254.169.254/a", "", "", false},
		{"loopback host", "https://127.0.0.1/a", "", "", false},
		{"public IPv4 host", "https://93.184.216.34/a", "", "", false},
		{"upper-case hexadecimal host", "https://0X7F000001/a", "", "", false},
		{"decimal host", "https://2130706433/a", "", "", false},
		{"hexadecimal host", "https://0x7f000001/a", "", "", false},
		{"localhost host", "https://localhost/a", "", "", false},
		{"name under localhost", "https://git.localhost/a", "", "", false},
		{"name that holds an address", "https://127.0.0.1.example.com/a", "127.0.0.1.example.com", "a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, ok := source.SplitHTTPS(tc.in)
			if ok != tc.ok {
				t.Fatalf("SplitHTTPS(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}

			if u.Host != tc.host || strings.Join(u.Parts, "/") != tc.path {
				t.Errorf(
					"SplitHTTPS(%q) = %q and %q, want %q and %q",
					tc.in,
					u.Host,
					strings.Join(u.Parts, "/"),
					tc.host,
					tc.path,
				)
			}
		})
	}
}
