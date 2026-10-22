package job_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
)

// refusalCount counts the refusals that the blob code records.
type refusalCount struct{ n atomic.Int64 }

func (r *refusalCount) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	r.n.Add(1)

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

	refusals := new(refusalCount)

	b, err := job.NewArtifactBlobs(counted, refusals)
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	return b, counted, refusals
}

// planRef returns the reference of the part k of the plan pln-1 of job-1.
func planRef(ns namespace.ID, k job.OutputKind) job.PlanPartRef {
	return job.PlanPartRef{Namespace: ns, Job: "job-1", Plan: "pln-1", Kind: k}
}

// chunkRef returns the reference of the chunk seq of the log jlv-1 of job-1.
func chunkRef(ns namespace.ID, seq int) job.LogChunkRef {
	return job.LogChunkRef{Namespace: ns, Job: "job-1", Version: "jlv-1", Seq: seq}
}

func TestNewArtifactBlobsNeedsItsPorts(t *testing.T) {
	t.Parallel()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	if _, err = job.NewArtifactBlobs(nil, new(refusalCount)); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("no store = %v, want ErrInvalid", err)
	}

	if _, err = job.NewArtifactBlobs(inner, nil); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("no recorder = %v, want ErrInvalid", err)
	}
}

func TestArtifactBlobsRoundTrip(t *testing.T) {
	t.Parallel()

	const chunk = 3

	b, _, _ := newArtifactBlobs(t)
	ns := namespace.NewID()
	part := planRef(ns, job.OutputPlanApply)

	if err := b.PutPlanPart(t.Context(), part, strings.NewReader("plan")); err != nil {
		t.Fatalf("PutPlanPart = %v", err)
	}

	if err := b.PutLogChunk(t.Context(), chunkRef(ns, chunk), strings.NewReader("log")); err != nil {
		t.Fatalf("PutLogChunk = %v", err)
	}

	rc, err := b.OpenPlanPart(t.Context(), part)
	if err != nil {
		t.Fatalf("OpenPlanPart = %v", err)
	}

	got, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err != nil || closeErr != nil || string(got) != "plan" {
		t.Fatalf("plan part = %q, %v, %v; want plan", got, err, closeErr)
	}

	// Written once: a second put keeps the first object.
	if err = b.PutPlanPart(t.Context(), part, strings.NewReader("other")); !errors.Is(err, blob.ErrExists) {
		t.Fatalf("second PutPlanPart = %v, want blob.ErrExists", err)
	}

	if _, err = b.OpenLogChunk(t.Context(), chunkRef(ns, chunk+1)); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("OpenLogChunk of a chunk that is not stored = %v, want blob.ErrNotFound", err)
	}

	// The same IDs in another namespace are another blob.
	if _, err = b.OpenPlanPart(t.Context(), planRef(namespace.NewID(), job.OutputPlanApply)); !errors.Is(err,
		blob.ErrNotFound) {
		t.Fatalf("OpenPlanPart in another namespace = %v, want blob.ErrNotFound", err)
	}

	if err = b.DeletePlanPart(t.Context(), part); err != nil {
		t.Fatalf("DeletePlanPart = %v", err)
	}

	if err = b.DeletePlanPart(t.Context(), part); err != nil {
		t.Fatalf("DeletePlanPart of a part that is gone = %v", err)
	}
}

// hostileCall is a call of the blob code with a part that makes no key.
type hostileCall struct {
	name string
	do   func(b *job.ArtifactBlobs, ns namespace.ID) error
}

// hostileCalls lists calls whose parts are not one segment, lack their
// prefix, or are out of range.
func hostileCalls() []hostileCall {
	put := func(ref job.PlanPartRef) func(b *job.ArtifactBlobs, ns namespace.ID) error {
		return func(b *job.ArtifactBlobs, ns namespace.ID) error {
			ref.Namespace = ns

			return b.PutPlanPart(context.Background(), ref, strings.NewReader("x"))
		}
	}

	chunk := func(ref job.LogChunkRef) func(b *job.ArtifactBlobs, ns namespace.ID) error {
		return func(b *job.ArtifactBlobs, ns namespace.ID) error {
			ref.Namespace = ns

			return b.PutLogChunk(context.Background(), ref, strings.NewReader("x"))
		}
	}

	apply := job.OutputPlanApply

	return []hostileCall{
		{
			"a job ID with a slash",
			put(job.PlanPartRef{Namespace: "", Job: "job-1/../job-2", Plan: "pln-1", Kind: apply}),
		},
		{"a job ID with no prefix", put(job.PlanPartRef{Namespace: "", Job: "x", Plan: "pln-1", Kind: apply})},
		{"a plan ID with a slash", put(job.PlanPartRef{Namespace: "", Job: "job-1", Plan: "pln-1/x", Kind: apply})},
		{
			"a plan ID with the prefix of a log",
			put(job.PlanPartRef{Namespace: "", Job: "job-1", Plan: "jlv-1", Kind: apply}),
		},
		{
			"a part kind that is not a plan output",
			put(job.PlanPartRef{Namespace: "", Job: "job-1", Plan: "pln-1", Kind: "../x"}),
		},
		{"a version with a slash", chunk(job.LogChunkRef{Namespace: "", Job: "job-1", Version: "jlv-1/2", Seq: 0})},
		{
			"a version with the prefix of a plan",
			chunk(job.LogChunkRef{Namespace: "", Job: "job-1", Version: "pln-1", Seq: 0}),
		},
		{"a negative chunk number", chunk(job.LogChunkRef{Namespace: "", Job: "job-1", Version: "jlv-1", Seq: -1})},
		{
			"a chunk number above the limit",
			chunk(job.LogChunkRef{Namespace: "", Job: "job-1", Version: "jlv-1", Seq: job.MaxLogChunks}),
		},
		{"an open of a part with a hostile plan ID", func(b *job.ArtifactBlobs, ns namespace.ID) error {
			_, err := b.OpenPlanPart(context.Background(),
				job.PlanPartRef{Namespace: ns, Job: "job-1", Plan: "pln-1/../../x", Kind: apply})

			return err
		}},
		{"an open of a chunk with a hostile version", func(b *job.ArtifactBlobs, ns namespace.ID) error {
			_, err := b.OpenLogChunk(context.Background(),
				job.LogChunkRef{Namespace: ns, Job: "job-1", Version: "../jlv-1", Seq: 0})

			return err
		}},
		{"a delete of a part with a hostile job ID", func(b *job.ArtifactBlobs, ns namespace.ID) error {
			return b.DeletePlanPart(context.Background(),
				job.PlanPartRef{Namespace: ns, Job: "../job-1", Plan: "pln-1", Kind: apply})
		}},
	}
}

