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
		tool  e2e.Tool
		env   string
		value string
		want  []string
	}{
		{
			name:  "pulumi pinned",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "pinned",
			want:  []string{"3.250.0", "3.200.0"},
		},
		{
			name:  "pulumi pinned and latest",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "pinned, latest",
			want:  []string{"3.250.0", "3.200.0", e2e.Latest},
		},
		{
			name:  "pulumi latest only",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "latest",
			want:  []string{e2e.Latest},
		},
		{
			name:  "pulumi default",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "",
			want:  []string{e2e.Latest, "3.250.0", "3.200.0"},
		},
		{
			name:  "pulumi pinned twice",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "pinned,3.250.0",
			want:  []string{"3.250.0", "3.200.0", "3.250.0"},
		},
		{
			name:  "tofu pinned",
			tool:  e2e.OpenTofu(),
			env:   "E2E_TOFU_VERSIONS",
			value: "pinned",
			want:  []string{"1.12.7", "1.11.14"},
		},
		{
			name:  "tofu latest only",
			tool:  e2e.OpenTofu(),
			env:   "E2E_TOFU_VERSIONS",
			value: "latest",
			want:  []string{e2e.Latest},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, tc.value)

			if got := tc.tool.Versions(); !slices.Equal(got, tc.want) {
				t.Fatalf("Versions() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		version string
		minimum string
		want    bool
		wantErr bool
	}{
		{name: "older minor", version: "3.272.0", minimum: "3.273.0", want: false, wantErr: false},
		{name: "same version", version: "3.273.0", minimum: "3.273.0", want: true, wantErr: false},
		{name: "newer minor", version: "3.274.0", minimum: "3.273.0", want: true, wantErr: false},
		{name: "newer major", version: "4.0.0", minimum: "3.273.0", want: true, wantErr: false},
		{name: "numeric not lexical", version: "3.1000.0", minimum: "3.273.0", want: true, wantErr: false},
		{name: "not x.y.z", version: "dev", minimum: "3.273.0", want: false, wantErr: true},
		{name: "bad minimum", version: "3.273.0", minimum: "latest", want: false, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := e2e.VersionAtLeast(tc.version, tc.minimum)
			if (err != nil) != tc.wantErr {
				t.Fatalf("VersionAtLeast(%q, %q) error = %v, want error %t", tc.version, tc.minimum, err, tc.wantErr)
			}

			if got != tc.want {
				t.Fatalf("VersionAtLeast(%q, %q) = %t, want %t", tc.version, tc.minimum, got, tc.want)
			}
		})
	}
}
