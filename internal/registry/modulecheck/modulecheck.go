// Package modulecheck reads the archive of an uploaded module and refuses an
// archive that is not safe to serve. The registry stores the archive as it
// came and a client unpacks it, so this check is the layer that stops a path
// that leaves the archive, a link that points outside it and an archive that
// unpacks to more than the limits allow.
//
// The check reads every entry and counts the bytes that the entry really
// holds. It never trusts a size in a header. It writes nothing to disk and
// never creates a link.
package modulecheck

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"strings"
)

// Limits of a module archive.
const (
	// MaxUnpackedBytes is the most bytes that the entries of an archive hold
	// when unpacked. It is a limit against a zip bomb.
	MaxUnpackedBytes int64 = 1 << 30
	// MaxEntries is the most entries (files, directories and links) of an archive.
	MaxEntries = 20000
	// MaxPathBytes is the longest path of an entry.
	MaxPathBytes = 1024
	// MaxLinkTargetBytes is the longest target of a symbolic link.
	MaxLinkTargetBytes = 1024
)

// Format is the archive format of a module.
type Format string

// The formats that the registry takes. The extension is the one that a client
// uses to choose how to unpack the file.
const (
	FormatZip   Format = "zip"
	FormatTarGz Format = "tar.gz"
)

// Extension returns the file name extension of f, without the dot.
func (f Format) Extension() string { return string(f) }

// ErrRefused reports an archive that the check refuses. The text names the
// entry and the rule, and holds no byte of the content.
var ErrRefused = errors.New("module archive refused")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Detect returns the format of data from its first bytes. It does not trust a
// file name. It returns ErrRefused for any other format.
func Detect(data []byte) (Format, error) {
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return FormatZip, nil
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		return FormatTarGz, nil
	default:
		return "", refuse("the archive is not a zip file and not a tar.gz file")
	}
}

// Limits bound the size of an archive when it is unpacked.
type Limits struct {
	// UnpackedBytes is the most bytes that the entries hold.
	UnpackedBytes int64
	// Entries is the most entries.
	Entries int
}

// DefaultLimits returns the limits of the registry.
func DefaultLimits() Limits {
	return Limits{UnpackedBytes: MaxUnpackedBytes, Entries: MaxEntries}
}

// Check reads the archive in data with the default limits. See CheckWith.
func Check(data []byte) (Format, error) {
	return CheckWith(data, DefaultLimits())
}

// CheckWith reads the archive in data and returns its format. It returns
// ErrRefused when an entry has an unsafe path, when a link points outside the
// archive or is written through another link, when the archive holds a special
// file or a hard link, when two entries have one path, or when a limit is
// passed.
func CheckWith(data []byte, limits Limits) (Format, error) {
	format, err := Detect(data)
	if err != nil {
		return "", err
	}

	entries, err := readEntries(format, data, limits)
	if err != nil {
		return "", err
	}

	if len(entries) == 0 {
		return "", refuse("the archive has no entry")
	}

	if treeErr := checkTree(entries); treeErr != nil {
		return "", treeErr
	}

	return format, nil
}

// readEntries reads every entry of an archive of the given format. It refuses a
// format that it does not know (fail closed).
func readEntries(format Format, data []byte, limits Limits) ([]entry, error) {
	switch format {
	case FormatZip:
		return readZip(data, limits)
	case FormatTarGz:
		return readTarGz(data, limits)
	default:
		return nil, refuse("the format %q is not supported", format)
	}
}

// entry is what the check keeps of one archive entry.
type entry struct {
	name string
	// target is the target of a symbolic link. It is empty for any other entry.
	target string
}

// budget counts the bytes and the entries of one archive.
type budget struct {
	limits  Limits
	bytes   int64
	entries int
}

// count counts one header of the archive and refuses at the limit of entries.
func (b *budget) count() error {
	b.entries++
	if b.entries > b.limits.Entries {
		return refuse("more than %d entries", b.limits.Entries)
	}

	return nil
}

// add counts one entry and checks its path.
func (b *budget) add(name string) error {
	if err := b.count(); err != nil {
		return err
	}

	return checkName(name)
}

