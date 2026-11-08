package modulecheck_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/modulecheck"
)

// kind is the kind of an entry of a test archive.
type kind int

const (
	kindFile kind = iota
	kindDir
	kindSymlink
	kindHardlink
	kindSpecial
)

// member is one entry of a test archive. For a link, body holds the target.
type member struct {
	name string
	body string
	kind kind
}

func file(name, body string) member { return member{name: name, body: body, kind: kindFile} }
func dir(name string) member        { return member{name: name, body: "", kind: kindDir} }
func symlink(name, target string) member {
	return member{name: name, body: target, kind: kindSymlink}
}

func hardlink(name, target string) member {
	return member{name: name, body: target, kind: kindHardlink}
}
func special(name string) member { return member{name: name, body: "", kind: kindSpecial} }

func makeZip(t *testing.T, members []member) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for _, m := range members {
		var h zip.FileHeader

		h.Name, h.Method = m.name, zip.Deflate

		switch m.kind {
		case kindSymlink:
			h.SetMode(os.ModeSymlink | 0o777)
		case kindDir:
			h.SetMode(os.ModeDir | 0o755)
		case kindSpecial:
			h.SetMode(os.ModeDevice | 0o600)
		case kindFile, kindHardlink:
			h.SetMode(0o644)
		default:
			t.Fatalf("unknown kind %d", m.kind)
		}

		w, err := zw.CreateHeader(&h)
		if err != nil {
			t.Fatalf("zip header: %v", err)
		}

		if _, err = w.Write([]byte(m.body)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

// tarHeader returns the header of m.
func tarHeader(m member) tar.Header {
	var h tar.Header

	h.Name, h.Mode, h.Typeflag, h.Size = m.name, 0o644, tar.TypeReg, int64(len(m.body))

	flags := map[kind]byte{
		kindSymlink: tar.TypeSymlink, kindHardlink: tar.TypeLink, kindDir: tar.TypeDir, kindSpecial: tar.TypeFifo,
	}

	if flag, ok := flags[m.kind]; ok {
		h.Typeflag, h.Size = flag, 0
	}

	if m.kind == kindSymlink || m.kind == kindHardlink {
		h.Linkname = m.body
	}

	return h
}

func makeTarGz(t *testing.T, members []member) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, m := range members {
		h := tarHeader(m)
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("tar header: %v", err)
		}

		if h.Size > 0 {
			if _, err := tw.Write([]byte(m.body)); err != nil {
				t.Fatalf("tar write: %v", err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return buf.Bytes()
}

// builders make an archive of each format.
func builders() map[string]func(*testing.T, []member) []byte {
	return map[string]func(*testing.T, []member) []byte{"zip": makeZip, "tar.gz": makeTarGz}
}

// wantRefused fails the test unless err refuses the archive with the text want.
func wantRefused(t *testing.T, err error, want string) {
	t.Helper()

	if !errors.Is(err, modulecheck.ErrRefused) || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want ErrRefused with %q", err, want)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	good := []member{file("main.tf", "x"), dir("sub/"), file("sub/a.tf", "y")}

	tests := []struct {
		name    string
		members []member
		refuse  string
	}{
		{"plain module", good, ""},
		{"dot prefix", []member{file("./main.tf", "x")}, ""},
		{"link inside", append(append([]member{}, good...), symlink("sub/l", "../main.tf")), ""},
		{"link to own directory", []member{symlink("a/l", ".")}, ""},
		{"parent segment", []member{file("../evil.tf", "x")}, "leaves the archive"},
		{"inner parent segment", []member{file("a/../../evil.tf", "x")}, "leaves the archive"},
		{"absolute path", []member{file("/etc/passwd", "x")}, "absolute"},
		{"backslash path", []member{file("a\\..\\b.tf", "x")}, "backslash"},
		{"drive path", []member{file("C:evil", "x")}, "colon"},
		{"control character", []member{file("a\x01b", "x")}, "control character"},
		{"link outside", []member{symlink("l", "../outside")}, "outside the archive"},
		{"deep link outside", []member{symlink("a/b/l", "../../../x")}, "outside the archive"},
		{"absolute link", []member{symlink("l", "/etc/passwd")}, "absolute target"},
		{"link with a drive", []member{symlink("l", "C:/x")}, "colon"},
		{"entry below a link", []member{symlink("d", "."), file("d/f.tf", "x")}, "below the link"},
		{"entry below a late link", []member{file("d/f.tf", "x"), symlink("d", ".")}, "below the link"},
		{"same path twice", []member{file("a.tf", "x"), file("a.tf", "y")}, "twice"},
		{"same path in other case", []member{file("a.tf", "x"), file("A.tf", "y")}, "twice"},
		{"special file", []member{special("dev")}, "special file"},
		{
			"link through a link",
			[]member{symlink("a/b/c/l1", "../../.."), symlink("l2", "a/b/c/l1/../etc")},
			"passes through the link",
		},
		{
			"link to a link",
			[]member{file("main.tf", "x"), symlink("l1", "main.tf"), symlink("l2", "l1")},
			"passes through",
		},
		{"link to a directory link", []member{symlink("d", "."), symlink("l", "d/x")}, "passes through"},
		{"loop of links", []member{symlink("a", "b"), symlink("b", "a")}, "passes through"},
		{"link to itself", []member{symlink("l", "l")}, "passes through"},
		{"link to a name above a link", []member{symlink("l", "sub"), symlink("m", "l/..")}, "passes through"},
		{"link in a directory to its parent", []member{file("main.tf", "x"), symlink("sub/up", "..")}, ""},
		{"link with dots", []member{file("main.tf", "x"), symlink("a/b/l", "../b/../../main.tf")}, ""},
		{"link above the root by dots", []member{symlink("a/l", "x/../../..")}, "outside the archive"},
		{"link with a bad segment", []member{symlink("l", "a b")}, "not allowed"},
	}

	for format, build := range builders() {
		for _, tc := range tests {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				got, err := modulecheck.Check(build(t, tc.members))
				if tc.refuse != "" {
					wantRefused(t, err, tc.refuse)

					return
				}

				if err != nil || string(got) != format {
					t.Fatalf("Check = %q, %v, want %q and no error", got, err, format)
				}
			})
		}
	}
}

// TestCheckNames sends paths that one platform reads as another. Each path is
// refused in both formats; the allowed symbols and near misses are accepted.
func TestCheckNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		members []member
		refuse  string
	}{
		{"trailing dot", []member{file("main.tf.", "x")}, "ends with a dot"},
		{"trailing dot beside the name", []member{file("main.tf", "x"), file("main.tf.", "y")}, "ends with a dot"},
		{"trailing space", []member{file("main.tf ", "x")}, "character that is not allowed"},
		{"space inside", []member{file("my file.tf", "x")}, "character that is not allowed"},
		{"alternate stream", []member{file("main.tf:stream", "x")}, "colon"},
		{"percent escape", []member{file("%2e%2e/x", "x")}, "character that is not allowed"},
		{"device CON", []member{file("CON", "x")}, "device"},
		{"device nul with extension", []member{file("nul.txt", "x")}, "device"},
		{"device in a directory", []member{file("sub/Com1.tf", "x")}, "device"},
		{"device lpt9 in lower case", []member{file("lpt9", "x")}, "device"},
		{"device with two extensions", []member{file("aux.tar.gz", "x")}, "device"},
		{"non-ASCII letter", []member{file("caf\u00e9.tf", "x")}, "not ASCII"},
		{"decomposed letter", []member{file("cafe\u0301.tf", "x")}, "not ASCII"},
		{"composed and decomposed pair", []member{file("caf\u00e9.tf", "x"), file("cafe\u0301.tf", "y")}, "not ASCII"},
		{"full-width dot", []member{file("a\uff0eb", "x")}, "not ASCII"},
		{"invalid UTF-8", []member{file("a\xffb", "x")}, "not ASCII"},
		{"empty segment", []member{file("a//b.tf", "x")}, "segment is empty"},
		{"segment above 255 bytes", []member{file(strings.Repeat("a", 256), "x")}, "longer than 255"},
		{"path above 1024 bytes", []member{file(strings.Repeat("a/", 600)+"f", "x")}, "longer than"},
		{"every allowed symbol", []member{file("a-b_c.d+e@f=g,h.tf", "x")}, ""},
		{"hidden file", []member{file(".gitignore", "x")}, ""},
		{"name that starts like a device", []member{file("console.tf", "x"), file("COM0", "y"), file("COMA", "z")}, ""},
		{"directory with a slash", []member{dir("sub/"), file("sub/main.tf", "x")}, ""},
		{"name with three dots inside", []member{file("a...b", "x")}, ""},
	}

	for format, build := range builders() {
		for _, tc := range tests {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := modulecheck.Check(build(t, tc.members))
				if tc.refuse != "" {
					wantRefused(t, err, tc.refuse)

					return
				}

				if err != nil {
					t.Fatalf("Check = %v, want no error", err)
				}
			})
		}
	}
}

