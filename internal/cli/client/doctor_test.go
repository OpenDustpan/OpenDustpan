package client_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli/client"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// doctorToken is a bootstrap admin token of 40 bytes. The tests prove that no
// output of doctor holds it.
const doctorToken = "doctor-token-0123456789abcdef0123456789"

// doctorKey is a key of 32 bytes in base64 with a line end: 45 bytes.
const doctorKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"

// doctorEnvVars answers every variable that doctor reads.
func doctorEnvVars() []string {
	return []string{
		"DUSTPAN_CONFIG", "DUSTPAN_DATABASE", "DUSTPAN_LISTEN", "DUSTPAN_TLS_CERT", "DUSTPAN_TLS_KEY",
		"DUSTPAN_PUBLIC_URL", "DUSTPAN_TRUSTED_PROXIES", "DUSTPAN_BLOB_DIR", "DUSTPAN_BACKUP_TARGET",
		"DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE", "DUSTPAN_ADMIN_TOKEN",
		"DUSTPAN_ADMIN_TOKEN_FILE", "DUSTPAN_SERVER",
	}
}

// cleanDoctorEnv clears every variable that doctor reads, so a case starts
// with the settings that it sets itself.
func cleanDoctorEnv(t *testing.T) {
	t.Helper()

	for _, name := range doctorEnvVars() {
		t.Setenv(name, "")
	}

	t.Setenv("DUSTPAN_DEV", "")

	if err := os.Unsetenv("DUSTPAN_DEV"); err != nil {
		t.Fatalf("unset DUSTPAN_DEV: %v", err)
	}
}

// writeDoctorKey writes a key file with contents and the mode.
func writeDoctorKey(t *testing.T, dir, contents string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(dir, "dustpan.key")

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod key file: %v", err)
	}

	return path
}

// runDoctor runs doctor with args and answers its report and its error.
func runDoctor(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := client.Doctor(t.Context(), args, &stdout, &stderr, "v1.0.0")

	return stdout.String(), err
}

// doctorCase is one run of the local checks.
type doctorCase struct {
	name    string
	env     map[string]string
	args    func(dir, key string) []string
	want    []string
	wantErr error
}

// goodArgs answers the flags of a good production install.
func goodArgs(dir, key string) []string {
	return []string{
		"--database", "sqlite:" + filepath.Join(dir, "data", "dustpan.db"),
		"--key-encryption-key-file", key,
		"--backup-target", "dir:" + filepath.Join(dir, "backups"),
		"--listen", "127.0.0.1:8080",
	}
}

