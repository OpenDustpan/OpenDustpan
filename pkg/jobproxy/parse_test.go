package jobproxy_test

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// inspectRule is a valid inspect proxy rule in its wire form.
const inspectRule = `{"id":"grafana-folders","operations":["plan","apply"],"host":"grafana.example.com",` +
	`"mode":"inspect","methods":["GET","POST"],"paths":["/api/folders/{{ unit.labels.team }}/**"],` +
	`"credential":"grafana-token"}`

// tunnelRule is a valid tunnel proxy rule in its wire form.
const tunnelRule = `{"id":"registry","operations":["plan"],"host":"registry.example.com","port":8443,"mode":"tunnel"}`

// doc wraps proxy rules in a proxy rule set document.
func doc(rules ...string) []byte {
	return []byte(`{"rules":[` + strings.Join(rules, ",") + `]}`)
}

// rule returns a wire proxy rule with id, host and mode inspect, and the
// extra fields of extra (a JSON fragment that starts with a comma, or "").
func rule(id, host, extra string) string {
	return fmt.Sprintf(`{"id":%q,"operations":["plan"],"host":%q,"mode":"inspect",`+
		`"methods":["GET"],"paths":["/api/**"]%s}`, id, host, extra)
}

func TestParseAcceptsAValidSet(t *testing.T) {
	t.Parallel()

	set, err := jobproxy.Parse(doc(inspectRule, tunnelRule))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(set.Rules) != 2 {
		t.Fatalf("got %d proxy rules, want 2", len(set.Rules))
	}

	inspect, tunnel := set.Rules[0], set.Rules[1]

	if inspect.Port != jobproxy.DefaultPort || inspect.ServerTrust != jobproxy.ServerTrustSystem {
		t.Errorf("effective values: port %d, serverTrust %q; want %d and %q",
			inspect.Port, inspect.ServerTrust, jobproxy.DefaultPort, jobproxy.ServerTrustSystem)
	}

	if tunnel.Port != 8443 || tunnel.Mode != jobproxy.ModeTunnel {
		t.Errorf("tunnel: port %d, mode %q; want 8443 and tunnel", tunnel.Port, tunnel.Mode)
	}
}

