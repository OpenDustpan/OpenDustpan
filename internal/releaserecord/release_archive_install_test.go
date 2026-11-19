package releaserecord_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// installTestVersion is the version of the archives that the install tests
// build. The release record of the copy of the tree lists it.
const installTestVersion = "v9.9.8"

// hostPlatform is the platform that one archive of a release is for.
type hostPlatform struct {
	os   string
	arch string
}

// hostArchivePlatform answers the platform of the host, or skips the test when
// the release has no archive for it.
func hostArchivePlatform(t *testing.T) hostPlatform {
	t.Helper()

	if testing.Short() {
		t.Skip("the test builds four platforms")
	}

	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skipf("the release has no archive for %s", runtime.GOOS)
	}

	switch runtime.GOARCH {
	case "amd64", "arm64":
	default:
		t.Skipf("the release has no archive for %s", runtime.GOARCH)
	}

	return hostPlatform{os: runtime.GOOS, arch: runtime.GOARCH}
}

// archiveName answers the name of the archive of one platform.
func (p hostPlatform) archiveName() string {
	return "opendustpan_" + installTestVersion + "_" + p.os + "_" + p.arch + ".tar.gz"
}

// buildArchiveSet builds the release archives of the test version in a copy of
// the work tree, checks them with release-verify.sh and answers the output
// directory.
func buildArchiveSet(t *testing.T) string {
	t.Helper()

	tree := copyWorkTree(t)
	extendRecord(t, tree, installTestVersion)

	outdir := filepath.Join(t.TempDir(), "set")

	for _, step := range []string{"build", "verify"} {
		if out, err := runInTree(t, tree, step, installTestVersion, outdir); err != nil {
			t.Fatalf("release-%s.sh: %v\n%s", step, err, out)
		}
	}

	return outdir
}

// installDirectory unpacks the archive of the host from the set into a new
// directory, after the checksum step, and answers the directory.
func installDirectory(t *testing.T, set, name string) string {
	t.Helper()

	archive := readIn(t, set, name)
	if err := verifyArchiveSum(readIn(t, set, "SHA256SUMS"), name, archive); err != nil {
		t.Fatalf("checksum step: %v", err)
	}

	dir := t.TempDir()

	root := openRootOf(t, dir)
	defer closeRoot(t, root)

	if err := unpackArchive(root, archive); err != nil {
		t.Fatalf("unpack step: %v", err)
	}

	return dir
}

// freePort answers a loopback port that no process holds now.
func freePort(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	if err = l.Close(); err != nil {
		t.Fatal(err)
	}

	return port
}

// binaryVersion runs the unpacked binary with --version. The command takes an
// argument list, so no argument passes through a shell.
func binaryVersion(t *testing.T, dir string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "./opendustpan", "--version")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("opendustpan --version: %v", err)
	}

	return strings.TrimSpace(string(out))
}

// randomText answers n random bytes in the encoding that encode names.
func randomText(t *testing.T, n int, encode func([]byte) string) string {
	t.Helper()

	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}

	return encode(raw)
}

// serveInstance is a running server: its loopback port and the directory of
// its log file serve.log.
type serveInstance struct {
	port   string
	logDir string
}

// startServe starts serve of the unpacked binary on a free loopback port, with
// a generated key, a SQLite database in a temporary directory and an admin
// token. It answers the port and the directory of the log file serve.log.
// Cleanup stops the process.
func startServe(t *testing.T, dir string) serveInstance {
	t.Helper()

	port := freePort(t)
	logDir := t.TempDir()

	logRoot := openRootOf(t, logDir)
	defer closeRoot(t, logRoot)

	logFile, err := logRoot.Create("serve.log")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if closeErr := logFile.Close(); closeErr != nil {
			t.Logf("close the server log: %v", closeErr)
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, "./opendustpan", "serve")
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir,
		"DUSTPAN_LISTEN=127.0.0.1:" + port,
		"DUSTPAN_DATABASE=sqlite:" + filepath.Join(t.TempDir(), "dustpan.db"),
		"DUSTPAN_KEY_ENCRYPTION_KEY=" + randomText(t, 32, base64.StdEncoding.EncodeToString),
		"DUSTPAN_ADMIN_TOKEN=" + randomText(t, 32, hex.EncodeToString),
	}

	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cancel()

		if waitErr := cmd.Wait(); waitErr != nil {
			t.Logf("serve stopped: %v", waitErr)
		}
	})

	return serveInstance{port: port, logDir: logDir}
}

// readyStatus asks GET /health/ready once and answers the status, or 0.
func readyStatus(t *testing.T, client *http.Client, port string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"http://127.0.0.1:"+port+"/health/ready",
		http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0
	}

	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode
}

// waitReady polls GET /health/ready until it answers 200 or the time ends.
func waitReady(t *testing.T, server serveInstance) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	for readyStatus(t, client, server.port) != http.StatusOK {
		pause := time.NewTimer(200 * time.Millisecond)

		select {
		case <-ctx.Done():
			pause.Stop()
			t.Fatalf(
				"GET /health/ready did not answer 200 in time; server output:\n%s",
				readIn(t, server.logDir, "serve.log"),
			)
		case <-pause.C:
		}
	}
}

