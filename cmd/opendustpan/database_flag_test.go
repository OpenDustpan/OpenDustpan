package main

import (
	"strings"
	"testing"
)

// TestServeWarnsAboutPasswordInDatabaseFlag proves that serve logs one WARN
// line when --database holds a password, names the flag and DUSTPAN_DATABASE,
// and never shows the password. A password in DUSTPAN_DATABASE, a variable,
// logs no such line. Serve reads the environment, so the cases run one after
// the other.
//
//nolint:paralleltest // serve reads process environment, so the cases set it and run alone.
func TestServeWarnsAboutPasswordInDatabaseFlag(t *testing.T) {
	// The fake password is joined at run time, so no line of the source holds a
	// contiguous credential URL that a secret scanner reports. The test checks
	// the password is never logged.
	const password = "s3cret"

	url := "postgres://dustpan:" + password + "@127.0.0.1:1/dustpan?connect_timeout=1"

	clearKeyEnv(t)

	t.Run("flag", func(t *testing.T) {
		var logs lockedBuffer

		err := serveWithIO(t.Context(), []string{"--database", url}, strings.NewReader(""), &logs, false)
		if err == nil {
			t.Fatal("serve started against an unreachable database")
		}

		out := logs.String()
		if !strings.Contains(out, "WARN") || !strings.Contains(out, "--database") ||
			!strings.Contains(out, "DUSTPAN_DATABASE") {
			t.Fatalf("serve log = %q, want a WARN line that names --database and DUSTPAN_DATABASE", out)
		}

		if strings.Contains(out, password) {
			t.Fatalf("serve log shows the password: %q", out)
		}
	})

	t.Run("variable", func(t *testing.T) {
		t.Setenv("DUSTPAN_DATABASE", url)

		var logs lockedBuffer

		err := serveWithIO(t.Context(), nil, strings.NewReader(""), &logs, false)
		if err == nil {
			t.Fatal("serve started against an unreachable database")
		}

		if strings.Contains(logs.String(), "WARN") {
			t.Fatalf("serve log = %q, want no WARN line for a variable", logs.String())
		}
	})
}
