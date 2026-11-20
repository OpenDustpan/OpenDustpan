package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wideModeEnv is the variable of the opt-out for a wide database file.
const wideModeEnv = "DUSTPAN_DATABASE_ALLOW_WIDE_FILE_MODE"

// wideDatabaseMode is the mode of an install that an earlier build made.
const wideDatabaseMode os.FileMode = 0o644

// widenDatabase seeds the database file at path and makes it readable by the
// group and others, as an older install or a volume with fixed permissions
// has it.
func widenDatabase(t *testing.T, path string) {
	t.Helper()

	seedMainKey(t, path, []byte(testKeyString))

	if err := os.Chmod(path, wideDatabaseMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

func requireHolds(t *testing.T, text string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("text does not hold %q:\n%s", want, text)
		}
	}
}

func TestServeRefusesWideDatabaseFileAndNamesTheFix(t *testing.T) {
	t.Setenv(wideModeEnv, "")
	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	widenDatabase(t, path)

	err := serveDatabase(t, path)
	if err == nil {
		t.Fatal("serve started on a database file with mode 0644, want a refusal")
	}

	requireHolds(t, err.Error(), path, "0644", "run chmod 600 "+path, "mount permissions")
}

func TestServeAllowsWideDatabaseFileWithTheFlag(t *testing.T) {
	t.Setenv(wideModeEnv, "")
	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	widenDatabase(t, path)

	logs, err := startServerLogs(t,
		"--database", "sqlite:"+path, "--listen", "127.0.0.1:0", "--database-allow-wide-file-mode")
	if err != nil {
		t.Fatalf("serve with the flag: %v", err)
	}

	// The warning names the file and the mode. The start log shows the setting.
	requireHolds(t, logs, "allows group or other access", path, `"mode":"0644"`, "database-allow-wide-file-mode")

	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode().Perm() != wideDatabaseMode {
		t.Fatalf("database = %v, %v, want mode 0644: the server must not change it", info, statErr)
	}
}

func TestServeAllowsWideDatabaseFileWithTheVariable(t *testing.T) {
	t.Setenv(wideModeEnv, "true")
	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	widenDatabase(t, path)

	if err := startServer(t, "--database", "sqlite:"+path, "--listen", "127.0.0.1:0"); err != nil {
		t.Fatalf("serve with the variable: %v", err)
	}
}

func TestServeWithTheFlagStillRefusesASidecarFileThatIsNotRegular(t *testing.T) {
	t.Setenv(wideModeEnv, "")
	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	widenDatabase(t, path)

	if err := os.Mkdir(path+"-shm", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := serve(t.Context(), []string{"--database", "sqlite:" + path, "--database-allow-wide-file-mode"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("serve = %v, want the not-regular-file refusal", err)
	}
}
