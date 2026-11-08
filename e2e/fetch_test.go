//go:build e2e

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
func release(t *testing.T, archive []byte, wantSum string) (*e2e.Fetcher, e2e.Tool, *atomic.Int32) {
	t.Helper()

	return releaseWith(t, archive, wantSum, func(w http.ResponseWriter, _ int32) {
		if _, err := w.Write(archive); err != nil {
			t.Errorf("serve archive: %v", err)
		}
	})
}

// releaseWith is release with serve answering each request for the archive.
// serve gets the number of the request, from 1.
func releaseWith(
	t *testing.T,
	archive []byte,
	wantSum string,
	serve func(w http.ResponseWriter, n int32),
) (*e2e.Fetcher, e2e.Tool, *atomic.Int32) {
	t.Helper()

	tool := e2e.Pulumi()

	name, err := tool.ArchiveName(fakeVersion)
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
		serve(w, downloads.Add(1))
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

	tool.LatestURL = ts.URL + "/latest"
	tool.ReleaseURL = ts.URL + "/%[2]s"

	return &e2e.Fetcher{Client: ts.Client(), CacheDir: filepath.Join(t.TempDir(), "cache")}, tool, &downloads
}

func TestFetchInstallsAndCaches(t *testing.T) {
	t.Parallel()

	fetcher, tool, downloads := release(t, validArchive(t), "")

	for range 2 {
		inst, err := fetcher.Install(t.Context(), tool, e2e.Latest)
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

// serveBroken sends the headers and half of archive, then drops the connection.
func serveBroken(t *testing.T, w http.ResponseWriter, archive []byte) {
	t.Helper()

	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))

	if _, err := w.Write(archive[:len(archive)/2]); err != nil {
		t.Errorf("serve half archive: %v", err)
	}

	panic(http.ErrAbortHandler)
}

func serveFull(t *testing.T, w http.ResponseWriter, archive []byte) {
	t.Helper()

	if _, err := w.Write(archive); err != nil {
		t.Errorf("serve archive: %v", err)
	}
}

// failFirst answers the first request with fail and later requests with archive.
func failFirst(
	archive []byte,
	fail func(t *testing.T, w http.ResponseWriter),
) func(*testing.T, http.ResponseWriter, int32) {
	return func(t *testing.T, w http.ResponseWriter, n int32) {
		t.Helper()

		if n == 1 {
			fail(t, w)

			return
		}

		serveFull(t, w, archive)
	}
}

// status answers every request with code.
func status(code int) func(*testing.T, http.ResponseWriter, int32) {
	return func(_ *testing.T, w http.ResponseWriter, _ int32) { w.WriteHeader(code) }
}

func TestFetchRetriesDownload(t *testing.T) {
	t.Parallel()

	archive := validArchive(t)

	cases := []struct {
		name          string
		serve         func(t *testing.T, w http.ResponseWriter, n int32)
		wantErr       error
		wantDownloads int32
	}{
		{
			name: "broken stream then success",
			serve: failFirst(archive, func(t *testing.T, w http.ResponseWriter) {
				t.Helper()
				serveBroken(t, w, archive)
			}),
			wantErr:       nil,
			wantDownloads: 2,
		},
		{
			name: "server error then success",
			serve: failFirst(archive, func(_ *testing.T, w http.ResponseWriter) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}),
			wantErr:       nil,
			wantDownloads: 2,
		},
		{
			name:          "not found is not retried",
			serve:         status(http.StatusNotFound),
			wantErr:       e2e.ErrUnexpectedStatus,
			wantDownloads: 1,
		},
		{
			name:          "server error on every attempt",
			serve:         status(http.StatusBadGateway),
			wantErr:       e2e.ErrUnexpectedStatus,
			wantDownloads: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fetcher, tool, downloads := releaseWith(t, archive, "", func(w http.ResponseWriter, n int32) {
				tc.serve(t, w, n)
			})

			// errors.Is(err, nil) is true only when err is nil.
			inst, err := fetcher.Install(t.Context(), tool, fakeVersion)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}

			if tc.wantErr == nil {
				if _, statErr := os.Stat(inst.Binary()); statErr != nil {
					t.Errorf("binary missing: %v", statErr)
				}
			}

			if got := downloads.Load(); got != tc.wantDownloads {
				t.Errorf("archive requested %d times, want %d", got, tc.wantDownloads)
			}
		})
	}
}

func TestFetchRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	fetcher, tool, _ := release(t, validArchive(t), "0000000000000000000000000000000000000000000000000000000000000000")

	_, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("got error %v, want ErrChecksumMismatch", err)
	}

	entries, readErr := os.ReadDir(fetcher.CacheDir)
	if readErr != nil {
		t.Fatalf("read cache: %v", readErr)
	}

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	// The lock file of the entry stays; nothing else may.
	for _, entry := range entries {
		if entry.Name() != name+".lock" {
			t.Errorf("cache holds %q after a rejected download, want only the lock file", entry.Name())
		}
	}
}

// TestFetchRejectsBadVersions sends labels that are not a plain release. Each
// is refused before it names a path or a URL: nothing is downloaded and the
// cache directory is not made.
func TestFetchRejectsBadVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		label string
	}{
		{name: "parent directory", label: "../x"},
		{name: "path after a release", label: "1.2.3/../../x"},
		{name: "sign", label: "+1.2.3"},
		{name: "two parts", label: "1.2"},
		{name: "pre-release", label: "1.2.3-rc.1"},
		{name: "empty", label: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fetcher, tool, downloads := release(t, validArchive(t), "")

			_, err := fetcher.Install(t.Context(), tool, tc.label)
			if !errors.Is(err, e2e.ErrBadVersion) {
				t.Fatalf("got error %v, want ErrBadVersion", err)
			}

			if got := downloads.Load(); got != 0 {
				t.Errorf("got %d downloads, want 0", got)
			}

			if _, statErr := os.Stat(fetcher.CacheDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("cache directory after a bad version: %v, want it not made", statErr)
			}
		})
	}
}

func TestFetchPinnedVersionFailsClosed(t *testing.T) {
	tofu := e2e.OpenTofu()
	pulumi := e2e.Pulumi()

	cases := []struct {
		name      string
		tool      e2e.Tool
		env       string
		versions  string
		label     string
		checksums string
	}{
		{
			name:      "opentofu default",
			tool:      tofu,
			env:       "E2E_TOFU_VERSIONS",
			versions:  "",
			label:     tofu.Single(),
			checksums: "tofu_" + tofu.Single() + "_SHA256SUMS",
		},
		{
			name:      "pulumi journal version",
			tool:      pulumi,
			env:       "E2E_PULUMI_VERSIONS",
			versions:  "3.268.0",
			label:     "3.268.0",
			checksums: "pulumi-3.268.0-checksums.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, tc.versions)

			checkPinnedFailsClosed(t, tc.tool, tc.label, tc.checksums)
		})
	}
}

// checkPinnedFailsClosed installs label, whose sum is pinned, against a fake
// server whose archive has another sum. It checks that Install fails closed and
// requests neither the checksum file nor the latest version.
func checkPinnedFailsClosed(t *testing.T, tool e2e.Tool, label, checksumFile string) {
	t.Helper()

	name, err := tool.ArchiveName(label)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	archive := validArchive(t)

	var checksums, latest atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) {
		if _, werr := w.Write(archive); werr != nil {
			t.Errorf("serve archive: %v", werr)
		}
	})
	mux.HandleFunc("/"+checksumFile, func(w http.ResponseWriter, _ *http.Request) {
		checksums.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		latest.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	tool.LatestURL = ts.URL + "/latest"
	tool.ReleaseURL = ts.URL + "/%[2]s"
	fetcher := &e2e.Fetcher{Client: ts.Client(), CacheDir: filepath.Join(t.TempDir(), "cache")}

	_, err = fetcher.Install(t.Context(), tool, label)
	if !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("install pinned %s: got error %v, want ErrChecksumMismatch", label, err)
	}

	entries, readErr := os.ReadDir(fetcher.CacheDir)
	if readErr != nil {
		t.Fatalf("read cache: %v", readErr)
	}

	// The lock file of the entry stays; nothing else may.
	for _, entry := range entries {
		if entry.Name() != name+".lock" {
			t.Errorf("cache holds %q after a rejected download, want only the lock file", entry.Name())
		}
	}

	if got := checksums.Load(); got != 0 {
		t.Errorf("checksum file requested %d times, want 0", got)
	}

	if got := latest.Load(); got != 0 {
		t.Errorf("latest version requested %d times, want 0", got)
	}
}

// errUnexpectedRequest fails every request of countingErrorTransport.
var errUnexpectedRequest = errors.New("unexpected network request")

