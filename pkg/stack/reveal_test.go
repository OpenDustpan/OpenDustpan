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

// TestSecretCiphertextOfBoundsLargeStringToken pins the memory bound of the
// lookup. The deployment is about 20 MiB and holds one 2 MiB string in a
// resource before the target, so the lookup skips that string. jsontext
// buffers a string token whole while it skips it, so the heap growth may reach
// about four times the string. The bound is 4 MiB plus four times the string,
// and it must not grow with the size of the document.
//
//nolint:paralleltest // The allocation measurement needs a quiet runtime, so this test never runs in parallel.
func TestSecretCiphertextOfBoundsLargeStringToken(t *testing.T) {
	const (
		fillers    = 2600
		largeBytes = 2 << 20
	)

	filler := `{"urn":"urn:pulumi:dev::web::filler:index:Filler::filler",` +
		`"type":"filler:index:Filler","inputs":{"blob":"` + strings.Repeat("x", 8000) + `"},"outputs":{}}`

	large := `{"urn":"urn:pulumi:dev::web::large:index:Large::large",` +
		`"type":"large:index:Large","inputs":{},"outputs":{"blob":"` + strings.Repeat("y", largeBytes) + `"}}`

	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("large-cipher")) + `"}`

	target := `{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack","inputs":{},"outputs":{"password":` + secret + `}}`

	var sb strings.Builder

	sb.WriteString(`{"secrets_providers":{"type":"service"},"resources":[`)

	for range fillers {
		sb.WriteString(filler + ",")
	}

	sb.WriteString(large + "," + target + "]}")

	deployment := []byte(sb.String())
	sb.Reset()

	if len(deployment) < 20<<20 {
		t.Fatalf("deployment = %d bytes, want at least 20 MiB", len(deployment))
	}

	path := "/resources/" + strconv.Itoa(fillers+1) + "/outputs/password"

	// Warm the path once, so one-time allocations stay out of the measurement.
	if _, _, err := stack.SecretCiphertextOf(deployment, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

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

	if string(got) != "large-cipher" {
		t.Fatalf("ciphertext = %q, want large-cipher", got)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	bound := uint64(4<<20 + 4*largeBytes)
	if growth := after.TotalAlloc - before.TotalAlloc; growth > bound {
		t.Fatalf("allocated = %d bytes, want at most %d for a %d-byte deployment with one %d-byte string",
			growth, bound, len(deployment), largeBytes)
	}
}

// TestSecretCiphertextOfRejectsNonObjectElement proves a path at an array
// element that is not an object answers not-found, even when a later resource
// holds a secret. The lookup must not bind to the later resource's brace.
func TestSecretCiphertextOfRejectsNonObjectElement(t *testing.T) {
	t.Parallel()

	secret := `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte("later-cipher")) + `"}`

	for _, element := range []string{`null`, `1`, `"text"`, `[{"k":1}]`} {
		deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` + element + `,` +
			`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},"outputs":{"k":` +
			secret + `}}]}`)

		if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/k"); err == nil {
			t.Fatalf("element %s: reveal answered a later resource's secret, want not-found", element)
		}
	}
}

// secretObject returns a secret object holding raw as its ciphertext, with
// extra members appended before the closing brace.
func secretObject(raw, extra string) string {
	return `{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString([]byte(raw)) + `"` + extra + `}`
}

