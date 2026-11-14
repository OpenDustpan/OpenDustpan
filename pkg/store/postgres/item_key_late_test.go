package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// jobRunner is the jobstore.Runner of one store, for the late item key cases.
type jobRunner struct {
	s *postgres.Store
}

func (r jobRunner) Write(ctx context.Context, fence namespace.Fence, fn func(jobstore.Tx) error) error {
	return postgres.JobWrite(ctx, r.s, fence, fn)
}

func (r jobRunner) Read(ctx context.Context, fn func(jobstore.Tx) error) error {
	return postgres.JobRead(ctx, r.s, fn)
}

func (r jobRunner) Logger() *slog.Logger { return nil }

func TestLateItemKeyCommits(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunLateItemKeyCommits(t, func(t *testing.T) storetest.LateKeyRig {
		t.Helper()

		s := open(t, base)

		return storetest.LateKeyRig{
			Store: s, Jobs: jobRunner{s: s},
			SetAttemptDeadline: func(d time.Duration) { postgres.SetItemKeyAttemptDeadline(s, d) },
		}
	})
}