// countingErrorTransport fails every request and counts it.
type countingErrorTransport struct {
	requests atomic.Int32
}

func (t *countingErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.requests.Add(1)

	return nil, errUnexpectedRequest
}

func TestFetchRefusesUnpinnedWithoutMatrix(t *testing.T) {
	tool := e2e.OpenTofu()

	if _, err := tool.ArchiveName(fakeVersion); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	cases := []struct {
		name         string
		versions     string
		label        string
		wantUnpinned bool
		wantRequests bool
	}{
		{
			name:         "latest without variable",
			versions:     "",
			label:        e2e.Latest,
			wantUnpinned: true,
			wantRequests: false,
		},
		{
			name:         "unpinned version without variable",
			versions:     "",
			label:        fakeVersion,
			wantUnpinned: true,
			wantRequests: false,
		},
		{
			name:         "latest with variable",
			versions:     e2e.Latest,
			label:        e2e.Latest,
			wantUnpinned: false,
			wantRequests: true,
		},
		{
			name:         "unpinned version with variable",
			versions:     fakeVersion,
			label:        fakeVersion,
			wantUnpinned: false,
			wantRequests: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("E2E_TOFU_VERSIONS", tc.versions)

			transport := new(countingErrorTransport)
			fetcher := &e2e.Fetcher{
				Client:   &http.Client{Transport: transport},
				CacheDir: filepath.Join(t.TempDir(), "cache"),
			}

			_, err := fetcher.Install(t.Context(), tool, tc.label)

			if got := errors.Is(err, e2e.ErrUnpinnedFetch); got != tc.wantUnpinned {
				t.Fatalf(
					"Install(%q) with E2E_TOFU_VERSIONS=%q error = %v, want ErrUnpinnedFetch %t",
					tc.label,
					tc.versions,
					err,
					tc.wantUnpinned,
				)
			}

			requests := transport.requests.Load()
			if tc.wantRequests && requests == 0 {
				t.Errorf("Install(%q) made no request, want at least one", tc.label)
			}

			if !tc.wantRequests && requests != 0 {
				t.Errorf("Install(%q) made %d requests, want none", tc.label, requests)
			}
		})
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

			fetcher, tool, _ := release(t, tarball(t, entries, "x"), "")

			_, err := fetcher.Install(t.Context(), tool, fakeVersion)
			if !errors.Is(err, e2e.ErrUnsafeArchive) {
				t.Fatalf("got error %v, want ErrUnsafeArchive", err)
			}
		})
	}
}

func TestFetchResolvesLatestOpenTofu(t *testing.T) {
	t.Parallel()

	tool := e2e.OpenTofu()

	const want = "1.13.1"

	name, err := tool.ArchiveName(want)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	archive := tarball(t, []tar.Header{{Name: "tofu", Typeflag: tar.TypeReg, Mode: 0o755}}, "#!/bin/sh\n")
	sum := sha256.Sum256(archive)

	mux := http.NewServeMux()
	mux.HandleFunc("/api.json", func(w http.ResponseWriter, _ *http.Request) {
		// Pre-releases and older minors listed first must not win.
		const body = `{"versions":[{"id":"1.14.0-rc1"},{"id":"1.9.5"},{"id":"` + want + `"},{"id":"1.13.0"}]}`
		if _, werr := fmt.Fprint(w, body); werr != nil {
			t.Errorf("serve api.json: %v", werr)
		}
	})
	mux.HandleFunc("/v"+want+"/tofu_"+want+"_SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		if _, werr := fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name); werr != nil {
			t.Errorf("serve checksums: %v", werr)
		}
	})
	mux.HandleFunc("/v"+want+"/"+name, func(w http.ResponseWriter, _ *http.Request) {
		if _, werr := w.Write(archive); werr != nil {
			t.Errorf("serve archive: %v", werr)
		}
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	tool.LatestURL = ts.URL + "/api.json"
	tool.ReleaseURL = ts.URL + "/v%[1]s/%[2]s"
	fetcher := &e2e.Fetcher{Client: ts.Client(), CacheDir: filepath.Join(t.TempDir(), "cache")}

	inst, err := fetcher.Install(t.Context(), tool, e2e.Latest)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if inst.Version != want || filepath.Base(inst.Binary()) != "tofu" {
		t.Errorf("got version %q binary %q, want %q tofu", inst.Version, inst.Binary(), want)
	}

	if _, err := os.Stat(inst.Binary()); err != nil {
		t.Errorf("binary missing: %v", err)
	}
}

// archiveWithSibling holds the CLI and a second file with the same content.
func archiveWithSibling(t *testing.T) []byte {
	t.Helper()

	return tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "pulumi/other-file", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "pulumi/same-size!", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\n")
}

// archiveWithEmptyDir holds the CLI and an empty directory.
func archiveWithEmptyDir(t *testing.T) []byte {
	t.Helper()

	return tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "pulumi/empty/", Typeflag: tar.TypeDir, Mode: 0o755},
	}, "#!/bin/sh\n")
}

