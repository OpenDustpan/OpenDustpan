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
	"strconv"
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

	if len(entries) != 0 {
		t.Errorf("cache holds %d entries after a rejected download, want none", len(entries))
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

	if len(entries) != 0 {
		t.Errorf("cache holds %d entries after a rejected download, want none", len(entries))
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
