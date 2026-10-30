package upload_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/internal/unitimport/unitimporttest"
	"opendustpan.org/opendustpan/internal/unitimport/upload"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// errReset is the error of a store that loses the response of a write.
var errReset = errors.New("connection reset")

// memCommits is a reference adapter of unitimport.UploadStore. The key of a
// row is its primary key in the SQL adapters.
type memCommits struct {
	rows map[string]unitimport.UploadCommit
	// lose, when set, stores the row and then answers its error, as a store
	// does when it loses the response.
	lose error
	mu   sync.Mutex
}

func newCommits() *memCommits {
	return &memCommits{rows: map[string]unitimport.UploadCommit{}, lose: nil, mu: sync.Mutex{}}
}

func (m *memCommits) CommitUploadVersion(_ context.Context, _ namespace.Fence, c unitimport.UploadCommit) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("upload commit: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := fmt.Sprintf("%s/%d", c.UnitDir, c.Sequence)
	if _, ok := m.rows[k]; ok {
		return fmt.Errorf("%w: version %d", unitimport.ErrUploadExists, c.Sequence)
	}

	m.rows[k] = c

	return m.lose
}

func (m *memCommits) UploadCommits(
	_ context.Context,
	_ namespace.Name,
	_, dir string,
) ([]unitimport.UploadCommit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []unitimport.UploadCommit

	for _, c := range m.rows {
		if c.UnitDir == dir {
			out = append(out, c)
		}
	}

	slices.SortFunc(out, func(a, b unitimport.UploadCommit) int { return a.Sequence - b.Sequence })

	return out, nil
}

// commitsOf returns the commit store of s: the tests keep it beside the
// source.
func commitsFor(c unitimport.UploadStore) upload.Commits {
	return upload.Commits{Store: c, Fence: namespace.Fence{Namespace: "acme", FailoverVersion: 1}, ImportID: "imp-1"}
}

func newSource(t *testing.T) (*upload.Source, *blobfs.Store) {
	t.Helper()

	s, store, _ := newSourceWith(t)

	return s, store
}

