//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// devBackupFlag is the flag that lets a production installation restore a
// backup of a dev installation.
const devBackupFlag = "--unsafe-allow-restoring-dev-installation-backups-into-production"

// TestDevBackupIntoProduction proves ADR 253 with the real binary. It signs a
// browser in to a dev installation, takes a backup of the dev database, and
// restores the backup into a production installation. Without the flag the
// start of the restore is refused with status 409, also for a check-only
// restore. With the flag the restore removes the dev person, warns, writes the
// audit entries, and the session of the dev person is refused.
func TestDevBackupIntoProduction(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the dev e2e step needs Linux (a pseudo terminal), not %s", runtime.GOOS)
	}

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("the dev e2e step needs the script utility for a pseudo terminal")
	}

	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")

	cookie := signInToDev(t, bin)
	targetDir := filepath.Join(t.TempDir(), "backups")
	backupID := backupDev(t, cookie.dir, targetDir)

	// The two steps share one production installation, one after the other.
	prod := t.TempDir()

	fix := devBackupFixture{bin: bin, prod: prod, targetDir: targetDir, backupID: backupID, session: cookie.session}

	checkRefusedWithoutFlag(t, fix)
	checkAllowedWithFlag(t, fix)
}

// devBackupFixture holds what the two steps of the test share.
type devBackupFixture struct {
	bin       string
	prod      string
	targetDir string
	backupID  string
	session   *http.Cookie
}

// checkRefusedWithoutFlag proves that a production installation refuses the
// restore, also a check-only restore, with status 409 and the sentence that
// names the flag.
func checkRefusedWithoutFlag(t *testing.T, fix devBackupFixture) {
	t.Helper()

	server := startProduction(t, fix.bin, fix.prod, fix.targetDir)
	defer server.stop(t)

	for _, body := range []string{
		`{"backupID":"` + fix.backupID + `"}`,
		`{"backupID":"` + fix.backupID + `","checkOnly":true}`,
	} {
		status, resp := post(t, server.url, "/api/server/restores", body)
		if status != http.StatusConflict {
			t.Fatalf("POST restores %s = %d, want 409 (%s)", body, status, resp)
		}

		for _, want := range []string{"production installation", "dev installation", devBackupFlag} {
			if !strings.Contains(resp, want) {
				t.Fatalf("refusal %q does not hold %q", resp, want)
			}
		}
	}
}

// checkAllowedWithFlag proves that, with the flag, the restore removes the dev
// person and refuses the session of that person.
func checkAllowedWithFlag(t *testing.T, fix devBackupFixture) {
	t.Helper()

	server := startProduction(t, fix.bin, fix.prod, fix.targetDir, devBackupFlag)
	defer server.stop(t)

	restoreDevBackup(t, server, fix.backupID)
	checkDevPersonGone(t, server, fix.session)
}

// devSignIn holds what the sign-in of the dev installation gave.
type devSignIn struct {
	dir     string
	session *http.Cookie
}

// signInToDev starts a dev installation, signs in with the code of the first
// start, stops it and answers its data directory and the session cookie.
func signInToDev(t *testing.T, bin string) devSignIn {
	t.Helper()

	server := startPlainDev(t, bin)

	match := devCodePattern.FindStringSubmatch(server.waitBanner(t, "#code="))
	if match == nil {
		t.Fatalf("the first start prints no sign-in link:\n%s", server.out.String())
	}

	status, cookies := devPost(t.Context(), t, server.url, match[1])
	if status != http.StatusNoContent || len(cookies) != 1 {
		t.Fatalf("sign-in: status %d with %d cookies, want 204 and the session cookie", status, len(cookies))
	}

	if err := server.stop(t); err != nil {
		t.Fatalf("stop dev: %v", err)
	}

	return devSignIn{dir: server.dir, session: cookies[0]}
}