// TestArtifactBlobsRefusesHostileParts proves that a part of a key that is not
// one segment, or lacks its prefix, never reaches the store. This is the layer
// that holds when the caller did not check the IDs.
func TestArtifactBlobsRefusesHostileParts(t *testing.T) {
	t.Parallel()

	b, store, refusals := newArtifactBlobs(t)
	ns := namespace.NewID()

	for _, h := range hostileCalls() {
		if err := h.do(b, ns); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", h.name, err)
		}
	}

	// A namespace ID that is not one makes no view.
	err := b.PutPlanPart(t.Context(), planRef("NS-UPPER", job.OutputPlanApply), strings.NewReader("x"))
	if err == nil {
		t.Error("a namespace ID that is not one: the call succeeded")
	}

	if n := store.Calls("Put") + store.Calls("Get") + store.Calls("Delete"); n != 0 {
		t.Fatalf("hostile calls reached the blob store %d times, want 0", n)
	}

	if n := refusals.n.Load(); n != 0 {
		t.Fatalf("refusals = %d, want 0: a refused part never makes a key", n)
	}
}

func TestArtifactBlobsKeepTheKeyLayout(t *testing.T) {
	t.Parallel()

	const chunk = 12

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	b, err := job.NewArtifactBlobs(inner, new(refusalCount))
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	ns := namespace.NewID()

	if err = b.PutPlanPart(t.Context(), planRef(ns, job.OutputPlanDisplay), strings.NewReader("x")); err != nil {
		t.Fatalf("PutPlanPart = %v", err)
	}

	if err = b.PutLogChunk(t.Context(), chunkRef(ns, chunk), strings.NewReader("x")); err != nil {
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

	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestJobBlobFamily(t *testing.T) {
	t.Parallel()

	if f := job.ArtifactBlobFamily(); f.Dir != "job" || f.Blobs != "job artifact blob" {
		t.Fatalf("family = %+v, want the directory job", f)
	}
}

// TestArtifactBlobsIDAllowlist proves that each ID segment of a key is checked
// against an allowlist, with a table of the characters that it refuses. A
// refused ID never reaches the blob store.
func TestArtifactBlobsIDAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{"letters, digits and a hyphen", "x-a1-9z", true},
		{"the longest segment", strings.Repeat("a", 64), true},
		{"an empty segment", "", false},
		{"a segment above the limit", strings.Repeat("a", 65), false},
		{"a slash", "a/b", false},
		{"a backslash", `a\b`, false},
		{"a dot", "a.b", false},
		{"two dots", "..", false},
		{"a space", "a b", false},
		{"a new line", "a\nb", false},
		{"a carriage return", "a\rb", false},
		{"a NUL", "a\x00b", false},
		{"a tab", "a\tb", false},
		{"a percent sign", "a%2fb", false},
		{"a colon", "a:b", false},
		{"an asterisk", "a*b", false},
		{"a capital", "aBc", false},
		{"an underscore", "a_b", false},
		{"a non-ASCII letter", "aé", false},
		{"a fullwidth slash", "a\uff0fb", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, store, _ := newArtifactBlobs(t)
			ns := namespace.NewID()

			_, planErr := b.OpenPlanPart(t.Context(),
				job.PlanPartRef{
					Namespace: ns, Job: job.ID(job.IDPrefix + tt.id), Plan: job.PlanIDPrefix + tt.id,
					Kind: job.OutputPlanApply,
				})
			_, chunkErr := b.OpenLogChunk(t.Context(),
				job.LogChunkRef{
					Namespace: ns, Job: job.ID(job.IDPrefix + tt.id), Version: job.LogVersionIDPrefix + tt.id,
					Seq: 0,
				})

			for what, err := range map[string]error{"plan part": planErr, "log chunk": chunkErr} {
				if got := !errors.Is(err, job.ErrInvalid); got != tt.ok {
					t.Errorf("%s with the ID segment %q: err = %v, want ok = %v", what, tt.id, err, tt.ok)
				}
			}

			if !tt.ok {
				if n := store.Calls("Get"); n != 0 {
					t.Errorf("a refused ID reached the blob store %d times, want 0", n)
				}
			}
		})
	}
}