func newSourceWith(t *testing.T) (*upload.Source, *blobfs.Store, *memCommits) {
	t.Helper()

	store, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	commits := newCommits()

	s, err := upload.New(store, commitsFor(commits), prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	return s, store, commits
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

	objs, err := store.List(t.Context(), prefix)
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
	objs, err := store.List(t.Context(), prefix)
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

	objs, err := store.List(t.Context(), prefix)
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
	}{"empty prefix": {"", 1}, "bad prefix": {"Upload/X", 1}, "no limit": {"upload/a", 0}} {
		if _, err = upload.New(store, commitsFor(newCommits()), tc.prefix, tc.max, time.Now); err == nil {
			t.Errorf("%s: New succeeded, want an error", name)
		}
	}

	none := upload.Commits{Store: nil, Fence: namespace.Fence{Namespace: "", FailoverVersion: 0}, ImportID: ""}

	if _, err = upload.New(store, none, "upload/a", 1, time.Now); err == nil {
		t.Error("New with no commit store succeeded, want an error")
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

func TestPutAfterALostResponseKeepsTheBody(t *testing.T) {
	t.Parallel()

	s, _, commits := newSourceWith(t)
	commits.lose = errReset

	v := put(t, s, unit(), 1, []byte("kept"))

	if got := readBody(t, s, v.ID); got != "kept" {
		t.Errorf("body = %q, want kept", got)
	}
}

func TestPutAfterAnUnknownCommitErrorKeepsTheFiles(t *testing.T) {
	t.Parallel()

	_, store, commits := newSourceWith(t)

	// A store that fails and keeps no row: the commit may still land, so the
	// two files of the attempt stay for the cancel or the release.
	failing := &failingCommits{memCommits: commits}

	s2, err := upload.New(store, commitsFor(failing), prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	_, err = s2.Put(t.Context(), unit(), 1, claims(1), strings.NewReader("kept"))
	if !errors.Is(err, unitimport.ErrSourceUnreachable) {
		t.Fatalf("put error = %v, want ErrSourceUnreachable", err)
	}

	objs, listErr := store.List(t.Context(), prefix)
	if listErr != nil || len(objs) != 3 {
		t.Errorf("objects = %d, %v, want unit.json, the body and the facts file", len(objs), listErr)
	}
}

// failingCommits fails every commit with an error that does not say whether
// the row exists, and it stores nothing.
type failingCommits struct {
	*memCommits
}

func (*failingCommits) CommitUploadVersion(context.Context, namespace.Fence, unitimport.UploadCommit) error {
	return errReset
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

	objs, err := store.List(t.Context(), prefix)
	if err != nil || len(objs) != 0 {
		t.Fatalf("list after delete = %v, %v, want none", objs, err)
	}
}

// replacingStore is a blob store that ignores a create-only write: a Put on a
// key that holds an object replaces the object with no error. A hold, when
// set, stops each Put of a facts file until n Puts of facts files wait, so
// that the uploads of one sequence write their facts before any commits.
type replacingStore struct {
	blob.Store

	mu      sync.Mutex
	waiting int
	open    chan struct{}
	n       int
}

func newReplacingStore(inner blob.Store, hold int) *replacingStore {
	return &replacingStore{Store: inner, mu: sync.Mutex{}, waiting: 0, open: make(chan struct{}), n: hold}
}

func (r *replacingStore) Put(ctx context.Context, k blob.Key, body io.Reader) error {
	if err := r.Delete(ctx, k); err != nil {
		return fmt.Errorf("replace: %w", err)
	}

	if err := r.Store.Put(ctx, k, body); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	name := k.String()[strings.LastIndex(k.String(), "/")+1:]
	if r.n == 0 || name == "unit.json" || !strings.HasSuffix(name, ".json") {
		return nil
	}

	r.mu.Lock()
	r.waiting++

	if r.waiting == r.n {
		close(r.open)
	}

	r.mu.Unlock()

	select {
	case <-r.open:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("hold: %w", ctx.Err())
	}
}

// racePuts puts each body as version 1 of unit() at the same time. It checks
// that exactly one put commits and that each other put gets a conflict error
// that names the stored sequence, and it returns the index of the winner.
func racePuts(t *testing.T, s *upload.Source, bodies []string) int {
	t.Helper()

	errs := make([]error, len(bodies))

	var wg sync.WaitGroup

	for n := range bodies {
		wg.Go(func() {
			_, errs[n] = s.Put(t.Context(), unit(), 1, claims(1), strings.NewReader(bodies[n]))
		})
	}

	wg.Wait()

	winner, losers := -1, 0

	for n, putErr := range errs {
		switch {
		case putErr == nil:
			winner = n
		case errors.Is(putErr, unitimport.ErrSourceInvalid) && errors.Is(putErr, unitimport.ErrUploadExists) &&
			strings.Contains(putErr.Error(), "sequence 1 is stored"):
			losers++
		default:
			t.Errorf("put %d error = %v, want nil or a conflict that names the stored sequence", n, putErr)
		}
	}

	if winner < 0 || losers != len(bodies)-1 {
		t.Fatalf("winner = %d, losers = %d, want exactly one commit and a conflict for each other put", winner, losers)
	}

	return winner
}

func TestTwoConcurrentPutsOnAStoreThatReplacesObjects(t *testing.T) {
	t.Parallel()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	store := newReplacingStore(inner, 2)

	s, err := upload.New(store, commitsFor(newCommits()), prefix, maxBytes, clock)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	bodies := []string{"first body", "second body"}
	winner := racePuts(t, s, bodies)

	versions, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if err != nil || len(versions) != 1 || versions[0].Size != int64(len(bodies[winner])) {
		t.Fatalf("versions = %+v, %v, want the one version of the winner", versions, err)
	}

	if got := readBody(t, s, versions[0].ID); got != bodies[winner] {
		t.Errorf("committed body = %q, want %q", got, bodies[winner])
	}

	// The loser deleted its own two files and nothing else.
	objs, err := inner.List(t.Context(), prefix)
	if err != nil || len(objs) != 3 {
		t.Errorf("objects = %d, %v, want unit.json, the body and the facts file of the winner", len(objs), err)
	}
}

func TestAFactsFileWithNoRowIsNotAVersion(t *testing.T) {
	t.Parallel()

	s, store := newSource(t)
	put(t, s, unit(), 1, []byte("one"))

	// The facts file of an attempt that lost, or that a crash left behind.
	writeKey(t, store, unit(), "0000000002-zzz.json", fmt.Sprintf(
		`{"tool":"pulumi","name":"acme/web/prod","sequence":2,"body":"0000000002.zzz.bin","atMs":%d,"actor":"","size":3,"sha256":%q}`,
		clock().UnixMilli(),
		strings.Repeat("0", 64),
	))

	versions, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions = %+v, %v, want the one committed version", versions, err)
	}

	_, err = s.Open(t.Context(), unitimport.Credential{}, unit(), "0000000002")
	if !errors.Is(err, unitimport.ErrSourceNotFound) {
		t.Errorf("open of an uncommitted sequence: error = %v, want ErrSourceNotFound", err)
	}

	put(t, s, unit(), 2, []byte("two"))
}

func TestReaderRefusesAHostileCommitRow(t *testing.T) {
	t.Parallel()

	hostile := []string{"../0000000001-aaa.json", "x/0000000001-aaa.json", "0000000001-aaa.bin", "unit.json", ""}

	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, _, commits := newSourceWith(t)
			put(t, s, unit(), 1, []byte("one"))

			commits.rows[dirName(unit())+"/1"] = unitimport.UploadCommit{
				ImportID: "imp-1", UnitDir: dirName(unit()), Sequence: 1, Facts: name, CommittedAt: clock(),
			}

			_, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
			if !errors.Is(err, unitimport.ErrSourceInvalid) {
				t.Errorf("versions with the row name %q: error = %v, want ErrSourceInvalid", name, err)
			}
		})
	}
}