// archiveWithRootDir holds a root directory entry and the CLI.
func archiveWithRootDir(t *testing.T) []byte {
	t.Helper()

	return tarball(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\n")
}

// archiveWithoutDirEntries holds only the CLI path.
func archiveWithoutDirEntries(t *testing.T) []byte {
	t.Helper()

	return tarball(t, []tar.Header{
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\n")
}

type cachedEntryCase struct {
	name          string
	archive       func(*testing.T) []byte
	change        func(*testing.T, e2e.Install, string)
	wantDownloads int32
	check         func(*testing.T, e2e.Install)
}

func cachedEntryCases() []cachedEntryCase {
	return []cachedEntryCase{
		{name: "one byte of the binary changed", archive: nil, change: changeBinary, wantDownloads: 2, check: nil},
		{name: "binary of another size", archive: nil, change: changeBinarySize, wantDownloads: 2, check: nil},
		{name: "binary removed", archive: nil, change: removeBinary, wantDownloads: 2, check: nil},
		{name: "binary not executable", archive: nil, change: removeBinaryExecBit, wantDownloads: 2, check: nil},
		{name: "binary with the setuid bit", archive: nil, change: addBinarySetuidBit, wantDownloads: 2, check: nil},
		{
			name:          "other file removed",
			archive:       archiveWithSibling,
			change:        removeOtherFile,
			wantDownloads: 2,
			check:         nil,
		},
		{
			name:          "other file replaced by a symbolic link",
			archive:       archiveWithSibling,
			change:        linkOtherFile,
			wantDownloads: 2,
			check:         nil,
		},
		{
			name:          "directory replaced by a file",
			archive:       archiveWithEmptyDir,
			change:        replaceDirectory,
			wantDownloads: 2,
			check:         nil,
		},
		{name: "extra file", archive: nil, change: addExtraFile, wantDownloads: 2, check: checkExtraFileGone},
		{name: "archive removed", archive: nil, change: removeArchive, wantDownloads: 2, check: nil},
		{name: "archive changed", archive: nil, change: changeArchive, wantDownloads: 2, check: nil},
		{
			name:          "binary replaced by a symbolic link",
			archive:       archiveWithSibling,
			change:        linkBinary,
			wantDownloads: 2,
			check:         nil,
		},
		{name: "no change", archive: nil, change: noChange, wantDownloads: 1, check: nil},
		{name: "root directory entry", archive: archiveWithRootDir, change: noChange, wantDownloads: 1, check: nil},
		{name: "implicit directory", archive: archiveWithoutDirEntries, change: noChange, wantDownloads: 1, check: nil},
	}
}

func changeBinary(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.WriteFile(inst.Binary(), []byte("#!/bin/shX"), 0o600); err != nil {
		t.Fatalf("change binary: %v", err)
	}
}

func removeBinaryExecBit(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.Chmod(inst.Binary(), 0o600); err != nil {
		t.Fatalf("change binary mode: %v", err)
	}
}

func addBinarySetuidBit(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.Chmod(inst.Binary(), 0o750|os.ModeSetuid); err != nil {
		t.Fatalf("change binary mode: %v", err)
	}
}

func changeBinarySize(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.WriteFile(inst.Binary(), []byte("short\n"), 0o600); err != nil {
		t.Fatalf("replace binary: %v", err)
	}
}

func removeBinary(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.Remove(inst.Binary()); err != nil {
		t.Fatalf("remove binary: %v", err)
	}
}

func removeOtherFile(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.Remove(filepath.Join(inst.Dir, "other-file")); err != nil {
		t.Fatalf("remove other file: %v", err)
	}
}

func linkOtherFile(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	other := filepath.Join(inst.Dir, "other-file")
	if err := os.Remove(other); err != nil {
		t.Fatalf("remove other file: %v", err)
	}

	if err := os.Symlink("same-size!", other); err != nil {
		t.Fatalf("link other file: %v", err)
	}
}

func replaceDirectory(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	empty := filepath.Join(inst.Dir, "empty")
	if err := os.Remove(empty); err != nil {
		t.Fatalf("remove directory: %v", err)
	}

	if err := os.WriteFile(empty, []byte("x"), 0o600); err != nil {
		t.Fatalf("replace directory: %v", err)
	}
}

func addExtraFile(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(inst.Dir, "extra"), []byte("extra"), 0o600); err != nil {
		t.Fatalf("add extra file: %v", err)
	}
}

func checkExtraFileGone(t *testing.T, inst e2e.Install) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(inst.Dir, "extra")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("extra file still in the cache: %v", err)
	}
}