// TestSecretCiphertextOfRejectsSecretInsideSecret proves a path below a
// secret answers not-found. The masked set holds the outer secret only, so
// the inner secret is not a masked path and must not answer.
func TestSecretCiphertextOfRejectsSecretInsideSecret(t *testing.T) {
	t.Parallel()

	outer := secretObject("outer-cipher", `,"inner":`+secretObject("inner-cipher", ""))

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{"a":` + outer + `},"outputs":{}}]}`)

	masked, err := stack.MaskedPaths(deployment)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Contains(masked, "/resources/0/inputs/a") || slices.Contains(masked, "/resources/0/inputs/a/inner") {
		t.Fatalf("masked = %q, want the outer secret only", masked)
	}

	got, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/inputs/a")
	if err != nil || string(got) != "outer-cipher" {
		t.Fatalf("outer reveal = %q, %v; want outer-cipher", got, err)
	}

	if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/inputs/a/inner"); err == nil {
		t.Fatal("reveal of a secret inside a secret answered, want not-found")
	}
}

// TestSecretCiphertextOfRejectsDuplicateKeys proves a document with a repeated
// key fails the lookup, so the reveal never answers a value that the masked
// read, which keeps the last key, shows as plain.
func TestSecretCiphertextOfRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	const (
		head = `{"secrets_providers":{"type":"service"},"resources":[`
		urn  = `"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A"`
	)

	cases := map[string]string{
		"duplicate resources key": head + `{` + urn + `,"inputs":{},"outputs":{"k":` +
			secretObject("first", "") + `}}],"resources":[{` + urn + `,"inputs":{},"outputs":{"k":"plain"}}]}`,
		"duplicate outputs key": head + `{` + urn + `,"inputs":{},` +
			`"outputs":{"k":` + secretObject("first", "") + `},"outputs":{"k":"plain"}}]}`,
		"duplicate key in the value's parent": head + `{` + urn + `,"inputs":{},"outputs":{"k":` +
			secretObject("first", "") + `,"k":"plain"}}]}`,
	}

	for name, doc := range cases {
		if _, _, err := stack.SecretCiphertextOf([]byte(doc), "/resources/0/outputs/k"); err == nil {
			t.Errorf("%s: reveal answered, want an error", name)
		}
	}
}

// TestSecretCiphertextOfRejectsNonCanonicalPath proves a path that is not in
// RFC 6901 canonical form answers not-found: an index with a sign or a leading
// zero, or an escape other than ~0 and ~1. The key a~b is named a~0b.
func TestSecretCiphertextOfRejectsNonCanonicalPath(t *testing.T) {
	t.Parallel()

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},` +
		`"outputs":{"a~b":` + secretObject("tilde", "") + `}}]}`)

	for _, path := range []string{
		"/resources/+0/outputs/a~0b",
		"/resources/00/outputs/a~0b",
		"/resources/0/outputs/a~b",
		"/resources/0/outputs/a~2b",
		"/resources/0/outputs/a~0b~",
	} {
		if _, _, err := stack.SecretCiphertextOf(deployment, path); err == nil {
			t.Errorf("reveal of %q answered, want not-found", path)
		}
	}

	got, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/a~0b")
	if err != nil || string(got) != "tilde" {
		t.Fatalf("canonical reveal = %q, %v; want tilde", got, err)
	}
}

// TestSecretCiphertextOfDoesNotReadLargeTarget proves the lookup does not copy
// a large target value that is not a secret. A 12 MiB string at the path is
// not read at all, and a 12 MiB object of small members is streamed.
//
//nolint:paralleltest // The allocation measurement needs a quiet runtime, so this test never runs in parallel.
func TestSecretCiphertextOfDoesNotReadLargeTarget(t *testing.T) {
	const size = 12 << 20

	members := make([]string, 0, size/1024)
	for i := range size / 1024 {
		members = append(members, `"m`+strconv.Itoa(i)+`":"`+strings.Repeat("x", 1000)+`"`)
	}

	// The ancestor check reads the object that holds the target in full, so a
	// large string beside the target costs the bound of the document: about
	// four times the string. A small-member object costs the 4 MiB floor.
	cases := map[string]struct {
		value string
		bound uint64
	}{
		"string": {`"` + strings.Repeat("y", size) + `"`, 4<<20 + 4*size},
		"object": {`{` + strings.Join(members, ",") + `}`, 4 << 20},
	}

	for name, tc := range cases {
		value := tc.value
		deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
			`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},` +
			`"outputs":{"password":` + value + `}}]}`)

		const path = "/resources/0/outputs/password"

		runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.
		runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.

		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		_, _, err := stack.SecretCiphertextOf(deployment, path)

		runtime.ReadMemStats(&after)

		if err == nil {
			t.Errorf("%s: reveal answered a value that is not a secret", name)
		}

		if growth := after.TotalAlloc - before.TotalAlloc; growth > tc.bound {
			t.Errorf("%s: allocated = %d bytes, want at most %d for a %d-byte target",
				name, growth, tc.bound, len(deployment))
		}
	}
}

