package jobproxy_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// matchSet is the proxy rule set of the match tests.
const matchSet = `{"rules":[
{"id":"folders","operations":["plan","apply"],"host":"grafana.example.com","mode":"inspect",
 "methods":["GET","POST"],"paths":["/api/folders/{{ unit.labels.team }}/**","/api/search"],"credential":"token"},
{"id":"health","operations":["plan"],"host":"grafana.example.com","mode":"inspect",
 "methods":["GET"],"paths":["/api/health","/api/orgs/*/users"]},
{"id":"root","operations":["plan"],"host":"plain.example.com","port":80,"mode":"inspect",
 "methods":["GET"],"paths":["/"]},
{"id":"plain-secret","operations":["plan"],"host":"plain.example.com","port":80,"mode":"inspect",
 "methods":["POST"],"paths":["/login"],"credential":"token"},
{"id":"registry","operations":["plan"],"host":"registry.example.com","mode":"tunnel"}
]}`

// rendered parses and renders matchSet for the team team-a.
func rendered(tb testing.TB) jobproxy.Rendered {
	tb.Helper()

	set, err := jobproxy.Parse([]byte(matchSet))
	if err != nil {
		tb.Fatalf("Parse: %v", err)
	}

	r, err := jobproxy.Render(set, vars())
	if err != nil {
		tb.Fatalf("Render: %v", err)
	}

	return r
}

// req returns a GET request of the operation plan to grafana.example.com:443.
func req(path string) jobproxy.Request {
	return jobproxy.Request{
		Operation: jobproxy.OperationPlan, Host: "grafana.example.com", Port: 443,
		Method: "GET", Path: path, PlainHTTP: false,
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()

	r := rendered(t)

	with := func(path string, change func(*jobproxy.Request)) jobproxy.Request {
		q := req(path)
		change(&q)

		return q
	}

	tests := []struct {
		name   string
		req    jobproxy.Request
		rule   string
		reason jobproxy.Reason
	}{
		{"folder of the team", req("/api/folders/team-a/dash"), "folders", jobproxy.ReasonNone},
		{"deep folder of the team", req("/api/folders/team-a/a/b/c"), "folders", jobproxy.ReasonNone},
		{"exact path", req("/api/search"), "folders", jobproxy.ReasonNone},
		{"one-segment wildcard", req("/api/orgs/1/users"), "health", jobproxy.ReasonNone},
		{"root path", with("/", func(q *jobproxy.Request) {
			q.Host, q.Port, q.PlainHTTP = "plain.example.com", 80, true
		}), "root", jobproxy.ReasonNone},
		{"tunnel", with("", func(q *jobproxy.Request) {
			q.Host, q.Method = "registry.example.com", ""
		}), "registry", jobproxy.ReasonNone},
		{
			"POST is allowed", with("/api/search", func(q *jobproxy.Request) { q.Method = "POST" }),
			"folders", jobproxy.ReasonNone,
		},
		{"apply operation", with("/api/search", func(q *jobproxy.Request) {
			q.Operation = jobproxy.OperationApply
		}), "folders", jobproxy.ReasonNone},

		{"folder of another team", req("/api/folders/team-b/dash"), "", jobproxy.ReasonNoRuleMatched},
		{"** needs one segment", req("/api/folders/team-a"), "", jobproxy.ReasonNoRuleMatched},
		{"* is one segment only", req("/api/orgs/1/2/users"), "", jobproxy.ReasonNoRuleMatched},
		{"* needs a segment", req("/api/orgs/users"), "", jobproxy.ReasonNoRuleMatched},
		{"case of the path", req("/API/search"), "", jobproxy.ReasonNoRuleMatched},
		{"longer path", req("/api/search/x"), "", jobproxy.ReasonNoRuleMatched},
		{
			"DELETE is not listed", with("/api/search", func(q *jobproxy.Request) { q.Method = "DELETE" }),
			"", jobproxy.ReasonNoRuleMatched,
		},
		{
			"lowercase method", with("/api/search", func(q *jobproxy.Request) { q.Method = "get" }),
			"", jobproxy.ReasonNoRuleMatched,
		},

		{"dot segment", req("/api/folders/team-a/../team-b/x"), "", jobproxy.ReasonPathNotCanonical},
		{"encoded dot segment", req("/api/folders/team-a/%2e%2e/team-b/x"), "", jobproxy.ReasonPathNotCanonical},
		{"encoded slash", req("/api/folders/team-a%2F..%2Fteam-b/x"), "", jobproxy.ReasonPathNotCanonical},
		{"empty segment", req("/api//search"), "", jobproxy.ReasonPathNotCanonical},
		{"query in the path", req("/api/search?x=1"), "", jobproxy.ReasonPathNotCanonical},
		{
			"encoded fullwidth solidus", req("/api/folders/team-a%EF%BC%8F..%EF%BC%8Fteam-b/x"),
			"", jobproxy.ReasonPathNotCanonical,
		},
		{"empty path on an inspect host", req(""), "", jobproxy.ReasonPathNotCanonical},

		{
			"unknown host", with("/api/search", func(q *jobproxy.Request) { q.Host = "evil.example.com" }),
			"", jobproxy.ReasonNoRuleHost,
		},
		{
			"mixed-case host", with("/api/search", func(q *jobproxy.Request) { q.Host = "Grafana.example.com" }),
			"", jobproxy.ReasonNoRuleHost,
		},
		{"host with a dot at the end", with("/api/search", func(q *jobproxy.Request) {
			q.Host = "grafana.example.com."
		}), "", jobproxy.ReasonNoRuleHost},
		{
			"another port", with("/api/search", func(q *jobproxy.Request) { q.Port = 8443 }),
			"", jobproxy.ReasonNoRuleHost,
		},
		{"operation with no rule", with("/api/search", func(q *jobproxy.Request) {
			q.Operation = jobproxy.OperationDestroy
		}), "", jobproxy.ReasonNoRuleHost},
		{"tunnel for another operation", with("", func(q *jobproxy.Request) {
			q.Host, q.Operation = "registry.example.com", jobproxy.OperationApply
		}), "", jobproxy.ReasonNoRuleHost},

		{"credential over plain HTTP", with("/login", func(q *jobproxy.Request) {
			q.Host, q.Port, q.Method, q.PlainHTTP = "plain.example.com", 80, "POST", true
		}), "plain-secret", jobproxy.ReasonPlainHTTPInjection},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := jobproxy.Match(r, tc.req)

			if d.Allowed != (tc.reason == jobproxy.ReasonNone) || d.RuleID != tc.rule || d.Reason != tc.reason {
				t.Fatalf("Match: got %+v, want rule %q and reason %q", d, tc.rule, tc.reason)
			}
		})
	}
}

func TestMatchOfTheZeroRenderedDenies(t *testing.T) {
	t.Parallel()

	d := jobproxy.Match(jobproxy.Rendered{}, req("/api/search"))
	if d.Allowed || d.Reason != jobproxy.ReasonNoRuleHost {
		t.Fatalf("Match: got %+v, want a denial with no-rule-host", d)
	}
}
