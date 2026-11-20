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
	archiveTotalLimit = 1 << 30
)

// verifyArchiveSum is the checksum step: the archive must match the line of its
// own name in SHA256SUMS. It reads the file as untrusted data. It refuses a
// malformed line (no two-space separator), a missing line, a second line for
// the same name, a line with a bad hash and a hash that differs. Defense in
// depth: the unpack step does not rely on it, and it does not rely on the
// unpack step.
func verifyArchiveSum(sums []byte, name string, archive []byte) error {
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])

	lines := 0

	var failure error

	for line := range strings.SplitSeq(string(sums), "\n") {
		if line == "" {
			continue
		}

		hash, file, found := strings.Cut(line, "  ")
		if !found {
			return fmt.Errorf("%w: SHA256SUMS holds a line with no two-space separator", errChecksum)
		}

		if file != name {
			continue
		}

		lines++

		if hash != got {
			failure = fmt.Errorf("%w: the line of %s holds a hash that does not match the archive", errChecksum, name)
		}
	}

	switch {
	case lines == 0:
		return fmt.Errorf("%w: SHA256SUMS has no line for %s", errChecksum, name)
	case lines > 1:
		return fmt.Errorf("%w: SHA256SUMS has %d lines for %s, want 1", errChecksum, lines, name)
	default:
		return failure
	}
}

// archiveNameAbsolute reports whether the name starts with a slash.
//
// Defense in depth: the part check of archiveEntryAllowed also refuses a
// leading slash, because the first part is empty. This check stays so that a
// change of that loop cannot let an absolute path pass. It does not rely on
// the part check or on the byte check.
func archiveNameAbsolute(name string) bool {
	return strings.HasPrefix(name, "/")
}

// archiveNameHasSeparatorByte reports whether the name holds a backslash or a
// NUL byte.
//
// Defense in depth: the byte check of archiveEntryAllowed also refuses both
// bytes, because neither is in its set. This check stays so that a change of
// that set cannot let them pass. It does not rely on the byte check.
func archiveNameHasSeparatorByte(name string) bool {
	return strings.ContainsAny(name, "\\\x00")
}

// archivePartAllowed reports whether one part of a name is valid: not empty,
// not a single or double dot, and made only of the bytes A-Z a-z 0-9 . _ ~ + -.
// The set has no byte that a file system folds, so a name has one spelling on
// a file system that ignores case or Unicode form.
func archivePartAllowed(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}

	for i := range len(part) {
		c := part[i]

		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '~', c == '+', c == '-':
		default:
			return false
		}
	}

	return true
}

// archiveEntryAllowed is the allowlist of the unpack step. The archive holds
// the binary, two files and the directory of the licenses. An entry that is
// not in this list is refused: an absolute path, a part that is empty or is a
// single or double dot, a part with a byte outside the set of
// archivePartAllowed, and any other name.
func archiveEntryAllowed(name string) bool {
	trimmed := strings.TrimSuffix(name, "/")

	if trimmed == "" || archiveNameAbsolute(trimmed) || archiveNameHasSeparatorByte(trimmed) {
		return false
	}

	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if !archivePartAllowed(part) {
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

// noteArchiveName records the name of an entry and refuses a name that the
// archive already used. The key is the cleaned name in lower case, so that a
// name is the same with and without a trailing slash, with a doubled slash or
// a leading dot part, and on a file system that ignores case.
func noteArchiveName(seen map[string]struct{}, name string) error {
	key := strings.ToLower(path.Clean(strings.TrimSuffix(name, "/")))
	if _, dup := seen[key]; dup {
		return fmt.Errorf("%w: entry %q appears more than once", errUnpack, name)
	}

	seen[key] = struct{}{}

	return nil
}

// admitArchiveEntry applies the allowlist and the seen-name check to one name.
func admitArchiveEntry(seen map[string]struct{}, name string) error {
	if !archiveEntryAllowed(name) {
		return fmt.Errorf("%w: entry %q is outside the allowlist", errUnpack, name)
	}

	return noteArchiveName(seen, name)
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

	// Defense in depth: unpackArchiveLimited refuses a repeated name before it
	// calls this function. O_EXCL stays so that a caller that skips that check
	// still cannot replace a file. It does not rely on that check.
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
	return unpackArchiveLimited(root, data, archiveFileLimit, archiveTotalLimit)
}

// unpackArchiveLimited is the unpack step with its two bounds as arguments:
// the most entries and the most bytes of all files together. A test passes
// small bounds, so that it needs no large archive.
func unpackArchiveLimited(root *os.Root, data []byte, maxEntries int, maxTotal int64) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: gzip: %w", errUnpack, err)
	}

	tr := tar.NewReader(zr)

	var total int64

	seen := make(map[string]struct{})

	for count := 0; ; count++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("%w: tar: %w", errUnpack, err)
		}

		if count >= maxEntries {
			return fmt.Errorf("%w: more than %d entries", errUnpack, maxEntries)
		}

		total += max(hdr.Size, 0)
		if total > maxTotal {
			return fmt.Errorf("%w: files hold more than %d bytes", errUnpack, maxTotal)
		}

		if err := admitArchiveEntry(seen, hdr.Name); err != nil {
			return err
		}

		if err := writeArchiveEntry(root, hdr, tr); err != nil {
			return err
		}
	}
}