// backupDev takes a backup of the database of the dev installation in dir into
// a new backup target directory, the way the server of that database would
// when it had a target. It answers the backup ID.
func backupDev(t *testing.T, dir, targetDir string) string {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(dir, "dev.db"))
	if err != nil {
		t.Fatalf("open the dev database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the dev database: %v", closeErr)
		}
	})

	target, err := fs.Open(targetDir)
	if err != nil {
		t.Fatalf("open the backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close the backup target: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open a blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close the blob store: %v", closeErr)
		}
	})

	service, err := backup.NewService(backup.Config{
		Exporter:    st,
		Kinds:       st,
		Registry:    st,
		Operations:  st.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
		Blobs:       blobs,
		StateOpener: nil,
		Target:      target,
		TargetName:  "dir:" + targetDir,
		Now:         nil,
		Version:     "e2e",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	_, id := backup.MintRestoreIDs()

	if _, runErr := service.Run(t.Context(), id); runErr != nil {
		t.Fatalf("back up the dev database: %v", runErr)
	}

	manifest, err := service.ReadManifest(t.Context(), id)
	if err != nil || manifest.InstallationKind != "dev" {
		t.Fatalf("manifest of the dev backup = %q, %v, want the kind dev", manifest.InstallationKind, err)
	}

	return id.String()
}

// productionServer is a running serve command.
type productionServer struct {
	cmd *exec.Cmd
	out *ptyBuffer
	url string
}

// startProduction starts serve on the database, the blob directory and the key
// file under dir, with the backup target and the extra arguments. It waits
// until the server is ready.
func startProduction(t *testing.T, bin, dir, targetDir string, extra ...string) *productionServer {
	t.Helper()

	addr := freeLoopbackAddr(t)
	base := []string{
		"serve", "--database", "sqlite:" + filepath.Join(dir, "serve.db"),
		"--key-encryption-key-file", filepath.Join(dir, "serve.key"), "--generate-key-encryption-key",
		"--listen", addr, "--blob-dir", filepath.Join(dir, "blobs"), "--backup-target", "dir:" + targetDir,
	}

	args := slices.Concat(base, extra)

	//nolint:gosec // The test runs the binary that it built, with arguments that it chose.
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DUSTPAN_ADMIN_TOKEN=" + serveToken}

	out := newPtyBuffer()
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = devStopLimit

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	server := &productionServer{cmd: cmd, out: out, url: "http://" + addr}

	waitReady(t, server.url, out)

	if slices.Contains(extra, devBackupFlag) &&
		!strings.Contains(out.String(), "restore a backup of a dev installation") {
		t.Fatalf("the start with the flag writes no warning:\n%s", out.String())
	}

	return server
}

// stop ends serve with SIGTERM and checks that it ends clean.
func (s *productionServer) stop(t *testing.T) {
	t.Helper()

	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop serve: %v", err)
	}

	if err := s.cmd.Wait(); err != nil {
		t.Fatalf("serve did not stop clean: %v\n%s", err, s.out.String())
	}
}

// serverRequest is one request to the server. A nil session sends the
// administrator token.
type serverRequest struct {
	method  string
	path    string
	body    string
	session *http.Cookie
}

// get sends a GET request with the administrator token.
func get(t *testing.T, base, path string) (int, string) {
	t.Helper()

	return serverCall(t, base, serverRequest{method: http.MethodGet, path: path, body: "", session: nil})
}

// getAs sends a GET request with the session cookie in place of the token.
func getAs(t *testing.T, base, path string, session *http.Cookie) (int, string) {
	t.Helper()

	return serverCall(t, base, serverRequest{method: http.MethodGet, path: path, body: "", session: session})
}

// post sends a POST request with the administrator token.
func post(t *testing.T, base, path, body string) (int, string) {
	t.Helper()

	return serverCall(t, base, serverRequest{method: http.MethodPost, path: path, body: body, session: nil})
}