// read copies r to dst until its end, and counts what it holds. It stops when
// the total passes the limit.
func (b *budget) read(r io.Reader, name string, dst io.Writer) error {
	left := b.limits.UnpackedBytes - b.bytes

	n, err := io.Copy(dst, io.LimitReader(r, left+1))
	b.bytes += n

	if b.bytes > b.limits.UnpackedBytes {
		return refuse("the archive unpacks to more than %d bytes (at %q)", b.limits.UnpackedBytes, name)
	}

	if err != nil {
		return refuse("entry %q cannot be read: %v", name, err)
	}

	return nil
}

// MaxSegmentBytes is the longest segment of a path.
const MaxSegmentBytes = 255

// asciiLimit is the first byte that is not ASCII.
const asciiLimit = 0x80

// nameSymbols are the characters, besides ASCII letters and digits, that a
// path of a module may hold. Every other character is refused: a space, a
// colon (a drive or an alternate stream), a backslash, a control character and
// every byte above 0x7f. A path with only ASCII has no second form in Unicode
// normalization, so two paths cannot differ by their normal form.
const nameSymbols = "-_.+@=,"

// isDeviceName reports whether name, in upper case and without an extension,
// is a name that Windows opens as a device: CON, PRN, AUX, NUL, COM1 to COM9
// and LPT1 to LPT9.
func isDeviceName(name string) bool {
	switch name {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}

	if len(name) != len("COM1") {
		return false
	}

	return (name[:3] == "COM" || name[:3] == "LPT") && name[3] >= '1' && name[3] <= '9'
}

// checkName refuses a path that can leave the directory where a client unpacks
// the archive, or that names a file in a way that one platform reads as
// another. The rules are an allowlist: a path is valid only when each segment
// passes checkSegment. One slash at the end marks a directory.
func checkName(name string) error {
	if name == "" || len(name) > MaxPathBytes {
		return refuse("entry path is empty or longer than %d bytes", MaxPathBytes)
	}

	if strings.HasPrefix(name, "/") {
		return refuse("entry %q has an absolute path", name)
	}

	for seg := range strings.SplitSeq(strings.TrimSuffix(name, "/"), "/") {
		switch seg {
		case ".":
		case "..":
			return refuse("entry %q leaves the archive with ..", name)
		default:
			if err := checkSegment(seg); err != nil {
				return refuse("entry %q: %v", name, err)
			}
		}
	}

	return nil
}

// segmentError is the reason that checkSegment refuses a segment.
type segmentError string

func (e segmentError) Error() string { return string(e) }

// checkSegment refuses a segment of a path that is not on the allowlist. The
// segment is not "." and not "..": the callers handle those two.
func checkSegment(seg string) error {
	if seg == "" || len(seg) > MaxSegmentBytes {
		return segmentError("a path segment is empty or longer than 255 bytes")
	}

	for i := range len(seg) {
		if err := checkSegmentByte(seg[i]); err != nil {
			return err
		}
	}

	if strings.HasSuffix(seg, ".") {
		return segmentError("a path segment ends with a dot")
	}

	base, _, _ := strings.Cut(seg, ".")
	if isDeviceName(strings.ToUpper(base)) {
		return segmentError("a path segment has the name of a device on Windows")
	}

	return nil
}

// checkSegmentByte refuses a byte that is not an ASCII letter, a digit or one
// of nameSymbols. The reason names the kind of byte.
func checkSegmentByte(c byte) error {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(nameSymbols, c) >= 0:
		return nil
	case c == '\\':
		return segmentError("a path has a backslash")
	case c == ':':
		return segmentError("a path has a colon")
	case c < 0x20 || c == 0x7f:
		return segmentError("a path has a control character")
	case c >= asciiLimit:
		return segmentError("a path has a byte that is not ASCII")
	default:
		return segmentError("a path has a character that is not allowed")
	}
}

