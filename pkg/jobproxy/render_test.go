package jobproxy_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// secretText is a value that no error may hold.
const secretText = "s3cr3t-value"

// vars returns the values of a job of the unit acme/payments.
func vars() jobproxy.Vars {
	return jobproxy.Vars{
		Tool:      "opentofu",
		Namespace: "acme",
		Name:      "payments",
		Labels:    map[string]string{"team": "team-a", "tier": "gold", "path": "a/b", "wild": "*", "blank": ""},
		Operation: jobproxy.OperationPlan,
		Inputs:    map[string]string{"folder": "f-1", "dotdot": ".."},
		Profiles: map[string]map[string]jobproxy.Value{
			"grafana": {
				"org":   {Text: "42", Secret: false},
				"token": {Text: secretText, Secret: true},
			},
		},
	}
}

// pathSet returns a proxy rule set with one inspect rule "folders" and path.
func pathSet(t *testing.T, path string) jobproxy.RuleSet {
	t.Helper()

	set, err := jobproxy.Parse([]byte(`{"rules":[{"id":"folders","operations":["plan"],` +
		`"host":"grafana.example.com","mode":"inspect","methods":["GET"],"paths":["` + path + `"]}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	return set
}

func TestRenderFillsVariables(t *testing.T) {
	t.Parallel()

	set := pathSet(t, "/api/{{ unit.tool }}/{{unit.namespace}}/{{ unit.name }}/{{ unit.labels.team }}/"+
		"{{ operation }}/{{ inputs.folder }}/{{ profile.grafana.org }}/*/**")

	r, err := jobproxy.Render(set, vars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	got := r.Rules()[0].Paths[0]
	want := "/api/opentofu/acme/payments/team-a/plan/f-1/42/*/**"

	if got != want {
		t.Fatalf("rendered path %q, want %q", got, want)
	}
}

func TestRenderRefusesValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		variable string
	}{
		{"secret profile value", "profile.grafana.token"},
		{"value with a slash", "unit.labels.path"},
		{"empty value", "unit.labels.blank"},
		{"missing label", "unit.labels.owner"},
		{"missing input", "inputs.other"},
		{"missing profile", "profile.aws.region"},
		{"missing profile key", "profile.grafana.user"},
		{"value that is a wildcard", "unit.labels.wild"},
		{"value that is a dot segment", "inputs.dotdot"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := jobproxy.Render(pathSet(t, "/api/{{ "+tc.variable+" }}"), vars())
			if !errors.Is(err, jobproxy.ErrRender) {
				t.Fatalf("Render: got %v, want ErrRender", err)
			}

			msg := err.Error()
			if !strings.Contains(msg, "folders") || !strings.Contains(msg, tc.variable) {
				t.Fatalf("the error %q does not name the proxy rule and the variable", msg)
			}

			if strings.Contains(msg, secretText) || strings.Contains(msg, "a/b") {
				t.Fatalf("the error %q holds the value", msg)
			}
		})
	}
}

func TestRenderRefusesAnEmptyBuiltInValue(t *testing.T) {
	t.Parallel()

	v := vars()
	v.Name = ""

	_, err := jobproxy.Render(pathSet(t, "/api/{{ unit.name }}"), v)
	if !errors.Is(err, jobproxy.ErrRender) {
		t.Fatalf("Render: got %v, want ErrRender", err)
	}
}

func TestRenderRefusesAnInvalidSet(t *testing.T) {
	t.Parallel()

	set := jobproxy.RuleSet{Rules: []jobproxy.Rule{{
		ID: "a", Operations: []jobproxy.Operation{jobproxy.OperationPlan}, Host: "a.example.com",
		Port: 443, Mode: jobproxy.ModeInspect, Methods: []string{"GET"}, Paths: []string{"/a/%2e%2e/b"},
		Credential: "", ClientCertificate: "", ServerTrust: jobproxy.ServerTrustSystem,
	}}}

	if _, err := jobproxy.Render(set, vars()); !errors.Is(err, jobproxy.ErrInvalid) {
		t.Fatalf("Render: got %v, want ErrInvalid", err)
	}
}

func TestDigestIsStable(t *testing.T) {
	t.Parallel()

	set := pathSet(t, "/api/{{ unit.labels.team }}/{{ profile.grafana.org }}")

	first, err := jobproxy.Render(set, vars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	digest := first.Digest()
	if len(digest) != 64 {
		t.Fatalf("digest %q is not a hex SHA-256", digest)
	}

	// Maps of the same values, built in another order, give the same digest.
	for range 20 {
		v := vars()
		v.Labels = map[string]string{}
		v.Labels["wild"] = "*"
		v.Labels["tier"] = "gold"
		v.Labels["team"] = "team-a"

		again, renderErr := jobproxy.Render(set, v)
		if renderErr != nil {
			t.Fatalf("Render: %v", renderErr)
		}

		if again.Digest() != digest {
			t.Fatalf("digest %q, want %q", again.Digest(), digest)
		}
	}

	other := vars()
	other.Labels["team"] = "team-b"

	changed, err := jobproxy.Render(set, other)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if changed.Digest() == digest {
		t.Fatal("another value gave the same digest")
	}
}

func TestRulesReturnsACopy(t *testing.T) {
	t.Parallel()

	r, err := jobproxy.Render(pathSet(t, "/api/x"), vars())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	digest := r.Digest()
	rules := r.Rules()
	rules[0].Paths[0] = "/**"
	rules[0].Methods[0] = "DELETE"

	if r.Digest() != digest {
		t.Fatal("a change of the copy changed the rendered proxy rules")
	}
}
