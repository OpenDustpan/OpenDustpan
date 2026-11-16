package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/registry"
)

// Bounds of the unpack of a provider archive. The archive is untrusted, so
// the unpack stops past each bound and refuses the publish.
const (
	// maxUnpackedBytes is the most real decompressed bytes of one archive.
	maxUnpackedBytes = 64 << 20
	// maxUnpackedFiles is the most files of one archive.
	maxUnpackedFiles = 1024
	// maxUnpackedFileBytes is the most real decompressed bytes of one file.
	maxUnpackedFileBytes = 16 << 20
	// maxUnpackRatio is the largest expansion of compressed size that the
	// unpack allows, counted on real bytes.
	maxUnpackRatio = 100
)

// unpackFacts holds what the bounded unpack of a provider archive found: the
// H1 digest of the unpacked files.
type unpackFacts struct {
	h1 string
}

// unpacked holds the checked entries of an archive: the sorted file names and
// the SHA-256 of the real bytes of each file.
type unpacked struct {
	names  []string
	hashes map[string][]byte
}

// requireZip returns registry.ErrBadArchive when data is not a zip file that
// the zip reader can open. It reads the central directory only: the unpack
// checks the entries later. The bytes come from outside the process.
func requireZip(name string, data []byte) error {
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		return fmt.Errorf("%w: %s is not a zip file", registry.ErrBadArchive, name)
	}

	return nil
}

// unpackProviderArchive opens data as a zip archive with the unpack bounds
// and returns the H1 digest over the sorted unpacked file entries. Every entry
// comes from outside the process. It reads no manifest: an entry with the name
// of the manifest is a file like the others, and only the manifest file of the
// request gives the protocols. It returns registry.ErrBadArchive for an unsafe
// entry or a limit past the bound.
func unpackProviderArchive(data []byte) (unpackFacts, error) {
	var none unpackFacts

	// Defense in depth: the measure step refuses an archive that is not a zip
	// file before the hash. This check stays so that a new caller of the
	// unpack cannot skip it. It does not rely on the measure step.
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return none, fmt.Errorf("%w: the archive is not a zip file", registry.ErrBadArchive)
	}

	entries, err := collectEntries(r)
	if err != nil {
		return none, err
	}

	return unpackFacts{h1: hashEntries(entries.names, entries.hashes)}, nil
}

// collectEntries checks every entry of the archive with the unpack bounds
// and returns the sorted file names and their hashes.
func collectEntries(r *zip.Reader) (unpacked, error) {
	collector := collector{
		names:         nil,
		hashes:        map[string][]byte{},
		seen:          map[string]bool{},
		totalReal:     0,
		totalCompress: 0,
	}

	for _, f := range r.File {
		if err := collector.add(f); err != nil {
			return unpacked{}, err
		}
	}

	return unpacked{names: collector.names, hashes: collector.hashes}, nil
}

// collector gathers the checked entries of one archive with the running
// totals of the ratio bound.
type collector struct {
	names         []string
	hashes        map[string][]byte
	seen          map[string]bool
	totalReal     int64
	totalCompress int64
}

// add checks one entry and reads its bytes into the collector.
func (c *collector) add(f *zip.File) error {
	entry, skip, err := checkEntry(f, c.seen)
	if err != nil {
		return err
	}

	if skip {
		return nil
	}

	read, err := readFileEntry(f)
	if err != nil {
		return err
	}

	c.seen[entry.folded] = true
	c.names = append(c.names, entry.name)
	c.hashes[entry.name] = read.sum

	if len(c.names) > maxUnpackedFiles {
		return fmt.Errorf("%w: more than %d files", registry.ErrBadArchive, maxUnpackedFiles)
	}

	return c.observe(read)
}

// observe counts one entry toward the total bounds.
func (c *collector) observe(read readEntry) error {
	c.totalReal += read.size
	c.totalCompress += read.compressed

	if c.totalReal > maxUnpackedBytes {
		return fmt.Errorf("%w: more than %d unpacked bytes", registry.ErrBadArchive, maxUnpackedBytes)
	}

	// Defense in depth: a zero compressed count with real bytes is refused
	// here too, so the ratio fails closed.
	if c.totalReal > maxUnpackRatio*c.totalCompress {
		return fmt.Errorf(
			"%w: expansion of more than %d times the compressed size",
			registry.ErrBadArchive,
			maxUnpackRatio,
		)
	}

	return nil
}

// checkedEntry is one zip entry after the name and type checks.
type checkedEntry struct {
	name   string
	folded string
}