func TestCheckHardLink(t *testing.T) {
	t.Parallel()

	_, err := modulecheck.Check(makeTarGz(t, []member{file("a.tf", "x"), hardlink("b.tf", "a.tf")}))
	wantRefused(t, err, "hard link")
}

func TestCheckFormat(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"empty":       nil,
		"text":        []byte("module \"x\" {}\n"),
		"truncated":   makeZip(t, []member{file("a.tf", "x")})[:20],
		"bad gzip":    {0x1f, 0x8b, 0x00},
		"empty zip":   makeZip(t, nil),
		"empty tar":   makeTarGz(t, nil),
		"gzip no tar": gzipOf(t, []byte("not a tar stream, not a tar stream, not a tar stream")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := modulecheck.Check(data); !errors.Is(err, modulecheck.ErrRefused) {
				t.Fatalf("Check = %v, want ErrRefused", err)
			}
		})
	}
}

func gzipOf(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)

	if _, err := gz.Write(data); err != nil {
		t.Fatalf("gzip: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return buf.Bytes()
}

// TestCheckLimits proves the limits of the unpacked size and the entry count
// with small limits. The sizes in the headers are not used: the check counts
// the bytes it reads.
func TestCheckLimits(t *testing.T) {
	t.Parallel()

	limits := modulecheck.Limits{UnpackedBytes: 10000, Entries: 3}
	big := strings.Repeat("a", 6000)

	tests := []struct {
		name    string
		members []member
		refuse  string
	}{
		{"under the limits", []member{file("a", big)}, ""},
		{"too many bytes", []member{file("a", big), file("b", big)}, "unpacks to more than"},
		{
			"too many entries",
			[]member{file("a", "x"), file("b", "x"), file("c", "x"), file("d", "x")},
			"more than 3 entries",
		},
	}

	for format, build := range builders() {
		for _, tc := range tests {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := modulecheck.CheckWith(build(t, tc.members), limits)
				if tc.refuse != "" {
					wantRefused(t, err, tc.refuse)

					return
				}

				if err != nil {
					t.Fatalf("CheckWith = %v, want no error", err)
				}
			})
		}
	}
}