// serverCall sends one request with the administrator token, or with the
// session cookie in place of it.
func serverCall(t *testing.T, base string, call serverRequest) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), call.method, base+call.path, strings.NewReader(call.body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if call.session != nil {
		req.AddCookie(&http.Cookie{
			Name: call.session.Name, Value: call.session.Value, Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	} else {
		// The audit API takes the credential of the tool APIs, and the
		// installation API takes a bearer credential.
		scheme := "Bearer "
		if strings.HasPrefix(call.path, "/api/audit") {
			scheme = "token "
		}

		req.Header.Set("Authorization", scheme+serveToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", call.method, call.path, err)
	}

	raw, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read %s %s: %v", call.method, call.path, readErr)
	}

	return resp.StatusCode, string(raw)
}

// restoreWarning is one warning of a restore answer.
type restoreWarning struct {
	Code string `json:"code"`
}

// restoreDevData is the dev data block of a restore answer.
type restoreDevData struct {
	Principals int64 `json:"principals"`
}

// restoreDoc is the part of a restore answer that the test reads.
type restoreDoc struct {
	State    string           `json:"state"`
	Error    string           `json:"error"`
	Warnings []restoreWarning `json:"warnings"`
	DevData  *restoreDevData  `json:"devData"`
}

// startRestore starts a restore of the backup and answers the restore ID.
func startRestore(t *testing.T, server *productionServer, backupID string) string {
	t.Helper()

	status, resp := post(t, server.url, "/api/server/restores", `{"backupID":"`+backupID+`"}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST restores = %d, want 202 (%s)", status, resp)
	}

	var started struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(resp), &started); err != nil || started.ID == "" {
		t.Fatalf("restore start %q: %v", resp, err)
	}

	return started.ID
}

// restoreDevBackup restores the backup, waits until it succeeds and checks the
// warning and the dev data of the answer.
func restoreDevBackup(t *testing.T, server *productionServer, backupID string) {
	t.Helper()

	id := startRestore(t, server, backupID)
	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		_, body := get(t, server.url, "/api/server/restores/"+id)

		var doc restoreDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode restore %q: %v", body, err)
		}

		switch doc.State {
		case "succeeded":
			checkRestoreAnswer(t, doc, body)

			return
		case "checking", "applying":
			select {
			case <-t.Context().Done():
				t.Fatalf("wait for restore %s: %v", id, t.Context().Err())
			case <-time.After(devPollInterval):
			}
		default:
			t.Fatalf("restore ends %q (%s), want succeeded", doc.State, doc.Error)
		}
	}

	t.Fatalf("restore %s never ended", id)
}

// checkRestoreAnswer checks the warning and the removed dev person of the
// answer of a restore that succeeded.
func checkRestoreAnswer(t *testing.T, doc restoreDoc, body string) {
	t.Helper()

	if len(doc.Warnings) == 0 || doc.Warnings[0].Code != "dev-backup-into-production" ||
		doc.DevData == nil || doc.DevData.Principals < 1 {
		t.Fatalf("restore answer %s, want the warning and the removed dev person", body)
	}
}

// checkDevPersonGone checks that the dev person is gone from the restored
// installation, that the audit log holds the entry of the removal, and that
// the session of the dev person is refused.
func checkDevPersonGone(t *testing.T, server *productionServer, session *http.Cookie) {
	t.Helper()

	_, principals := get(t, server.url, "/api/server/principals")
	if strings.Contains(strings.ToLower(principals), "dev:admin") {
		t.Fatalf("principals after the restore still hold dev:admin: %s", principals)
	}

	_, entries := get(t, server.url, "/api/audit/entries?organization=@server")
	if !strings.Contains(entries, "removed dev principal") {
		t.Fatalf("audit entries of @server hold no entry of the removal: %s", entries)
	}

	status, body := getAs(t, server.url, "/api/dustpan/v1/capabilities", session)
	if status == http.StatusOK && strings.Contains(body, "dev:admin") {
		t.Fatalf("the session of dev:admin is valid on the production installation: %s", body)
	}

	status, body = getAs(t, server.url, "/api/server/principals", session)
	if status != http.StatusUnauthorized {
		t.Fatalf("the session of dev:admin reads the principals: status %d, want 401 (%s)", status, body)
	}
}