// TestSecretCiphertextOfDeepPathAllocatesLinearly reveals a secret at the end
// of a deep path, in a document of about 8 MiB that nests 4000 objects, each
// with a 2 KiB string beside the path. A lookup that copies or re-reads the
// subtree at each level allocates a multiple of the document size. The test
// bounds the bytes that one reveal allocates, which does not depend on the
// scheduler. It runs without t.Parallel, because the measurement needs a quiet
// runtime.
//
//nolint:paralleltest // The allocation measurement needs a quiet runtime, so this test never runs in parallel.
func TestSecretCiphertextOfDeepPathAllocatesLinearly(t *testing.T) {
	const (
		depth = 4000
		// maxAlloc is about twice the 2.6 MiB that one reveal allocates under
		// -race, measured when the bound was set.
		maxAlloc = 5 << 20
	)

	padding := strings.Repeat("p", 2048)

	var (
		open   strings.Builder
		tokens strings.Builder
	)

	for range depth {
		open.WriteString(`{"pad":"` + padding + `","a":`)
		tokens.WriteString("/a")
	}

	open.WriteString(secretObject("deep-cipher", ""))
	open.WriteString(strings.Repeat("}", depth))

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},` +
		`"outputs":` + open.String() + `}]}`)

	const path = "/resources/0/outputs"

	reveal := func() uint64 {
		runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.
		runtime.GC() //nolint:revive // The allocation measurement needs a quiet runtime, so the test collects first.

		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		got, _, err := stack.SecretCiphertextOf(deployment, path+tokens.String())

		runtime.ReadMemStats(&after)

		if err != nil || string(got) != "deep-cipher" {
			t.Fatalf("deep reveal = %q, %v; want deep-cipher", got, err)
		}

		return after.TotalAlloc - before.TotalAlloc
	}

	reveal() // A warm-up run, so one-time allocations are not counted.

	if alloc := reveal(); alloc > maxAlloc {
		t.Fatalf("deep reveal allocated %d bytes, want at most %d for a %d-byte document",
			alloc, maxAlloc, len(deployment))
	}
}

// TestSecretCiphertextOfRejectsSignedAncestorInAnyOrder proves an object that
// is a secret answers not-found for a path below it, whether its signature
// member streams before or after the member on the path, and whether or not
// the value below holds a usable ciphertext.
func TestSecretCiphertextOfRejectsSignedAncestorInAnyOrder(t *testing.T) {
	t.Parallel()

	const (
		sig   = `"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270"`
		inner = `"inner":{` + sig + `,"ciphertext":"aW5uZXI="}`
		bare  = `"inner":{` + sig + `}`
	)

	cases := map[string]string{
		"signature first":                `{` + sig + `,` + inner + `}`,
		"signature last":                 `{` + inner + `,` + sig + `}`,
		"signature last, no ciphertext":  `{` + bare + `,` + sig + `}`,
		"signature first, no ciphertext": `{` + sig + `,` + bare + `}`,
	}

	for name, outer := range cases {
		deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
			`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},` +
			`"outputs":{"a":` + outer + `}}]}`)

		_, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/a/inner")
		if !isNotFound(err) {
			t.Errorf("%s: reveal error = %v, want not-found", name, err)
		}
	}
}

// TestSecretCiphertextOfKeyWithTildeTwo proves the lookup escapes a key that
// holds the text "~2". The key a~2b is named a~02b in a path. The path a~2b
// holds an escape that does not exist, so it answers not-found.
func TestSecretCiphertextOfKeyWithTildeTwo(t *testing.T) {
	t.Parallel()

	deployment := []byte(`{"secrets_providers":{"type":"service"},"resources":[` +
		`{"urn":"urn:pulumi:dev::web::a:index:A::x","type":"a:index:A","inputs":{},` +
		`"outputs":{"a~2b":` + secretObject("tilde-two", "") + `}}]}`)

	masked, err := stack.MaskedPaths(deployment)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Contains(masked, "/resources/0/outputs/a~02b") {
		t.Fatalf("masked = %q, want the escaped path a~02b", masked)
	}

	got, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/a~02b")
	if err != nil || string(got) != "tilde-two" {
		t.Fatalf("reveal of a~02b = %q, %v; want tilde-two", got, err)
	}

	if _, _, err := stack.SecretCiphertextOf(deployment, "/resources/0/outputs/a~2b"); !isNotFound(err) {
		t.Fatalf("reveal of a~2b error = %v, want not-found", err)
	}
}
