package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// archiveFile returns a measured provider archive with the given name.
func archiveFile(name string) measured {
	staged := StagedFile{Role: RoleArchive, Name: name, Token: ""}

	return measured{
		StagedFile: staged,
		key:        "", sum: "", size: 0, md5: "", bytes: nil,
		facts: unpackFacts{h1: "", protocols: nil}, unpackErr: nil,
	}
}

// TestMeasuredRead_keepsNoArchiveBytes reads the archives of a release of
// several platforms one after the other. After each read the measured file
// holds the hash, the size and the unpack facts, and no archive bytes, so the
// publish holds at most the one archive that it reads. It fails when read
// keeps the bytes of an archive.
func TestMeasuredRead_keepsNoArchiveBytes(t *testing.T) {
	t.Parallel()

	files := make([]measured, 0, 3)

	for i := range 3 {
		data := zipOf(t,
			stored("terraform-provider-payments", []byte("binary "+strconv.Itoa(i))),
			stored(manifestName, []byte(manifestBody)),
		)

		f := archiveFile("archive-" + strconv.Itoa(i) + ".zip")
		if err := f.read(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("read archive %d: %v", i, err)
		}

		sum := sha256.Sum256(data)
		if f.sum != hex.EncodeToString(sum[:]) || f.size != int64(len(data)) {
			t.Fatalf("archive %d: got sum %s and size %d, want the values of its bytes", i, f.sum, f.size)
		}

		if f.unpackErr != nil || f.facts.h1 == "" {
			t.Fatalf("archive %d: unpack %v, H1 %q", i, f.unpackErr, f.facts.h1)
		}

		files = append(files, f)
	}

	for _, f := range files {
		if f.bytes != nil {
			t.Fatalf("archive %s keeps %d bytes after its read", f.Name, len(f.bytes))
		}
	}
}

// TestMeasuredRead_keepsSumsBytes checks that the checksums file keeps its
// bytes: the signature check reads them after the measure step.
func TestMeasuredRead_keepsSumsBytes(t *testing.T) {
	t.Parallel()

	body := []byte("0123  archive.zip\n")

	f := archiveFile("SHA256SUMS")
	f.Role = RoleSums

	if err := f.read(bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("read the checksums file: %v", err)
	}

	if !bytes.Equal(f.bytes, body) {
		t.Fatalf("checksums file bytes: got %q, want %q", f.bytes, body)
	}
}

// TestMeasuredRead_bound checks that read stops past the limit of the role
// with ErrTooLarge, and keeps no hash and no unpack facts. It fails when the
// bound of the read is removed.
func TestMeasuredRead_bound(t *testing.T) {
	t.Parallel()

	data := zipOf(t,
		stored("terraform-provider-payments", make([]byte, 4096)),
		stored(manifestName, []byte(manifestBody)),
	)

	f := archiveFile("big.zip")

	err := f.read(bytes.NewReader(data), int64(len(data))-1)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("read past the limit: got %v, want %v", err, ErrTooLarge)
	}

	if f.sum != "" || f.size != 0 || f.facts.h1 != "" || f.bytes != nil {
		t.Fatalf("read past the limit kept a result: sum %q, size %d, H1 %q", f.sum, f.size, f.facts.h1)
	}
}

// TestMeasuredRead_refusesNonZipBeforeHash checks that read refuses an
// archive that is not a zip file with registry.ErrBadArchive and keeps no
// hash, so no hash check and no signature check sees it. It fails when the
// zip check of read is removed.
func TestMeasuredRead_refusesNonZipBeforeHash(t *testing.T) {
	t.Parallel()

	body := []byte("this is not a zip file")

	f := archiveFile("not-a-zip.zip")

	err := f.read(bytes.NewReader(body), int64(len(body)))
	if !errors.Is(err, registry.ErrBadArchive) {
		t.Fatalf("read a non-zip archive: got %v, want %v", err, registry.ErrBadArchive)
	}

	if f.sum != "" || f.size != 0 {
		t.Fatalf("read a non-zip archive kept the hash %q and the size %d", f.sum, f.size)
	}
}
