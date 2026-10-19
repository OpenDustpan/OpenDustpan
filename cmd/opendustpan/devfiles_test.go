package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{in: "plain", want: "'plain'"},
		{in: "", want: "''"},
		{in: "with space", want: "'with space'"},
		{in: "it's", want: `'it'\''s'`},
		{in: "$HOME `x` \"y\"", want: "'$HOME `x` \"y\"'"},
	}

	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// loadEnvFile sources text in a POSIX sh with the starting environment and
// answers the variables that the file sets, as the shell of a user sees them.
func loadEnvFile(t *testing.T, text string, start map[string]string, names []string) map[string]string {
	t.Helper()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on this host")
	}

	file := filepath.Join(t.TempDir(), "dev.env")
	if writeErr := os.WriteFile(file, []byte(text), 0o600); writeErr != nil {
		t.Fatalf("write dev.env: %v", writeErr)
	}

	var script strings.Builder

	script.WriteString(". " + shellQuote(file) + "\n")

	for _, name := range names {
		script.WriteString("printf '%s=%s\\n' " + name + " \"${" + name + "-<unset>}\"\n")
	}

	args := []string{"-c", script.String()}
	cmd := exec.CommandContext(t.Context(), sh, args...) //nolint:gosec // The shell and the script are the test's own.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}

	for k, v := range start {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("source dev.env: %v\n%s", err, out)
	}

	got := map[string]string{}

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		got[name] = value
	}

	return got
}

// devEnvNames are the variables that the tests of dev.env read back.
var devEnvNames = []string{ //nolint:gochecknoglobals // A frozen list for the tests.
	"PULUMI_BACKEND_URL", "DUSTPAN_SERVER", "TF_CLI_CONFIG_FILE", "TF_HTTP_USERNAME", "DUSTPAN_DEV",
	"DUSTPAN_ADMIN_TOKEN", "PULUMI_ACCESS_TOKEN", "TF_HTTP_PASSWORD", "DUSTPAN_ADMIN_TOKEN_FILE", "SSL_CERT_FILE",
	"SSL_CERT_DIR",
}

// unsetMark is what the shell of a test prints for a variable that is not set.
const unsetMark = "<unset>"

const (
	testQuotedToken   = "tok'en with space" //nolint:gosec // A fixture, not a credential.
	testShellToken    = "0123456789abcdef0123456789abcdef-from-the-shell"
	testFileToken     = "secret-value-in-a-file-0123456789abcdef"
	testBundleInShell = "/data dir/dev.ca-bundle.crt"
)

func baseEnvInput() devEnvInput {
	return devEnvInput{
		serverURL: "http://127.0.0.1:8080", envFile: "/data dir/dev.env", tfrcFile: "/data dir/dev.tfrc",
		token:      devToken{value: testQuotedToken, file: "/data dir/dev.token", source: tokenMade},
		bundleFile: "",
	}
}

func TestRenderDevEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(in *devEnvInput)
		start  map[string]string
		want   map[string]string
		// refuse is text that dev.env must not hold, or empty.
		refuse string
	}{
		{
			name:   "a token that dev made is in the file",
			mutate: func(*devEnvInput) {},
			start:  nil,
			want: map[string]string{
				"PULUMI_BACKEND_URL": "http://127.0.0.1:8080", "DUSTPAN_SERVER": "http://127.0.0.1:8080",
				"TF_CLI_CONFIG_FILE": "/data dir/dev.tfrc", "TF_HTTP_USERNAME": "dustpan", "DUSTPAN_DEV": "1",
				"DUSTPAN_ADMIN_TOKEN": testQuotedToken, "PULUMI_ACCESS_TOKEN": testQuotedToken,
				"TF_HTTP_PASSWORD": testQuotedToken, "DUSTPAN_ADMIN_TOKEN_FILE": unsetMark, "SSL_CERT_FILE": unsetMark,
				"SSL_CERT_DIR": unsetMark,
			},
			refuse: "",
		},
		{
			name: "a token from the variable stays in the variable",
			mutate: func(in *devEnvInput) {
				in.token = devToken{value: testShellToken, file: "", source: tokenEnv}
			},
			start: map[string]string{"DUSTPAN_DEV_ADMIN_TOKEN": testShellToken},
			want: map[string]string{
				"DUSTPAN_ADMIN_TOKEN": testShellToken, "PULUMI_ACCESS_TOKEN": testShellToken,
				"TF_HTTP_PASSWORD": testShellToken, "DUSTPAN_ADMIN_TOKEN_FILE": unsetMark,
			},
			refuse: testShellToken,
		},
		{
			name: "a token from a file stays in the file",
			mutate: func(in *devEnvInput) {
				in.token = devToken{value: testFileToken, file: "/keys/admin token", source: tokenFile}
			},
			start: nil,
			want: map[string]string{ //nolint:gosec // The values mark variables that are not set.
				"DUSTPAN_ADMIN_TOKEN_FILE": "/keys/admin token",
				"DUSTPAN_ADMIN_TOKEN":      unsetMark,
				"PULUMI_ACCESS_TOKEN":      unsetMark,
				"TF_HTTP_PASSWORD":         unsetMark,
			},
			refuse: testFileToken,
		},
		{
			name:   "a certificate bundle sets SSL_CERT_FILE and clears SSL_CERT_DIR",
			mutate: func(in *devEnvInput) { in.bundleFile = testBundleInShell },
			start:  map[string]string{"SSL_CERT_DIR": "/etc/ssl/certs"},
			want:   map[string]string{"SSL_CERT_FILE": testBundleInShell, "SSL_CERT_DIR": unsetMark},
			refuse: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := baseEnvInput()
			tt.mutate(&in)

			text := renderDevEnv(in)

			if tt.refuse != "" && strings.Contains(text, tt.refuse) {
				t.Fatalf("dev.env holds %q", tt.refuse)
			}

			got := loadEnvFile(t, text, tt.start, devEnvNames)

			for name, want := range tt.want {
				if got[name] != want {
					t.Errorf("%s = %q, want %q", name, got[name], want)
				}
			}
		})
	}
}

