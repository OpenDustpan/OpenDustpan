package jobproxy_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// FuzzParse checks that Parse never panics, and that a set it accepts holds
// at most MaxRules proxy rules that each pass Validate.
func FuzzParse(f *testing.F) {
	f.Add(doc(inspectRule, tunnelRule))
	f.Add([]byte(`{"rules":[],"rules":[]}`))
	f.Add(doc(rule("a", "API.example.com.", "")))
	f.Add(doc(rule("a", "10.0.0.1", "")))
	f.Add([]byte(`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect",` +
		`"methods":["GET"],"paths":["/a/**/b"]}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		set, err := jobproxy.Parse(data)
		if err != nil {
			if !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Parse: %v is not ErrInvalid", err)
			}

			return
		}

		if len(set.Rules) > jobproxy.MaxRules {
			t.Fatalf("Parse accepted %d proxy rules", len(set.Rules))
		}

		if err := set.Validate(); err != nil {
			t.Fatalf("Validate of a parsed set: %v", err)
		}
	})
}

// FuzzCanonicalPath checks that a canonical path has no dot segment, no
// empty segment, no encoded slash or backslash, no NUL and is valid UTF-8,
// and that CanonicalPath never changes it.
func FuzzCanonicalPath(f *testing.F) {
	for _, s := range []string{"/", "/api/x", "/a/../b", "/a/%2e%2e/b", "/a%2Fb", "/a%5cb", "//a", "/a/", "/a%00"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, p string) {
		got, ok := jobproxy.CanonicalPath(p)
		if !ok {
			return
		}

		if got != p {
			t.Fatalf("CanonicalPath(%q) = %q", p, got)
		}

		if reason := notCanonical(p); reason != "" {
			t.Fatalf("CanonicalPath accepted %q: %s", p, reason)
		}
	})
}

// notCanonical returns why p is not canonical, or "". It is a second,
// simpler statement of the rules of CanonicalPath.
func notCanonical(p string) string {
	if !utf8.ValidString(p) || strings.Contains(p, "\x00") {
		return "not UTF-8 or holds NUL"
	}

	lower := strings.ToLower(p)
	for _, bad := range []string{"%2f", "%5c", "%00", "%2e", "%25", "\\", ";", "?", "#"} {
		if strings.Contains(lower, bad) {
			return "holds " + bad
		}
	}

	if p == "/" {
		return ""
	}

	for seg := range strings.SplitSeq(p[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "has an empty or a dot segment"
		}
	}

	return ""
}

// FuzzMatch checks that Match never allows a path that is not canonical, a
// host or a port that no proxy rule names, or a path outside the folder of
// the team.
func FuzzMatch(f *testing.F) {
	f.Add("GET", "grafana.example.com", 443, "/api/folders/team-a/x")
	f.Add("GET", "grafana.example.com", 443, "/api/folders/team-a/../team-b")
	f.Add("GET", "grafana.example.com", 443, "/api/folders/team-a%2F..%2Fteam-b/x")
	f.Add("GET", "GRAFANA.example.com", 443, "/api/search")
	f.Add("GET", "grafana.example.com.", 443, "/api/search")
	f.Add("POST", "plain.example.com", 80, "/login")

	r := rendered(f)

	f.Fuzz(func(t *testing.T, method, host string, port int, path string) {
		req := jobproxy.Request{
			Operation: jobproxy.OperationPlan, Host: host, Port: port,
			Method: method, Path: path, PlainHTTP: false,
		}

		if reason := wrongDecision(req, jobproxy.Match(r, req)); reason != "" {
			t.Fatalf("Match(%+v): %s", req, reason)
		}
	})
}

// wrongDecision returns why d is wrong for req against matchSet, or "".
func wrongDecision(req jobproxy.Request, d jobproxy.Decision) string {
	switch {
	case !d.Allowed && d.Reason == jobproxy.ReasonNone:
		return "a denial has no reason"
	case !d.Allowed:
		return ""
	case d.RuleID == "registry":
		if req.Host != "registry.example.com" || req.Port != 443 {
			return "the tunnel proxy rule allowed another host or port"
		}

		return ""
	}

	if _, ok := jobproxy.CanonicalPath(req.Path); !ok {
		return "it allowed a path that is not canonical"
	}

	if req.Host != "grafana.example.com" && req.Host != "plain.example.com" {
		return "it allowed a host that no proxy rule names"
	}

	if strings.HasPrefix(req.Path, "/api/folders/") && !strings.HasPrefix(req.Path, "/api/folders/team-a/") {
		return "it allowed a path outside the folder of the team"
	}

	return ""
}
