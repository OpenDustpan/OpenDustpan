//go:build e2e

package e2e_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

func TestIsMatrixEnd(t *testing.T) {
	cases := []struct {
		name     string
		versions string
		label    string
		want     bool
	}{
		{name: "default latest", versions: "", label: e2e.Latest, want: true},
		{name: "default oldest", versions: "", label: "3.200.0", want: true},
		{name: "default middle", versions: "", label: "3.250.0", want: false},
		{name: "highest without latest", versions: "3.200.0,3.250.0,3.300.0", label: "3.300.0", want: true},
		{name: "middle without latest", versions: "3.300.0,3.250.0,3.200.0", label: "3.250.0", want: false},
		{name: "only version", versions: "3.250.0", label: "3.250.0", want: true},
		{name: "not x.y.z", versions: "3.200.0,dev,3.250.0", label: "dev", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("E2E_PULUMI_VERSIONS", tc.versions)

			if got := e2e.Pulumi().IsMatrixEnd(tc.label); got != tc.want {
				t.Fatalf("IsMatrixEnd(%q) with matrix %q = %t, want %t", tc.label, tc.versions, got, tc.want)
			}
		})
	}
}

func TestVersionsPinnedLeavesOutLatest(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "pinned", value: "pinned", want: []string{"3.250.0", "3.200.0"}},
		{name: "pinned and latest", value: "pinned, latest", want: []string{"3.250.0", "3.200.0", e2e.Latest}},
		{name: "latest only", value: "latest", want: []string{e2e.Latest}},
		{name: "default", value: "", want: []string{e2e.Latest, "3.250.0", "3.200.0"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("E2E_PULUMI_VERSIONS", tc.value)

			if got := e2e.Pulumi().Versions(); !slices.Equal(got, tc.want) {
				t.Fatalf("Versions() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
