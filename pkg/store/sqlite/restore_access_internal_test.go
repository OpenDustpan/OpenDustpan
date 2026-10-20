package sqlite

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// keepWait bounds one wait for the parked restore, the blocked write, or
// their end.
const keepWait = 30 * time.Second

// keepMemSink holds the bytes of each portable file in memory, by path.
type keepMemSink struct {
	files map[string]*bytes.Buffer
}

// keepMemWriter is the writer of one file of keepMemSink.
type keepMemWriter struct {
	*bytes.Buffer
}

// Close does nothing: the buffer stays in the sink.
func (keepMemWriter) Close() error {
	return nil
}

func (s *keepMemSink) open(path string) (io.WriteCloser, error) {
	buf := &bytes.Buffer{}
	s.files[path] = buf

	return keepMemWriter{Buffer: buf}, nil
}

func (s *keepMemSink) read(path string) (io.ReadCloser, error) {
	buf, ok := s.files[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}

	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// openSeededKeepStore opens a fresh store with one namespace, exports it and
// stages the export back, so a keep-access apply has staged data to swap.
func openSeededKeepStore(t *testing.T) *Store {
	t.Helper()

	s, openErr := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	seed := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now().UTC(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), seed); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	sink := &keepMemSink{files: map[string]*bytes.Buffer{}}

	sum, files, exportErr := s.ExportPortable(t.Context(), sink.open)
	if exportErr != nil {
		t.Fatalf("export portable: %v", exportErr)
	}

	if _, stageErr := s.StagePortable(t.Context(), sink.read, sum, files, storetest.NoStageOptions()); stageErr != nil {
		t.Fatalf("stage portable: %v", stageErr)
	}

	return s
}

// awaitKeepSignal waits for the parked restore to reach the hook.
func awaitKeepSignal(t *testing.T, entered <-chan struct{}) {
	t.Helper()

	select {
	case <-entered:
	case <-time.After(keepWait):
		t.Fatal("keep-access restore never reached the hook")
	}
}

// awaitKeepResult waits for a parked call to end and answers its error.
func awaitKeepResult(t *testing.T, done <-chan error, what string) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(keepWait):
		t.Fatal(what + " never ended")

		return nil
	}
}

// TestKeepAccessHoldsWriteLockAcrossSwap proves the keep-access restore holds
// the write lock from the live read through the swap: a concurrent write
// that starts while the restore waits between them stays blocked until the
// restore ends, so a revoke between the copy and the swap cannot be lost.
// Without the lock, or with the lock taken only around the swap, the write
// lands in between and the test fails. All store calls hold the lock shared
// and only the restore holds it exclusive, so the test uses a namespace
// create as the concurrent writer.
func TestKeepAccessHoldsWriteLockAcrossSwap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openSeededKeepStore(t)

	entered := make(chan struct{})
	release := make(chan struct{})

	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	s.keepAccessHook = func(context.Context) {
		close(entered)
		<-release
	}

	applyDone := make(chan error, 1)

	go func() {
		applyDone <- s.ApplyStagingKeepingAccess(ctx)
	}()

	awaitKeepSignal(t, entered)

	writeDone := make(chan error, 1)

	go func() {
		rival := namespace.Namespace{
			ID:              namespace.NewID(),
			CreatedAt:       time.Now().UTC(),
			Name:            "rival",
			HomeRegion:      "eu-west",
			FailoverVersion: 1,
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
		}
		writeDone <- s.CreateNamespace(context.WithoutCancel(ctx), rival)
	}()

	select {
	case err := <-writeDone:
		t.Fatalf("concurrent write ended during the keep: %v", err)
	case <-time.After(time.Second):
	}

	close(release)

	if err := awaitKeepResult(t, applyDone, "keep-access restore"); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	if err := awaitKeepResult(t, writeDone, "concurrent write"); err != nil {
		t.Fatalf("concurrent write after the keep: %v", err)
	}

	if _, err := s.GetNamespace(ctx, "rival"); err != nil {
		t.Fatalf("get rival namespace after the keep: %v", err)
	}
}
