package stack_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestRebindSecretsProvider proves that the state of the service secrets
// provider names the stack that serves the deployment, and that every other
// deployment comes back byte for byte.
func TestRebindSecretsProvider(t *testing.T) {
	t.Parallel()

	ref := stack.Ref{Namespace: "dev", Project: "sec", Stack: "f2"}

	const (
		resources = `"resources":[{"urn":"urn:pulumi:f::sec::pulumi:pulumi:Stack::sec-f","type":"pulumi:pulumi:Stack"}]`
		stale     = `{"type":"service","state":{"url":"http://127.0.0.1:18080","owner":"dev","project":"sec","stack":"f"}}`
	)

	cases := []struct {
		name       string
		deployment string
		wantState  map[string]string
		unchanged  bool
	}{
		{
			name:       "an old stack name becomes the current one",
			deployment: `{"manifest":{},"secrets_providers":` + stale + `,` + resources + `}`,
			wantState: map[string]string{
				"url": "http://127.0.0.1:18080", "owner": "dev", "project": "sec", "stack": "f2",
			},
			unchanged: false,
		},
		{
			name:       "a provider entry after the resources",
			deployment: `{` + resources + `,"secrets_providers":` + stale + `}`,
			wantState: map[string]string{
				"url": "http://127.0.0.1:18080", "owner": "dev", "project": "sec", "stack": "f2",
			},
			unchanged: false,
		},
		{
			name: "a state of the current stack stays as it is",
			deployment: `{"secrets_providers":{"type":"service","state":` +
				`{"url":"http://x","owner":"dev","project":"sec","stack":"f2"}},` + resources + `}`,
			wantState: nil,
			unchanged: true,
		},
		{
			name:       "another provider stays as it is",
			deployment: `{"secrets_providers":{"type":"passphrase","state":{"salt":"v1:abc"}},` + resources + `}`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "no provider entry",
			deployment: `{"manifest":{},` + resources + `}`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "a service provider without a state",
			deployment: `{"secrets_providers":{"type":"service"},` + resources + `}`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "a state that is not an object",
			deployment: `{"secrets_providers":{"type":"service","state":"f"},` + resources + `}`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "a null deployment",
			deployment: `null`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "a deployment that is not JSON",
			deployment: `{"secrets_providers":`,
			wantState:  nil,
			unchanged:  true,
		},
		{
			name:       "a hostile name in a nested field is not a provider",
			deployment: `{"resources":[{"secrets_providers":` + stale + `}]}`,
			wantState:  nil,
			unchanged:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := []byte(tc.deployment)
			got := stack.RebindSecretsProvider(in, ref)

			if tc.unchanged {
				if !bytes.Equal(got, in) {
					t.Fatalf("deployment = %s, want it unchanged: %s", got, in)
				}

				return
			}

			checkRebound(t, got, tc.wantState, resources)
		})
	}
}

// TestRebindSecretsProviderRebindsEveryCopy proves that each top-level copy of
// a repeated secrets_providers member is rebound. The save of a checkpoint
// accepts a repeated member, and the Pulumi CLI reads the last copy, so the
// last copy must name the stack too.
func TestRebindSecretsProviderRebindsEveryCopy(t *testing.T) {
	t.Parallel()

	ref := stack.Ref{Namespace: "dev", Project: "sec", Stack: "f2"}

	const (
		stale = `{"type":"service","state":{"owner":"dev","project":"sec","stack":"f"}}`
		local = `{"type":"passphrase","state":{"salt":"v1:abc"}}`
	)

	cases := []struct {
		name       string
		deployment string
		wantNew    int
	}{
		{
			name:       "two service provider copies",
			deployment: `{"secrets_providers":` + stale + `,"resources":[],"secrets_providers":` + stale + `}`,
			wantNew:    2,
		},
		{
			name:       "a passphrase copy, then a service provider copy",
			deployment: `{"secrets_providers":` + local + `,"resources":[],"secrets_providers":` + stale + `}`,
			wantNew:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := stack.RebindSecretsProvider([]byte(tc.deployment), ref)

			if n := bytes.Count(got, []byte(`"stack":"f2"`)); n != tc.wantNew {
				t.Fatalf("copies that name the stack f2 = %d, want %d: %s", n, tc.wantNew, got)
			}

			if bytes.Contains(got, []byte(`"stack":"f"}`)) {
				t.Fatalf("a copy still names the old stack f: %s", got)
			}

			if !json.Valid(got) {
				t.Fatalf("deployment is not JSON: %s", got)
			}
		})
	}
}

// checkRebound checks that got is JSON, that its provider state holds want and
// that the resources are as they were.
func checkRebound(t *testing.T, got []byte, want map[string]string, resources string) {
	t.Helper()

	if !json.Valid(got) {
		t.Fatalf("deployment is not JSON: %s", got)
	}

	state := providerState(t, got)
	for key, value := range want {
		if state[key] != value {
			t.Errorf("state %q = %q, want %q", key, state[key], value)
		}
	}

	if !bytes.Contains(got, []byte(resources)) {
		t.Errorf("resources changed: %s", got)
	}
}

// providersDoc is the secrets provider entry of a deployment in the tests.
type providersDoc struct {
	State map[string]string `json:"state"`
}

// deploymentDoc is the part of a deployment that the tests read.
type deploymentDoc struct {
	Providers providersDoc `json:"secrets_providers"` //nolint:tagliatelle // The Pulumi wire name; it must not change.
}

// providerState reads the state of the secrets provider of a deployment.
func providerState(t *testing.T, deployment []byte) map[string]string {
	t.Helper()

	var doc deploymentDoc

	if err := json.Unmarshal(deployment, &doc); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}

	return doc.Providers.State
}
