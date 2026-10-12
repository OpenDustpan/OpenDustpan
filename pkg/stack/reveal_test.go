package stack_test

import (
	"encoding/base64"
	"runtime"
	"slices"
	"strconv"
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

// TestSecretCiphertextOfEscapedKeys proves the streaming lookup unescapes
// value paths the same way as the masked read: keys with slashes and tildes
// resolve through their escaped tokens, and each masked path reveals its own
// ciphertext.
func TestSecretCiphertextOfEscapedKeys(t *testing.T) {
	t.Parallel()

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[{` +
		`"urn":"urn:pulumi:dev::web::pkg:index:Res::res","type":"pkg:index:Res",` +
		`"inputs":{"a/b":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("slash-cipher")) + `"},` +
		`"c~d":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("tilde-cipher")) + `"}},` +
		`"outputs":{}}]}`)

	masked, err := stack.MaskedPaths(deployment)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Contains(masked, "/resources/0/inputs/a~1b") {
		t.Fatalf("masked = %q, want the escaped slash path", masked)
	}

	if !slices.Contains(masked, "/resources/0/inputs/c~0d") {
		t.Fatalf("masked = %q, want the escaped tilde path", masked)
	}

	for path, want := range map[string]string{
		"/resources/0/inputs/a~1b": "slash-cipher",
		"/resources/0/inputs/c~0d": "tilde-cipher",
	} {
		cipher, _, err := stack.SecretCiphertextOf(deployment, path)
		if err != nil {
			t.Fatalf("ciphertext of %q: %v", path, err)
		}

		if string(cipher) != want {
			t.Fatalf("ciphertext of %q = %q, want %q", path, cipher, want)
		}
	}
}

// TestSecretCiphertextOfStreamsLargeDeployment proves a reveal near the end
// of a deployment much larger than the decoder buffer finds the secret while
// the heap allocated by the lookup stays far below the deployment size.
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestSecretCiphertextOfStreamsLargeDeployment(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	const fillers = 2600

	var sb strings.Builder

	sb.WriteString(`{"secrets_providers":{"type":"service"},"resources":[`)

	filler := `{"urn":"urn:pulumi:dev::web::filler:index:Filler::filler",` +
		`"type":"filler:index:Filler","inputs":{"blob":"` + strings.Repeat("x", 8000) + `"},"outputs":{}}`

	for i := range fillers {
		if i > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString(filler)
	}

	sb.WriteString(`,{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack","inputs":{},` +
		`"outputs":{"password":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("large-cipher")) + `"}}}]}`)

	deployment := []byte(sb.String())
	sb.Reset()

	if len(deployment) < 20<<20 {
		t.Fatalf("deployment = %d bytes, want at least 20 MiB", len(deployment))
	}

	path := "/resources/" + strconv.Itoa(fillers) + "/outputs/password"

	// Warm the path once, so one-time allocations stay out of the
	// measurement.
	if _, _, err := stack.SecretCiphertextOf(deployment, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	cipher, masked, err := stack.SecretCiphertextOf(deployment, path)

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if string(cipher) != "large-cipher" {
		t.Fatalf("ciphertext = %q, want large-cipher", cipher)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	if growth := after.TotalAlloc - before.TotalAlloc; growth > 4<<20 {
		t.Fatalf(
			"heap allocated = %d bytes, want below 4 MiB for a %d-byte deployment",
			growth,
			len(deployment),
		)
	}
}
