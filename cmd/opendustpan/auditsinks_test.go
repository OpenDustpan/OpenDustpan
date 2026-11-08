package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob/fs"
)

// noEnv is a lookup that finds no variable.
func noEnv(string) (string, bool) { return "", false }

// envOf returns a lookup that finds only the given variables.
func envOf(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]

		return v, ok
	}
}

// openTestSinks opens items against a fresh blob directory and closes the
// sinks when the test ends.
func openTestSinks(t *testing.T, items []string, lookup func(string) (string, bool)) (openedSinks, error) {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	sinks, err := openAuditSinks(items, blobs, lookup)
	if err == nil {
		t.Cleanup(func() {
			if closeErr := sinks.close(); closeErr != nil {
				t.Errorf("close sinks: %v", closeErr)
			}
		})
	}

	return sinks, err
}

func TestAuditSinksEmptyValueOpensNothing(t *testing.T) {
	t.Parallel()

	sinks, err := openTestSinks(t, []string{" ", ""}, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if len(sinks.ship) != 0 {
		t.Fatalf("sinks: got %d, want 0", len(sinks.ship))
	}
}

func TestAuditSinksNamesKindsAndIntervals(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")
	items := []string{
		"file:" + path,
		" siem=syslog+tcp://127.0.0.1:6514?facility=16&max-size=2048 ",
		"archive=blob:audit",
	}

	sinks, err := openTestSinks(t, items, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got := make([]string, 0, len(sinks.ship))
	for _, s := range sinks.ship {
		got = append(got, s.Name+"@"+s.Interval.String())
	}

	want := "file@0s siem@0s archive@" + blobSinkInterval.String()
	if strings.Join(got, " ") != want {
		t.Fatalf("sinks: got %v, want %s", got, want)
	}

	if len(sinks.files) != 1 {
		t.Fatalf("file sinks: got %d, want 1", len(sinks.files))
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the file sink did not create its file: %v", statErr)
	}
}

func TestAuditSinkNameBeforeAnEqualsSignInAPathIsNoName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a=b.jsonl")

	sinks, err := openTestSinks(t, []string{"file:" + path}, noEnv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if sinks.ship[0].Name != "file" {
		t.Fatalf("name: got %q, want file", sinks.ship[0].Name)
	}
}

func TestAuditSinksRefuseBadItems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "unknown kind", raw: "kafka://broker:9092"},
		{name: "syslog over unix", raw: "syslog+unix:///dev/log"},
		{name: "syslog facility not a number", raw: "syslog+udp://127.0.0.1:514?facility=audit"},
		{name: "syslog facility out of range", raw: "syslog+udp://127.0.0.1:514?facility=24"},
		{name: "syslog max-size not a number", raw: "syslog+udp://127.0.0.1:514?max-size=big"},
		{name: "syslog max-size too low", raw: "syslog+udp://127.0.0.1:514?max-size=100"},
		{name: "syslog tls with a missing CA file", raw: "syslog+tls://127.0.0.1:6514?ca-file=/nonexistent/ca.pem"},
		{name: "webhook without a secret", raw: "webhook:https://hooks.example/audit"},
		{name: "webhook over plain http", raw: "webhook:http://hooks.example/audit"},
		{name: "blob with an invalid prefix", raw: "blob:Audit Logs"},
		{name: "file with an empty path", raw: "file:"},
		{name: "blob under the namespaces prefix", raw: "blob:namespaces/audit"},
		{name: "blob under the directory of a namespace", raw: "blob:namespaces/ns-acmeacmeacmeacmeacme/tofu"},
		{name: "blob under the Pulumi state prefix", raw: "blob:pulumi"},
		{name: "blob under the audit checkpoint prefix", raw: "blob:audit-checkpoints/audit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := noEnv
			if strings.HasPrefix(tc.raw, "webhook:http:") {
				lookup = envOf(map[string]string{webhookSecretEnvVar: "s3cret"})
			}

			_, err := openTestSinks(t, []string{tc.raw}, lookup)
			if err == nil {
				t.Fatalf("open %q: got nil error", tc.raw)
			}

			if !errors.Is(err, errAuditSink) && !errors.Is(err, errNoWebhookSecret) {
				t.Fatalf("open %q: got %v, want an --audit-sink error", tc.raw, err)
			}
		})
	}
}

// TestAuditSinkRefusesTheNamespacesPrefix proves that an operator sink under
// namespaces/ is refused with the reserved prefix error. The state blobs and
// the item key blobs live there, and the sweepers delete unknown objects.
func TestAuditSinkRefusesTheNamespacesPrefix(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"blob:namespaces", "blob:namespaces/audit", "blob:namespaces/ns-acmeacmeacmeacmeacme/x"} {
		_, err := openTestSinks(t, []string{raw}, noEnv)
		if !errors.Is(err, errReservedPrefix) {
			t.Errorf("open %q: err = %v, want errReservedPrefix", raw, err)
		}
	}
}

