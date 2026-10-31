package upload_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/internal/unitimport/unitimporttest"
	"opendustpan.org/opendustpan/internal/unitimport/upload"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
)

// clock() is the fixed time of every case.
func clock() time.Time { return time.UnixMilli(1_800_000_000_000).UTC() }

// unit() is the one unit() of the cases.
func unit() unitimport.SourceUnit {
	return unitimport.SourceUnit{Tool: "pulumi", Name: "acme/web/prod"}
}

const (
	canary   = "canary-source-token-7f3a"
	maxBytes = 1 << 10
	prefix   = "upload/ns-1/imp-1"
)

func newSource(t *testing.T) (*upload.Source, *blobfs.Store) {
	t.Helper()

	store, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	s, err := upload.New(store, prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	return s, store
}

func claims(seq int) upload.Version {
	return upload.Version{At: clock().Add(time.Duration(seq) * time.Second), Actor: "ada", Metadata: nil}
}

func put(t *testing.T, s *upload.Source, u unitimport.SourceUnit, seq int, body []byte) unitimport.SourceVersion {
	t.Helper()

	v, err := s.Put(t.Context(), u, seq, claims(seq), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("put %s/%d: %v", u.Name, seq, err)
	}

	return v
}

func TestSourceConformance(t *testing.T) {
	t.Parallel()

	unitimporttest.RunSource(t, func(t *testing.T) unitimporttest.SourceFixture {
		t.Helper()

		s, _ := newSource(t)
		next := map[unitimport.SourceUnit]int{}

		return unitimporttest.SourceFixture{
			Source:     s,
			Credential: unitimport.NewCredential([]byte(canary)),
			Canary:     canary,
			Seed: func(t *testing.T, name string, bodies [][]byte) unitimport.SourceUnit {
				t.Helper()

				u := unitimport.SourceUnit{Tool: "opentofu", Name: name}
				for _, b := range bodies {
					next[u]++
					put(t, s, u, next[u], b)
				}

				return u
			},
			Append: func(t *testing.T, u unitimport.SourceUnit, body []byte) {
				t.Helper()

				next[u]++
				put(t, s, u, next[u], body)
			},
		}
	})
}

func TestPutKeepsTheClaimsOfTheClient(t *testing.T) {
	t.Parallel()

	s, _ := newSource(t)
	meta := []byte(`{"kind":"update","message":"deploy"}`)
	version := upload.Version{At: clock(), Actor: "ada\x00\n lovelace", Metadata: meta}

	v, err := s.Put(t.Context(), unit(), 1, version, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("versions = %+v, %v, want the one version", got, err)
	}

	for _, sv := range []unitimport.SourceVersion{v, got[0]} {
		if !sv.ClientReported || !sv.At.Equal(clock()) || sv.Actor != "ada lovelace" || sv.Size != 2 ||
			!bytes.Equal(sv.Metadata, meta) {
			t.Errorf("version = %+v, want the claims of the client, with a clean actor", sv)
		}
	}
}

func TestPutWithNoTimeUsesTheTimeOfTheUpload(t *testing.T) {
	t.Parallel()

	s, _ := newSource(t)

	v, err := s.Put(
		t.Context(),
		unit(),
		1,
		upload.Version{At: time.Time{}, Actor: "", Metadata: nil},
		strings.NewReader("{}"),
	)
	if err != nil || !v.At.Equal(clock()) {
		t.Fatalf("version = %+v, %v, want the time %v", v, err, clock())
	}
}

func TestPutRefuses(t *testing.T) {
	t.Parallel()

	bigMeta := []byte(`"` + strings.Repeat("x", upload.MaxMetadataBytes) + `"`)

	cases := []struct {
		name string
		seq  int
		meta []byte
		body string
		prep int
	}{
		{"sequence zero", 0, nil, "{}", 0},
		{"a gap in the sequence", 2, nil, "{}", 0},
		{"a repeated sequence", 1, nil, "{}", 1},
		{"a body above the limit", 1, nil, strings.Repeat("x", maxBytes+1), 0},
		{"metadata that is not JSON", 1, []byte("{nope"), "{}", 0},
		{"metadata above the limit", 1, bigMeta, "{}", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, store := newSource(t)

			for i := 1; i <= tc.prep; i++ {
				put(t, s, unit(), i, []byte("first"))
			}

			v := upload.Version{At: clock(), Actor: "", Metadata: tc.meta}

			_, err := s.Put(t.Context(), unit(), tc.seq, v, strings.NewReader(tc.body))
			if !errors.Is(err, unitimport.ErrSourceInvalid) {
				t.Fatalf("put error = %v, want ErrSourceInvalid", err)
			}

			checkUnchanged(t, s, store, tc.prep)
		})
	}
}

