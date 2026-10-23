package unitimporttest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/unitimport"
)

// SourceFixture is one fresh source for one subtest.
//
// Credential is the credential that the source accepts. It holds Canary, a
// text that must not reach an error text. Seed puts the bodies in the source
// as the versions of the unit name, oldest first, and returns the unit.
// Append puts one more version after the newest version of u.
type SourceFixture struct {
	Source     unitimport.Source
	Credential unitimport.Credential
	Canary     string
	Seed       func(t *testing.T, name string, bodies [][]byte) unitimport.SourceUnit
	Append     func(t *testing.T, u unitimport.SourceUnit, body []byte)
}

// SourceFactory returns a fresh fixture for one subtest.
type SourceFactory func(t *testing.T) SourceFixture

// RunSource runs every case of the source suite.
func RunSource(t *testing.T, newFixture SourceFactory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, f SourceFixture)
	}{
		{"the kind is known", testSourceKind},
		{"list the units", testSourceUnits},
		{"versions come oldest first with their facts", testSourceVersions},
		{"versions come in pages", testSourcePages},
		{"open streams the bytes of a version", testSourceOpen},
		{"head follows a new version", testSourceHead},
		{"an unknown unit or version", testSourceNotFound},
		{"a canceled context stops each method", testSourceCanceled},
		{"no error text holds the credential or a byte of state", testSourceErrorText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newFixture(t))
		})
	}
}

// bodies returns n distinct bodies.
func bodies(n int) [][]byte {
	out := make([][]byte, 0, n)
	for i := range n {
		out = append(out, []byte(strings.Repeat("state-", i+1)+"end"))
	}

	return out
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// allVersions pages through every version of u, oldest first.
func allVersions(t *testing.T, f SourceFixture, u unitimport.SourceUnit, limit int) []unitimport.SourceVersion {
	t.Helper()

	out := []unitimport.SourceVersion{}

	var after unitimport.VersionID

	for range 100 {
		page, err := f.Source.Versions(t.Context(), f.Credential, u, after, limit)
		if err != nil {
			t.Fatalf("versions after %q: %v", after, err)
		}

		if len(page) == 0 {
			return out
		}

		if len(page) > limit {
			t.Fatalf("page of %d versions, limit %d", len(page), limit)
		}

		out = append(out, page...)
		after = page[len(page)-1].ID
	}

	t.Fatal("the pages of versions do not end")

	return nil
}

func readAll(t *testing.T, f SourceFixture, u unitimport.SourceUnit, id unitimport.VersionID) []byte {
	t.Helper()

	r, err := f.Source.Open(t.Context(), f.Credential, u, id)
	if err != nil {
		t.Fatalf("open %q: %v", id, err)
	}

	defer func() {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close %q: %v", id, closeErr)
		}
	}()

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %q: %v", id, err)
	}

	return b
}

func testSourceKind(t *testing.T, f SourceFixture) {
	t.Helper()

	if k := f.Source.Kind(); !k.Valid() {
		t.Fatalf("kind = %q, want a known kind", k)
	}
}

func testSourceUnits(t *testing.T, f SourceFixture) {
	t.Helper()

	a := f.Seed(t, "acme/web", bodies(1))
	b := f.Seed(t, "acme/db", bodies(2))

	all, err := f.Source.Units(t.Context(), f.Credential, unitimport.Selection{Names: nil})
	if err != nil {
		t.Fatalf("units: %v", err)
	}

	if !slices.Contains(all, a) || !slices.Contains(all, b) {
		t.Fatalf("units = %v, want %v and %v", all, a, b)
	}

	one, err := f.Source.Units(t.Context(), f.Credential, unitimport.Selection{Names: []string{"acme/db"}})
	if err != nil || len(one) != 1 || one[0] != b {
		t.Fatalf("selected units = %v, %v, want only %v", one, err, b)
	}
}

func testSourceVersions(t *testing.T, f SourceFixture) {
	t.Helper()

	want := bodies(3)
	u := f.Seed(t, "acme/web", want)
	got := allVersions(t, f, u, 10)

	if len(got) != len(want) {
		t.Fatalf("%d versions, want %d", len(got), len(want))
	}

	seen := map[unitimport.VersionID]bool{}

	for i, v := range got {
		if v.ID == "" || seen[v.ID] {
			t.Errorf("version %d has the ID %q, want a new ID", i, v.ID)
		}

		seen[v.ID] = true

		if v.Size != int64(len(want[i])) || v.SHA256 != digest(want[i]) || v.At.IsZero() {
			t.Errorf("version %d = %+v, want size %d, digest %s and a time", i, v, len(want[i]), digest(want[i]))
		}

		if i > 0 && v.At.Before(got[i-1].At) {
			t.Errorf("version %d is older than version %d", i, i-1)
		}
	}
}

