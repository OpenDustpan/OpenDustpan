package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestListReadsWaitForTheSwapLock proves that the workspace list and the
// stack list take the swap lock before their snapshot. While a session holds
// the lock in ACCESS EXCLUSIVE mode, as a restore swap does, the read waits.
// It answers after the session commits.
func TestListReadsWaitForTheSwapLock(t *testing.T) {
	t.Parallel()

	reads := []struct {
		name string
		read func(ctx context.Context, s *Store) error
	}{
		{name: "workspace list", read: func(ctx context.Context, s *Store) error {
			q := workspace.ListQuery{Namespace: "locked", NameContains: "", Offset: 0, Limit: 10}
			_, err := s.QueryWorkspaces(ctx, q)

			return err
		}},
		{name: "stack list", read: func(ctx context.Context, s *Store) error {
			q := stack.PageQuery{After: nil, Project: "", Namespaces: []namespace.Name{"locked"}, Limit: 10}
			_, err := s.QueryStacks(ctx, q)

			return err
		}},
	}

	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkReadWaitsForSwapLock(t, tc.read)
		})
	}
}

// checkReadWaitsForSwapLock runs read while another session holds the swap
// lock, and checks that read answers only after that session commits.
func checkReadWaitsForSwapLock(t *testing.T, read func(ctx context.Context, s *Store) error) {
	t.Helper()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))

	if err := s.CreateNamespace(ctx, newLockNamespace()); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	holder, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}

	t.Cleanup(func() {
		rollbackErr := holder.Rollback(context.WithoutCancel(ctx))
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back holder: %v", rollbackErr)
		}
	})

	lock := "LOCK TABLE " + swapLockTable(liveSchema(ctx, t, s)) + " IN ACCESS EXCLUSIVE MODE"
	if _, err = holder.Exec(ctx, lock); err != nil {
		t.Fatalf("hold the swap lock: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- read(ctx, s) }()

	select {
	case readErr := <-done:
		t.Fatalf("read answered while the swap lock was held: %v, want it to wait", readErr)
	case <-time.After(lockWait):
	}

	if err = holder.Commit(ctx); err != nil {
		t.Fatalf("commit holder: %v", err)
	}

	if err = <-done; err != nil {
		t.Fatalf("read after the swap lock was released: %v, want an answer", err)
	}
}

// TestOnlySnapshotOptionsNamesRepeatableRead proves that no read of the store
// builds its own REPEATABLE READ options. Such a read takes no swap lock, and
// its conflict errors do not go through readConflict. Every snapshot read must
// use snapshotOptions, inSnapshotTx or inSnapshotConn.
func TestOnlySnapshotOptionsNamesRepeatableRead(t *testing.T) {
	t.Parallel()

	src := os.DirFS(".")

	files, err := fs.Glob(src, "*.go")
	if err != nil {
		t.Fatalf("list the files of the package: %v", err)
	}

	var found []string

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		body, readErr := fs.ReadFile(src, name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if n := strings.Count(string(body), "pgx.RepeatableRead"); n > 0 {
			found = append(found, fmt.Sprintf("%s: %d", name, n))
		}
	}

	if want := []string{"postgres.go: 1"}; !slices.Equal(found, want) {
		t.Fatalf("files that name pgx.RepeatableRead = %v, want %v (only snapshotOptions)", found, want)
	}
}