// readZip reads every entry of a zip archive.
func readZip(data []byte, limits Limits) ([]entry, error) {
	// The count in the end-of-central-directory record is checked before the
	// reader builds the directory, so an archive cannot make the server hold a
	// list of entries that is longer than the limit.
	if err := checkZipCount(data, limits); err != nil {
		return nil, err
	}

	guard := &directoryGuard{r: bytes.NewReader(data), left: zipDirectoryBytes(limits)}

	zr, err := zip.NewReader(guard, int64(len(data)))
	if err != nil {
		if errors.Is(err, errDirectoryTooLarge) {
			return nil, refuse(
				"the central directory of the zip archive is larger than %d bytes",
				zipDirectoryBytes(limits),
			)
		}

		return nil, refuse("the zip archive cannot be read: %v", err)
	}

	// The directory is built. The content of the entries is read without the
	// guard: the budget of the unpacked size limits it.
	guard.left = -1

	b := budget{limits: limits, bytes: 0, entries: 0}

	var out []entry

	for _, f := range zr.File {
		if addErr := b.add(f.Name); addErr != nil {
			return nil, addErr
		}

		e := entry{name: f.Name, target: ""}
		mode := f.Mode()

		switch {
		case mode&fs.ModeSymlink != 0:
			e.target, err = readZipLink(&b, f)
		case mode.IsDir() || strings.HasSuffix(f.Name, "/"):
		case mode.IsRegular():
			err = readZipFile(&b, f)
		default:
			err = refuse("entry %q is a special file", f.Name)
		}

		if err != nil {
			return nil, err
		}

		out = append(out, e)
	}

	return out, nil
}

// Sizes in the end-of-central-directory record of a zip archive.
// zipDirectoryExtra is a budget of 1,024 bytes for the extra field and the
// comment of each directory entry. It does not cover every legal directory:
// each of these fields can hold up to 65,535 bytes. A directory that is larger
// than the total budget of all its entries is refused.
const (
	zipEndRecordLen    = 22
	zipMaxCommentLen   = 1<<16 - 1
	zip64LocatorLen    = 20
	zip64EndRecordLen  = 56
	zipMaxCount16      = 0xffff
	zipDirectoryHeader = 46
	zipDirectoryExtra  = 1024
)

// zipDirectoryBytes returns the most bytes that the reader may read to build
// the directory. It covers the search for the end record and the longest
// directory that limits.Entries entries can have. This is a second layer: the
// count check works on the number that the archive states, and this limit works
// on the bytes that the reader really reads, however many entries they hold.
func zipDirectoryBytes(limits Limits) int64 {
	perEntry := int64(zipDirectoryHeader + MaxPathBytes + zipDirectoryExtra)

	return int64(zipEndRecordLen+zipMaxCommentLen) + perEntry*int64(limits.Entries)
}

// errDirectoryTooLarge reports that the reader read more than the guard allows.
var errDirectoryTooLarge = errors.New("the central directory is too large")

// directoryGuard is a reader that stops after a number of bytes while left is
// not negative. A negative left turns the guard off.
type directoryGuard struct {
	r    *bytes.Reader
	left int64
}

func (g *directoryGuard) ReadAt(p []byte, off int64) (int, error) {
	if g.left >= 0 {
		g.left -= int64(len(p))
		if g.left < 0 {
			return 0, errDirectoryTooLarge
		}
	}

	n, err := g.r.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("read the zip archive: %w", err)
	}

	return n, nil
}

// checkZipCount reads the number of entries that the end-of-central-directory
// record states, and refuses an archive above the limit. It reads the record in
// the way of the zip reader of Go: the last record signature whose comment
// fits in the file.
func checkZipCount(data []byte, limits Limits) error {
	end := -1

	lowest := max(0, len(data)-zipEndRecordLen-zipMaxCommentLen)
	for i := len(data) - zipEndRecordLen; i >= lowest; i-- {
		if string(data[i:i+4]) == "PK\x05\x06" {
			end = i

			break
		}
	}

	if end < 0 {
		return refuse("the zip archive has no end record")
	}

	count := uint64(binary.LittleEndian.Uint16(data[end+10:]))

	if count == zipMaxCount16 {
		var err error

		if count, err = zip64Count(data, end); err != nil {
			return err
		}
	}

	if count > math.MaxInt32 || int(count) > limits.Entries {
		return refuse("the zip archive states more than %d entries", limits.Entries)
	}

	return nil
}