func TestWebhookSecretComesFromTheVariableOrAPrivateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	private := filepath.Join(dir, "private")
	shared := filepath.Join(dir, "shared")

	if err := os.WriteFile(private, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write private secret: %v", err)
	}

	if err := os.WriteFile(shared, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write shared secret: %v", err)
	}

	// The test makes a file that others can read on purpose.
	chmodErr := os.Chmod(shared, 0o644) //nolint:gosec // See the comment above.
	if chmodErr != nil {
		t.Fatalf("chmod shared secret: %v", chmodErr)
	}

	cases := []struct {
		vars    map[string]string
		name    string
		want    string
		wantErr error
	}{
		{
			name:    "variable wins over the file",
			vars:    map[string]string{webhookSecretEnvVar: "from-env", webhookSecretFileEnvVar: private},
			want:    "from-env",
			wantErr: nil,
		},
		{
			name:    "private file",
			vars:    map[string]string{webhookSecretFileEnvVar: private},
			want:    "from-file",
			wantErr: nil,
		},
		{
			name:    "file that others can read",
			vars:    map[string]string{webhookSecretFileEnvVar: shared},
			want:    "",
			wantErr: errSecretFilePermissions,
		},
		{name: "nothing set", vars: map[string]string{}, want: "", wantErr: errNoWebhookSecret},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := webhookSecret(envOf(tc.vars))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("secret: got %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil || string(got) != tc.want {
				t.Fatalf("secret: got %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

// TestSecretFromRefusesControlCharacters proves that a secret with a
// control character is refused, and that the error names the variable,
// never the value.
func TestSecretFromRefusesControlCharacters(t *testing.T) {
	t.Parallel()

	const value = "secret-value"

	dir := t.TempDir()
	nulFile := filepath.Join(dir, "nul")

	if err := os.WriteFile(nulFile, []byte("before\x00after"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	for _, tc := range []struct {
		name   string
		vars   map[string]string
		source string
	}{
		{
			name:   "fingerprint token",
			vars:   map[string]string{oidcSecretEnvVar: "\x00fingerprint:00112233445566778899aabbccddeeff"},
			source: oidcSecretEnvVar,
		},
		{
			name:   "newline",
			vars:   map[string]string{oidcSecretEnvVar: value + "\n" + value},
			source: oidcSecretEnvVar,
		},
		{
			name:   "DEL",
			vars:   map[string]string{oidcSecretEnvVar: value + "\x7f"},
			source: oidcSecretEnvVar,
		},
		{
			name:   "NUL in the file",
			vars:   map[string]string{oidcSecretFileEnvVar: nulFile},
			source: oidcSecretFileEnvVar,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSecretControlRefused(t, tc.vars, tc.source, value)
		})
	}

	got, err := secretFrom(
		envOf(map[string]string{oidcSecretEnvVar: value}), oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret,
	)
	if err != nil || string(got) != value {
		t.Fatalf("secretFrom of a normal secret = %q, %v; want %q", got, err, value)
	}
}

// checkSecretControlRefused proves that secretFrom refuses vars with the
// sentinel, names source in the error, and never repeats value or the text
// of the file.
func checkSecretControlRefused(t *testing.T, vars map[string]string, source, value string) {
	t.Helper()

	got, err := secretFrom(envOf(vars), oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret)
	if !errors.Is(err, errSecretControlChar) {
		t.Fatalf("secretFrom = %q, %v; want %v", got, err, errSecretControlChar)
	}

	if !strings.Contains(err.Error(), source) {
		t.Errorf("error %q does not name %s", err, source)
	}

	for _, text := range []string{value, "before", "after", "fingerprint"} {
		if strings.Contains(err.Error(), text) {
			t.Errorf("error %q holds %q", err, text)
		}
	}
}

func TestAWebhookSinkOpensWithASecret(t *testing.T) {
	t.Parallel()

	sinks, err := openTestSinks(t, []string{"webhook:https://hooks.example/audit?a=1,2"}, envOf(map[string]string{
		webhookSecretEnvVar: "s3cret",
	}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if sinks.ship[0].Name != "webhook" || sinks.ship[0].Interval != 0 {
		t.Fatalf("webhook sink: got %+v", sinks.ship[0])
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeOpensTheAuditSinkFile(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	args := []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--audit-sink", "file:" + path,
	}

	if err := startServer(t, args...); err != nil {
		t.Fatalf("serve with a file audit sink: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("serve did not open the audit sink file: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("audit sink file mode %04o, want 0600", perm)
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeRefusesAnUnknownAuditSink(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()

	var logs lockedBuffer

	err := serve(t.Context(), []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--audit-sink", "kafka://broker:9092",
	}, &logs)
	if !errors.Is(err, errAuditSink) {
		t.Fatalf("serve with an unknown sink: got %v, want an --audit-sink error", err)
	}
}