func removeArchive(t *testing.T, _ e2e.Install, archivePath string) {
	t.Helper()

	if err := os.Remove(archivePath); err != nil {
		t.Fatalf("remove archive: %v", err)
	}
}

func changeArchive(t *testing.T, _ e2e.Install, archivePath string) {
	t.Helper()

	changeCachedArchive(t, filepath.Dir(archivePath), filepath.Base(archivePath), func(data []byte) {
		// The gzip modification time does not change the extracted content,
		// so only the archive sum can refuse this entry.
		data[4] ^= 0xff
	})
}

func linkBinary(t *testing.T, inst e2e.Install, _ string) {
	t.Helper()

	if err := os.Remove(inst.Binary()); err != nil {
		t.Fatalf("remove binary: %v", err)
	}

	if err := os.Symlink("same-size!", inst.Binary()); err != nil {
		t.Fatalf("link binary: %v", err)
	}
}

func noChange(*testing.T, e2e.Install, string) {}

func TestFetchChecksCachedEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range cachedEntryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkCachedEntryCase(t, tc)
		})
	}
}

func checkCachedEntryCase(t *testing.T, tc cachedEntryCase) {
	t.Helper()

	archive := validArchive(t)
	if tc.archive != nil {
		archive = tc.archive(t)
	}

	fetcher, tool, downloads := release(t, archive, "")

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	archivePath := filepath.Join(fetcher.CacheDir, name)

	inst, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	tc.change(t, inst, archivePath)

	// A new Fetcher stands for the next run, which must check the cache.
	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	inst, err = fresh.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}

	if got := downloads.Load(); got != tc.wantDownloads {
		t.Errorf("archive downloaded %d times, want %d", got, tc.wantDownloads)
	}

	data, err := os.ReadFile(inst.Binary())
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}

	if want := "#!/bin/sh\n"; string(data) != want {
		t.Errorf("binary holds %q, want %q", data, want)
	}

	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("cached archive missing: %v", err)
	}

	if tc.check != nil {
		tc.check(t, inst)
	}
}

// changeCachedArchive applies change to the cached archive through a root,
// so the test touches only a file below the cache directory.
func changeCachedArchive(t *testing.T, cacheDir, name string, change func([]byte)) {
	t.Helper()

	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		t.Fatalf("open cache dir: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close cache dir: %v", closeErr)
		}
	}()

	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	change(data)

	if err := root.WriteFile(name, data, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

// cacheFiles holds the files and links of one hand-made cache entry.
type cacheFiles struct {
	files map[string]string
	links map[string]string
}

// writeCacheEntry writes an archive and its extracted files and links into
// cacheDir, as an earlier run would have left them.
func writeCacheEntry(cacheDir, archiveName string, archive []byte, tree cacheFiles) error {
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return fmt.Errorf("open cache dir: %w", err)
	}

	err = fillCacheEntry(root, archiveName, archive, tree)

	return errors.Join(err, root.Close())
}

func fillCacheEntry(root *os.Root, archiveName string, archive []byte, tree cacheFiles) error {
	dest := strings.TrimSuffix(archiveName, ".tar.gz")

	for name, content := range tree.files {
		target := path.Join(dest, name)
		if err := root.MkdirAll(path.Dir(target), 0o750); err != nil {
			return fmt.Errorf("create %q: %w", target, err)
		}

		if err := root.WriteFile(target, []byte(content), 0o750); err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}

		// The mode that extraction sets, so that only the planted change
		// differs from a fresh entry.
		if err := root.Chmod(target, 0o750); err != nil {
			return fmt.Errorf("set the mode of %q: %w", target, err)
		}
	}

	for name, link := range tree.links {
		target := path.Join(dest, name)
		if err := root.MkdirAll(path.Dir(target), 0o750); err != nil {
			return fmt.Errorf("create %q: %w", target, err)
		}

		if err := root.Symlink(link, target); err != nil {
			return fmt.Errorf("link %q: %w", target, err)
		}
	}

	if err := root.WriteFile(archiveName, archive, 0o600); err != nil {
		return fmt.Errorf("write archive: %w", err)
	}

	return nil
}