// TestDoctorLocalChecks proves each check of the settings with a pass, a warn
// or a fail, the exit code, and that no line holds a token. Doctor reads the
// process environment, so the cases run one after the other.
func TestDoctorLocalChecks(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")

	cases := []doctorCase{
		{
			name: "a good install", env: map[string]string{"DUSTPAN_ADMIN_TOKEN": doctorToken}, args: goodArgs,
			want: []string{
				"pass  key: ", "pass  key place: the key file is not next to the data", "pass  database: ",
				"pass  admin token: ", "pass  backup target: ", "pass  listen address: ", "pass  trusted proxies: ",
			},
			wantErr: nil,
		},
		{
			name: "no key", env: nil,
			args: func(dir, _ string) []string {
				return []string{"--database", "sqlite:" + filepath.Join(dir, "dustpan.db")}
			},
			want:    []string{"fail  key: no key-encryption key is set", "opendustpan key generate --out <path>"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "a key in the variable",
			env:  map[string]string{"DUSTPAN_KEY_ENCRYPTION_KEY": strings.TrimSpace(doctorKey)},
			args: func(dir, _ string) []string {
				return []string{"--database", "sqlite:" + filepath.Join(dir, "dustpan.db")}
			},
			want:    []string{"pass  key: the key-encryption key is set in DUSTPAN_KEY_ENCRYPTION_KEY"},
			wantErr: nil,
		},
		{
			name: "a missing key file", env: nil,
			args: func(dir, _ string) []string {
				return []string{
					"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
					"--key-encryption-key-file", filepath.Join(dir, "missing.key"),
				}
			},
			want:    []string{"fail  key: the key file ", "cannot be read"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "no database", env: nil,
			args: func(_, key string) []string {
				return []string{"--key-encryption-key-file", key}
			},
			want:    []string{"fail  database: no database is set"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "a short admin token", env: map[string]string{"DUSTPAN_ADMIN_TOKEN": "short-token"}, args: goodArgs,
			want:    []string{"fail  admin token: an admin token has fewer than 32 bytes"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "no backup target",
			env:  nil,
			args: func(dir, key string) []string {
				return []string{
					"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
					"--key-encryption-key-file", key,
				}
			},
			want: []string{
				"warn  backup target: no backup target is set",
				"warn  listen address: :8080 is not a loopback address",
			},
			wantErr: nil,
		},
		{
			name: "TLS on a public address", env: nil,
			args: func(dir, key string) []string {
				return append(goodArgs(dir, key), "--listen", ":8443", "--tls-cert", "c.pem", "--tls-key", "k.pem")
			},
			want:    []string{"pass  listen address: :8443 uses TLS"},
			wantErr: nil,
		},
		{
			name: "a proxy with https and no trusted proxies", env: nil,
			args: func(dir, key string) []string {
				return append(goodArgs(dir, key), "--listen", ":8080", "--public-url", "https://dustpan.example")
			},
			want: []string{
				"pass  listen address: :8080 is behind a proxy",
				"warn  trusted proxies: --public-url is set and --trusted-proxies is not",
			},
			wantErr: nil,
		},
		{
			name: "a proxy with trusted proxies", env: nil,
			args: func(dir, key string) []string {
				return append(
					goodArgs(dir, key),
					"--public-url",
					"https://dustpan.example",
					"--trusted-proxies",
					"10.0.0.0/8",
				)
			},
			want:    []string{"pass  trusted proxies: --trusted-proxies is set"},
			wantErr: nil,
		},
		{
			name: "an operand", env: nil,
			args:    func(dir, key string) []string { return append(goodArgs(dir, key), "extra") },
			want:    nil,
			wantErr: client.ErrUsage,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			checkDoctorCase(t, tc)
		})
	}
}

// checkDoctorCase runs one case of TestDoctorLocalChecks.
func checkDoctorCase(t *testing.T, tc doctorCase) {
	t.Helper()

	cleanDoctorEnv(t)

	for name, value := range tc.env {
		t.Setenv(name, value)
	}

	dir := t.TempDir()
	key := writeDoctorKey(t, dir, doctorKey, 0o600)

	out, err := runDoctor(t, tc.args(dir, key)...)
	if !errors.Is(err, tc.wantErr) {
		t.Fatalf("doctor error = %v, want %v\n%s", err, tc.wantErr, out)
	}

	for _, want := range tc.want {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, doctorToken) || strings.Contains(out, "short-token") {
		t.Fatalf("doctor output holds an admin token:\n%s", out)
	}
}

// TestDoctorKeyFile proves the checks of the key file: the mode, the size and
// the place. A key file of dev has a long first line, so its size fails the
// check. Doctor reads the process environment, so the cases run one after the
// other.
func TestDoctorKeyFile(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")

	devKey := "opendustpan dev key: this key is for a trial, and opendustpan serve refuses it\n" + doctorKey

	cases := []struct {
		name     string
		contents string
		mode     os.FileMode
		sameDir  bool
		want     string
	}{
		{"mode 0600", doctorKey, 0o600, false, "pass  key: "},
		{"mode 0400", doctorKey, 0o400, false, "pass  key: "},
		{"mode 0000", doctorKey, 0o000, false, "the owner cannot read it, so serve cannot read it"},
		{"mode 0200", doctorKey, 0o200, false, "the owner cannot read it, so serve cannot read it"},
		{"mode 0640", doctorKey, 0o640, false, "fail  key: the key file "},
		{"mode 0644", doctorKey, 0o644, false, "has mode 0644: run chmod 600"},
		{"too short", "AAAA\n", 0o600, false, "has 5 bytes"},
		{"too long", strings.Repeat("A", 100), 0o600, false, "has 100 bytes"},
		{"a key of dev", devKey, 0o600, false, "fail  key: "},
		{"next to the data", doctorKey, 0o600, true, "warn  key place: the key file is next to the data"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			cleanDoctorEnv(t)

			dir := t.TempDir()
			keyDir := dir

			if !tc.sameDir {
				keyDir = t.TempDir()
			}

			key := writeDoctorKey(t, keyDir, tc.contents, tc.mode)

			out, err := runDoctor(t,
				"--database", "sqlite:"+filepath.Join(dir, "dustpan.db"), "--key-encryption-key-file", key)
			if err != nil && !errors.Is(err, client.ErrDoctorFailed) {
				t.Fatalf("doctor: %v", err)
			}

			if !strings.Contains(out, tc.want) {
				t.Fatalf("doctor output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestDoctorRefusesDevEnvironment proves that doctor stops in a shell that
// holds DUSTPAN_DEV, as serve does.
func TestDoctorRefusesDevEnvironment(t *testing.T) {
	cleanDoctorEnv(t)
	t.Setenv("DUSTPAN_DEV", "1")

	if _, err := runDoctor(t); !errors.Is(err, client.ErrDoctorDevEnvironment) {
		t.Fatalf("doctor error = %v, want ErrDoctorDevEnvironment", err)
	}
}

// fakeServer is a server that answers the three routes that doctor asks.
type fakeServer struct {
	kind     string
	minimum  string
	backups  []map[string]string
	sawToken atomic.Bool
}

// newFake answers a fake server that reports the installation kind kind, the
// minimum binary version minimum and the backups.
func newFake(kind, minimum string, backups []map[string]string) *fakeServer {
	return &fakeServer{kind: kind, minimum: minimum, backups: backups, sawToken: atomic.Bool{}}
}

// start serves the fake with the token doctorToken.
func (f *fakeServer) start(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/dustpan/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		f.sawToken.Store(r.Header.Get("Authorization") == "Bearer "+doctorToken)

		doc := map[string]string{"apiVersion": "v1"}
		if f.kind != "" {
			doc["installationKind"] = f.kind
		}

		writeFake(t, w, doc)
	})

	mux.HandleFunc("GET /api/dustpan/v1/upgrade-check", func(w http.ResponseWriter, _ *http.Request) {
		embedded, version := schema.EmbeddedMigrationNames()

		writeFake(t, w, map[string]any{
			"version": "test", "schemaVersion": version, "appliedMigrations": embedded,
			"minimumBinaryVersion": f.minimum, "keyEncryptionKey": true, "runningJournaledUpdates": 0,
		})
	})

	mux.HandleFunc("GET /api/server/backups", func(w http.ResponseWriter, _ *http.Request) {
		writeFake(t, w, map[string]any{"backups": f.backups})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// writeFake writes doc as JSON.
func writeFake(t *testing.T, w http.ResponseWriter, doc any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		t.Errorf("encode answer: %v", err)
	}
}

// backupFailed answers a failed backup that ended age ago.
func backupFailed(age time.Duration) map[string]string {
	return map[string]string{
		"id": "bkp-2", "state": "failed", "endedAt": time.Now().Add(-age).UTC().Format(time.RFC3339Nano),
	}
}

// backupEnded answers a succeeded backup that ended age ago.
func backupEnded(age time.Duration) map[string]string {
	return map[string]string{
		"id": "bkp-1", "state": "succeeded", "endedAt": time.Now().Add(-age).UTC().Format(time.RFC3339Nano),
	}
}

// TestDoctorServerChecks proves the checks with --server: the kind, the
// upgrade and the age of the last backup. The kind fails, the other two warn.
// Doctor reads the process environment, so the cases run one after the other.
func TestDoctorServerChecks(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")

	cases := []struct {
		name    string
		fake    *fakeServer
		token   string
		want    []string
		wantErr error
	}{
		{
			name: "a good server", token: doctorToken,
			fake: newFake("production", "", []map[string]string{backupEnded(time.Hour)}),
			want: []string{
				"pass  server kind: ",
				"pass  server upgrade: ",
				"pass  server backup: the last backup ended 1h0m0s ago",
			},
			wantErr: nil,
		},
		{
			name: "a dev server", token: doctorToken,
			fake:    newFake("dev", "", []map[string]string{backupEnded(time.Hour)}),
			want:    []string{"fail  server kind: the server reports the installation kind dev, not production"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "no kind in the document", token: doctorToken,
			fake:    newFake("", "", nil),
			want:    []string{"fail  server kind: the capabilities document has no installationKind"},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name:  "no token",
			token: "",
			fake:  newFake("production", "", nil),
			want: []string{
				"fail  server kind: ",
				"warn  server upgrade: not checked",
				"warn  server backup: not checked",
			},
			wantErr: client.ErrDoctorFailed,
		},
		{
			name: "a binary below the minimum", token: doctorToken,
			fake:    newFake("production", "v9.0.0", []map[string]string{backupEnded(time.Hour)}),
			want:    []string{"warn  server upgrade: cannot start: "},
			wantErr: nil,
		},
		{
			name: "an old backup", token: doctorToken,
			fake:    newFake("production", "", []map[string]string{backupEnded(26 * time.Hour)}),
			want:    []string{"warn  server backup: the last backup ended 26h0m0s ago"},
			wantErr: nil,
		},
		{
			name: "a failed backup newer than a succeeded one", token: doctorToken,
			fake: newFake("production", "", []map[string]string{backupFailed(time.Hour), backupEnded(2 * time.Hour)}),
			want: []string{"pass  server backup: the last backup ended 2h0m0s ago"}, wantErr: nil,
		},
		{
			name: "a failed backup newer than an old succeeded one", token: doctorToken,
			fake: newFake("production", "", []map[string]string{backupFailed(time.Hour), backupEnded(30 * time.Hour)}),
			want: []string{"warn  server backup: the last backup ended 30h0m0s ago"}, wantErr: nil,
		},
		{
			name:  "only failed backups",
			token: doctorToken,
			fake: newFake(
				"production",
				"",
				[]map[string]string{backupFailed(time.Hour), backupFailed(2 * time.Hour)},
			),
			want:    []string{"warn  server backup: the server has no successful backup"},
			wantErr: nil,
		},
		{
			name: "no backup", token: doctorToken,
			fake:    newFake("production", "", nil),
			want:    []string{"warn  server backup: the server has no successful backup"},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			cleanDoctorEnv(t)
			t.Setenv("DUSTPAN_ADMIN_TOKEN", tc.token)

			srv := tc.fake.start(t)

			dir := t.TempDir()
			key := writeDoctorKey(t, dir, doctorKey, 0o600)

			args := append(goodArgs(dir, key), "--server", srv.URL)

			out, err := runDoctor(t, args...)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("doctor error = %v, want %v\n%s", err, tc.wantErr, out)
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("doctor output lacks %q:\n%s", want, out)
				}
			}

			if tc.token != "" && !tc.fake.sawToken.Load() {
				t.Fatal("doctor did not send the token to the capabilities route")
			}
		})
	}
}
