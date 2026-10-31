package objectstore_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
	"opendustpan.org/opendustpan/pkg/logsink/objectstore"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		store := openStore(t)

		s, err := objectstore.New(store, "audit")
		if err != nil {
			t.Fatalf("new: %v", err)
		}

		return logsinktest.TestSink{
			Sink: s,
			Delivered: func(t *testing.T, _ int) [][]byte {
				t.Helper()

				return readAll(t, store, "audit")
			},
		}
	})
}

func TestKeyHoldsTheDateAndTheTimeRange(t *testing.T) {
	t.Parallel()

	store := openStore(t)

	s, err := objectstore.New(store, "logs/app")
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	first := time.Date(2026, time.October, 13, 23, 59, 59, 0, time.UTC)
	recs := []logsink.Record{
		{Time: first, Level: logsink.LevelInfo, Message: "a", Attrs: nil},
		{Time: first.Add(2 * time.Second), Level: logsink.LevelInfo, Message: "b", Attrs: nil},
	}

	if writeErr := s.Write(t.Context(), recs); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	objs, err := blobtest.ListAll(t.Context(), store, "logs/app")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(objs) != 1 {
		t.Fatalf("objects: got %d, want 1", len(objs))
	}

	want := regexp.MustCompile(`^logs/app/2026-10-13/1791935999000-1791936001000-[0-9a-f]{16}\.jsonl\.gz$`)
	if !want.MatchString(objs[0].Key.String()) {
		t.Fatalf("key %q does not match %s", objs[0].Key, want)
	}
}

func TestAPartitionSplitsTheBatchByScope(t *testing.T) {
	t.Parallel()

	store := openStore(t)

	s, err := objectstore.New(store, "audit", objectstore.WithPartition("scope"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	rec := func(scope string) logsink.Record {
		return logsink.Record{
			Time: at, Level: logsink.LevelInfo, Message: "audit entry",
			Attrs: []logsink.Attr{logsink.String("scope", scope)},
		}
	}

	recs := []logsink.Record{rec("acme"), rec("@server"), rec("acme"), rec("My_ns")}
	if writeErr := s.Write(t.Context(), recs); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	objs, err := blobtest.ListAll(t.Context(), store, "audit")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	got := make([]string, 0, len(objs))
	for _, o := range objs {
		got = append(got, strings.SplitN(o.Key.String(), "/", 3)[1])
	}

	if want := []string{"_40server", "_4dy_5fns", "acme"}; !slices.Equal(got, want) {
		t.Fatalf("partitions: got %v, want %v", got, want)
	}

	if lines := readAll(t, store, "audit/acme"); len(lines) != 2 {
		t.Fatalf("acme lines: got %d, want 2", len(lines))
	}
}

func TestNewRejectsABadPrefix(t *testing.T) {
	t.Parallel()

	if _, err := objectstore.New(openStore(t), "Audit Logs"); err == nil {
		t.Fatal("new with an invalid prefix: got nil error")
	}

	if _, err := objectstore.New(nil, "audit"); err == nil {
		t.Fatal("new without a store: got nil error")
	}
}

func openStore(t *testing.T) *fs.Store {
	t.Helper()

	store, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	return store
}

// readAll answers the lines of every object under prefix, in key order.
func readAll(t *testing.T, store blob.Store, prefix string) [][]byte {
	t.Helper()

	objs, err := blobtest.ListAll(t.Context(), store, prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var out [][]byte

	for _, o := range objs {
		rc, err := store.Get(t.Context(), o.Key)
		if err != nil {
			t.Fatalf("get %s: %v", o.Key, err)
		}

		zr, err := gzip.NewReader(rc)
		if err != nil {
			t.Fatalf("gzip %s: %v", o.Key, err)
		}

		data, err := io.ReadAll(zr)

		if closeErr := rc.Close(); closeErr != nil {
			t.Fatalf("close %s: %v", o.Key, closeErr)
		}

		if err != nil {
			t.Fatalf("read %s: %v", o.Key, err)
		}

		for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
			out = append(out, line)
		}
	}

	return out
}

// closeOnCleanup closes s when the test ends and reports a failed close.
func closeOnCleanup(t *testing.T, s logsink.Sink) {
	t.Helper()

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sink: %v", err)
		}
	})
}
