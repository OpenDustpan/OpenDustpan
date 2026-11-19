package session_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestValidIssuer proves the one issuer rule for good and hostile values.
func TestValidIssuer(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"https://id.example.com":     true,
		"http://127.0.0.2:9000":      true,
		"http://localhost":           true,
		"http://id.example.com":      false,
		"https://u:p@id.example.com": false,
		"https://id.example.com?x=1": false,
		"https://id.example.com#x":   false,
		"https://":                   false,
		"":                           false,
	}

	for issuer, want := range tests {
		if got := session.ValidIssuer(issuer); got != want {
			t.Errorf("ValidIssuer(%q) = %v, want %v", issuer, got, want)
		}
	}
}
