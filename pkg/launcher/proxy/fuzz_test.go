package proxy_test

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// FuzzParseConnectTarget checks that an accepted target is already in its
// one valid spelling, and that the parser never panics.
func FuzzParseConnectTarget(f *testing.F) {
	for _, s := range []string{
		"api.example.com:443", "127.0.0.1:443", "[::1]:443", "a.b:0", "A.b:1", "a.b.:1",
		"a.b:65536", "a.b:0443", "", ":", "a..b:1", "a.b:1:1", "a@b.c:1", "a.b\x00:1",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		host, port, err := proxy.ParseConnectTarget(s)
		if err != nil {
			return
		}

		if host != strings.ToLower(host) || strings.HasSuffix(host, ".") || strings.ContainsAny(host, ":[]@ /\\%") {
			t.Fatalf("accepted a host that is not valid: %q", host)
		}

		if port < 1 || port > 65535 {
			t.Fatalf("accepted port %d", port)
		}

		if got := host + ":" + strconv.Itoa(port); got != s {
			t.Fatalf("the parser changed its input: %q -> %q", s, got)
		}
	})
}

// holdsForm reports whether body holds a form of secret.
func holdsForm(secret string, body []byte) bool {
	for _, form := range proxy.ProbeForms(secret) {
		if bytes.Contains(body, form) {
			return true
		}
	}

	return false
}

// FuzzScanWindow checks the scan on random bodies, secrets and split points:
// a stream with a form always breaks, a stream with none always passes
// intact, and the bytes before a break hold no whole form.
func FuzzScanWindow(f *testing.F) {
	f.Add("secret-token", "xxxxsecret-tokenxxxx", 7)
	f.Add("a\"b", "pre a\\\"b post", 3)
	f.Add("tok", "dG9r", 2)
	f.Add("tok", "nothing here", 5)

	f.Fuzz(func(t *testing.T, secret, body string, split int) {
		if secret == "" || len(secret) > 256 || len(body) > 4096 {
			return
		}

		split = max(0, min(split, len(body)))
		r := proxy.ProbeScanReader([]string{secret}, &chunkReader{chunks: []string{body[:split], body[split:]}})

		got, err := io.ReadAll(r)
		holds := holdsForm(secret, []byte(body))

		if holds && !errors.Is(err, proxy.ErrAuthInResponse) {
			t.Fatalf("a body with a form did not break: err %v", err)
		}

		if !holds && (err != nil || string(got) != body) {
			t.Fatalf("a clean body changed: err %v, %q -> %q", err, body, got)
		}

		if holdsForm(secret, got) {
			t.Fatal("the reader released a whole form")
		}
	})
}
