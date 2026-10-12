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
//
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

// TestSecretCiphertextOfRejectsResourceWithoutURN proves a secret in a
// resource without a URN answers not-found: the masked read holds no path
// for such a resource, so the reveal lookup must not answer it either.
func TestSecretCiphertextOfRejectsResourceWithoutURN(t *testing.T) {
	t.Parallel()

	cipher := base64.StdEncoding.EncodeToString([]byte("orphan-cipher"))
	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[{` +
		`"type":"pkg:index:Res",` +
		`"inputs":{"password":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` + cipher + `"}},` +
		`"outputs":{}}]}`)

	if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/inputs/password"); err == nil {
		t.Fatal("URN-less resource succeeded, want a not-found error")
	} else if !isNotFound(err) {
		t.Fatalf("URN-less resource error = %v, want a not-found error", err)
	}

	plain := []byte(`{"secrets_providers":{"type":"service"},"resources":[null]}`)

	if _, _, err := stack.SecretCiphertextOf(plain, "/resources/0/inputs/password"); err == nil {
		t.Fatal("non-object resource succeeded, want a not-found error")
	} else if !isNotFound(err) {
		t.Fatalf("non-object resource error = %v, want a not-found error", err)
	}
}

// TestSecretCiphertextOfURNLast proves the streaming lookup finds the secret
// when the URN streams after the section holding it.
func TestSecretCiphertextOfURNLast(t *testing.T) {
	t.Parallel()

	cipher := base64.StdEncoding.EncodeToString([]byte("late-cipher"))
	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[{` +
		`"type":"pkg:index:Res","inputs":{},` +
		`"outputs":{"password":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` + cipher + `"}},` +
		`"urn":"urn:pulumi:dev::web::pkg:index:Res::res"}]}`)

	got, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/password")
	if err != nil {
		t.Fatalf("ciphertext: %v", err)
	}

	if string(got) != "late-cipher" {
		t.Fatalf("ciphertext = %q, want late-cipher", got)
	}
}

// TestSecretCiphertextOfLargeSectionBeforeURN proves the lookup keeps no
// section of a resource in memory when the section streams before the URN.
// A 6 MiB section ahead of the URN must not show in the allocation of the
// lookup.
//
//nolint:paralleltest // The allocation measurement needs a quiet runtime, so this test never runs in parallel.
func TestSecretCiphertextOfLargeSectionBeforeURN(t *testing.T) {
	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("late-cipher")) + `"}`

	// The section holds 6 MiB as 1 KiB strings. One huge string would cost
	// the decoder about four times its size in buffers, which the test would
	// measure instead of the kept section.
	chunk := `"` + strings.Repeat("x", 1<<10) + `"`
	chunks := strings.Repeat(chunk+",", (6<<10)-1) + chunk

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"type":"a:index:A","inputs":{},"outputs":{"blob":[` + chunks + `],` +
		`"password":` + secret + `},` +
		`"urn":"urn:pulumi:dev::web::a:index:A::x"}]}`)

	path := "/resources/0/outputs/password"

	runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	got, masked, err := stack.SecretCiphertextOf(deployment, path)

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if string(got) != "late-cipher" {
		t.Fatalf("ciphertext = %q, want late-cipher", got)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	if growth := after.TotalAlloc - before.TotalAlloc; growth > 4<<20 {
		t.Fatalf("allocated = %d bytes, want below 4 MiB for a 6 MiB section", growth)
	}
}

// TestSecretCiphertextOfDuplicateURN proves the masked set and the reveal
// agree on the index of a resource whose URN repeats an earlier one. The
// secret answers at its array index, which the masked set lists; the index of
// the earlier resource answers not-found.
func TestSecretCiphertextOfDuplicateURN(t *testing.T) {
	t.Parallel()

	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("dup-cipher")) + `"}`

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},"outputs":{"plain":"v"}},` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},"outputs":{"k":` + secret + `}}]}`)

	const path = "/resources/1/outputs/k"

	masked, err := stack.MaskedPaths(deployment)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Contains(masked, path) {
		t.Fatalf("masked = %q, want it to hold %q", masked, path)
	}

	got, _, err := stack.SecretCiphertextOf(deployment, path)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if string(got) != "dup-cipher" {
		t.Fatalf("ciphertext = %q, want dup-cipher", got)
	}

	if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/k"); err == nil {
		t.Fatal("reveal of /resources/0/outputs/k answered, want not-found")
	}
}