// zip64Count reads the number of entries from the Zip64 end record that the
// locator before the end record names.
func zip64Count(data []byte, end int) (uint64, error) {
	loc := end - zip64LocatorLen
	if loc < 0 || string(data[loc:loc+4]) != "PK\x06\x07" {
		return 0, refuse("the zip archive states 65535 entries or more and has no Zip64 record")
	}

	offset := binary.LittleEndian.Uint64(data[loc+8:])
	if offset > uint64(len(data)) || uint64(len(data))-offset < zip64EndRecordLen ||
		string(data[offset:offset+4]) != "PK\x06\x06" {
		return 0, refuse("the Zip64 record of the zip archive is not valid")
	}

	return binary.LittleEndian.Uint64(data[offset+32:]), nil
}

// readZipFile counts the real bytes of a regular file.
func readZipFile(b *budget, f *zip.File) error {
	r, err := f.Open()
	if err != nil {
		return refuse("entry %q cannot be opened: %v", f.Name, err)
	}

	readErr := b.read(r, f.Name, io.Discard)
	closeErr := r.Close()

	if readErr != nil {
		return readErr
	}

	if closeErr != nil {
		return refuse("entry %q cannot be read: %v", f.Name, closeErr)
	}

	return nil
}

// readZipLink returns the target of a symbolic link of a zip archive.
func readZipLink(b *budget, f *zip.File) (string, error) {
	if f.UncompressedSize64 > MaxLinkTargetBytes {
		return "", refuse("link %q has a target longer than %d bytes", f.Name, MaxLinkTargetBytes)
	}

	r, err := f.Open()
	if err != nil {
		return "", refuse("entry %q cannot be opened: %v", f.Name, err)
	}

	var target bytes.Buffer

	readErr := b.read(io.LimitReader(r, MaxLinkTargetBytes+1), f.Name, &target)
	closeErr := r.Close()

	if readErr != nil {
		return "", readErr
	}

	if closeErr != nil {
		return "", refuse("entry %q cannot be read: %v", f.Name, closeErr)
	}

	return target.String(), nil
}

// readTarGz reads every entry of a tar archive in a gzip stream. One limit
// covers the whole decompressed stream, headers and padding included.
func readTarGz(data []byte, limits Limits) ([]entry, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, refuse("the gzip stream cannot be read: %v", err)
	}

	entries, readErr := readTar(gz, limits)
	closeErr := gz.Close()

	if readErr != nil {
		return nil, readErr
	}

	if closeErr != nil {
		return nil, refuse("the gzip stream cannot be read: %v", closeErr)
	}

	return entries, nil
}

// readTar reads the entries of the tar stream in r. The stream stops at one
// byte more than MaxUnpackedBytes, so a stream above the limit is seen.
func readTar(r io.Reader, limits Limits) ([]entry, error) {
	limited := &io.LimitedReader{R: r, N: limits.UnpackedBytes + 1}
	tr := tar.NewReader(limited)

	b := budget{limits: limits, bytes: 0, entries: 0}

	var out []entry

	for {
		e, more, err := readTarNext(&b, tr, limited)
		if err != nil {
			return nil, err
		}

		if !more {
			break
		}

		if e.name != "" {
			out = append(out, e)
		}
	}

	// Bytes after the end of the tar stream count too.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return nil, refuse("the gzip stream cannot be read: %v", err)
	}

	if limited.N <= 0 {
		return nil, refuse("the archive unpacks to more than %d bytes", limits.UnpackedBytes)
	}

	return out, nil
}

// readTarNext reads the next header of the stream and the body that follows it.
// It returns more as false at the end of the stream. It returns an entry with
// no name for a header that is metadata.
func readTarNext(b *budget, tr *tar.Reader, limited *io.LimitedReader) (entry, bool, error) {
	var none entry

	hdr, err := tr.Next()
	if errors.Is(err, io.EOF) {
		return none, false, nil
	}

	if limited.N <= 0 {
		return none, false, refuse("the archive unpacks to more than %d bytes", b.limits.UnpackedBytes)
	}

	if err != nil {
		return none, false, refuse("the tar stream cannot be read: %v", err)
	}

	// A pax global header is metadata for the tools that write the archive, for
	// example git archive. The check counts it as an entry and takes only a
	// comment record. Other archive readers apply a path or a link override of
	// a global header to the entries that follow it, and this check does not.
	if hdr.Typeflag == tar.TypeXGlobalHeader {
		for key := range hdr.PAXRecords {
			if key != "comment" {
				return none, false, refuse("the pax global header has the record %q", key)
			}
		}

		return none, true, b.count()
	}

	e, err := readTarEntry(b, tr, hdr)
	if limited.N <= 0 {
		return none, false, refuse("the archive unpacks to more than %d bytes", b.limits.UnpackedBytes)
	}

	return e, err == nil, err
}

