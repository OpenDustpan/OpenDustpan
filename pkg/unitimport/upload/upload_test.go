package upload_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/unitimport"
	"opendustpan.org/opendustpan/pkg/unitimport/unitimporttest"
	"opendustpan.org/opendustpan/pkg/unitimport/upload"
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

func TestPutReplacesACrashedUpload(t *testing.T) {
	t.Parallel()

	s, store := newSource(t)
	put(t, s, unit(), 1, []byte("one"))

	// A body with no facts file is a crash between the two writes.
	objs, err := store.List(t.Context(), prefix)
	if err != nil || len(objs) == 0 {
		t.Fatalf("list = %v, %v, want the files of the first version", objs, err)
	}

	first := objs[0].Key.String()

	orphan, err := blob.ParseKey(first[:strings.LastIndex(first, "/")] + "/0000000002.bin")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	if err = store.Put(t.Context(), orphan, strings.NewReader("half")); err != nil {
		t.Fatalf("put orphan: %v", err)
	}

	v := put(t, s, unit(), 2, []byte("two"))

	r, err := s.Open(t.Context(), unitimport.Credential{}, unit(), v.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	b, readErr := io.ReadAll(r)
	if closeErr := r.Close(); readErr != nil || closeErr != nil || string(b) != "two" {
		t.Errorf("body = %q, read error %v, close error %v, want two", b, readErr, closeErr)
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
