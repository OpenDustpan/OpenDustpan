package main

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestServeRefusesPasswordInDatabaseFlag proves that serve stops before it
// opens the database when --database holds a password, names the flag and
// DUSTPAN_DATABASE, and never shows the password. A password in
// DUSTPAN_DATABASE, a variable, does not fail with this error: serve fails
// later instead. Serve reads the environment, so the cases run one after the
// other.
//
//nolint:paralleltest // serve reads process environment, so the cases set it and run alone.
func TestServeRefusesPasswordInDatabaseFlag(t *testing.T) {
	// The fake password is joined at run time, so no line of the source holds a
	// contiguous credential URL that a secret scanner reports. The test checks
	// the password is never shown.
	const password = "s3cret"

	url := "postgres://dustpan:" + password + "@127.0.0.1:1/dustpan?connect_timeout=1"

	clearKeyEnv(t)

	t.Run("flag", func(t *testing.T) {
		var logs lockedBuffer

		err := serveWithIO(t.Context(), []string{"--database", url}, strings.NewReader(""), &logs, false)

		requireFlagRefusal(t, err, logs.String(), password)
	})

	t.Run("variable", func(t *testing.T) {
		t.Setenv("DUSTPAN_DATABASE", url)

		var logs lockedBuffer

		err := serveWithIO(t.Context(), nil, strings.NewReader(""), &logs, false)

		requireLaterFailure(t, err, logs.String(), password)
	})
}

// requireFlagRefusal fails unless err is the flag password refusal that names
// the flag and the variable and never shows the password.
func requireFlagRefusal(t *testing.T, err error, logs, password string) {
	t.Helper()

	if err == nil {
		t.Fatal("serve started against an unreachable database")
	}

	if !errors.Is(err, cli.ErrFlagPassword) {
		t.Fatalf("serve error = %v, want ErrFlagPassword", err)
	}

	if !strings.Contains(err.Error(), "--database") || !strings.Contains(err.Error(), "DUSTPAN_DATABASE") {
		t.Fatalf("serve error = %q, want --database and DUSTPAN_DATABASE", err.Error())
	}

	if strings.Contains(err.Error(), password) || strings.Contains(logs, password) {
		t.Fatalf("serve output shows the password: error %q logs %q", err.Error(), logs)
	}
}

// requireLaterFailure fails when err is nil or is the flag password refusal,
// and fails when the output shows the password.
func requireLaterFailure(t *testing.T, err error, logs, password string) {
	t.Helper()

	if err == nil {
		t.Fatal("serve started against an unreachable database")
	}

	if errors.Is(err, cli.ErrFlagPassword) {
		t.Fatalf("serve error = %v, want a later failure, not ErrFlagPassword", err)
	}

	if strings.Contains(err.Error(), password) || strings.Contains(logs, password) {
		t.Fatalf("serve output shows the password: error %q logs %q", err.Error(), logs)
	}
}
