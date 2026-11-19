package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/cli/client"
)

// doctorAdminTokenLine runs doctor with a good database and key and returns
// its admin token line. The test sets the token variables first.
func doctorAdminTokenLine(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dustpan.key")

	if err := os.WriteFile(keyPath, []byte(testKeyBase64()+"\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatalf("chmod key file: %v", err)
	}

	t.Setenv("DUSTPAN_DEV", "")

	if err := os.Unsetenv("DUSTPAN_DEV"); err != nil {
		t.Fatalf("unset DUSTPAN_DEV: %v", err)
	}

	var stdout, stderr bytes.Buffer

	// The exit code does not matter here: other checks may warn or fail. The
	// test reads the admin token line only.
	doctorErr := client.Doctor(t.Context(), []string{
		"--database", "sqlite:" + filepath.Join(dir, "data", "dustpan.db"),
		"--key-encryption-key-file", keyPath,
	}, &stdout, &stderr, "v1.0.0")
	if doctorErr != nil && !errors.Is(doctorErr, client.ErrDoctorFailed) {
		t.Fatalf("doctor: %v", doctorErr)
	}

	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if strings.Contains(line, "admin token:") {
			return line
		}
	}

	t.Fatalf("doctor printed no admin token line:\n%s", stdout.String())

	return ""
}

// adminTokenCase is one state of the token settings.
type adminTokenCase struct {
	name     string
	variable string      // value of the token variable
	file     string      // content of the token file; empty means no file
	fileMode os.FileMode // mode of the token file
	fails    bool        // serve refuses to start and doctor fails
}

// checkAgreement sets the token settings of tc, runs doctor and serve, and
// checks that both give the answer of tc.
func checkAgreement(t *testing.T, tc adminTokenCase) {
	t.Helper()

	t.Setenv(adminTokenFileEnvVar, "")
	t.Setenv(adminTokenEnvVar, tc.variable)

	if tc.file != "" {
		t.Setenv(adminTokenFileEnvVar, writeTokenFile(t, tc.file, tc.fileMode))
	}

	// Doctor runs first: serve clears the token variable after it reads it.
	line := doctorAdminTokenLine(t)
	_, serveErr := adminAuthenticator(cli.LayeredLookup(os.LookupEnv, nil))

	if serveFails := serveErr != nil; serveFails != tc.fails {
		t.Fatalf("serve admin token error = %v, want failure %v", serveErr, tc.fails)
	}

	if doctorFails := strings.HasPrefix(line, "fail"); doctorFails != tc.fails {
		t.Fatalf("doctor line = %q, want failure %v", line, tc.fails)
	}

	if strings.Contains(line, tokenA) {
		t.Fatalf("doctor line shows a token: %q", line)
	}
}

// TestServeAndDoctorAgreeOnAdminToken proves that doctor fails exactly when
// serve refuses to start for the admin token: both read the token through
// the same function.
func TestServeAndDoctorAgreeOnAdminToken(t *testing.T) {
	// The cases change the process environment, so they run one after the
	// other.
	t.Setenv(adminTokenEnvVar, "")

	cases := []adminTokenCase{
		{name: "no token", variable: "", file: "", fileMode: 0, fails: true},
		{name: "a blank token variable", variable: "  ", file: "", fileMode: 0, fails: true},
		{name: "an empty token file", variable: "", file: "\n", fileMode: 0o600, fails: true},
		{name: "a token in the variable", variable: tokenA, file: "", fileMode: 0, fails: false},
		{name: "a token in a private file", variable: "", file: tokenA + "\n", fileMode: 0o600, fails: false},
		{name: "a token in a group-readable file", variable: "", file: tokenA + "\n", fileMode: 0o640, fails: true},
		{
			name:     "a duplicate token in the variable",
			variable: tokenA + "," + tokenA,
			file:     "",
			fileMode: 0,
			fails:    true,
		},
		{
			name:     "a duplicate token in a private file",
			variable: "",
			file:     tokenA + "\n" + tokenA + "\n",
			fileMode: 0o600,
			fails:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(adminTokenEnvVar, tc.variable)
			checkAgreement(t, tc)
		})
	}

	// The error of serve with no token is the sentinel that doctor reads.
	t.Setenv(adminTokenEnvVar, "")
	t.Setenv(adminTokenFileEnvVar, "")

	if _, err := adminAuthenticator(cli.LayeredLookup(os.LookupEnv, nil)); !errors.Is(err, errAdminTokenMissing) {
		t.Fatalf("serve error = %v, want %v", err, errAdminTokenMissing)
	}
}