func TestParseAcceptsAnEmptySet(t *testing.T) {
	t.Parallel()

	set, err := jobproxy.Parse([]byte(`{"rules":[]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(set.Rules) != 0 {
		t.Fatalf("got %d proxy rules, want 0", len(set.Rules))
	}
}

func TestParseRefuses(t *testing.T) {
	t.Parallel()

	many := make([]string, jobproxy.MaxRules+1)
	for i := range many {
		many[i] = rule(fmt.Sprintf("r%d", i), "api.example.com", "")
	}

	tests := []struct {
		name string
		doc  []byte
	}{
		{"not JSON", []byte(`{"rules":`)},
		{"a list", []byte(`[]`)},
		{"no rules field", []byte(`{}`)},
		{"rules null", []byte(`{"rules":null}`)},
		{"trailing data", []byte(`{"rules":[]} {}`)},
		{"unknown top field", []byte(`{"rules":[],"extra":1}`)},
		{"unknown rule field", doc(rule("a", "api.example.com", `,"note":"x"`))},
		{"key in another case", doc(rule("a", "api.example.com", `,"Host":"evil.example.com"`))},
		{"duplicate top key", []byte(`{"rules":[],"rules":[]}`)},
		{"duplicate rule key", doc(rule("a", "api.example.com", `,"host":"evil.example.com"`))},
		{"257 rules", doc(many...)},
		{
			"document over 64 KiB",
			append(doc(rule("a", "api.example.com", "")), make([]byte, jobproxy.MaxDocumentBytes)...),
		},
		{"empty id", doc(rule("", "api.example.com", ""))},
		{"id with dot", doc(rule("a.b", "api.example.com", ""))},
		{"id over 64 bytes", doc(rule(strings.Repeat("a", 65), "api.example.com", ""))},
		{"duplicate id", doc(rule("a", "api.example.com", ""), rule("a", "b.example.com", ""))},
		{"mixed-case host", doc(rule("a", "API.example.com", ""))},
		{"host with a dot at the end", doc(rule("a", "api.example.com.", ""))},
		{"host with no dot", doc(rule("a", "localhost", ""))},
		{"host with a port", doc(rule("a", "api.example.com:443", ""))},
		{"host with a scheme", doc(rule("a", "https://api.example.com", ""))},
		{"numeric host", doc(rule("a", "127.1", ""))},
		{"IPv4 literal", doc(rule("a", "10.0.0.5", ""))},
		{"public IPv4 literal", doc(rule("a", "8.8.8.8", ""))},
		{"IPv6 literal", doc(rule("a", "fd00::1", ""))},
		{"bracketed IPv6 literal", doc(rule("a", "[::1]", ""))},
		{"label with a leading dash", doc(rule("a", "-api.example.com", ""))},
		{"empty label", doc(rule("a", "api..example.com", ""))},
		{"port 0", doc(rule("a", "api.example.com", `,"port":0`))},
		{"port over 65535", doc(rule("a", "api.example.com", `,"port":65536`))},
		{"unknown mode", []byte(`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"open"}]}`)},
		{"no operation", []byte(`{"rules":[{"id":"a","operations":[],"host":"a.example.com","mode":"tunnel"}]}`)},
		{
			"unknown operation",
			[]byte(`{"rules":[{"id":"a","operations":["import"],"host":"a.example.com","mode":"tunnel"}]}`),
		},
		{
			"operation twice",
			[]byte(`{"rules":[{"id":"a","operations":["plan","plan"],"host":"a.example.com","mode":"tunnel"}]}`),
		},
		{
			"tunnel with paths",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"tunnel","paths":["/x"]}]}`,
			),
		},
		{
			"tunnel with a serverTrust",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"tunnel","serverTrust":"myca"}]}`,
			),
		},
		{
			"tunnel with a credential",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"tunnel","credential":"t"}]}`,
			),
		},
		{
			"inspect with no method",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","paths":["/x"]}]}`,
			),
		},
		{
			"inspect with no path",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","methods":["GET"]}]}`,
			),
		},
		{
			"method CONNECT",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","methods":["CONNECT"],"paths":["/x"]}]}`,
			),
		},
		{
			"lowercase method",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","methods":["get"],"paths":["/x"]}]}`,
			),
		},
		{
			"method twice",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","methods":["GET","GET"],"paths":["/x"]}]}`,
			),
		},
		{
			"path twice",
			[]byte(
				`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect","methods":["GET"],"paths":["/x","/x"]}]}`,
			),
		},
		{"credential with a dot", doc(rule("a", "api.example.com", `,"credential":"a.b"`))},
		{"serverTrust with a slash", doc(rule("a", "api.example.com", `,"serverTrust":"a/b"`))},
		{"two modes for one host", doc(rule("a", "api.example.com", ""),
			`{"id":"b","operations":["plan"],"host":"api.example.com","mode":"tunnel"}`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := jobproxy.Parse(tc.doc)
			if !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Parse: got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseRefusesPathPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
	}{
		{"empty", ""},
		{"relative", "api/x"},
		{"dot segment", "/api/../x"},
		{"single dot segment", "/api/./x"},
		{"encoded dot segment", "/api/%2e%2e/x"},
		{"encoded dot segment in uppercase", "/api/%2E%2E/x"},
		{"encoded slash", "/api/a%2Fb"},
		{"encoded slash in lowercase", "/api/a%2fb"},
		{"encoded backslash", "/api/a%5Cb"},
		{"encoded NUL", "/api/a%00b"},
		{"encoded percent", "/api/a%25b"},
		{"empty segment", "/api//x"},
		{"slash at the end", "/api/"},
		{"semicolon", "/api;x=1"},
		{"query", "/api?x=1"},
		{"fragment", "/api#x"},
		{"backslash", "/api\\x"},
		{"space", "/api x"},
		{"** not last", "/api/**/x"},
		{"partial wildcard", "/api/a*"},
		{"three stars", "/api/***"},
		{"unknown variable", "/api/{{ unit.id }}"},
		{"variable inside a segment", "/api/team-{{ unit.name }}"},
		{"empty variable", "/api/{{}}"},
		{"profile variable with no key", "/api/{{ profile.grafana }}"},
		{"over 2048 bytes", "/" + strings.Repeat("a", jobproxy.MaxPathBytes)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pattern := fmt.Sprintf(`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com",`+
				`"mode":"inspect","methods":["GET"],"paths":[%q]}]}`, tc.pattern)

			_, err := jobproxy.Parse([]byte(pattern))
			if !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Parse: got %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseAcceptsPathPatterns(t *testing.T) {
	t.Parallel()

	patterns := []string{
		"/",
		"/api",
		"/api/*/x",
		"/api/**",
		"/api/{{ unit.name }}",
		"/api/{{unit.namespace}}/{{ operation }}",
		"/api/{{ unit.labels.app.team }}",
		"/api/{{ inputs.folder }}",
		"/api/{{ profile.grafana.org-id }}",
		"/api/v1:batch",
		"/api/caf%C3%A9",
		"/api/%3B",
	}

	for _, p := range patterns {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			pattern := fmt.Sprintf(`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com",`+
				`"mode":"inspect","methods":["GET"],"paths":[%q]}]}`, p)

			if _, err := jobproxy.Parse([]byte(pattern)); err != nil {
				t.Fatalf("Parse: %v", err)
			}
		})
	}
}

func TestParseErrorHoldsNoValue(t *testing.T) {
	t.Parallel()

	_, err := jobproxy.Parse(doc(rule("a", "SECRET-HOST.example.com", "")))
	if !errors.Is(err, jobproxy.ErrInvalid) {
		t.Fatalf("Parse: got %v, want ErrInvalid", err)
	}

	if strings.Contains(err.Error(), "SECRET-HOST") {
		t.Fatalf("the error %q holds the value of the host", err)
	}
}

func TestParseIPHostWithAllowedRanges(t *testing.T) {
	t.Parallel()

	private := netip.MustParsePrefix("10.0.0.0/8")

	tests := []struct {
		name    string
		host    string
		allowed []netip.Prefix
		ok      bool
	}{
		{"inside the range", "10.1.2.3", []netip.Prefix{private}, true},
		{"outside the range", "192.168.1.1", []netip.Prefix{private}, false},
		{"public address", "8.8.8.8", []netip.Prefix{private}, false},
		{"loopback", "127.0.0.1", []netip.Prefix{private}, false},
		{"metadata address", "169.254.169.254", []netip.Prefix{private}, false},
		{"not canonical", "10.001.2.3", []netip.Prefix{private}, false},
		{"IPv4-mapped IPv6", "::ffff:10.1.2.3", []netip.Prefix{private}, false},
		{"no range", "10.1.2.3", nil, false},
		{"loopback range", "127.0.0.1", []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, false},
		{"range too wide", "10.1.2.3", []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := jobproxy.Parse(doc(rule("a", tc.host, "")), tc.allowed...)

			switch {
			case tc.ok && err != nil:
				t.Fatalf("Parse: %v", err)
			case !tc.ok && !errors.Is(err, jobproxy.ErrInvalid):
				t.Fatalf("Parse: got %v, want ErrInvalid", err)
			default:
				// The answer is the one that the case wants.
			}
		})
	}
}