func TestReaderRefusesAFactsFileOfAnotherSequence(t *testing.T) {
	t.Parallel()

	s, store, commits := newSourceWith(t)
	put(t, s, unit(), 1, []byte("one"))

	row := commits.rows[dirName(unit())+"/1"]

	writeKey(
		t,
		store,
		unit(),
		row.Facts,
		`{"tool":"pulumi","name":"acme/web/prod","sequence":7,"body":"x.bin","size":1,"sha256":"","atMs":0,"actor":""}`,
	)

	_, err := s.Versions(t.Context(), unitimport.Credential{}, unit(), "", 10)
	if !errors.Is(err, unitimport.ErrSourceInvalid) {
		t.Errorf("versions: error = %v, want ErrSourceInvalid", err)
	}
}

// dirName returns the unit directory name that the commit rows hold.
func dirName(u unitimport.SourceUnit) string {
	sum := sha256.Sum256([]byte(u.Tool + "\x00" + u.Name))

	return hex.EncodeToString(sum[:16])
}

// writeKey writes the object name in the directory of u.
func writeKey(t *testing.T, store blob.Store, u unitimport.SourceUnit, name, content string) {
	t.Helper()

	k, err := blob.ParseKey(prefix + "/" + dirName(u) + "/" + name)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// The test replaces the object, so that it can set up a bad file.
	if err = store.Delete(t.Context(), k); err != nil {
		t.Fatalf("delete %s: %v", name, err)
	}

	if err = store.Put(t.Context(), k, strings.NewReader(content)); err != nil {
		t.Fatalf("put %s: %v", name, err)
	}
}