// checkUnchanged checks that the unit() holds want versions and no body without
// a version.
func checkUnchanged(t *testing.T, s *upload.Source, store *blobfs.Store, want int) {
	t.Helper()

	versions, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if want == 0 {
		if !errors.Is(err, unitimport.ErrSourceNotFound) {
			t.Errorf("versions after a refused put = %v, %v, want not found", versions, err)
		}
	} else if err != nil || len(versions) != want {
		t.Errorf("versions after a refused put = %v, %v, want %d", versions, err, want)
	}

	objs, err := blobtest.ListAll(t.Context(), store, prefix)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	bodies := 0

	for _, o := range objs {
		if strings.HasSuffix(o.Key.String(), ".bin") {
			bodies++
		}
	}

	if bodies != want {
		t.Errorf("%d bodies after a refused put, want %d", bodies, want)
	}
}

func TestPutIgnoresACrashedUpload(t *testing.T) {
	t.Parallel()

	s, store := newSource(t)
	put(t, s, unit(), 1, []byte("one"))

	// A body that no facts file names is a crash between the two writes.
	objs, err := blobtest.ListAll(t.Context(), store, prefix)
	if err != nil || len(objs) == 0 {
		t.Fatalf("list = %v, %v, want the files of the first version", objs, err)
	}

	first := objs[0].Key.String()

	orphan, err := blob.ParseKey(first[:strings.LastIndex(first, "/")] + "/0000000002.crashed.bin")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	if err = store.Put(t.Context(), orphan, strings.NewReader("half")); err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	v := put(t, s, unit(), 2, []byte("two"))

	if got := readBody(t, s, v.ID); got != "two" {
		t.Errorf("body = %q, want two", got)
	}

	if _, err = store.Get(t.Context(), orphan); err != nil {
		t.Errorf("get orphan: %v, want it untouched", err)
	}
}

// readBody returns the body of the version id of unit().
func readBody(t *testing.T, s *upload.Source, id unitimport.VersionID) string {
	t.Helper()

	r, err := s.Open(t.Context(), unitimport.Credential{}, unit(), id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	b, readErr := io.ReadAll(r)
	if closeErr := r.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read error %v, close error %v", readErr, closeErr)
	}

	return string(b)
}

// stalledReader gives one byte, then waits for release, then ends.
type stalledReader struct {
	started chan struct{}
	release chan struct{}
	given   bool
}

func (r *stalledReader) Read(p []byte) (int, error) {
	if !r.given {
		r.given = true
		p[0] = 'a'

		close(r.started)

		return 1, nil
	}

	<-r.release

	return 0, io.EOF
}

func TestTwoPutsOfOneSequence(t *testing.T) {
	t.Parallel()

	s, store := newSource(t)
	slow := &stalledReader{started: make(chan struct{}), release: make(chan struct{}), given: false}
	done := make(chan error, 1)

	go func() {
		_, err := s.Put(t.Context(), unit(), 1, claims(1), slow)
		done <- err
	}()

	<-slow.started

	// The second upload commits while the first still streams its body.
	winner := put(t, s, unit(), 1, []byte("winner"))

	close(slow.release)

	if err := <-done; !errors.Is(err, unitimport.ErrSourceInvalid) ||
		!strings.Contains(err.Error(), "sequence 1 is stored") {
		t.Fatalf("slow put error = %v, want ErrSourceInvalid with sequence 1 is stored", err)
	}

	if got := readBody(t, s, winner.ID); got != "winner" {
		t.Errorf("committed body = %q, want winner", got)
	}

	checkUnchanged(t, s, store, 1)
}

func TestPutKeepsTheFactsWithinTheLimit(t *testing.T) {
	t.Parallel()

	// Each < is one byte in the file, and six bytes in the escaped form.
	angle := []byte(`"` + strings.Repeat("<", upload.MaxMetadataBytes-2) + `"`)

	s, store := newSource(t)

	v := upload.Version{At: clock(), Actor: "", Metadata: angle}
	if _, err := s.Put(t.Context(), unit(), 1, v, strings.NewReader("{}")); err != nil {
		t.Fatalf("put metadata of the largest size: %v", err)
	}

	got, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if err != nil || len(got) != 1 || !bytes.Equal(got[0].Metadata, angle) {
		t.Fatalf("versions after a put with < in the metadata = %d, %v, want the metadata back", len(got), err)
	}

	long := unitimport.SourceUnit{Tool: "pulumi", Name: strings.Repeat("n", unitimport.MaxTextBytes+1)}

	_, err = s.Put(t.Context(), long, 1, claims(1), strings.NewReader("{}"))
	if !errors.Is(err, unitimport.ErrSourceInvalid) {
		t.Fatalf("put with a long name: error = %v, want ErrSourceInvalid", err)
	}

	long = unitimport.SourceUnit{Tool: strings.Repeat("t", unitimport.MaxTextBytes+1), Name: "web"}

	_, err = s.Put(t.Context(), long, 1, claims(1), strings.NewReader("{}"))
	if !errors.Is(err, unitimport.ErrSourceInvalid) {
		t.Fatalf("put with a long tool: error = %v, want ErrSourceInvalid", err)
	}

	objs, err := blobtest.ListAll(t.Context(), store, prefix)
	if err != nil || len(objs) != 3 {
		t.Errorf("objects = %d, %v, want the 3 files of the one version", len(objs), err)
	}
}