func TestValidateRefusesHandBuiltRules(t *testing.T) {
	t.Parallel()

	valid := jobproxy.Rule{
		ID: "a", Operations: []jobproxy.Operation{jobproxy.OperationPlan}, Host: "a.example.com",
		Port: 443, Mode: jobproxy.ModeInspect, Methods: []string{"GET"}, Paths: []string{"/x"},
		Credential: "", ClientCertificate: "", ServerTrust: jobproxy.ServerTrustSystem,
	}

	tests := []struct {
		name   string
		change func(r *jobproxy.Rule)
	}{
		{"empty server trust", func(r *jobproxy.Rule) { r.ServerTrust = "" }},
		{"port 0", func(r *jobproxy.Rule) { r.Port = 0 }},
		{"dot segment", func(r *jobproxy.Rule) { r.Paths = []string{"/a/../b"} }},
		{"public IP host", func(r *jobproxy.Rule) { r.Host = "8.8.8.8" }},
		{"loopback IP host", func(r *jobproxy.Rule) { r.Host = "127.0.0.1" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := valid
			tc.change(&r)

			err := jobproxy.RuleSet{Rules: []jobproxy.Rule{r}}.Validate()
			if !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Validate: got %v, want ErrInvalid", err)
			}
		})
	}

	if err := (jobproxy.RuleSet{Rules: []jobproxy.Rule{valid}}).Validate(); err != nil {
		t.Fatalf("Validate of a valid rule: %v", err)
	}
}
