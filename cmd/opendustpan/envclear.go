package main

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/cli"
)

// clearSensitiveEnv removes each variable of a sensitive setting from the
// process environment. The start has read them all before the serve hook
// runs, so a child process and a crash dump do not see them. A reload
// resolves against the snapshot of the fingerprints and does not need the
// real value. It tries every variable and joins the failures: a variable that
// stays set stops the start.
func clearSensitiveEnv(unset func(string) error) error {
	var errs []error

	for _, name := range cli.SensitiveEnvs() {
		if err := unset(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: clear variable: %w", name, err))
		}
	}

	return errors.Join(errs...)
}
