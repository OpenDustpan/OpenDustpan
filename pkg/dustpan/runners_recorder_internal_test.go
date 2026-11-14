package dustpan

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// recorderKey is a key-encryption key of 32 bytes for the test.
const recorderKey = "0123456789abcdef0123456789abcdef"

// fullRecorderInputs returns the inputs of a server that has every store.
func fullRecorderInputs(t *testing.T) *jobRecorderInputs {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob directory: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(recorderKey))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return &jobRecorderInputs{
		jobs: st.Jobs(), blobs: blobs, updates: st, namespaceStore: st, wrapper: wrapper, now: time.Now,
		logger: slog.New(slog.DiscardHandler), namespaces: namespaces, retention: defaultJobRetention(),
	}
}

// TestNewDurableRecorderNeedsItsStores proves that a server that lacks one
// store builds no recorder, so the step and log paths of a job session answer
// not-found and no launcher writes a byte. Each case lacks one store and has
// all the others. The last case has all of them and builds the recorder.
func TestNewDurableRecorderNeedsItsStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*jobRecorderInputs)
		want   bool
	}{
		{name: "no job store", mutate: func(in *jobRecorderInputs) { in.jobs = nil }, want: false},
		{name: "no blob store", mutate: func(in *jobRecorderInputs) { in.blobs = nil }, want: false},
		{name: "no update store", mutate: func(in *jobRecorderInputs) { in.updates = nil }, want: false},
		{name: "no namespace store", mutate: func(in *jobRecorderInputs) { in.namespaceStore = nil }, want: false},
		{name: "no namespace service", mutate: func(in *jobRecorderInputs) { in.namespaces = nil }, want: false},
		{name: "every store", mutate: func(*jobRecorderInputs) {}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := fullRecorderInputs(t)
			tc.mutate(in)

			got, ok, err := newDurableRecorder(in)
			if err != nil {
				t.Fatalf("newDurableRecorder: %v", err)
			}

			if ok != tc.want || (got != nil) != tc.want {
				t.Fatalf("newDurableRecorder = %v, %t, want a recorder: %t", got, ok, tc.want)
			}
		})
	}
}

// TestNewDurableRecorderAppliesTheConfiguredRetention proves that the
// recorder gets the periods of the inputs, so a configured period is the one
// that the recorder gives to new plans and log versions.
func TestNewDurableRecorderAppliesTheConfiguredRetention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want jobRetention
	}{
		{name: "the defaults", want: defaultJobRetention()},
		{name: "two days of logs", want: jobRetention{Plan: 400 * 24 * time.Hour, JobLog: 48 * time.Hour}},
		{name: "two days of plans", want: jobRetention{Plan: 48 * time.Hour, JobLog: 400 * 24 * time.Hour}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := fullRecorderInputs(t)
			in.retention = tc.want

			got, ok, err := newDurableRecorder(in)
			if err != nil || !ok {
				t.Fatalf("newDurableRecorder = %t, %v, want a recorder", ok, err)
			}

			if got.Retention() != tc.want {
				t.Errorf("recorder retention = %+v, want %+v", got.Retention(), tc.want)
			}
		})
	}
}
