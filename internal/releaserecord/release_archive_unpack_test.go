package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

var (
	errChecksum = errors.New("checksum step refused the archive")
	errUnpack   = errors.New("unpack step refused the archive")
)

const (
	archiveEntryLimit = 512 << 20
	archiveFileLimit  = 4096
)

// verifyArchiveSum is the checksum step: the archive must match the line of its
// own name in SHA256SUMS. It reads the file as untrusted data. It refuses a
// missing line, a line with a bad hash and a hash that differs. Defense in
// depth: the unpack step does not rely on it, and it does not rely on the
// unpack step.
func verifyArchiveSum(sums []byte, name string, archive []byte) error {
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])

	for line := range strings.SplitSeq(string(sums), "\n") {
		hash, file, found := strings.Cut(line, "  ")
		if !found || file != name {
			continue
		}

		if len(hash) != 64 {
			return fmt.Errorf("%w: the line of %s has a hash of %d characters, want 64", errChecksum, name, len(hash))
		}

		if hash != got {
			return fmt.Errorf("%w: the checksum of %s does not match its line", errChecksum, name)
		}

		return nil
	}

	return fmt.Errorf("%w: SHA256SUMS has no line for %s", errChecksum, name)
}

// archiveEntryAllowed is the allowlist of the unpack step. The archive holds
// the binary, two files and the directory of the licenses. An entry that is
// not in this list is refused: an absolute path, a part that is empty or is a
// single or double dot, a backslash and any other name.
func archiveEntryAllowed(name string) bool {
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, "\\\x00") {
		return false
	}

	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}

	switch parts[0] {
	case "opendustpan", "LICENSE", "README.md":
		return len(parts) == 1
	case "third_party_licenses":
		return true
	default:
		return false
	}
}

// writeArchiveEntry writes one regular file or directory below root. Defense
// in depth: os.Root refuses a name that leaves the directory, a link and a
// path through a link, even when archiveEntryAllowed lets the name pass.
func writeArchiveEntry(root *os.Root, hdr *tar.Header, body io.Reader) error {
	name := strings.TrimSuffix(hdr.Name, "/")

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(name, 0o755); err != nil {
			return fmt.Errorf("%w: make directory %q: %w", errUnpack, hdr.Name, err)
		}

		return nil
	case tar.TypeReg:
		return writeArchiveFile(root, name, hdr, body)
	default:
		return fmt.Errorf(
			"%w: entry %q has type %q, only files and directories are allowed",
			errUnpack,
			hdr.Name,
			hdr.Typeflag,
		)
	}
}

// writeArchiveFile writes one regular file. It never replaces a file.
func writeArchiveFile(root *os.Root, name string, hdr *tar.Header, body io.Reader) error {
	if hdr.Size < 0 || hdr.Size > archiveEntryLimit {
		return fmt.Errorf("%w: entry %q has size %d", errUnpack, hdr.Name, hdr.Size)
	}

	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return fmt.Errorf("%w: make directory of %q: %w", errUnpack, hdr.Name, err)
	}

	mode := os.FileMode(0o644)
	if name == "opendustpan" {
		mode = 0o755
	}

	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("%w: open %q: %w", errUnpack, hdr.Name, err)
	}

	_, copyErr := io.CopyN(file, body, hdr.Size)
	if closeErr := file.Close(); copyErr != nil || closeErr != nil {
		return fmt.Errorf("%w: write %q: %w", errUnpack, hdr.Name, errors.Join(copyErr, closeErr))
	}

	return nil
}

// unpackArchive is the unpack step. It reads the archive as untrusted data and
// refuses each entry that is outside the allowlist, before it writes anything
// for that entry. It does not rely on the checksum step.
func unpackArchive(root *os.Root, data []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: gzip: %w", errUnpack, err)
	}

	tr := tar.NewReader(zr)

	for count := 0; ; count++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("%w: tar: %w", errUnpack, err)
		}

		if count >= archiveFileLimit {
			return fmt.Errorf("%w: more than %d entries", errUnpack, archiveFileLimit)
		}

		if !archiveEntryAllowed(hdr.Name) {
			return fmt.Errorf("%w: entry %q is outside the allowlist", errUnpack, hdr.Name)
		}

		if err := writeArchiveEntry(root, hdr, tr); err != nil {
			return err
		}
	}
}