// TestReleaseArchiveInstallStartsTheServer builds the archives of a test
// version, checks the archive of the host against SHA256SUMS, unpacks it,
// reads --version, starts serve on loopback and reads GET /health/ready.
func TestReleaseArchiveInstallStartsTheServer(t *testing.T) {
	t.Parallel()

	platform := hostArchivePlatform(t)
	dir := installDirectory(t, buildArchiveSet(t), platform.archiveName())

	if got := binaryVersion(t, dir); got != installTestVersion {
		t.Fatalf("--version printed %q, want %q", got, installTestVersion)
	}

	waitReady(t, startServe(t, dir))
}

// syntheticArchive makes a small valid archive and the SHA256SUMS line for it
// in a new directory. It answers the directory and the archive bytes.
func syntheticArchive(t *testing.T, name string) (string, []byte) {
	t.Helper()

	var raw bytes.Buffer

	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)

	for _, file := range []struct{ name, body string }{
		{"LICENSE", "license\n"}, {"README.md", "readme\n"}, {"opendustpan", "binary\n"},
	} {
		hdr := &tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if _, err := tw.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	root := openRootOf(t, dir)

	defer closeRoot(t, root)

	sum := sha256.Sum256(raw.Bytes())
	if err := root.WriteFile("SHA256SUMS", []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(name, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir, raw.Bytes()
}

// installGuide answers the text of the install page.
func installGuide(t *testing.T) string {
	t.Helper()

	root := openRootOf(t, "../..")
	defer closeRoot(t, root)

	raw, err := root.ReadFile("docs/guides/install.md")
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

// archiveSection answers the text of the section "Install from a binary
// archive" of the install page, up to the next section.
func archiveSection(t *testing.T) string {
	t.Helper()

	_, rest, found := strings.Cut(installGuide(t), "\n## Install from a binary archive\n")
	if !found {
		t.Fatal("the install page has no section \"Install from a binary archive\"")
	}

	section, _, _ := strings.Cut(rest, "\n## ")

	return section
}

// TestInstallGuideArchiveSectionPromisesNoDownload checks that the install
// page says that binary archives are not available yet, and that it holds no
// shell block and no download address for an archive: no release exists, and
// the release workflow uploads no archive.
func TestInstallGuideArchiveSectionPromisesNoDownload(t *testing.T) {
	t.Parallel()

	section := archiveSection(t)

	if !strings.Contains(section, "not available yet") {
		t.Error("the archive section does not say that binary archives are not available yet")
	}

	for _, banned := range []string{"```sh", "releases/download", "curl", "tar -xzf", "sha256sum"} {
		if strings.Contains(section, banned) {
			t.Errorf("the archive section holds %q: the page must not promise a download", banned)
		}
	}

	for _, want := range []string{"quickstart.md", "build-image-local.sh"} {
		if !strings.Contains(section, want) {
			t.Errorf("the archive section does not point to %q, which works today", want)
		}
	}

	if strings.Contains(installGuide(t), "releases/download") {
		t.Error("the install page holds a release download address")
	}
}

// refuseChecksumCase sends one hostile pair of a SHA256SUMS file and an
// archive to the checksum step. The archive is a valid archive, so only the
// checksum can refuse it.
func refuseChecksumCase(t *testing.T, label, name string, tc checksumCase) {
	t.Helper()

	if err := verifyArchiveSum(tc.sums, name, tc.archive); err == nil {
		t.Errorf("verifyArchiveSum accepted the case %q", label)
	}
}

// checksumCase is one hostile pair of a SHA256SUMS file and an archive.
type checksumCase struct {
	sums    []byte
	archive []byte
}

// TestReleaseArchiveInstallRefusesAWrongChecksum sends a damaged archive and
// hostile SHA256SUMS files to the checksum step. The unpack step is not used.
func TestReleaseArchiveInstallRefusesAWrongChecksum(t *testing.T) {
	t.Parallel()

	platform := hostPlatform{os: runtime.GOOS, arch: runtime.GOARCH}
	name := platform.archiveName()

	dir, archive := syntheticArchive(t, name)
	sums := readIn(t, dir, "SHA256SUMS")

	flipped := bytes.Clone(archive)
	flipped[len(flipped)/2] ^= 0x01

	good := strings.TrimSpace(string(sums))
	bad := strings.Repeat("0", 64) + "  " + name

	cases := map[string]checksumCase{
		"flipped byte":           {sums, flipped},
		"line of another name":   {[]byte(strings.Replace(string(sums), name, "other.tar.gz", 1)), archive},
		"short hash":             {[]byte("abcd  " + name + "\n"), archive},
		"empty file":             {nil, archive},
		"hash of other bytes":    {[]byte(bad + "\n"), archive},
		"good line then bad":     {[]byte(good + "\n" + bad + "\n"), archive},
		"bad line then good":     {[]byte(bad + "\n" + good + "\n"), archive},
		"two good lines":         {[]byte(good + "\n" + good + "\n"), archive},
		"line with no separator": {[]byte(good + "\nnotaline\n"), archive},
		"single-space line":      {[]byte(strings.Replace(good, "  ", " ", 1) + "\n"), archive},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			refuseChecksumCase(t, label, name, tc)
		})
	}

	t.Run("good set", func(t *testing.T) {
		t.Parallel()

		if err := verifyArchiveSum(sums, name, archive); err != nil {
			t.Errorf("verifyArchiveSum refused a good set: %v", err)
		}
	})
}
