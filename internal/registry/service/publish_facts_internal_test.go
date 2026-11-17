package service

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// factsFile returns a measured archive with the given name, unpack error and
// H1 digest.
func factsFile(name, h1 string, unpackErr error) measured {
	file := archiveFile(name)
	file.facts = unpackFacts{h1: h1}
	file.unpackErr = unpackErr

	return file
}

const factsTestH1 = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// factsRelease returns a release with one archive, a.zip.
func factsRelease() *registry.Release {
	rel := new(registry.Release)
	rel.Archives = []registry.Archive{{Platform: "", FileName: "a.zip", SHA256: "", Size: 0, H1: ""}}

	return rel
}

// TestApplyUnpackFacts_unpackError checks that the error of the unpack is
// returned as it is, and not the text of the empty H1. It fails when the
// unpack error check is removed.
func TestApplyUnpackFacts_unpackError(t *testing.T) {
	t.Parallel()

	unpackErr := fmt.Errorf("%w: unpack failed for this test", registry.ErrInvalid)
	rel := factsRelease()

	err := applyUnpackFacts([]measured{factsFile("a.zip", "", unpackErr)}, rel)
	if err == nil || !errors.Is(err, unpackErr) || errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("got %v, want the unpack error", err)
	}

	if rel.Archives[0].H1 != "" {
		t.Fatalf("H1 = %q after a refusal, want empty", rel.Archives[0].H1)
	}
}

// TestApplyUnpackFacts_noH1 checks that an archive without an error and
// without an H1 is refused. It fails when the empty H1 check is removed.
func TestApplyUnpackFacts_noH1(t *testing.T) {
	t.Parallel()

	rel := factsRelease()

	err := applyUnpackFacts([]measured{factsFile("a.zip", "", nil)}, rel)
	if err == nil || !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("got %v, want %v", err, registry.ErrBadArchive)
	}

	if rel.Archives[0].H1 != "" {
		t.Fatalf("H1 = %q after a refusal, want empty", rel.Archives[0].H1)
	}
}

// TestApplyUnpackFacts_foreignName checks that an archive whose name is not in
// the release is refused and writes no digest. It fails when the name check is
// removed.
func TestApplyUnpackFacts_foreignName(t *testing.T) {
	t.Parallel()

	rel := factsRelease()

	err := applyUnpackFacts([]measured{factsFile("other.zip", factsTestH1, nil)}, rel)
	if err == nil || !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v, want %v", err, ErrBadRequest)
	}

	if rel.Archives[0].H1 != "" {
		t.Fatalf("H1 = %q after a refusal, want empty", rel.Archives[0].H1)
	}
}

// TestApplyUnpackFacts_writesH1 checks that a valid archive gets its H1.
func TestApplyUnpackFacts_writesH1(t *testing.T) {
	t.Parallel()

	rel := factsRelease()

	if err := applyUnpackFacts([]measured{factsFile("a.zip", factsTestH1, nil)}, rel); err != nil {
		t.Fatalf("got %v, want no error", err)
	}

	if rel.Archives[0].H1 != factsTestH1 {
		t.Fatalf("H1 = %q, want %q", rel.Archives[0].H1, factsTestH1)
	}
}

// TestCheckFileList_unknownRole checks that a file with a role that is not a
// role of the registry is refused. It fails when the role allowlist is removed.
func TestCheckFileList_unknownRole(t *testing.T) {
	t.Parallel()

	files := []StagedFile{
		{Role: RoleSums, Name: "sums", Token: "t1"},
		{Role: RoleSignature, Name: "sig", Token: "t2"},
		{Role: RoleArchive, Name: "a.zip", Token: "t3"},
		{Role: FileRole("extra"), Name: "x.bin", Token: "t4"},
	}

	err := checkFileList(files)
	if err == nil || !errors.Is(err, ErrBadRequest) {
		t.Fatalf("unknown role: got %v, want %v", err, ErrBadRequest)
	}
}
