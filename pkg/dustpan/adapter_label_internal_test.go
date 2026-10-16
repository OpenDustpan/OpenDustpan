package dustpan

import "testing"

// TestAdapterForRequest proves the adapter label of each route family, from
// the route pattern and from the path when no pattern matched.
func TestAdapterForRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		path    string
		want    string
	}{
		{name: "pulumi pattern", pattern: "GET /api/user", path: "/api/user", want: "pulumi"},
		{name: "tfe pattern", pattern: "GET /api/v2/ping", path: "/api/v2/ping", want: "opentofu"},
		{name: "tfe discovery", pattern: "", path: "/.well-known/terraform.json", want: "opentofu"},
		{name: "http backend pattern", pattern: "/tofu/state/", path: "/tofu/state/acme/prod", want: "opentofu"},
		{name: "http backend path", pattern: "", path: "/tofu/state/acme/prod", want: "opentofu"},
		{name: "inventory", pattern: "GET /api/inventory/units", path: "/api/inventory/units", want: "inventory"},
		{name: "audit", pattern: "GET /api/audit/entries", path: "/api/audit/entries", want: adapterServer},
		{name: "installation", pattern: "", path: "/api/server/no-such-route", want: adapterServer},
		{name: "probe", pattern: "GET /health/live", path: "/health/live", want: adapterServer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := adapterForRequest(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("adapterForRequest(%q, %q) = %q, want %q", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
