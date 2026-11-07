package jobproxy_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// parse parses doc or fails the test.
func parse(t *testing.T, d []byte) jobproxy.RuleSet {
	t.Helper()

	set, err := jobproxy.Parse(d)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	return set
}

func TestJoinAllowsWhatEachSetAllows(t *testing.T) {
	t.Parallel()

	ns := parse(t, doc(rule("ns-api", "api.example.com", "")))
	blueprint := parse(t, doc(rule("bp-docs", "docs.example.com", "")))
	unit := parse(t, doc(`{"id":"unit-api","operations":["plan"],"host":"api.example.com","mode":"inspect",`+
		`"methods":["POST"],"paths":["/api/**"]}`))

	joined, err := jobproxy.Join(ns, blueprint, unit)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	r, err := jobproxy.Render(joined, vars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	tests := []struct {
		host, method, rule string
	}{
		{"api.example.com", "GET", "ns-api"},
		{"api.example.com", "POST", "unit-api"},
		{"docs.example.com", "GET", "bp-docs"},
		{"docs.example.com", "POST", ""},
	}

	for _, tc := range tests {
		d := jobproxy.Match(r, jobproxy.Request{
			Operation: jobproxy.OperationPlan, Host: tc.host, Port: 443,
			Method: tc.method, Path: "/api/x", PlainHTTP: false,
		})
		if d.RuleID != tc.rule || d.Allowed != (tc.rule != "") {
			t.Errorf("%s %s: got %+v, want rule %q", tc.method, tc.host, d, tc.rule)
		}
	}
}

func TestJoinRefuses(t *testing.T) {
	t.Parallel()

	a := parse(t, doc(rule("a", "api.example.com", "")))

	tests := []struct {
		name string
		sets []jobproxy.RuleSet
	}{
		{"an ID in two sets", []jobproxy.RuleSet{a, parse(t, doc(rule("a", "other.example.com", "")))}},
		{"two modes for one host", []jobproxy.RuleSet{a, parse(t, []byte(
			`{"rules":[{"id":"b","operations":["plan"],"host":"api.example.com","mode":"tunnel"}]}`,
		))}},
		{"an invalid set", []jobproxy.RuleSet{a, {Rules: []jobproxy.Rule{{
			ID: "x", Operations: nil, Host: "", Port: 0, Mode: "", Methods: nil, Paths: nil,
			Credential: "", ClientCertificate: "", ServerTrust: "",
		}}}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := jobproxy.Join(tc.sets...); !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Join: got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestJoinOfNoSetIsEmpty(t *testing.T) {
	t.Parallel()

	joined, err := jobproxy.Join()
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	if len(joined.Rules) != 0 {
		t.Fatalf("got %d proxy rules, want 0", len(joined.Rules))
	}
}
