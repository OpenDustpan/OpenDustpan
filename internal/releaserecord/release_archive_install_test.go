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

// binaryVersion runs the unpacked binary with --version. The path goes in the
// environment, so that the command line holds no variable, and no argument
// passes through a shell.
func binaryVersion(t *testing.T, dir string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", `exec "$SERVER_BIN" --version`)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "SERVER_BIN=" + filepath.Join(dir, "opendustpan")}

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
	cmd := exec.CommandContext(
		ctx,
		"sh",
		"-c",
		`exec "$SERVER_BIN" serve --listen "$SERVER_LISTEN" --database "$SERVER_DATABASE"`,
	)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir,
		"SERVER_BIN=" + filepath.Join(dir, "opendustpan"),
		"SERVER_LISTEN=127.0.0.1:" + port,
		"SERVER_DATABASE=sqlite:" + filepath.Join(t.TempDir(), "dustpan.db"),
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

// guideBlocks answers the sh blocks of one section of the install page.
func guideBlocks(t *testing.T, heading string) []string {
	t.Helper()

	root := openRootOf(t, "../..")
	defer closeRoot(t, root)

	raw, err := root.ReadFile("docs/guides/install.md")
	if err != nil {
		t.Fatal(err)
	}

	blocks := []string{}
	current := []string{}
	inside, fenced := false, false

	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			inside, fenced = line == "## "+heading, false
		case !inside:
		case line == "```sh":
			fenced, current = true, current[:0]
		case line == "```" && fenced:
			fenced = false
			blocks = append(blocks, strings.Join(current, "\n")+"\n")
		case fenced:
			current = append(current, line)
		default:
		}
	}

	if len(blocks) == 0 {
		t.Fatalf("the install page has no section %q with sh blocks", heading)
	}

	return blocks
}

// guideBlockWith answers the one block that holds want.
func guideBlockWith(t *testing.T, blocks []string, want string) string {
	t.Helper()

	var found []string

	for _, block := range blocks {
		if strings.Contains(block, want) {
			found = append(found, block)
		}
	}

	if len(found) != 1 {
		t.Fatalf("the section has %d blocks with %q, want 1", len(found), want)
	}

	return found[0]
}

// runGuideBlock runs one block of the page with sh in dir, with the
// placeholders replaced. On a host with no sha256sum, it uses the command that
// the page names for macOS. The text of the block goes in the environment.
func runGuideBlock(t *testing.T, dir, block string, platform hostPlatform) (string, error) {
	t.Helper()

	script := strings.NewReplacer("<version>", installTestVersion, "<os>", platform.os, "<arch>", platform.arch).
		Replace(block)

	if _, err := exec.LookPath("sha256sum"); err != nil {
		script = strings.ReplaceAll(script, "sha256sum -c -", "shasum -a 256 -c -")
	}

	cmd := exec.CommandContext(t.Context(), "sh", "-e", "-c", `eval "$GUIDE_BLOCK"`)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GUIDE_BLOCK=" + script}

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// checksumCase is one hostile pair of a SHA256SUMS file and an archive.
type checksumCase struct {
	sums    []byte
	archive []byte
}

// refuseChecksumCase sends one case to the checksum step alone and to the
// documented check command alone. The archive is a valid archive, so only the
// checksum can refuse it.
func refuseChecksumCase(t *testing.T, label, name, check string, tc checksumCase) {
	t.Helper()

	if err := verifyArchiveSum(tc.sums, name, tc.archive); err == nil {
		t.Errorf("verifyArchiveSum accepted the case %q", label)
	}

	work := t.TempDir()
	root := openRootOf(t, work)

	defer closeRoot(t, root)

	if err := root.WriteFile("SHA256SUMS", tc.sums, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := root.WriteFile(name, tc.archive, 0o600); err != nil {
		t.Fatal(err)
	}

	platform := hostPlatform{os: runtime.GOOS, arch: runtime.GOARCH}
	if out, err := runGuideBlock(t, work, check, platform); err == nil {
		t.Errorf("the documented check command accepted the case %q:\n%s", label, out)
	}
}

// TestReleaseArchiveInstallRefusesAWrongChecksum sends a damaged archive and
// hostile SHA256SUMS files to the checksum step and to the documented check
// command, each on its own. The unpack step is not used.
func TestReleaseArchiveInstallRefusesAWrongChecksum(t *testing.T) {
	t.Parallel()

	platform := hostPlatform{os: runtime.GOOS, arch: runtime.GOARCH}
	name := platform.archiveName()
	check := guideBlockWith(t, guideBlocks(t, "Install from a binary archive"), "sha256sum -c")

	dir, archive := syntheticArchive(t, name)
	sums := readIn(t, dir, "SHA256SUMS")

	flipped := bytes.Clone(archive)
	flipped[len(flipped)/2] ^= 0x01

	cases := map[string]checksumCase{
		"flipped byte":         {sums, flipped},
		"line of another name": {[]byte(strings.Replace(string(sums), name, "other.tar.gz", 1)), archive},
		"short hash":           {[]byte("abcd  " + name + "\n"), archive},
		"empty file":           {nil, archive},
		"hash of other bytes":  {[]byte(strings.Repeat("0", 64) + "  " + name + "\n"), archive},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			refuseChecksumCase(t, label, name, check, tc)
		})
	}

	t.Run("good set", func(t *testing.T) {
		t.Parallel()

		if err := verifyArchiveSum(sums, name, archive); err != nil {
			t.Errorf("verifyArchiveSum refused a good set: %v", err)
		}

		if out, err := runGuideBlock(t, dir, check, platform); err != nil {
			t.Errorf("the documented check command refused a good set: %v\n%s", err, out)
		}
	})
}

// TestInstallGuideArchiveBlocksRun takes the check block and the unpack block
// of the section "Install from a binary archive" from the install page,
// replaces the placeholders and runs them with sh in the directory of a built
// set. Then it runs the unpacked binary.
func TestInstallGuideArchiveBlocksRun(t *testing.T) {
	t.Parallel()

	platform := hostArchivePlatform(t)
	blocks := guideBlocks(t, "Install from a binary archive")
	set := buildArchiveSet(t)

	for _, want := range []string{"sha256sum -c", "tar -xzf"} {
		block := guideBlockWith(t, blocks, want)
		if out, err := runGuideBlock(t, set, block, platform); err != nil {
			t.Fatalf("the block with %q failed: %v\n%s", want, err, out)
		}
	}

	dir := filepath.Join(set, "opendustpan-"+installTestVersion)
	if got := binaryVersion(t, dir); got != installTestVersion {
		t.Fatalf("the unpacked binary printed %q for --version, want %q", got, installTestVersion)
	}
}
