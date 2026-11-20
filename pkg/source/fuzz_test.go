package source_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/source"
)

// FuzzSplitHTTPS checks the invariants of each URL that SplitHTTPS takes: the
// host is empty or a host that AllowedHost takes, no path part is empty, "."
// or "..", no part holds a character that the parser refuses, and the text
// of the result parses back to the same result.
func FuzzSplitHTTPS(f *testing.F) {
	for _, s := range []string{
		"https://git.example.com/platform/modules.git", "https://git.example.com/a/b/", "https://h/a//b",
		"https://10.0.0.5/a", "https://localhost/a", "https://user@h/a", "https://h:443/a", "https://h/%2e%2e/a",
		"https://", "https:///a", "http://h/a", "https://h/a/../b", "https://xn--e1afmkfd.example/a",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		u, ok := source.SplitHTTPS(s)
		if !ok {
			return
		}

		if u.Host != "" && !source.AllowedHost(u.Host) {
			t.Fatalf("SplitHTTPS(%q) takes the host %q, which AllowedHost refuses", s, u.Host)
		}

		checkParts(t, s, u.Parts)

		back, ok := source.SplitHTTPS(u.String())
		if !ok || back.Host != u.Host || !slices.Equal(back.Parts, u.Parts) {
			t.Fatalf("SplitHTTPS(%q) = %+v, %v, want %+v", u.String(), back, ok, u)
		}
	})
}

// FuzzURLPattern checks Validate and Match on any pattern and target:
//   - a match needs two URLs that SplitHTTPS takes, with one host and the
//     same number of path parts;
//   - ".git" at the end of the target does not change the answer;
//   - a valid pattern with no "*" matches itself.
func FuzzURLPattern(f *testing.F) {
	f.Add("https://git.example.com/platform/*", "https://git.example.com/platform/modules.git")
	f.Add("https://git.example.com/platform/*", "https://git.example.com/platform/a/b")
	f.Add("https://GIT.example.com/a/b.git", "https://git.example.com/a/b")
	f.Add("https://git.example.com/*/b", "https://git.example.com/a/b")
	f.Add("https://git.example.com/a", "https://git.example.com/a%2Fb")
	f.Add("https://127.0.0.1/a", "https://127.0.0.1/a")

	f.Fuzz(func(t *testing.T, pattern, target string) {
		p := source.URLPattern(pattern)
		valid := p.Validate() == nil

		checkMatchAgrees(t, p, target)
		checkGitSuffix(t, p, target)

		if valid && !strings.Contains(pattern, "*") && !p.Match(pattern) {
			t.Fatalf("the valid pattern %q does not match itself", pattern)
		}
	})
}

// FuzzDecodeFilter checks that DecodeFilter fails only with its two errors,
// with no copy of the text, and that each filter that it takes passes
// Validate and encodes to one text that decodes to the same filter.
func FuzzDecodeFilter(f *testing.F) {
	for _, s := range []string{
		"", "{}", "null", "[]", `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`,
		`{"submodules":["https://git.example.com/platform/*"],"largeFiles":["https://git.example.com/a/b"],` +
			`"failOnDenied":true,"protectedBranches":["main","release/1.0"]}`,
		`{"submodules":["https://10.0.0.1/a"]}`, `{"protectedBranches":["main","main"]}`, `{"other":1}`, `{} {}`,
		`{"submodules":null}`,
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		filter, err := source.DecodeFilter(text)
		if err != nil {
			checkDecodeError(t, text, err)

			return
		}

		checkFilterRoundTrip(t, text, filter)
	})
}