func testSourcePages(t *testing.T, f SourceFixture) {
	t.Helper()

	u := f.Seed(t, "acme/web", bodies(5))

	whole := allVersions(t, f, u, 100)
	paged := allVersions(t, f, u, 2)

	if len(paged) != 5 || len(whole) != 5 {
		t.Fatalf("%d paged and %d whole versions, want 5 each", len(paged), len(whole))
	}

	for i := range paged {
		if paged[i].ID != whole[i].ID {
			t.Errorf("paged version %d = %q, want %q", i, paged[i].ID, whole[i].ID)
		}
	}

	rest, err := f.Source.Versions(t.Context(), f.Credential, u, whole[3].ID, 10)
	if err != nil || len(rest) != 1 || rest[0].ID != whole[4].ID {
		t.Fatalf("versions after the fourth = %v, %v, want the fifth", rest, err)
	}
}

func testSourceOpen(t *testing.T, f SourceFixture) {
	t.Helper()

	want := bodies(2)
	u := f.Seed(t, "acme/web", want)

	for i, v := range allVersions(t, f, u, 10) {
		got := readAll(t, f, u, v.ID)
		if !bytes.Equal(got, want[i]) || digest(got) != v.SHA256 {
			t.Errorf("version %d bytes = %q, want %q", i, got, want[i])
		}
	}
}

func testSourceHead(t *testing.T, f SourceFixture) {
	t.Helper()

	u := f.Seed(t, "acme/web", bodies(2))

	versions := allVersions(t, f, u, 10)

	head, err := f.Source.Head(t.Context(), f.Credential, u)
	if err != nil || head != versions[1].ID {
		t.Fatalf("head = %q, %v, want %q", head, err, versions[1].ID)
	}

	f.Append(t, u, []byte("newer"))

	newHead, err := f.Source.Head(t.Context(), f.Credential, u)
	if err != nil || newHead == head {
		t.Fatalf("head after a new version = %q, %v, want another ID than %q", newHead, err, head)
	}

	rest, err := f.Source.Versions(t.Context(), f.Credential, u, head, 10)
	if err != nil || len(rest) != 1 || rest[0].ID != newHead {
		t.Fatalf("versions after the old head = %v, %v, want the new version", rest, err)
	}
}

func testSourceNotFound(t *testing.T, f SourceFixture) {
	t.Helper()

	u := f.Seed(t, "acme/web", bodies(1))
	missing := unitimport.SourceUnit{Tool: u.Tool, Name: "acme/missing"}
	ctx := t.Context()

	_, err := f.Source.Versions(ctx, f.Credential, missing, "", 10)
	mustIs(t, "versions of a missing unit", err, unitimport.ErrSourceNotFound)

	_, err = f.Source.Head(ctx, f.Credential, missing)
	mustIs(t, "head of a missing unit", err, unitimport.ErrSourceNotFound)

	_, err = f.Source.Open(ctx, f.Credential, missing, "1")
	mustIs(t, "open of a missing unit", err, unitimport.ErrSourceNotFound)

	_, err = f.Source.Open(ctx, f.Credential, u, "missing-version")
	mustIs(t, "open of a missing version", err, unitimport.ErrSourceNotFound)
}

func testSourceCanceled(t *testing.T, f SourceFixture) {
	t.Helper()

	u := f.Seed(t, "acme/web", bodies(1))
	versions := allVersions(t, f, u, 10)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := f.Source.Units(ctx, f.Credential, unitimport.Selection{Names: nil})
	mustIs(t, "units", err, context.Canceled)

	_, err = f.Source.Versions(ctx, f.Credential, u, "", 10)
	mustIs(t, "versions", err, context.Canceled)

	_, err = f.Source.Head(ctx, f.Credential, u)
	mustIs(t, "head", err, context.Canceled)

	r, openErr := f.Source.Open(ctx, f.Credential, u, versions[0].ID)
	if openErr == nil {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}

		t.Fatal("open with a canceled context succeeded")
	}

	mustIs(t, "open", openErr, context.Canceled)
}

func testSourceErrorText(t *testing.T, f SourceFixture) {
	t.Helper()

	u := f.Seed(t, "acme/web", bodies(1))
	missing := unitimport.SourceUnit{Tool: u.Tool, Name: "acme/missing"}

	errs := make([]error, 0, 3)

	_, err := f.Source.Versions(t.Context(), f.Credential, missing, "", 10)
	errs = append(errs, err)

	_, err = f.Source.Head(t.Context(), f.Credential, missing)
	errs = append(errs, err)

	_, err = f.Source.Open(t.Context(), f.Credential, u, "missing-version")
	errs = append(errs, err)

	for _, e := range errs {
		if e == nil {
			t.Fatal("an error is nil, want an error")
		}

		if text := e.Error(); strings.Contains(text, f.Canary) || strings.Contains(text, "state-") {
			t.Errorf("error text %q holds the credential or a byte of state", text)
		}
	}

	if errors.Is(errs[0], unitimport.ErrSourceDenied) {
		t.Error("a missing unit reads as a denied credential")
	}
}