// TestCheckPadding proves that bytes after the end of the tar stream count.
func TestCheckPadding(t *testing.T) {
	t.Parallel()

	var raw bytes.Buffer

	tw := tar.NewWriter(&raw)
	h := tarHeader(file("a", "x"))

	if err := tw.WriteHeader(&h); err != nil {
		t.Fatalf("header: %v", err)
	}

	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw.Write(make([]byte, 5000))

	_, err := modulecheck.CheckWith(gzipOf(t, raw.Bytes()), modulecheck.Limits{UnpackedBytes: 4000, Entries: 10})
	if !errors.Is(err, modulecheck.ErrRefused) {
		t.Fatalf("CheckWith = %v, want ErrRefused", err)
	}
}

// gitArchive returns a tar.gz file in the form of git archive: a pax global
// header first, then the files. The global header holds records.
func gitArchive(t *testing.T, records map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	var global tar.Header

	global.Name, global.Typeflag, global.Format = "pax_global_header", tar.TypeXGlobalHeader, tar.FormatPAX
	global.PAXRecords = records

	if err := tw.WriteHeader(&global); err != nil {
		t.Fatalf("global header: %v", err)
	}

	for _, m := range []member{dir("module/"), file("module/main.tf", "x")} {
		h := tarHeader(m)
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("header: %v", err)
		}

		if h.Size > 0 {
			if _, err := tw.Write([]byte(m.body)); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return buf.Bytes()
}

// TestCheckGitArchive proves that a pax global header is read as metadata: a
// comment is accepted, and any other record is refused because other archive
// readers apply it to the entries after the header.
func TestCheckGitArchive(t *testing.T) {
	t.Parallel()

	const comment = "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name    string
		records map[string]string
		refuse  string
	}{
		{"comment", map[string]string{"comment": comment}, ""},
		{"comment and path", map[string]string{"comment": comment, "path": "../evil"}, "global header"},
		{"comment and linkpath", map[string]string{"comment": comment, "linkpath": "/etc/passwd"}, "global header"},
		{"path only", map[string]string{"path": "../evil"}, "global header"},
		{"mtime only", map[string]string{"mtime": "1700000000"}, "global header"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := modulecheck.Check(gitArchive(t, tc.records))
			if tc.refuse != "" {
				wantRefused(t, err, tc.refuse)

				return
			}

			if err != nil || got != modulecheck.FormatTarGz {
				t.Fatalf("Check = %q, %v, want tar.gz and no error", got, err)
			}
		})
	}

	// A global header counts as an entry, so it cannot be repeated without end.
	_, err := modulecheck.CheckWith(
		gitArchive(t, map[string]string{"comment": comment}),
		modulecheck.Limits{UnpackedBytes: 1 << 20, Entries: 2},
	)
	wantRefused(t, err, "more than 2 entries")
}