func TestNewRefuses(t *testing.T) {
	t.Parallel()

	store, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	for name, tc := range map[string]struct {
		prefix string
		max    int64
	}{
		"empty prefix": {"", 1}, "bad prefix": {"Upload/X", 1}, "no limit": {"upload/a", 0},
		"audit checkpoint prefix": {"audit-checkpoints", 1}, "below the audit checkpoint prefix": {"audit-checkpoints/epochs", 1},
	} {
		if _, err = upload.New(store, tc.prefix, tc.max, time.Now); err == nil {
			t.Errorf("%s: New succeeded, want an error", name)
		}
	}
}

func TestSourceIgnoresTheCredential(t *testing.T) {
	t.Parallel()

	s, _ := newSource(t)
	put(t, s, unit(), 1, []byte("{}"))

	for _, cred := range []unitimport.Credential{{}, unitimport.NewCredential([]byte(canary))} {
		if _, err := s.Head(t.Context(), cred, unit()); err != nil {
			t.Errorf("head: %v", err)
		}
	}
}

// lostResponseStore stores the facts key and then reports ErrExists, as a
// store does when it retries a write after it lost the response.
type lostResponseStore struct {
	blob.Store
}

func (l lostResponseStore) Put(ctx context.Context, k blob.Key, r io.Reader) error {
	if err := l.Store.Put(ctx, k, r); err != nil {
		return err
	}

	if strings.HasSuffix(k.String(), "1.json") {
		return blob.ErrExists
	}

	return nil
}

func TestPutAfterALostResponseKeepsTheBody(t *testing.T) {
	t.Parallel()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	s, err := upload.New(lostResponseStore{Store: inner}, prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	v := put(t, s, unit(), 1, []byte("kept"))

	if got := readBody(t, s, v.ID); got != "kept" {
		t.Errorf("body = %q, want kept", got)
	}
}

func TestDeleteRemovesEachObject(t *testing.T) {
	t.Parallel()

	s, store := newSource(t)
	put(t, s, unit(), 1, []byte("one"))

	orphan, err := blob.ParseKey(prefix + "/pulumi/acme/web/prod/2.deadbeef.bin")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	if err = store.Put(t.Context(), orphan, strings.NewReader("orphan")); err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	if err = s.Delete(t.Context()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	objs, err := blobtest.ListAll(t.Context(), store, prefix)
	if err != nil || len(objs) != 0 {
		t.Fatalf("list after delete = %v, %v, want none", objs, err)
	}
}

// wideList is a blob store whose List answers an audit checkpoint copy
// beside the keys below the prefix, as a List that matches too much does.
type wideList struct {
	*blobfs.Store

	extra blob.Key
}

func (w wideList) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := w.Store.ListPage(ctx, req)

	page.Objects = append(page.Objects, blob.Object{ModifiedAt: time.Time{}, Key: w.extra})

	return page, err
}

// TestDeleteKeepsAuditCheckpointCopies sends an audit checkpoint copy
// straight to the delete loop, as if the prefix check of New and the prefix
// filter of List had failed. The delete refuses it and the copy stays.
func TestDeleteKeepsAuditCheckpointCopies(t *testing.T) {
	t.Parallel()

	store, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	copyKey, err := audit.CheckpointKey(0, "acme", 1, strings.Repeat("a", 32))
	if err != nil {
		t.Fatalf("checkpoint key: %v", err)
	}

	if err = store.Put(t.Context(), copyKey, strings.NewReader("{}")); err != nil {
		t.Fatalf("put copy: %v", err)
	}

	s, err := upload.New(wideList{Store: store, extra: copyKey}, prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	if err = s.Delete(t.Context()); !errors.Is(err, unitimport.ErrInvalid) {
		t.Fatalf("delete: got %v, want ErrInvalid", err)
	}

	body, err := store.Get(t.Context(), copyKey)
	if err != nil {
		t.Fatalf("copy after the delete: %v", err)
	}

	if err = body.Close(); err != nil {
		t.Fatalf("close copy: %v", err)
	}
}
