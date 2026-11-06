package jobproxy_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

func TestCanonicalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"root", "/", true},
		{"plain", "/api/v1/folders", true},
		{"sub-delimiters", "/api/a!$&'()*+,=:@b", true},
		{"encoded non-ASCII", "/api/caf%C3%A9", true},
		{"encoded semicolon", "/api/a%3Bb", true},
		{"encoded space", "/api/a%20b", true},
		{"uppercase stays", "/API/Folders", true},
		{"empty", "", false},
		{"relative", "api", false},
		{"double slash", "//api", false},
		{"empty segment", "/api//x", false},
		{"slash at the end", "/api/", false},
		{"dot", "/api/./x", false},
		{"two dots", "/api/../x", false},
		{"two dots at the end", "/api/..", false},
		{"two encoded dots", "/api/%2e%2e/x", false},
		{"two encoded dots in uppercase", "/api/%2E%2E/x", false},
		{"one of two dots encoded", "/api/.%2E/x", false},
		{"encoded letter", "/api/%61", false},
		{"encoded slash", "/api/a%2Fb", false},
		{"encoded slash in lowercase", "/api/a%2fb", false},
		{"encoded backslash", "/api/a%5Cb", false},
		{"encoded backslash in lowercase", "/api/a%5cb", false},
		{"encoded NUL", "/api/a%00b", false},
		{"encoded percent", "/api/a%2541", false},
		{"encoded control", "/api/a%0Ab", false},
		{"encoded DEL", "/api/a%7Fb", false},
		{"lowercase hex", "/api/caf%c3%a9", false},
		{"short escape", "/api/a%2", false},
		{"bad escape", "/api/a%zz", false},
		{"encoded invalid UTF-8", "/api/a%FFb", false},
		{"raw NUL", "/api/a\x00b", false},
		{"raw non-UTF-8", "/api/a\xffb", false},
		{"raw non-ASCII", "/api/café", false},
		{"semicolon", "/api;jsessionid=1", false},
		{"query", "/api?x=1", false},
		{"fragment", "/api#x", false},
		{"backslash", "/api\\x", false},
		{"space", "/api x", false},
		{"too long", "/" + strings.Repeat("a", jobproxy.MaxPathBytes), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := jobproxy.CanonicalPath(tc.path)
			if ok != tc.ok {
				t.Fatalf("CanonicalPath(%q): ok %v, want %v", tc.path, ok, tc.ok)
			}

			if ok && got != tc.path {
				t.Fatalf("CanonicalPath(%q) = %q: it changed the path", tc.path, got)
			}
		})
	}
}