func mustWriteCacheEntry(t *testing.T, cacheDir, archiveName string, archive []byte, tree cacheFiles) {
	t.Helper()

	if err := writeCacheEntry(cacheDir, archiveName, archive, tree); err != nil {
		t.Fatalf("write cache entry: %v", err)
	}
}

func TestFetchChecksArchiveWithoutBinary(t *testing.T) {
	t.Parallel()

	archive := tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/other", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\n")

	fetcher, tool, downloads := release(t, archive, "")

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	mustWriteCacheEntry(
		t,
		fetcher.CacheDir,
		name,
		archive,
		cacheFiles{
			files: map[string]string{"pulumi/other": "#!/bin/sh\n"},
			links: nil,
		},
	)

	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	if _, err := fresh.Install(t.Context(), tool, fakeVersion); !errors.Is(err, e2e.ErrMissingBinary) {
		t.Fatalf("got error %v, want ErrMissingBinary", err)
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}
}

func TestFetchKeepsEntryFromAnotherProcess(t *testing.T) {
	t.Parallel()

	archive := validArchive(t)

	var (
		fetcher *e2e.Fetcher
		tool    e2e.Tool
	)

	fetcher, tool, downloads := releaseWith(t, archive, "", func(w http.ResponseWriter, n int32) {
		if n == 1 {
			name, err := tool.ArchiveName(fakeVersion)
			if err != nil {
				t.Errorf("archive name: %v", err)

				return
			}

			writeErr := writeCacheEntry(fetcher.CacheDir, name, archive, cacheFiles{
				files: map[string]string{"pulumi/pulumi": "#!/bin/sh\n"},
				links: nil,
			})
			if writeErr != nil {
				t.Errorf("write cache entry: %v", writeErr)

				return
			}
		}

		serveFull(t, w, archive)
	})

	inst, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}

	data, err := os.ReadFile(inst.Binary())
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}

	if want := "#!/bin/sh\n"; string(data) != want {
		t.Errorf("binary holds %q, want %q", data, want)
	}
}

func TestFetchRejectsEntryFromAnotherProcess(t *testing.T) {
	t.Parallel()

	archive := validArchive(t)

	var (
		fetcher *e2e.Fetcher
		tool    e2e.Tool
	)

	fetcher, tool, downloads := releaseWith(t, archive, "", func(w http.ResponseWriter, n int32) {
		if n == 1 {
			name, err := tool.ArchiveName(fakeVersion)
			if err != nil {
				t.Errorf("archive name: %v", err)

				return
			}

			writeErr := writeCacheEntry(fetcher.CacheDir, name, []byte("another archive"), cacheFiles{
				files: map[string]string{"pulumi/pulumi": "#!/bin/sh\n"},
				links: nil,
			})
			if writeErr != nil {
				t.Errorf("write cache entry: %v", writeErr)

				return
			}
		}

		serveFull(t, w, archive)
	})

	if _, err := fetcher.Install(t.Context(), tool, fakeVersion); err == nil {
		t.Fatal("install accepted an entry that failed the check")
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}
}

func TestFetchChecksUnsupportedArchiveEntry(t *testing.T) {
	t.Parallel()

	archive := tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "pulumi/link", Typeflag: tar.TypeLink, Linkname: "pulumi/pulumi", Mode: 0o755},
	}, "#!/bin/sh\n")

	fetcher, tool, downloads := release(t, archive, "")

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	mustWriteCacheEntry(t, fetcher.CacheDir, name, archive, cacheFiles{
		files: map[string]string{"pulumi/pulumi": "#!/bin/sh\n"},
		links: nil,
	})

	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	if _, err := fresh.Install(t.Context(), tool, fakeVersion); !errors.Is(err, e2e.ErrUnsafeArchive) {
		t.Fatalf("got error %v, want ErrUnsafeArchive", err)
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}
}

type symlinkCase struct {
	name          string
	target        string
	wantErr       error
	wantDownloads int32
}

