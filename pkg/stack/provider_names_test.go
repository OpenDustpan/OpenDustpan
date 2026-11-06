package stack_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestProviderNames proves the wire values of the secrets provider constants.
// A rename of a value changes what the Pulumi CLI reads, so the test pins each
// value.
func TestProviderNames(t *testing.T) {
	t.Parallel()

	if stack.SecretsProvidersMember != "secrets_providers" {
		t.Errorf("SecretsProvidersMember = %q, want %q", stack.SecretsProvidersMember, "secrets_providers")
	}

	if stack.ServiceSecretsProvider != "service" {
		t.Errorf("ServiceSecretsProvider = %q, want %q", stack.ServiceSecretsProvider, "service")
	}
}
