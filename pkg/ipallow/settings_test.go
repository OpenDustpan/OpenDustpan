package ipallow_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/ipallow"
)

// TestInstallationListReducesSecurity is the hostile test of the security flag
// of the installation list: a clear and each widening of the list need a
// fresh sign-in, a narrowing or an equal list does not. It fails if the flag
// checks only a clear.
func TestInstallationListReducesSecurity(t *testing.T) {
	t.Parallel()

	var reduces func(old, next string) bool

	for _, d := range ipallow.SettingDeclarations() {
		if d.Key == ipallow.SettingInstallation {
			reduces = d.ReducesSecurity
		}
	}

	if reduces == nil {
		t.Fatal("the installation list has no security flag")
	}

	tests := []struct {
		name      string
		old, next string
		want      bool
	}{
		{"clear", "10.0.0.0/8", "", true},
		{"widen to all IPv4 and IPv6", "10.0.0.0/8", "0.0.0.0/0,::/0", true},
		{"widen to a larger prefix", "10.1.0.0/16", "10.0.0.0/8", true},
		{"add an entry", "10.0.0.0/8", "10.0.0.0/8,192.0.2.0/24", true},
		{"add an IPv6 entry", "10.0.0.0/8", "10.0.0.0/8,2001:db8::/32", true},
		{"first list", "", "10.0.0.0/8", true},
		{"old value does not decode", "bad", "10.0.0.0/8", true},
		{"new value does not decode", "10.0.0.0/8", "bad", true},
		{"narrow a prefix", "10.0.0.0/8", "10.1.0.0/16", false},
		{"remove an entry", "10.0.0.0/8,192.0.2.0/24", "10.0.0.0/8", false},
		{"same list", "10.0.0.0/8", "10.0.0.0/8", false},
		{"same list, other order", "10.0.0.0/8,192.0.2.0/24", "192.0.2.0/24,10.0.0.0/8", false},
		{"both empty", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := reduces(tc.old, tc.next); got != tc.want {
				t.Fatalf("ReducesSecurity(%q, %q) = %t, want %t", tc.old, tc.next, got, tc.want)
			}
		})
	}
}
