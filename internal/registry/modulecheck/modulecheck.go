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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
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

// add counts one entry and refuses at the limit.
func (b *budget) add(name string) error {
	b.entries++
	if b.entries > b.limits.Entries {
		return refuse("more than %d entries", b.limits.Entries)
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

// checkName refuses a path that can leave the directory where a client unpacks
// the archive. The rules use an allowlist of what a path may hold.
func checkName(name string) error {
	switch {
	case name == "" || len(name) > MaxPathBytes:
		return refuse("entry path is empty or longer than %d bytes", MaxPathBytes)
	case strings.HasPrefix(name, "/"):
		return refuse("entry %q has an absolute path", name)
	case strings.Contains(name, "\\"):
		return refuse("entry %q has a backslash in its path", name)
	case len(name) >= 2 && name[1] == ':':
		return refuse("entry %q has a drive in its path", name)
	}

	for _, c := range []byte(name) {
		if c < 0x20 || c == 0x7f {
			return refuse("entry %q has a control character in its path", name)
		}
	}

	if slices.Contains(strings.Split(name, "/"), "..") {
		return refuse("entry %q leaves the archive with ..", name)
	}

	return nil
}

// readZip reads every entry of a zip archive.
func readZip(data []byte, limits Limits) ([]entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, refuse("the zip archive cannot be read: %v", err)
	}

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
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if limited.N <= 0 {
			return nil, refuse("the archive unpacks to more than %d bytes", limits.UnpackedBytes)
		}

		if err != nil {
			return nil, refuse("the tar stream cannot be read: %v", err)
		}

		e, entryErr := readTarEntry(&b, tr, hdr)
		if limited.N <= 0 {
			return nil, refuse("the archive unpacks to more than %d bytes", limits.UnpackedBytes)
		}

		if entryErr != nil {
			return nil, entryErr
		}

		out = append(out, e)
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

// checkTree checks the paths together: no path twice, no link that points
// outside the archive and no entry below a link.
func checkTree(entries []entry) error {
	seen := make(map[string]bool, len(entries))
	links := map[string]bool{}

	for _, e := range entries {
		clean := path.Clean(e.name)
		// Two paths that differ only in case are one path on a file system that
		// folds case.
		key := strings.ToLower(clean)

		if seen[key] {
			return refuse("entry %q appears twice", e.name)
		}

		seen[key] = true

		if e.target == "" {
			continue
		}

		if err := checkLink(clean, e.target); err != nil {
			return err
		}

		links[key] = true
	}

	// An entry below a link would be written through the link. A target that
	// looks safe as text can then leave the archive, so no entry may have a
	// link for an ancestor.
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

// checkLink refuses a link whose target is not inside the archive. The target
// is relative to the directory of the link.
func checkLink(name, target string) error {
	switch {
	case target == "":
		return refuse("link %q has no target", name)
	case strings.HasPrefix(target, "/") || strings.Contains(target, "\\") || (len(target) >= 2 && target[1] == ':'):
		return refuse("link %q has an absolute target", name)
	}

	for _, c := range []byte(target) {
		if c < 0x20 || c == 0x7f {
			return refuse("link %q has a control character in its target", name)
		}
	}

	joined := path.Join(path.Dir(name), target)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return refuse("link %q points outside the archive", name)
	}

	return nil
}