func TestFetchChecksSymlinkTarget(t *testing.T) {
	t.Parallel()

	tests := []symlinkCase{
		// Extraction refuses a link, so the cache check refuses it too, even
		// when the cached link matches the archive.
		{name: "matching target", target: "pulumi", wantErr: e2e.ErrUnsafeArchive, wantDownloads: 1},
		{name: "other target", target: "other", wantErr: e2e.ErrUnsafeArchive, wantDownloads: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSymlinkTargetCase(t, tc)
		})
	}
}

func checkSymlinkTargetCase(t *testing.T, tc symlinkCase) {
	t.Helper()

	archive := tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "pulumi/link", Typeflag: tar.TypeSymlink, Linkname: "pulumi", Mode: 0o755},
	}, "#!/bin/sh\n")

	fetcher, tool, downloads := release(t, archive, "")

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	mustWriteCacheEntry(
		t,
		fetcher.CacheDir,
		name,
		archive,
		cacheFiles{
			files: map[string]string{"pulumi/pulumi": "#!/bin/sh\n"},
			links: map[string]string{"pulumi/link": tc.target},
		},
	)

	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	inst, err := fresh.Install(t.Context(), tool, fakeVersion)
	if !errors.Is(err, tc.wantErr) {
		t.Fatalf("got error %v, want %v", err, tc.wantErr)
	}

	if tc.wantErr == nil {
		data, readErr := os.ReadFile(inst.Binary())
		if readErr != nil {
			t.Fatalf("read binary: %v", readErr)
		}

		if want := "#!/bin/sh\n"; string(data) != want {
			t.Errorf("binary holds %q, want %q", data, want)
		}
	}

	if got := downloads.Load(); got != tc.wantDownloads {
		t.Errorf("archive downloaded %d times, want %d", got, tc.wantDownloads)
	}
}

func TestFetchRefusesCacheWhenNewDownloadMismatches(t *testing.T) {
	t.Parallel()

	good := validArchive(t)
	bad := tarball(t, []tar.Header{
		{Name: "pulumi/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "pulumi/pulumi", Typeflag: tar.TypeReg, Mode: 0o755},
	}, "#!/bin/sh\necho bad\n")

	fetcher, tool, downloads := releaseWith(t, good, "", func(w http.ResponseWriter, n int32) {
		if n == 1 {
			serveFull(t, w, good)

			return
		}

		serveFull(t, w, bad)
	})

	inst, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	if writeErr := os.WriteFile(inst.Binary(), []byte("changed\n"), 0o600); writeErr != nil {
		t.Fatalf("change binary: %v", writeErr)
	}

	// A new Fetcher must not fall back to the changed entry.
	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	refused, err := fresh.Install(t.Context(), tool, fakeVersion)
	if !errors.Is(err, e2e.ErrChecksumMismatch) {
		t.Fatalf("got error %v, want ErrChecksumMismatch", err)
	}

	if refused.Binary() != "" {
		t.Errorf("got binary %q from a refused entry, want none", refused.Binary())
	}

	if _, statErr := os.Stat(inst.Binary()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("changed binary still in the cache: %v", statErr)
	}

	if got := downloads.Load(); got != 2 {
		t.Errorf("archive downloaded %d times, want 2", got)
	}
}

func TestFetchRefusesEntryWithoutArchive(t *testing.T) {
	t.Parallel()

	fetcher, tool, downloads := release(t, validArchive(t), "")

	if _, err := fetcher.Install(t.Context(), tool, fakeVersion); err != nil {
		t.Fatalf("first install: %v", err)
	}

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	if removeErr := os.Remove(filepath.Join(fetcher.CacheDir, name)); removeErr != nil {
		t.Fatalf("remove archive: %v", removeErr)
	}

	// A new Fetcher sees the extracted directory from an older layout.
	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	if _, err := fresh.Install(t.Context(), tool, fakeVersion); err != nil {
		t.Fatalf("install over an entry without an archive: %v", err)
	}

	if got := downloads.Load(); got != 2 {
		t.Errorf("archive downloaded %d times, want 2", got)
	}
}

func TestFetchChecksCachedEntryOnce(t *testing.T) {
	t.Parallel()

	fetcher, tool, downloads := release(t, validArchive(t), "")

	first, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	if removeErr := os.Remove(filepath.Join(fetcher.CacheDir, name)); removeErr != nil {
		t.Fatalf("remove archive: %v", removeErr)
	}

	second, err := fetcher.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("second install with the same fetcher: %v", err)
	}

	if second.Binary() != first.Binary() {
		t.Errorf("got binary %q, want %q", second.Binary(), first.Binary())
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}

	fresh := &e2e.Fetcher{Client: fetcher.Client, CacheDir: fetcher.CacheDir}

	if _, err := fresh.Install(t.Context(), tool, fakeVersion); err != nil {
		t.Fatalf("install with a new fetcher: %v", err)
	}

	if got := downloads.Load(); got != 2 {
		t.Errorf("archive downloaded %d times, want 2", got)
	}
}

