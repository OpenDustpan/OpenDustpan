package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/internal/cli"
)

// clearSensitiveEnv removes each variable of a sensitive setting, and each
// secret variable that no setting declares, from the process environment.
// The start has read them all before the serve hook runs, so a child process
// and a crash dump do not see them. A reload resolves against the snapshot of
// the fingerprints and does not need the real value. It tries every variable
// and joins the failures: a variable that stays set stops the start.
//
// It logs at Info the count and the names of the variables that were set and
// are now cleared, so that the automatic action has a record. The log never
// holds a value: lookup answers the value, and the code drops it at once.
func clearSensitiveEnv(
	ctx context.Context, logger *slog.Logger, lookup func(string) (string, bool), unset func(string) error,
) error {
	var errs []error

	cleared := []string{}

	names := append(cli.SensitiveEnvs(), cli.ExternalSecretEnvs()...)

	for _, name := range names {
		_, wasSet := lookup(name)

		if err := unset(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: clear variable: %w", name, err))

			continue
		}

		if wasSet {
			cleared = append(cleared, name)
		}
	}

	logger.InfoContext(
		ctx,
		"cleared sensitive environment variables",
		slog.Int("count", len(cleared)),
		slog.Any("names", cleared),
	)

	return errors.Join(errs...)
}