// FuzzValidBranchName checks that each name that ValidBranchName takes has
// at most MaxBranchName bytes and keeps these rules of git for a ref name: no
// control character, space or revision syntax character, no "..", "//" or
// "@{", no part that starts with "." or ends with ".lock", and no "-" or "/"
// at the start.
func FuzzValidBranchName(f *testing.F) {
	for _, s := range []string{
		"main", "release/1.0", "feature/a_b-c", "-x", "a..b", ".hidden", "a/.b", "x.lock", "a//b", "a/", "a b",
		"a@{b", "a~1", "a^", "a:b", "a?b", "a*b", "a[b", "a\\b", "é",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		if !source.ValidBranchName(name) {
			return
		}

		switch {
		case name == "" || len(name) > source.MaxBranchName:
			t.Fatalf("ValidBranchName(%q) takes a name of %d bytes", name, len(name))
		case strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"),
			strings.HasSuffix(name, ".lock"):
			t.Fatalf("ValidBranchName(%q) takes a name with a refused start or end", name)
		case strings.Contains(name, ".."), strings.Contains(name, "//"), strings.Contains(name, "@{"),
			strings.Contains(name, "/."), strings.Contains(name, ".lock/"):
			t.Fatalf("ValidBranchName(%q) takes a name with a refused sequence", name)
		case strings.ContainsFunc(name, func(r rune) bool { return r <= ' ' || r >= 0x7f }),
			strings.ContainsAny(name, "~^:?*[\\"):
			t.Fatalf("ValidBranchName(%q) takes a name with a refused character", name)
		default:
		}
	})
}

// checkParts fails t for a path part that SplitHTTPS(s) must not answer.
func checkParts(t *testing.T, s string, parts []string) {
	t.Helper()

	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/@?#:%\\ \t\n\r\x00") {
			t.Fatalf("SplitHTTPS(%q) has the path part %q", s, part)
		}
	}
}

// checkMatchAgrees fails t when p matches target, but the two URLs do not
// parse, or have another host or another number of path parts.
func checkMatchAgrees(t *testing.T, p source.URLPattern, target string) {
	t.Helper()

	if !p.Match(target) {
		return
	}

	pu, pok := source.SplitHTTPS(string(p))
	tu, tok := source.SplitHTTPS(target)

	if !pok || !tok || !strings.EqualFold(pu.Host, tu.Host) || len(pu.Parts) != len(tu.Parts) {
		t.Fatalf("URLPattern(%q).Match(%q) is true for URLs that do not agree", p, target)
	}
}

// checkGitSuffix fails t when ".git" at the end of target changes the
// answer of p.
func checkGitSuffix(t *testing.T, p source.URLPattern, target string) {
	t.Helper()

	tu, ok := source.SplitHTTPS(target)
	if !ok || len(tu.Parts) == 0 || strings.HasSuffix(tu.Parts[len(tu.Parts)-1], ".git") {
		return
	}

	plain := tu.String()
	if p.Match(plain) != p.Match(plain+".git") {
		t.Fatalf("URLPattern(%q) matches %q and %q differently", p, plain, plain+".git")
	}
}

// checkDecodeError fails t unless err of DecodeFilter(text) is one of its
// two errors and holds no copy of the text.
func checkDecodeError(t *testing.T, text string, err error) {
	t.Helper()

	if !errors.Is(err, source.ErrInvalid) && !errors.Is(err, source.ErrFilterTooLarge) {
		t.Fatalf("DecodeFilter(%q) = %v, want ErrInvalid or ErrFilterTooLarge", text, err)
	}

	if len(text) >= 16 && strings.Contains(err.Error(), text) {
		t.Fatalf("DecodeFilter(%q): the error holds the text", text)
	}
}

// checkFilterRoundTrip fails t unless filter, which DecodeFilter(text)
// answered, passes Validate and encodes to one text that decodes to the
// same filter.
func checkFilterRoundTrip(t *testing.T, text string, filter source.Filter) {
	t.Helper()

	if err := filter.Validate(); err != nil {
		t.Fatalf("DecodeFilter(%q) takes a filter that Validate refuses: %v", text, err)
	}

	encoded, err := source.EncodeFilter(filter)
	if err != nil {
		// The JSON encoder escapes some characters, so a filter near the
		// size limit can grow above it.
		if errors.Is(err, source.ErrFilterTooLarge) {
			return
		}

		t.Fatalf("EncodeFilter of DecodeFilter(%q): %v", text, err)
	}

	again, err := source.DecodeFilter(encoded)
	if err != nil {
		t.Fatalf("DecodeFilter(%q): %v", encoded, err)
	}

	if back, backErr := source.EncodeFilter(again); backErr != nil || back != encoded {
		t.Fatalf("EncodeFilter(DecodeFilter(%q)) = %q, %v, want the same text", encoded, back, backErr)
	}
}