// TestFetchSerializesInstallsOfOneEntry runs a second Install with its own
// Fetcher on the same cache directory while the first one downloads. The
// second call reads the checksums, then must wait for the first one and use
// its entry. It must not refuse that entry and download again.
func TestFetchSerializesInstallsOfOneEntry(t *testing.T) {
	t.Parallel()

	archive := validArchive(t)
	tool := e2e.Pulumi()

	name, err := tool.ArchiveName(fakeVersion)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	sum := sha256.Sum256(archive)

	var (
		downloads, checksums atomic.Int32
		secondChecksum       = make(chan struct{})
	)

	cache := filepath.Join(t.TempDir(), "cache")
	second := make(chan error, 1)

	var client *http.Client

	ctx := t.Context()

	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) {
		if downloads.Add(1) == 1 {
			go func(ctx context.Context) {
				fresh := &e2e.Fetcher{Client: client, CacheDir: cache}
				_, installErr := fresh.Install(ctx, tool, fakeVersion)
				second <- installErr
			}(ctx)

			<-secondChecksum
		}

		serveFull(t, w, archive)
	})
	mux.HandleFunc("/pulumi-"+fakeVersion+"-checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		if _, writeErr := fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name); writeErr != nil {
			t.Errorf("serve checksums: %v", writeErr)
		}

		if checksums.Add(1) == 2 {
			close(secondChecksum)
		}
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client = ts.Client()
	tool.ReleaseURL = ts.URL + "/%[2]s"

	first := &e2e.Fetcher{Client: client, CacheDir: cache}

	inst, err := first.Install(t.Context(), tool, fakeVersion)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}

	if err := <-second; err != nil {
		t.Fatalf("second install: %v", err)
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}

	if _, err := os.Stat(inst.Binary()); err != nil {
		t.Errorf("binary missing after the second install: %v", err)
	}
}

// TestFetchChecksEntryOnceUnderTheLock runs two Installs of one Fetcher at the
// same time. Both find no record of a check before they take the lock of the
// entry. The second one must use the check of the first one and not check the
// entry again.
func TestFetchChecksEntryOnceUnderTheLock(t *testing.T) {
	t.Parallel()

	tests := []entryOnceCase{
		{name: "a filled cache", filled: true},
		{name: "an empty cache", filled: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkEntryOnceCase(t, tc)
		})
	}
}

// entryOnceCase is a case of TestFetchChecksEntryOnceUnderTheLock. With
// filled, another Fetcher fills the cache first.
type entryOnceCase struct {
	name   string
	filled bool
}

// checkEntryOnceCase holds both Installs before the lock until each one has
// found no record of a check.
func checkEntryOnceCase(t *testing.T, tc entryOnceCase) {
	t.Helper()

	filler, tool, downloads := release(t, validArchive(t), "")

	if tc.filled {
		if _, err := filler.Install(t.Context(), tool, fakeVersion); err != nil {
			t.Fatalf("fill the cache: %v", err)
		}
	}

	fetcher := &e2e.Fetcher{Client: filler.Client, CacheDir: filler.CacheDir}

	var arrived sync.WaitGroup

	arrived.Add(2)
	e2e.SetBeforeLock(fetcher, func() {
		arrived.Done()
		arrived.Wait()
	})

	errs := make(chan error, 2)
	for range 2 {
		go func(ctx context.Context) {
			_, err := fetcher.Install(ctx, tool, fakeVersion)
			errs <- err
		}(t.Context())
	}

	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("install: %v", err)
		}
	}

	if got := e2e.CacheChecks(fetcher); got != 1 {
		t.Errorf("cache entry checked %d times, want 1", got)
	}

	if got := downloads.Load(); got != 1 {
		t.Errorf("archive downloaded %d times, want 1", got)
	}
}
