package job_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
)

// refusalCount counts the refusals that the blob code records.
type refusalCount struct{ n int }

func (r *refusalCount) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	r.n++

	return nil
}

func newArtifactBlobs(t *testing.T) (*job.ArtifactBlobs, *blobfault.Store, *refusalCount) {
	t.Helper()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	counted, err := blobfault.New(inner)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	refusals := &refusalCount{}

	b, err := job.NewArtifactBlobs(counted, refusals)
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	return b, counted, refusals
}

func TestNewArtifactBlobsNeedsItsPorts(t *testing.T) {
	t.Parallel()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	if _, err = job.NewArtifactBlobs(nil, &refusalCount{}); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("no store = %v, want ErrInvalid", err)
	}

	if _, err = job.NewArtifactBlobs(inner, nil); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("no recorder = %v, want ErrInvalid", err)
	}
}

func TestArtifactBlobsRoundTrip(t *testing.T) {
	t.Parallel()

	b, _, _ := newArtifactBlobs(t)
	ns := namespace.NewID()

	if err := b.PutPlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanApply, strings.NewReader("plan")); err != nil {
		t.Fatalf("PutPlanPart = %v", err)
	}

	if err := b.PutLogChunk(t.Context(), ns, "job-1", "jlv-1", 3, strings.NewReader("log")); err != nil {
		t.Fatalf("PutLogChunk = %v", err)
	}

	rc, err := b.OpenPlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanApply)
	if err != nil {
		t.Fatalf("OpenPlanPart = %v", err)
	}

	got, _ := io.ReadAll(rc)
	_ = rc.Close() //nolint:errcheck // Test read.

	if string(got) != "plan" {
		t.Fatalf("plan part = %q, want plan", got)
	}

	// Written once: a second put keeps the first object.
	err = b.PutPlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanApply, strings.NewReader("other"))
	if !errors.Is(err, blob.ErrExists) {
		t.Fatalf("second PutPlanPart = %v, want blob.ErrExists", err)
	}

	if _, err = b.OpenLogChunk(t.Context(), ns, "job-1", "jlv-1", 4); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("OpenLogChunk of a chunk that is not stored = %v, want blob.ErrNotFound", err)
	}

	// The same IDs in another namespace are another blob.
	if _, err = b.OpenPlanPart(t.Context(), namespace.NewID(), "job-1", "pln-1", job.OutputPlanApply); !errors.Is(err,
		blob.ErrNotFound) {
		t.Fatalf("OpenPlanPart in another namespace = %v, want blob.ErrNotFound", err)
	}

	if err = b.DeletePlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanApply); err != nil {
		t.Fatalf("DeletePlanPart = %v", err)
	}

	if err = b.DeletePlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanApply); err != nil {
		t.Fatalf("DeletePlanPart of a part that is gone = %v", err)
	}
}

// TestArtifactBlobsRefusesHostileParts proves that a part of a key that is not
// one segment, or lacks its prefix, never reaches the store. This is the layer
// that holds when the caller did not check the IDs.
func TestArtifactBlobsRefusesHostileParts(t *testing.T) {
	t.Parallel()

	b, store, refusals := newArtifactBlobs(t)
	ns := namespace.NewID()

	hostile := []struct {
		name string
		do   func() error
	}{
		{"a job ID with a slash", func() error {
			return b.PutPlanPart(t.Context(), ns, "job-1/../job-2", "pln-1", job.OutputPlanApply, strings.NewReader("x"))
		}},
		{"a job ID with no prefix", func() error {
			return b.PutPlanPart(t.Context(), ns, "x", "pln-1", job.OutputPlanApply, strings.NewReader("x"))
		}},
		{"a plan ID with a slash", func() error {
			return b.PutPlanPart(t.Context(), ns, "job-1", "pln-1/x", job.OutputPlanApply, strings.NewReader("x"))
		}},
		{"a plan ID with the prefix of a log", func() error {
			return b.PutPlanPart(t.Context(), ns, "job-1", "jlv-1", job.OutputPlanApply, strings.NewReader("x"))
		}},
		{"a part kind that is not a plan output", func() error {
			return b.PutPlanPart(t.Context(), ns, "job-1", "pln-1", "../x", strings.NewReader("x"))
		}},
		{"a version with a slash", func() error {
			return b.PutLogChunk(t.Context(), ns, "job-1", "jlv-1/2", 0, strings.NewReader("x"))
		}},
		{"a version with the prefix of a plan", func() error {
			return b.PutLogChunk(t.Context(), ns, "job-1", "pln-1", 0, strings.NewReader("x"))
		}},
		{"a negative chunk number", func() error {
			return b.PutLogChunk(t.Context(), ns, "job-1", "jlv-1", -1, strings.NewReader("x"))
		}},
		{"a chunk number above the limit", func() error {
			return b.PutLogChunk(t.Context(), ns, "job-1", "jlv-1", job.MaxLogChunks, strings.NewReader("x"))
		}},
		{"an open of a part with a hostile plan ID", func() error {
			_, err := b.OpenPlanPart(t.Context(), ns, "job-1", "pln-1/../../x", job.OutputPlanApply)

			return err
		}},
		{"an open of a chunk with a hostile version", func() error {
			_, err := b.OpenLogChunk(t.Context(), ns, "job-1", "../jlv-1", 0)

			return err
		}},
		{"a delete of a part with a hostile job ID", func() error {
			return b.DeletePlanPart(t.Context(), ns, "../job-1", "pln-1", job.OutputPlanApply)
		}},
		{"a namespace ID that is not one", func() error {
			return b.PutPlanPart(t.Context(), "NS-UPPER", "job-1", "pln-1", job.OutputPlanApply, strings.NewReader("x"))
		}},
	}

	for _, h := range hostile {
		if err := h.do(); err == nil {
			t.Errorf("%s: the call succeeded", h.name)
		}
	}

	if n := store.Calls("Put") + store.Calls("Get") + store.Calls("Delete"); n != 0 {
		t.Fatalf("hostile calls reached the blob store %d times, want 0", n)
	}

	if refusals.n != 0 {
		t.Fatalf("refusals = %d, want 0: a refused part never makes a key", refusals.n)
	}
}

func TestArtifactBlobsKeepTheKeyLayout(t *testing.T) {
	t.Parallel()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	b, err := job.NewArtifactBlobs(inner, &refusalCount{})
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	ns := namespace.NewID()

	if err = b.PutPlanPart(t.Context(), ns, "job-1", "pln-1", job.OutputPlanDisplay, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("PutPlanPart = %v", err)
	}

	if err = b.PutLogChunk(t.Context(), ns, "job-1", "jlv-1", 12, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("PutLogChunk = %v", err)
	}

	objects, err := inner.List(t.Context(), "namespaces/"+ns.String()+"/job/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}

	got := make([]string, 0, len(objects))
	for _, o := range objects {
		got = append(got, o.Key.String())
	}

	want := []string{
		"namespaces/" + ns.String() + "/job/job-1/log/jlv-1/00000012",
		"namespaces/" + ns.String() + "/job/job-1/plan/pln-1/plan-display",
	}

	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestJobBlobFamily(t *testing.T) {
	t.Parallel()

	if f := job.ArtifactBlobFamily(); f.Dir != "job" || f.Blobs != "job artifact blob" {
		t.Fatalf("family = %+v, want the directory job", f)
	}
}
