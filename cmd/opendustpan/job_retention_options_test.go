package main

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/history"
)

// serverRetention builds the NewServer options of f over a fresh SQLite store
// and answers the retention periods that they carry.
func serverRetention(t *testing.T, f *serveFlags) history.RetentionSettings {
	t.Helper()

	st, err := openStore(
		t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"),
		slog.New(slog.DiscardHandler), openMigrate, "",
	)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob directory: %v", err)
	}

	opts, err := f.serverOptions(st, blobs, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("serverOptions: %v", err)
	}

	return dustpan.JobRetentionOf(opts...)
}

// TestServerOptionsCarryTheJobRetention proves that the retention flags of
// serve reach the NewServer options. Without the option the recorder would
// use the default again and no other test would fail.
func TestServerOptionsCarryTheJobRetention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want history.RetentionSettings
	}{
		{"defaults", nil, history.DefaultRetentionSettings()},
		{
			"two days of job logs",
			[]string{"--job-log-retention", "48h"},
			history.RetentionSettings{Plan: history.DefaultRetention, JobLog: 48 * time.Hour},
		},
		{
			"both periods",
			[]string{"--plan-retention", "72h", "--job-log-retention", "48h"},
			history.RetentionSettings{Plan: 72 * time.Hour, JobLog: 48 * time.Hour},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			if err := newTestFlagSet(&f).Parse(tc.args); err != nil {
				t.Fatalf("parse %v: %v", tc.args, err)
			}

			if err := f.check(); err != nil {
				t.Fatalf("check: %v", err)
			}

			if got := serverRetention(t, &f); got != tc.want {
				t.Errorf("options retention = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDevServerOptionsCarryTheDefaultJobRetention proves that dev takes the
// periods from the setting defaults and passes them to the server options.
func TestDevServerOptionsCarryTheDefaultJobRetention(t *testing.T) {
	t.Parallel()

	var (
		r    devRun
		cert devCert
	)

	r.dir = t.TempDir()

	f, err := r.serveFlags("https://127.0.0.1:1", cert, "dev-test")
	if err != nil {
		t.Fatalf("serveFlags: %v", err)
	}

	if got := serverRetention(t, &f); got != history.DefaultRetentionSettings() {
		t.Errorf("dev options retention = %+v, want %+v", got, history.DefaultRetentionSettings())
	}
}
