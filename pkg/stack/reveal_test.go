package stack_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// revealDeployment returns a deployment with the service provider and one
// secret output holding raw as its ciphertext.
func revealDeployment(raw string) string {
	cipher := base64.StdEncoding.EncodeToString([]byte(raw))

	return `{"secrets_providers":{"type":"service"},"resources":[{` +
		`"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack",` +
		`"inputs":{},"outputs":{"password":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` + cipher + `"},"greeting":"hi"}}]}`
}

// TestMaskedPathsHoldsSecret proves the masked set names the secret output
// path of the deployment.
func TestMaskedPathsHoldsSecret(t *testing.T) {
	t.Parallel()

	masked, err := stack.MaskedPaths([]byte(revealDeployment("cipher")))
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if len(masked) != 1 || !strings.HasSuffix(masked[0], "/outputs/password") {
		t.Fatalf("masked = %q, want one path ending in /outputs/password", masked)
	}
}

// TestSecretsProviderDefaultsToService proves a deployment without a
// provider entry reads as the service provider.
func TestSecretsProviderDefaultsToService(t *testing.T) {
	t.Parallel()

	if got := stack.SecretsProvider([]byte(`{"resources":[]}`)); got != "service" {
		t.Fatalf("provider = %q, want service", got)
	}

	if got := stack.SecretsProvider([]byte(revealDeployment("cipher"))); got != "service" {
		t.Fatalf("provider = %q, want service", got)
	}
}

// TestSecretsProviderNamesOther proves a passphrase deployment names its
// provider.
func TestSecretsProviderNamesOther(t *testing.T) {
	t.Parallel()

	deployment := `{"secrets_providers":{"type":"passphrase"},"resources":[]}`

	if got := stack.SecretsProvider([]byte(deployment)); got != "passphrase" {
		t.Fatalf("provider = %q, want passphrase", got)
	}
}

// TestSecretCiphertextOf proves the ciphertext at a masked path decodes to
// the stored bytes, and that an unknown path fails closed with a 404
// sentinel.
func TestSecretCiphertextOf(t *testing.T) {
	t.Parallel()

	deployment := []byte(revealDeployment("cipher-bytes"))

	masked, err := stack.MaskedPaths(deployment)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	cipher, _, err := stack.SecretCiphertextOf(deployment, masked[0])
	if err != nil {
		t.Fatalf("ciphertext: %v", err)
	}

	if string(cipher) != "cipher-bytes" {
		t.Fatalf("ciphertext = %q, want cipher-bytes", cipher)
	}

	if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/greeting"); err == nil {
		t.Fatal("plain path succeeded, want a not-found error")
	} else if !isNotFound(err) {
		t.Fatalf("plain path error = %v, want a not-found error", err)
	}
}

// TestPlainValueDecodesJSON proves JSON plaintext stays JSON and other
// plaintext reads as a string.
func TestPlainValueDecodesJSON(t *testing.T) {
	t.Parallel()

	if got := stack.PlainValue([]byte(`"hi"`)); got != "hi" {
		t.Fatalf("plain value = %#v, want hi", got)
	}

	if got := stack.PlainValue([]byte(`not json`)); got != "not json" {
		t.Fatalf("plain value = %#v, want the raw string", got)
	}
}

// isNotFound reports whether err wraps the not-found sentinel of the masked
// read.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "resource not found")
}
