package main

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
)

// TestEgressPublicOnlyFlag shows the sources of --egress-public-only: the
// default is off, and the flag and the variable turn it on.
func TestEgressPublicOnlyFlag(t *testing.T) {
	t.Parallel()

	on := map[string]string{"DUSTPAN_EGRESS_PUBLIC_ONLY": "true"}

	tests := []struct {
		name string
		env  map[string]string
		args []string
		want egress.PublicOnly
	}{
		{"default", nil, nil, false},
		{"flag", nil, []string{"--egress-public-only"}, true},
		{"flag false", nil, []string{"--egress-public-only=false"}, false},
		{"variable", on, nil, true},
		{"flag over variable", on, []string{"--egress-public-only=false"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := parseServeFlags(t, tt.env, tt.args...)

			if got := f.publicOnly(); got != tt.want {
				t.Fatalf("publicOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}
