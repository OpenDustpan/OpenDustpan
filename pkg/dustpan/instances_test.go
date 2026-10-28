package dustpan_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
)

// instanceRowCase describes one start of a server for TestRunWritesAndDeletesTheInstanceRow.
type instanceRowCase struct {
	name string
	// claimed is the kind that claimed the database before the start.
	claimed installation.Kind
	// kind is the kind option of the server.
	kind installation.Kind
	// withStore tells whether the server gets the instance store.
	withStore bool
	wantErr   error
	// wantServing tells whether the server serves, and so wrote its row.
	wantServing bool
}

// TestRunWritesAndDeletesTheInstanceRow proves that Start writes the version
// row of the process before the server serves, that the row holds the
// instance ID, the binary version and a start time, and that a clean stop
// deletes it. A start that the kind check stops writes no row.
func TestRunWritesAndDeletesTheInstanceRow(t *testing.T) {
	t.Parallel()

	tests := []instanceRowCase{
		{
			name: "row written and deleted", claimed: "", kind: installation.KindProduction, withStore: true,
			wantErr: nil, wantServing: true,
		},
		{
			name: "no store writes no row", claimed: "", kind: installation.KindProduction, withStore: false,
			wantErr: nil, wantServing: true,
		},
		{
			name: "kind mismatch writes no row", claimed: installation.KindDev, kind: installation.KindProduction,
			withStore: true, wantErr: installation.ErrKindMismatch, wantServing: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkInstanceRow(t, tt)
		})
	}
}

// checkInstanceRow runs one case of TestRunWritesAndDeletesTheInstanceRow.
func checkInstanceRow(t *testing.T, tt instanceRowCase) {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	if tt.claimed != "" {
		if _, err := st.ClaimKind(t.Context(), tt.claimed); err != nil {
			t.Fatalf("claim %q: %v", tt.claimed, err)
		}
	}

	var during []installation.Instance

	reached := make(chan struct{})

	opts := slices.Concat(fullServerOptions(t, st, blobs, authn), []dustpan.Option{
		dustpan.WithListener(openTestListener(t)),
		dustpan.WithInstallationKind(tt.kind),
		dustpan.WithInstallationKindStore(st),
		dustpan.WithVersion("v0.3.0"),
		dustpan.WithInstanceID("ins-test"),
		dustpan.WithBeforeServe(func(ctx context.Context) error {
			rows, err := st.ListInstances(ctx)
			if err != nil {
				return err
			}

			during = rows

			close(reached)

			return nil
		}),
	})
	if tt.withStore {
		opts = append(opts, dustpan.WithInstanceStore(st))
	}

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := time.Now()
	done := runServer(ctx, srv)

	if tt.wantServing {
		waitReached(t, reached)
		cancel()
	}

	if runErr := <-done; !errors.Is(runErr, tt.wantErr) {
		t.Fatalf("Run = %v, want %v", runErr, tt.wantErr)
	}

	if tt.withStore && tt.wantServing {
		requireOwnRow(t, during, started)
	} else {
		requireNoRows(t, during)
	}

	rows, err := st.ListInstances(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows after the stop = %+v, %v, want none", rows, err)
	}
}

// waitReached waits until the server calls the function of WithBeforeServe.
func waitReached(t *testing.T, reached <-chan struct{}) {
	t.Helper()

	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not reach the function of WithBeforeServe")
	}
}

// requireNoRows fails when the server saw a version row while it served.
func requireNoRows(t *testing.T, during []installation.Instance) {
	t.Helper()

	if len(during) != 0 {
		t.Fatalf("rows while serving = %+v, want none", during)
	}
}

// requireOwnRow fails when the rows that the server saw while it served are
// not the one row of its process.
func requireOwnRow(t *testing.T, during []installation.Instance, started time.Time) {
	t.Helper()

	if len(during) != 1 || during[0].ID != "ins-test" || during[0].BinaryVersion != "v0.3.0" ||
		during[0].StartedAt.Before(started.Add(-time.Second)) {
		t.Fatalf("rows while serving = %+v, want one row ins-test at v0.3.0 that started after the test began", during)
	}
}