func TestRenderDevTFRC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		serverURL string
		token     string
		want      string
		wantErr   bool
	}{
		{
			name: "host with a port", serverURL: "http://127.0.0.1:8080", token: "abc",
			want: "credentials \"127.0.0.1:8080\" {\n  token = \"abc\"\n}\n", wantErr: false,
		},
		{
			name: "https and an IPv6 address", serverURL: "https://[::1]:9000", token: "a\"b",
			want: "credentials \"[::1]:9000\" {\n  token = \"a\\\"b\"\n}\n", wantErr: false,
		},
		{name: "no host", serverURL: "nothing", token: "abc", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := renderDevTFRC(tt.serverURL, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("tfrc = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDialAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr net.Addr
		want string
	}{
		{
			name: "loopback",
			addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080, Zone: ""},
			want: "127.0.0.1:8080",
		},
		{
			name: "IPv4 wildcard becomes loopback", addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8080, Zone: ""},
			want: "127.0.0.1:8080",
		},
		{
			name: "IPv6 wildcard becomes IPv6 loopback",
			addr: &net.TCPAddr{IP: net.IPv6unspecified, Port: 8080, Zone: ""}, want: "[::1]:8080",
		},
		{name: "no IP is a wildcard", addr: &net.TCPAddr{IP: nil, Port: 9, Zone: ""}, want: "127.0.0.1:9"},
		{
			name: "IPv6 stays in brackets", addr: &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 443, Zone: ""},
			want: "[2001:db8::1]:443",
		},
		{name: "an address of another kind", addr: &net.UnixAddr{Name: "/run/x", Net: "unix"}, want: "/run/x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := dialAddress(tt.addr); got != tt.want {
				t.Fatalf("dialAddress = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWritePrivateFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "dev.env")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // The old file of an earlier start.
		t.Fatalf("seed: %v", err)
	}

	if err := writePrivateFile(path, []byte("new")); err != nil {
		t.Fatalf("writePrivateFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v, want mode 0600", info, err)
	}

	if got := readFileOf(t, path); string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the directory holds %v, %v, want the file alone", entries, err)
	}
}

func TestDevAddrFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	started := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, devAddrName)

	addr := devAddr{Started: started, Addr: "127.0.0.1:8080", Cert: "/d/dev.crt", PID: 42}
	if err := writeDevAddr(dir, addr); err != nil {
		t.Fatalf("writeDevAddr: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("dev.addr = %v, %v, want mode 0600", info, err)
	}

	var got devAddr
	if jsonErr := json.Unmarshal(readFileOf(t, path), &got); jsonErr != nil {
		t.Fatalf("decode: %v", jsonErr)
	}

	if got.Addr != "127.0.0.1:8080" || got.Cert != "/d/dev.crt" || got.PID != 42 || !got.Started.Equal(started) {
		t.Fatalf("dev.addr = %+v", got)
	}
}

func TestRemoveDevAddr(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, devAddrName)

	if err := writeDevAddr(dir, devAddr{Started: time.Time{}, Addr: "127.0.0.1:1", Cert: "", PID: 1}); err != nil {
		t.Fatalf("writeDevAddr: %v", err)
	}

	// The second call finds no file, and that is no error.
	for range 2 {
		if err := removeDevAddr(dir); err != nil {
			t.Fatalf("removeDevAddr: %v", err)
		}
	}

	if _, err := os.Stat(path); err == nil {
		t.Fatal("dev.addr stays after removeDevAddr")
	}
}