// patchZipCount returns a copy of a zip archive whose end record states count
// entries.
func patchZipCount(t *testing.T, data []byte, count uint16) []byte {
	t.Helper()

	out := bytes.Clone(data)
	end := bytes.LastIndex(out, []byte("PK\x05\x06"))

	if end < 0 {
		t.Fatal("no end record")
	}

	binary.LittleEndian.PutUint16(out[end+8:], count)
	binary.LittleEndian.PutUint16(out[end+10:], count)

	return out
}

// TestCheckZipCount proves that the entry count in the end record is checked
// before the directory is built, with a count that the archive does not hold.
func TestCheckZipCount(t *testing.T) {
	t.Parallel()

	small := makeZip(t, []member{file("a.tf", "x"), file("b.tf", "y")})

	tests := []struct {
		name  string
		count uint16
	}{
		{"above the limit", 20001},
		{"the largest count", 65535},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := modulecheck.Check(patchZipCount(t, small, tc.count))
			wantRefused(t, err, "states")
		})
	}
}

// TestCheckZipDirectoryBytes proves the second layer: an archive that states a
// small count but holds a long directory is stopped by the number of bytes that
// the reader may read. The count 65541 is 5 in 16 bits, which the reader of Go
// accepts.
func TestCheckZipDirectoryBytes(t *testing.T) {
	t.Parallel()

	const entries = 1<<16 + 5

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for i := range entries {
		var h zip.FileHeader

		h.Name, h.Method = fmt.Sprintf("f%06d", i), zip.Store

		if _, err := zw.CreateHeader(&h); err != nil {
			t.Fatalf("zip: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	lying := patchZipCount(t, buf.Bytes(), uint16(entries%(1<<16)))

	_, err := modulecheck.CheckWith(lying, modulecheck.Limits{UnpackedBytes: 1 << 20, Entries: 10})
	wantRefused(t, err, "larger than")
}

// longPath returns a valid path of exactly MaxPathBytes bytes. The first
// segment holds i, so each path is unique, and every segment is at most 255
// bytes.
func longPath(i int) string {
	return fmt.Sprintf("%0255d", i) + "/" +
		strings.Repeat("a", 255) + "/" +
		strings.Repeat("b", 255) + "/" +
		strings.Repeat("c", 254) + "/d"
}

// TestCheckZipLongPaths proves that the directory budget holds the longest
// path that the check allows for every entry that the entry limit allows. The
// entry count is still checked before the directory is built.
func TestCheckZipLongPaths(t *testing.T) {
	t.Parallel()

	if got := len(longPath(0)); got != modulecheck.MaxPathBytes {
		t.Fatalf("long path is %d bytes, want %d", got, modulecheck.MaxPathBytes)
	}

	members := make([]member, modulecheck.MaxEntries)
	for i := range members {
		members[i] = file(longPath(i), "")
	}

	data := makeZip(t, members)
	if len(data) >= 64<<20 {
		t.Fatalf("the test archive is %d bytes, want less than 64 MiB", len(data))
	}

	tests := []struct {
		name   string
		limits modulecheck.Limits
		refuse string
	}{
		{"at the entry limit", modulecheck.DefaultLimits(), ""},
		{
			"above a lower entry limit",
			modulecheck.Limits{UnpackedBytes: modulecheck.MaxUnpackedBytes, Entries: modulecheck.MaxEntries - 1},
			"states more than",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := modulecheck.CheckWith(data, tc.limits)
			if tc.refuse != "" {
				wantRefused(t, err, tc.refuse)

				return
			}

			if err != nil || got != modulecheck.FormatZip {
				t.Fatalf("CheckWith = %q, %v, want zip and no error", got, err)
			}
		})
	}
}