// checkEntry cleans the name of a zip entry and reports whether the unpack
// skips it. It refuses an absolute path, a dot-dot segment and a symlink, so
// no entry leaves the staging directory.
func checkEntry(f *zip.File, seen map[string]bool) (checkedEntry, bool, error) {
	var none checkedEntry

	if f.Name == "" {
		return none, false, fmt.Errorf("%w: the archive holds an empty entry name", registry.ErrBadArchive)
	}

	if strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, `\`) {
		return none, false, fmt.Errorf("%w: entry %q is absolute", registry.ErrBadArchive, f.Name)
	}

	// The H1 digest writes one line of hash and name for each file. A name
	// with a newline could forge the line of another file, and a backslash
	// is a separator on some systems. Only the characters of the allowlist
	// pass.
	if !allowedEntryName(f.Name) {
		return none, false, fmt.Errorf(
			"%w: entry %q holds a character that is not allowed",
			registry.ErrBadArchive,
			f.Name,
		)
	}

	for part := range strings.SplitSeq(f.Name, "/") {
		if part == ".." {
			return none, false, fmt.Errorf("%w: entry %q leaves the directory", registry.ErrBadArchive, f.Name)
		}
	}

	if strings.HasSuffix(f.Name, "/") {
		return none, true, nil
	}

	if f.FileInfo().Mode()&os.ModeSymlink != 0 {
		return none, false, fmt.Errorf("%w: entry %q is a symlink", registry.ErrBadArchive, f.Name)
	}

	name := path.Clean(f.Name)
	folded := strings.ToLower(name)

	if seen[folded] {
		return none, false, fmt.Errorf("%w: duplicate entry %q", registry.ErrBadArchive, f.Name)
	}

	return checkedEntry{name: name, folded: folded}, false, nil
}

// allowedEntryName reports whether every character of name is an ASCII letter,
// an ASCII digit or one of the characters "._-+/@~=,". It refuses a control
// character, a space, a backslash and every character outside ASCII.
func allowedEntryName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-+/@~=,", r):
		default:
			return false
		}
	}

	return true
}

// readEntry holds the checked bytes of one file entry: the SHA-256 and the
// size of the real bytes, and the compressed size.
type readEntry struct {
	sum        []byte
	size       int64
	compressed int64
}

// readFileEntry reads the real bytes of one file entry with the per-file
// bound. It counts real bytes, never the header sizes, so a lying header
// cannot pass a limit.
func readFileEntry(f *zip.File) (readEntry, error) {
	var none readEntry

	if f.CompressedSize64 > math.MaxInt64 {
		return none, fmt.Errorf("%w: entry %q is over the size bound", registry.ErrBadArchive, f.Name)
	}

	r, err := f.Open()
	if err != nil {
		return none, fmt.Errorf("%w: entry %q cannot be read", registry.ErrBadArchive, f.Name)
	}

	hash := sha256.New()
	limited := io.LimitReader(r, maxUnpackedFileBytes+1)

	size, err := io.Copy(hash, limited)
	closeErr := r.Close()

	if err == nil {
		err = closeErr
	}

	if err != nil {
		return none, fmt.Errorf("%w: entry %q cannot be read", registry.ErrBadArchive, f.Name)
	}

	if size > maxUnpackedFileBytes {
		return none, fmt.Errorf("%w: entry %q is over %d bytes", registry.ErrBadArchive, f.Name, maxUnpackedFileBytes)
	}

	compressed, err := rawCompressedBytes(f, size)
	if err != nil {
		return none, fmt.Errorf("%w: entry %q cannot be read", registry.ErrBadArchive, f.Name)
	}

	return readEntry{sum: hash.Sum(nil), size: size, compressed: compressed}, nil
}

// byteCounter is a writer that counts the bytes written to it.
type byteCounter struct {
	n int64
}

func (c *byteCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))

	return len(p), nil
}

// rawCompressedBytes counts the compressed bytes of one entry that the
// decoder consumes. It does not use the header size, and it does not use the
// length of the raw reader: both come from the publisher's claim, and a claim
// that is larger than the real stream makes the raw reader run on into the
// next entries. The count is the bytes that the decoder takes from the raw
// reader, so a lying header cannot change the ratio. A stored entry has as
// many compressed bytes as real bytes.
func rawCompressedBytes(f *zip.File, realSize int64) (int64, error) {
	switch f.Method {
	case zip.Store:
		return realSize, nil
	case zip.Deflate:
		raw, err := f.OpenRaw()
		if err != nil {
			return 0, fmt.Errorf("open raw entry: %w", err)
		}

		return deflateConsumed(raw)
	default:
		return 0, fmt.Errorf("%w: compression method %d is not supported", registry.ErrBadArchive, f.Method)
	}
}

// deflateConsumed runs a deflate decoder over raw and returns the bytes that
// the decoder consumed. A bufio.Reader gives the decoder a byte reader, so the
// decoder does not read ahead on its own. The bytes that bufio holds but the
// decoder did not take are subtracted. The output is bounded.
func deflateConsumed(raw io.Reader) (int64, error) {
	counted := &byteCounter{n: 0}
	buffered := bufio.NewReader(io.TeeReader(raw, counted))

	dec := flate.NewReader(buffered)

	_, err := io.Copy(io.Discard, io.LimitReader(dec, maxUnpackedFileBytes+1))
	closeErr := dec.Close()

	if err == nil {
		err = closeErr
	}

	if err != nil {
		return 0, fmt.Errorf("count raw entry: %w", err)
	}

	return counted.n - int64(buffered.Buffered()), nil
}

// hashEntries returns the H1 digest over the sorted unpacked file entries:
// the dirhash h1 algorithm, one line of hex SHA-256 and name per file.
func hashEntries(names []string, hashes map[string][]byte) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)

	h := sha256.New()

	for _, name := range sorted {
		h.Write([]byte(hex.EncodeToString(hashes[name]) + "  " + name + "\n"))
	}

	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
