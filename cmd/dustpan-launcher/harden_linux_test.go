package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// stepCount is the number of steps of the hardening test. The steps fork
// from the threads that run their goroutines: with many steps, a flag on
// one thread only shows as a step without it.
const stepCount = 30

// needMemory skips the test when dir is not on tmpfs. In CI the test fails,
// so that a runner without tmpfs does not pass with no check run.
func needMemory(t *testing.T, dir string) {
	t.Helper()

	if onMemory(dir) {
		return
	}

	if os.Getenv("CI") != "" {
		t.Fatalf("needs %s on tmpfs in CI", dir)
	}

	t.Skipf("needs %s on tmpfs", dir)
}

// sessionServer is the session side of a server, in memory: it opens one
// session with steps, stores the log of each step and the finish.
type sessionServer struct {
	steps []protocol.Step

	mu       sync.Mutex
	logs     map[string][]byte
	finished *protocol.Finish
}

func newSessionServer() *sessionServer {
	return &sessionServer{steps: nil, mu: sync.Mutex{}, logs: map[string][]byte{}, finished: nil}
}

// ServeHTTP answers the calls of the launcher. It checks no proof: the
// session client has its own tests.
func (s *sessionServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.URL.Path == protocol.PathSession:
		s.writeJSON(w, &protocol.SessionResponse{
			Session: "ses-1", ExpiresAt: time.Now().Add(time.Minute), Lease: protocol.FromStd(time.Minute),
			AssignmentVersion: 1,
			Assignment: protocol.Assignment{
				Namespace: "acme", Unit: "", Tool: "", Job: "job-1", Phase: "plan", Sweep: "", ServerURL: "",
				Source: nil, Steps: s.steps, Plan: nil, GitCredentials: "none", GitHelper: "", Profile: "",
			},
		})
	case r.URL.Path == protocol.PathSessionRenew:
		s.writeJSON(
			w,
			&protocol.RenewResponse{ExpiresAt: time.Now().Add(time.Minute), Canceled: false, SessionOpen: false},
		)
	case r.URL.Path == protocol.PathSessionFinish:
		var f protocol.Finish
		if json.Unmarshal(body, &f) == nil {
			s.finished = &f
		}

		w.WriteHeader(http.StatusNoContent)
	case strings.Contains(r.URL.Path, "/logs/"):
		step, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/runner/v1/session/steps/"), "/")
		s.logs[step] = append(s.logs[step], body...)

		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *sessionServer) writeJSON(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if _, err = w.Write(raw); err != nil {
		return
	}
}

// TestEachStepHasNoNewPrivs runs the launcher binary against a fake server
// with many steps. Each step reads its own NoNewPrivs flag, which every
// step must have, whatever thread of the launcher forked it.
func TestEachStepHasNoNewPrivs(t *testing.T) {
	t.Parallel()

	needMemory(t, "/dev/shm")

	grep, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("needs grep")
	}

	srv := newSessionServer()

	for i := range stepCount {
		srv.steps = append(srv.steps, protocol.Step{
			Name: "read-" + strconv.Itoa(i), Role: "hook", Args: []string{grep, "NoNewPrivs", "/proc/self/status"},
			Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		})
	}

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	code, stderr := runAgainst(t, ts, buildLauncher(t))
	if code != int(launcher.ExitOK) {
		t.Fatalf("exit %d, want 0: %s", code, stderr)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()

	if srv.finished == nil || !srv.finished.Succeeded {
		t.Fatalf("finish = %+v, want succeeded", srv.finished)
	}

	for _, s := range srv.steps {
		if got := strings.Join(strings.Fields(string(srv.logs[s.Name])), " "); got != "NoNewPrivs: 1" {
			t.Errorf("step %s: %q, want NoNewPrivs: 1", s.Name, got)
		}
	}
}

// runAgainst runs bin in the mode run against ts, with the handoff token
// on a pipe as descriptor 3 and the files extra from descriptor 4, and
// returns its exit code and its messages.
func runAgainst(t *testing.T, ts *httptest.Server, bin string, extra ...*os.File) (int, string) {
	t.Helper()

	dir := t.TempDir()
	cert := filepath.Join(dir, "server.pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: ts.Certificate().Raw})

	if err := os.WriteFile(cert, pemData, 0o600); err != nil {
		t.Fatalf("write the certificate: %v", err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	if _, err = w.WriteString("dph_" + "0123456789abcdef"); err != nil {
		t.Fatalf("write the handoff: %v", err)
	}

	if err = w.Close(); err != nil {
		t.Fatalf("close the pipe: %v", err)
	}

	var stderr bytes.Buffer

	cmd := command(bin, "run", "--server", ts.URL, "--handoff", "pipe", "--work-dir", filepath.Join(dir, "work"),
		"--secrets-dir", "/dev/shm")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "SSL_CERT_FILE=" + cert}
	cmd.ExtraFiles = append([]*os.File{r}, extra...)
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if closeErr := r.Close(); closeErr != nil {
		t.Errorf("close the read end: %v", closeErr)
	}

	var exit *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exit) {
		t.Fatalf("run the launcher: %v", runErr)
	}

	return cmd.ProcessState.ExitCode(), stderr.String()
}

// TestNoInheritedDescriptorReachesAStep starts the launcher with three
// open files above the handoff pipe, as a runner could leave them. A step
// that lists its own descriptors finds only the standard three and the
// directory that it reads.
func TestNoInheritedDescriptorReachesAStep(t *testing.T) {
	t.Parallel()

	needMemory(t, "/dev/shm")

	ls, err := exec.LookPath("ls")
	if err != nil {
		t.Skip("needs ls")
	}

	extra := make([]*os.File, 0, 3)

	for range 3 {
		f, openErr := os.OpenInRoot(t.TempDir(), ".")
		if openErr != nil {
			t.Fatalf("open: %v", openErr)
		}

		t.Cleanup(func() {
			if closeErr := f.Close(); closeErr != nil {
				t.Errorf("close: %v", closeErr)
			}
		})

		extra = append(extra, f)
	}

	srv := newSessionServer()
	srv.steps = []protocol.Step{{
		Name: "fds", Role: "hook", Args: []string{ls, "-1", "/proc/self/fd"}, Env: nil, Dir: "", Timeout: 0,
		AllowFailure: false, ChangesResources: false, Outputs: nil,
	}}

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	code, stderr := runAgainst(t, ts, buildLauncher(t), extra...)
	if code != int(launcher.ExitOK) {
		t.Fatalf("exit %d, want 0: %s", code, stderr)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()

	// ls reads the directory through the lowest free descriptor, 3.
	if got := strings.Join(strings.Fields(string(srv.logs["fds"])), " "); got != "0 1 2 3" {
		t.Fatalf("descriptors of the step = %q, want 0 1 2 3", got)
	}
}
