package main

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
)

func TestSinkPortsAllowTheListedPorts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		allowed []int
		denied  []int
	}{
		{"unset", "", []int{443}, []int{22, 8443}},
		{"one port", "8443", []int{443, 8443}, []int{22, 9443}},
		{"two ports with spaces", " 8443, 9443 ", []int{443, 8443, 9443}, []int{22}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ports, err := parseSinkPorts(tt.value)
			if err != nil {
				t.Fatalf("parseSinkPorts(%q) = %v", tt.value, err)
			}

			checkPorts(t, ports, tt.allowed, tt.denied)
		})
	}
}

// checkPorts checks that ports allows each port of allowed and no port of
// denied.
func checkPorts(t *testing.T, ports egress.Ports, allowed, denied []int) {
	t.Helper()

	for _, p := range allowed {
		if !ports.Allows(p) {
			t.Errorf("port %d is refused, want allowed", p)
		}
	}

	for _, p := range denied {
		if ports.Allows(p) {
			t.Errorf("port %d is allowed, want refused", p)
		}
	}
}

func TestSinkPortsRefuseAnItemThatIsNoPort(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"https", "8000-9000", "0", "65536", "-1", "8443,x"} {
		if _, err := parseSinkPorts(value); !errors.Is(err, errDestinationPorts) {
			t.Errorf("parseSinkPorts(%q) = %v, want errDestinationPorts", value, err)
		}
	}
}
