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
		{name: "latest label", versions: "", label: e2e.Latest, want: true},
		{name: "default newest", versions: "", label: "3.262.0", want: true},
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
			want:  []string{"3.262.0", "3.250.0", "3.200.0"},
		},
		{
			name:  "pulumi pinned and latest",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "pinned, latest",
			want:  []string{"3.262.0", "3.250.0", "3.200.0", e2e.Latest},
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
			want:  []string{"3.262.0", "3.250.0", "3.200.0"},
		},
		{
			name:  "pulumi pinned twice",
			tool:  e2e.Pulumi(),
			env:   "E2E_PULUMI_VERSIONS",
			value: "pinned,3.250.0",
			want:  []string{"3.262.0", "3.250.0", "3.200.0", "3.250.0"},
		},
		{
			name:  "tofu pinned",
			tool:  e2e.OpenTofu(),
			env:   "E2E_TOFU_VERSIONS",
			value: "pinned",
			want:  []string{"1.12.12", "1.11.14", "1.10.10"},
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

func TestSingle(t *testing.T) {
	cases := []struct {
		name       string
		pulumi     string
		tofu       string
		wantPulumi string
		wantTofu   string
	}{
		{
			name:       "not set",
			pulumi:     "",
			tofu:       "",
			wantPulumi: "3.262.0",
			wantTofu:   "1.12.12",
		},
		{
			name:       "latest",
			pulumi:     "latest",
			tofu:       "latest",
			wantPulumi: e2e.Latest,
			wantTofu:   e2e.Latest,
		},
		{
			name:       "pinned",
			pulumi:     "pinned",
			tofu:       "pinned",
			wantPulumi: "3.262.0",
			wantTofu:   "1.12.12",
		},
		{
			name:       "latest then a pin",
			pulumi:     "latest,3.250.0",
			tofu:       "latest,1.11.14",
			wantPulumi: "3.250.0",
			wantTofu:   "1.11.14",
		},
		{
			name:       "commas only",
			pulumi:     ",",
			tofu:       ",",
			wantPulumi: "",
			wantTofu:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("E2E_PULUMI_VERSIONS", tc.pulumi)
			t.Setenv("E2E_TOFU_VERSIONS", tc.tofu)

			if got := e2e.Pulumi().Single(); got != tc.wantPulumi {
				t.Fatalf("Pulumi Single() with %q = %q, want %q", tc.pulumi, got, tc.wantPulumi)
			}

			if got := e2e.OpenTofu().Single(); got != tc.wantTofu {
				t.Fatalf("OpenTofu Single() with %q = %q, want %q", tc.tofu, got, tc.wantTofu)
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
