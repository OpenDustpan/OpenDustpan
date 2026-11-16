package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	// maxManifestBytes is the most bytes of terraform-registry-manifest.json
	// that the unpack reads.
	maxManifestBytes = 1 << 20
	// manifestName is the entry of a provider archive that holds the plugin
	// protocol versions.
	manifestName = "terraform-registry-manifest.json"
)

// unpackFacts holds what the bounded unpack of a provider archive found: the
// H1 digest of the unpacked files and the manifest protocols.
type unpackFacts struct {
	h1        string
	protocols []string
}

// unpacked holds the checked entries of an archive: the sorted file names,
// the SHA-256 of the real bytes of each file and the manifest bytes.
type unpacked struct {
	names         []string
	hashes        map[string][]byte
	manifestBytes []byte
}

// unpackProviderArchive opens data as a zip archive with the unpack bounds
// and returns the H1 digest over the sorted unpacked file entries and the
// protocols of the manifest. Every entry comes from outside the process. It
// returns registry.ErrBadArchive for an unsafe entry or a limit past the
// bound, and registry.ErrInvalid for a missing, empty or unknown protocol
// list.
func unpackProviderArchive(data []byte) (unpackFacts, error) {
	var none unpackFacts

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return none, fmt.Errorf("%w: the archive is not a zip file", registry.ErrBadArchive)
	}

	entries, err := collectEntries(r)
	if err != nil {
		return none, err
	}

	protocols, err := manifestProtocols(entries.manifestBytes)
	if err != nil {
		return none, err
	}

	return unpackFacts{h1: hashEntries(entries.names, entries.hashes), protocols: protocols}, nil
}

// collectEntries checks every entry of the archive with the unpack bounds
// and returns the sorted file names, their hashes and the manifest bytes.
func collectEntries(r *zip.Reader) (unpacked, error) {
	collector := collector{
		names:         nil,
		hashes:        map[string][]byte{},
		manifestBytes: nil,
		seen:          map[string]bool{},
		totalReal:     0,
		totalCompress: 0,
	}

	for _, f := range r.File {
		if err := collector.add(f); err != nil {
			return unpacked{}, err
		}
	}

	if collector.manifestBytes == nil {
		return unpacked{}, fmt.Errorf(
			"%w: the archive holds no %s: state the protocols of the manifest",
			registry.ErrInvalid,
			manifestName,
		)
	}

	return unpacked{names: collector.names, hashes: collector.hashes, manifestBytes: collector.manifestBytes}, nil
}

// collector gathers the checked entries of one archive with the running
// totals of the ratio bound.
type collector struct {
	names         []string
	hashes        map[string][]byte
	manifestBytes []byte
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

	read, err := readFileEntry(f, entry.name)
	if err != nil {
		return err
	}

	c.seen[entry.folded] = true
	c.names = append(c.names, entry.name)
	c.hashes[entry.name] = read.sum

	if len(c.names) > maxUnpackedFiles {
		return fmt.Errorf("%w: more than %d files", registry.ErrBadArchive, maxUnpackedFiles)
	}

	return c.observe(entry, read)
}

// observe counts one entry toward the total bounds and keeps the manifest.
func (c *collector) observe(entry checkedEntry, read readEntry) error {
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

	if entry.isManifest {
		if c.manifestBytes != nil {
			return fmt.Errorf("%w: duplicate entry %q", registry.ErrBadArchive, manifestName)
		}

		c.manifestBytes = read.held
	}

	return nil
}

// checkedEntry is one zip entry after the name and type checks.
type checkedEntry struct {
	name       string
	folded     string
	isManifest bool
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

	return checkedEntry{name: name, folded: folded, isManifest: name == manifestName}, false, nil
}

// readEntry holds the checked bytes of one file entry: the SHA-256 and the
// size of the real bytes, the compressed size, and the bytes when the entry
// is the manifest.
type readEntry struct {
	sum        []byte
	size       int64
	compressed int64
	held       []byte
}

// readFileEntry reads the real bytes of one file entry with the per-file
// bound. It counts real bytes, never the header sizes, so a lying header
// cannot pass a limit.
func readFileEntry(f *zip.File, name string) (readEntry, error) {
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

	out := readEntry{sum: hash.Sum(nil), size: size, compressed: compressed, held: nil}

	if name != manifestName {
		return out, nil
	}

	if size > maxManifestBytes {
		return none, fmt.Errorf(
			"%w: entry %q is over %d bytes",
			registry.ErrBadArchive,
			f.Name,
			maxManifestBytes,
		)
	}

	held, err := readAllEntry(f, maxManifestBytes+1)
	if err != nil {
		return none, fmt.Errorf("%w: entry %q cannot be read", registry.ErrBadArchive, f.Name)
	}

	out.held = held

	return out, nil
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

// readAllEntry returns the bytes of one entry, bounded.
func readAllEntry(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open entry: %w", err)
	}

	held, err := io.ReadAll(io.LimitReader(r, limit))
	closeErr := r.Close()

	if err == nil {
		err = closeErr
	}

	if err != nil {
		return nil, err
	}

	return held, nil
}

// manifestProtocols decodes the manifest bytes and returns its protocol
// versions. The manifest names its list protocol_versions, so the decode
// reads the document without a struct. Malformed JSON is a bad archive; a
// missing, empty or unknown protocol list is invalid.
func manifestProtocols(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))

	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return nil, fmt.Errorf("%w: the manifest is not JSON", registry.ErrBadArchive)
	}

	if dec.More() {
		return nil, fmt.Errorf("%w: the manifest holds more than one value", registry.ErrBadArchive)
	}

	var meta map[string]json.RawMessage
	if err := json.Unmarshal(top["metadata"], &meta); err != nil {
		return nil, fmt.Errorf("%w: the manifest has no metadata", registry.ErrInvalid)
	}

	var protocols []string
	if err := json.Unmarshal(meta["protocol_versions"], &protocols); err != nil {
		return nil, fmt.Errorf("%w: the manifest lists no protocol", registry.ErrInvalid)
	}

	if len(protocols) == 0 {
		return nil, fmt.Errorf("%w: the manifest lists no protocol", registry.ErrInvalid)
	}

	known := []string{"5.0", "6.0"}

	for _, p := range protocols {
		if !slices.Contains(known, p) {
			return nil, fmt.Errorf("%w: protocol %q is not known", registry.ErrInvalid, p)
		}
	}

	return protocols, nil
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
