//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	prod := t.TempDir()

	t.Run("refused without the flag", func(t *testing.T) {
		server := startProduction(t, bin, prod, targetDir, false)
		defer server.stop(t)

		for _, body := range []string{
			`{"backupID":"` + backupID + `"}`,
			`{"backupID":"` + backupID + `","checkOnly":true}`,
		} {
			status, resp := serverCall(t, server.url, http.MethodPost, "/api/server/restores", body, nil)
			if status != http.StatusConflict {
				t.Fatalf("POST restores %s = %d, want 409 (%s)", body, status, resp)
			}

			for _, want := range []string{"production installation", "dev installation", devBackupFlag} {
				if !strings.Contains(resp, want) {
					t.Fatalf("refusal %q does not hold %q", resp, want)
				}
			}
		}
	})

	t.Run("allowed with the flag", func(t *testing.T) {
		server := startProduction(t, bin, prod, targetDir, true)
		defer server.stop(t)

		restoreDevBackup(t, server, backupID)
		checkDevPersonGone(t, server, cookie.session)
	})
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

	match := devCodePattern.FindStringSubmatch(server.out.String())
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
		Exporter: st, Kinds: st, Registry: st, Blobs: blobs, StateOpener: nil, Target: target,
		TargetName: "dir:" + targetDir, Now: nil, Version: "e2e", MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	_, id := backup.MintRestoreIDs()

	if _, err := service.Run(t.Context(), id); err != nil {
		t.Fatalf("back up the dev database: %v", err)
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
// file under dir, with the backup target, and the flag when allowDev is true.
// It waits until the server is ready.
func startProduction(t *testing.T, bin, dir, targetDir string, allowDev bool) *productionServer {
	t.Helper()

	addr := freeLoopbackAddr(t)
	args := []string{
		"serve", "--database", "sqlite:" + filepath.Join(dir, "serve.db"),
		"--key-encryption-key-file", filepath.Join(dir, "serve.key"), "--generate-key-encryption-key",
		"--listen", addr, "--blob-dir", filepath.Join(dir, "blobs"), "--backup-target", "dir:" + targetDir,
	}

	if allowDev {
		args = append(args, devBackupFlag)
	}

	//nolint:gosec // The test runs the binary that it built, with arguments that it chose.
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DUSTPAN_ADMIN_TOKEN=" + serveToken}

	out := &ptyBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = devStopLimit

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	server := &productionServer{cmd: cmd, out: out, url: "http://" + addr}

	waitReady(t, server.url, out)

	if allowDev && !strings.Contains(out.String(), "restore a backup of a dev installation") {
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

// serverCall sends one request with the administrator token, and an optional
// session cookie in place of it.
func serverCall(t *testing.T, base, method, path, body string, session *http.Cookie) (int, string) {
	t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(t.Context(), method, base+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if session != nil {
		req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	} else {
		// The audit API takes the credential of the tool APIs, and the
		// installation API takes a bearer credential.
		scheme := "Bearer "
		if strings.HasPrefix(path, "/api/audit") {
			scheme = "token "
		}

		req.Header.Set("Authorization", scheme+serveToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	raw := make([]byte, 0, 1<<12)
	buf := make([]byte, 1<<12)

	for {
		n, readErr := resp.Body.Read(buf)
		raw = append(raw, buf[:n]...)

		if readErr != nil {
			break
		}
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	return resp.StatusCode, string(raw)
}

// restoreDevBackup restores the backup, waits until it succeeds and checks the
// warning and the dev data of the answer.
func restoreDevBackup(t *testing.T, server *productionServer, backupID string) {
	t.Helper()

	status, resp := serverCall(t, server.url, http.MethodPost, "/api/server/restores", `{"backupID":"`+backupID+`"}`, nil)
	if status != http.StatusAccepted {
		t.Fatalf("POST restores = %d, want 202 (%s)", status, resp)
	}

	var started struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(resp), &started); err != nil || started.ID == "" {
		t.Fatalf("restore start %q: %v", resp, err)
	}

	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		_, body := serverCall(t, server.url, http.MethodGet, "/api/server/restores/"+started.ID, "", nil)

		var doc struct {
			State    string `json:"state"`
			Error    string `json:"error"`
			Warnings []struct {
				Code string `json:"code"`
			} `json:"warnings"`
			DevData *struct {
				Principals int64 `json:"principals"`
			} `json:"devData"`
		}

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode restore %q: %v", body, err)
		}

		switch doc.State {
		case "succeeded":
			if len(doc.Warnings) == 0 || doc.Warnings[0].Code != "dev-backup-into-production" ||
				doc.DevData == nil || doc.DevData.Principals < 1 {
				t.Fatalf("restore answer %s, want the warning and the removed dev person", body)
			}

			return
		case "checking", "applying":
			time.Sleep(devPollInterval) //nolint:forbidigo // A bounded poll for a restore that the server runs.
		default:
			t.Fatalf("restore ends %q (%s), want succeeded", doc.State, doc.Error)
		}
	}

	t.Fatalf("restore %s never ended", started.ID)
}

// checkDevPersonGone checks that the dev person is gone from the restored
// installation, that the audit log holds the entry of the removal, and that
// the session of the dev person is refused.
func checkDevPersonGone(t *testing.T, server *productionServer, session *http.Cookie) {
	t.Helper()

	_, principals := serverCall(t, server.url, http.MethodGet, "/api/server/principals", "", nil)
	if strings.Contains(strings.ToLower(principals), "dev:admin") {
		t.Fatalf("principals after the restore still hold dev:admin: %s", principals)
	}

	_, entries := serverCall(t, server.url, http.MethodGet, "/api/audit/entries?organization=@server", "", nil)
	if !strings.Contains(entries, "removed dev principal") {
		t.Fatalf("audit entries of @server hold no entry of the removal: %s", entries)
	}

	if status, body := serverCall(t, server.url, http.MethodGet, "/api/dustpan/v1/capabilities", "", session); status == http.StatusOK &&
		strings.Contains(body, "dev:admin") {
		t.Fatalf("the session of dev:admin is valid on the production installation: %s", body)
	}

	status, body := serverCall(t, server.url, http.MethodGet, "/api/server/principals", "", session)
	if status != http.StatusUnauthorized {
		t.Fatalf("the session of dev:admin reads the principals: status %d, want 401 (%s)", status, body)
	}
}
