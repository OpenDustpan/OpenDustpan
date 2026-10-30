package modulecheck_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
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
		{"drive path", []member{file("C:evil", "x")}, "drive"},
		{"control character", []member{file("a\x01b", "x")}, "control character"},
		{"link outside", []member{symlink("l", "../outside")}, "outside the archive"},
		{"deep link outside", []member{symlink("a/b/l", "../../../x")}, "outside the archive"},
		{"absolute link", []member{symlink("l", "/etc/passwd")}, "absolute target"},
		{"link with a drive", []member{symlink("l", "C:/x")}, "absolute target"},
		{"entry below a link", []member{symlink("d", "."), file("d/f.tf", "x")}, "below the link"},
		{"entry below a late link", []member{file("d/f.tf", "x"), symlink("d", ".")}, "below the link"},
		{"same path twice", []member{file("a.tf", "x"), file("a.tf", "y")}, "twice"},
		{"same path in other case", []member{file("a.tf", "x"), file("A.tf", "y")}, "twice"},
		{"special file", []member{special("dev")}, "special file"},
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
