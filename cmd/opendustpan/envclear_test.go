package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

var errUnsetFailed = errors.New("unset failed")

// TestClearSensitiveEnvRemovesEverySensitiveVariable proves that, after the
// clear, os.LookupEnv finds none of the variables of a sensitive setting.
//
//nolint:paralleltest // The test sets process environment, so it runs in order.
func TestClearSensitiveEnvRemovesEverySensitiveVariable(t *testing.T) {
	names := cli.SensitiveEnvs()

	for _, want := range []string{
		"DUSTPAN_OIDC_CLIENT_SECRET", "DUSTPAN_AUDIT_WEBHOOK_SECRET", "DUSTPAN_LOG_WEBHOOK_SECRET",
		"DUSTPAN_AUDIT_OTLP_HEADERS", "DUSTPAN_LOG_OTLP_HEADERS", "DUSTPAN_ADMIN_TOKEN", "DUSTPAN_KEY_ENCRYPTION_KEY",
	} {
		found := false

		for _, name := range names {
			found = found || name == want
		}

		if !found {
			t.Errorf("SensitiveEnvs lacks %s", want)
		}
	}

	for _, name := range names {
		t.Setenv(name, "canary-value")
	}

	if err := clearSensitiveEnv(os.Unsetenv); err != nil {
		t.Fatalf("clearSensitiveEnv: %v", err)
	}

	for _, name := range names {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s is still set after the clear", name)
		}
	}
}

// TestClearSensitiveEnvFailsClosed proves a failing unset stops the start,
// names the variable, and does not stop the clear of the other variables.
func TestClearSensitiveEnvFailsClosed(t *testing.T) {
	t.Parallel()

	var cleared []string

	err := clearSensitiveEnv(func(name string) error {
		if name == "DUSTPAN_OIDC_CLIENT_SECRET" {
			return errUnsetFailed
		}

		cleared = append(cleared, name)

		return nil
	})
	if !errors.Is(err, errUnsetFailed) || !strings.Contains(err.Error(), "DUSTPAN_OIDC_CLIENT_SECRET") {
		t.Fatalf("err = %v, want a failure that names DUSTPAN_OIDC_CLIENT_SECRET", err)
	}

	if len(cleared) != len(cli.SensitiveEnvs())-1 {
		t.Errorf("cleared %d variables, want %d", len(cleared), len(cli.SensitiveEnvs())-1)
	}
}