// readTarEntry checks one header and counts the bytes of its body.
func readTarEntry(b *budget, tr *tar.Reader, hdr *tar.Header) (entry, error) {
	if err := b.add(hdr.Name); err != nil {
		return entry{}, err
	}

	e := entry{name: hdr.Name, target: ""}

	switch hdr.Typeflag {
	case tar.TypeReg:
		if err := b.read(tr, hdr.Name, io.Discard); err != nil {
			return entry{}, err
		}
	case tar.TypeDir:
	case tar.TypeSymlink:
		if len(hdr.Linkname) > MaxLinkTargetBytes {
			return entry{}, refuse("link %q has a target longer than %d bytes", hdr.Name, MaxLinkTargetBytes)
		}

		e.target = hdr.Linkname
	case tar.TypeLink:
		return entry{}, refuse("entry %q is a hard link: use a symbolic link or a copy", hdr.Name)
	default:
		return entry{}, refuse("entry %q is a special file", hdr.Name)
	}

	return e, nil
}

// checkTree checks the paths together: no path twice, no link whose target
// leaves the archive or passes through a link, and no entry below a link.
func checkTree(entries []entry) error {
	seen := make(map[string]bool, len(entries))
	links := map[string]bool{}

	// A path is compared in lower case. The names hold only ASCII, so the lower
	// case form is the form of a file system that folds case.
	for _, e := range entries {
		key := strings.ToLower(path.Clean(e.name))

		if seen[key] {
			return refuse("entry %q appears twice", e.name)
		}

		seen[key] = true

		if e.target != "" {
			links[key] = true
		}
	}

	for _, e := range entries {
		if e.target == "" {
			continue
		}

		if err := checkLink(path.Clean(e.name), e.target, links); err != nil {
			return err
		}
	}

	return checkBelowLinks(entries, links)
}

// checkBelowLinks refuses an entry that has a link for an ancestor. Such an
// entry would be written through the link, and a target that looks safe as text
// can then leave the archive.
func checkBelowLinks(entries []entry, links map[string]bool) error {
	for _, e := range entries {
		dir := path.Dir(strings.ToLower(path.Clean(e.name)))

		for dir != "." && dir != "/" {
			if links[dir] {
				return refuse("entry %q is below the link %q", e.name, dir)
			}

			dir = path.Dir(dir)
		}
	}

	return nil
}

// checkLink refuses a link whose target is not safe. The target is relative to
// the directory of the link. The check walks the target one segment at a time
// and keeps the path that the walk has reached. It refuses when the walk goes
// above the root of the archive, and when any path that the walk reaches is a
// link: a target that passes through another link, or ends at one, is read
// through that link on a disk, and the text of the target no longer says where
// it goes. A chain of links and a loop of links are refused by the same rule.
func checkLink(name, target string, links map[string]bool) error {
	if target == "" {
		return refuse("link %q has no target", name)
	}

	if strings.HasPrefix(target, "/") {
		return refuse("link %q has an absolute target", name)
	}

	here := path.Dir(name)

	for seg := range strings.SplitSeq(target, "/") {
		switch seg {
		case "", ".":
		case "..":
			if here == "." {
				return refuse("link %q points outside the archive", name)
			}

			here = path.Dir(here)
		default:
			if err := checkSegment(seg); err != nil {
				return refuse("link %q has a target that is not allowed: %v", name, err)
			}

			here = path.Join(here, seg)
			if links[strings.ToLower(here)] {
				return refuse("link %q has a target that passes through the link %q", name, here)
			}
		}
	}

	return nil
}
