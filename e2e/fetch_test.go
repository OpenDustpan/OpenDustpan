//go:build e2e

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const fakeVersion = "9.9.9"

// tarball builds a gzipped archive from entries, in order.
func tarball(t *testing.T, entries []tar.Header, content string) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for i := range entries {
		hdr := entries[i]

		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(content))
		}

		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}

		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatalf("write tar body: %v", err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	return buf.Bytes()
}

func validArchive(t *testing.T) []byte {
	t.Helper()

	return tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\n")
}

// release serves archive and a checksum file claiming wantSum (or the real sum if empty).
func release(t *testing.T, archive []byte, wantSum string) (*e2e.Fetcher, *atomic.Int32) {
	t.Helper()

	name, err := e2e.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	if wantSum == "" {
		sum := sha256.Sum256(archive)
		wantSum = hex.EncodeToString(sum[:])
	}

	var downloads atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)

		if _, err := w.Write(archive); err != nil {
			t.Errorf("serve archive: %v", err)
		}
	})
	mux.HandleFunc("/pulumi-"+fakeVersion+"-checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprintf(w, "%s  %s\n", wantSum, name); err != nil {
			t.Errorf("serve checksums: %v", err)
		}
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprintln(w, fakeVersion); err != nil {
			t.Errorf("serve latest: %v", err)
		}
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return &e2e.Fetcher{
		Client:       ts.Client(),
		CacheDir:     filepath.Join(t.TempDir(), "cache"),
		DownloadBase: ts.URL,
		LatestURL:    ts.URL + "/latest",
	}, &downloads
}

func TestFetchInstallsAndCaches(t *testing.T) {
	t.Parallel()

	fetcher, downloads := release(t, validArchive(t), "")

	for range 2 {
		inst, err := fetcher.Install(t.Context(), e2e.Latest)
		if err != nil {
			t.Fatalf("install: %v", err)
		}

		if inst.Version != fakeVersion || inst.ChecksumSource != "published" {
			t.Errorf(
				"got version %q from %q checksum, want %q from published",
				inst.Version,
				inst.ChecksumSource,
				fakeVersion,
			)
		}

		if _, err := os.Stat(inst.Binary()); err != nil {
			t.Errorf("binary missing: %v", err)
		}
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}
}

func TestFetchRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	fetcher, _ := release(t, validArchive(t), "0000000000000000000000000000000000000000000000000000000000000000")

	_, err := fetcher.Install(t.Context(), fakeVersion)
	if !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("got error %v, want ErrChecksumMismatch", err)
	}

	entries, readErr := os.ReadDir(fetcher.CacheDir)
	if readErr != nil {
		t.Fatalf("read cache: %v", readErr)
	}

	if len(entries) != 0 {
		t.Errorf("cache holds %d entries after a rejected download, want none", len(entries))
	}
}

func TestFetchRejectsUnsafeArchives(t *testing.T) {
	t.Parallel()

	tests := map[string][]tar.Header{
		"symlink": {
			{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
			{Name: "pulumi/pulumi", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0o755},
		},
		"path traversal": {
			{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644},
		},
	}

	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fetcher, _ := release(t, tarball(t, entries, "x"), "")

			_, err := fetcher.Install(t.Context(), fakeVersion)
			if !errors.Is(err, e2e.ErrUnsafeArchive) {
				t.Fatalf("got error %v, want ErrUnsafeArchive", err)
			}
		})
	}
}
