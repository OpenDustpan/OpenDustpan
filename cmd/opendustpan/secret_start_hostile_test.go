package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// The tests in this file drive the start path of each secret setting, as if
// secretFrom were the only layer between the variable or the file and the
// consumer of the secret. A secret setting is a variable and its _FILE
// variable: the client secret of the OpenID Connect provider, the bind
// password of the LDAP sign-in, and the HMAC key of the audit and the log
// webhook sinks.
//
// The package internal/cli reads no secret value. It keeps only a
// fingerprint of each variable (SnapshotEnv), so it has no table here.

// secretStart is one secret setting and the code that the start runs for it.
type secretStart struct {
	name    string
	env     string
	fileEnv string
	// start runs the start path with the lookup and answers the secret that
	// the consumer holds, or the error of the start.
	start func(t *testing.T, lookup func(string) (string, bool)) (string, error)
}

func secretStarts() []secretStart {
	return []secretStart{
		{"oidc client secret", oidcSecretEnvVar, oidcSecretFileEnvVar, startOIDCSecret},
		{"ldap bind password", ldapBindEnvVar, ldapBindFileEnvVar, startLDAPSecret},
		{
			"audit webhook key", webhookSecretEnvVar, webhookSecretFileEnvVar,
			func(t *testing.T, lookup func(string) (string, bool)) (string, error) {
				t.Helper()

				return startWebhookSecret(t, auditSinkConfig(nil, lookup, nil))
			},
		},
		{
			"log webhook key", logWebhookSecretEnvVar, logWebhookSecretFileEnvVar,
			func(t *testing.T, lookup func(string) (string, bool)) (string, error) {
				t.Helper()

				cfg := auditSinkConfig(nil, lookup, nil)
				cfg.setting = errLogSink
				cfg.secretEnv, cfg.secretFileEnv = logWebhookSecretEnvVar, logWebhookSecretFileEnvVar

				return startWebhookSecret(t, cfg)
			},
		},
	}
}

func startOIDCSecret(t *testing.T, lookup func(string) (string, bool)) (string, error) {
	t.Helper()

	var f serveFlags

	f.oidcIssuer, f.oidcProvider, f.oidcClientID = "https://idp.example", "corp", "dustpan"

	provider, ok, err := f.oidcProviderOption(lookup)
	if err != nil {
		return "", err
	}

	if !ok {
		t.Fatal("oidcProviderOption gave no provider")
	}

	return provider.ClientSecret, nil
}

func startLDAPSecret(t *testing.T, lookup func(string) (string, bool)) (string, error) {
	t.Helper()

	var f serveFlags

	f.ldap = completeLDAP()

	providers, err := f.ldapProviders(lookup, func(string) error { return nil })
	if err != nil {
		return "", err
	}

	if len(providers) != 1 {
		t.Fatalf("ldapProviders gave %d providers, want 1", len(providers))
	}

	return providers[0].BindPassword.Reveal(), nil
}

// startWebhookSecret opens the webhook sink of cfg against a test receiver,
// sends one record and answers the candidate key that the signature proves, or an
// empty text when no candidate matches.
func startWebhookSecret(t *testing.T, cfg sinkConfig) (string, error) {
	t.Helper()

	var (
		mu        sync.Mutex
		timestamp string
		signature string
		body      []byte
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request body: %v", readErr)
		}

		mu.Lock()
		timestamp = r.Header.Get(webhook.HeaderTimestamp)
		signature = r.Header.Get(webhook.HeaderSignature)
		body = payload
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	cfg.rules = egress.Rules{Allow: nil, LoopbackForTests: true}
	cfg.options = egresshttp.Options{RootCAs: serverPool(t, srv), Resolver: nil}

	sink, err := openWebhookSink(srv.URL+"/hook", cfg)
	if err != nil {
		return "", err
	}

	t.Cleanup(func() {
		if closeErr := sink.Close(); closeErr != nil {
			t.Errorf("close webhook sink: %v", closeErr)
		}
	})

	if writeErr := sink.Write(t.Context(), []logsink.Record{testRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	mu.Lock()
	defer mu.Unlock()

	for _, candidate := range webhookKeyCandidates() {
		if webhook.Sign([]byte(candidate), timestamp, body) == signature {
			return candidate, nil
		}
	}

	return "", nil
}

// webhookKeyCandidates answers the keys that the consumer test can tell apart.
// A webhook sink never shows its key, so the test recomputes the signature
// of the request with each candidate.
func webhookKeyCandidates() []string { return []string{"  pass  ", "pass", " pass", "pass "} }

// secretInput gives the variables of one setting for a secret text in a
// variable or in a file, and the name of the variable that holds it.
func secretInput(t *testing.T, setting secretStart, source, text string) (map[string]string, string) {
	t.Helper()

	if source == "variable" {
		return map[string]string{setting.env: text}, setting.env
	}

	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	return map[string]string{setting.fileEnv: path}, setting.fileEnv
}

// checkSecretStartRefuses proves that the start of setting refuses text in
// source, names the variable in the error, and never repeats canary.
func checkSecretStartRefuses(t *testing.T, setting secretStart, source, text, canary string) {
	t.Helper()

	vars, named := secretInput(t, setting, source, text)

	got, err := setting.start(t, envOf(vars))
	if !errors.Is(err, errSecretControlChar) {
		t.Fatalf("start = %q, %v; want %v", got, err, errSecretControlChar)
	}

	if !strings.Contains(err.Error(), named) {
		t.Errorf("error %q does not name %s", err, named)
	}

	if strings.Contains(err.Error(), canary) {
		t.Errorf("error %q holds the value", err)
	}
}

// TestSecretStartRefusesHostileText proves, for each secret setting and for
// its variable and its file, that the start refuses a lone CR, two LF, a
// byte order mark, a NUL, and the other hidden text, names the variable in
// the error, and never repeats the value.
func TestSecretStartRefusesHostileText(t *testing.T) {
	t.Parallel()

	const canary = "canary-secret"

	hostile := []struct{ name, value string }{
		{"lone CR", canary + "\r"},
		{"two LF", canary + "\n\n"},
		{"BOM", "\xef\xbb\xbf" + canary},
		{"NUL", canary + "\x00" + canary},
		{"C1 control", canary + "\u0085"},
		{"line separator", canary + "\u2028"},
		{"invalid UTF-8", canary + "\xff"},
	}

	for _, setting := range secretStarts() {
		for _, h := range hostile {
			for _, source := range []string{"variable", "file"} {
				t.Run(setting.name+"/"+h.name+"/"+source, func(t *testing.T) {
					t.Parallel()

					checkSecretStartRefuses(t, setting, source, h.value, canary)
				})
			}
		}
	}
}

// TestSecretStartKeepsSpaces proves that the consumer of each secret setting
// gets "  pass  " from the file "  pass  \n", with no trim by the start or by
// the consumer.
func TestSecretStartKeepsSpaces(t *testing.T) {
	t.Parallel()

	const want = "  pass  "

	for _, setting := range secretStarts() {
		for _, source := range []string{"variable", "file"} {
			t.Run(setting.name+"/"+source, func(t *testing.T) {
				t.Parallel()

				text := want
				if source == "file" {
					text += "\n"
				}

				vars, _ := secretInput(t, setting, source, text)

				got, err := setting.start(t, envOf(vars))
				if err != nil || got != want {
					t.Fatalf("consumer secret = %q, %v; want %q", got, err, want)
				}
			})
		}
	}
}
