package run_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/run"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

const (
	server  = "https://dustpan.example.com"
	handoff = "dph_0123456789abcdefSECRET"
	// helperVar makes the test binary run as a step program.
	helperVar = "LAUNCHER_TEST_HELPER"
)

// stepProgram is the test binary as a step program. It writes to the
// standard output, which is the log of the step.
//   - spawn: start a child that sleeps, print its process ID and end.
//   - spawn-wait: the same, then wait until a signal ends it.
//   - names: print the names of the variables of the environment, never
//     their values.
func stepProgram(mode string) {
	switch mode {
	case "spawn", "spawn-wait":
		child := exec.CommandContext(context.Background(), "sleep", "300")
		if err := child.Start(); err != nil {
			return
		}

		if _, err := fmt.Fprintf(os.Stdout, "child=%d\n", child.Process.Pid); err != nil {
			return
		}

		if mode == "spawn-wait" {
			<-time.After(5 * time.Minute)
		}
	case "names":
		var names []string

		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			names = append(names, name)
		}

		slices.Sort(names)

		if _, err := fmt.Fprintln(os.Stdout, strings.Join(names, " ")); err != nil {
			return
		}
	default:
	}
}

// fakeServer is the session side of the server, in memory.
type fakeServer struct {
	mu         sync.Mutex
	version    int
	assignment protocol.Assignment
	// openAnswer replaces the answer to the exchange when it is not nil.
	openAnswer *session.Response
	// renew answers the renewal number n, from 1. Nil answers 200.
	renew func(n int) *session.Response
	// begin runs when the launcher begins a step, before the answer. It runs
	// without the lock, so that a renewal is answered while it waits. A
	// non-nil answer replaces the default answer.
	begin func(name string) *session.Response
	// finishAnswer replaces the answer to the finish when it is not nil.
	finishAnswer *session.Response
	renewals     int
	begun        []string
	logs         map[string][][]byte
	ends         map[string]protocol.StepEnd
	finish       *protocol.Finish
	finishes     int
}

func newFake(steps ...protocol.Step) *fakeServer {
	return &fakeServer{
		mu: sync.Mutex{}, version: 1,
		assignment: protocol.Assignment{
			Namespace: "acme", Unit: "opentofu/acme/net", Tool: "opentofu", Job: "job-1", Phase: "plan", Sweep: "",
			ServerURL: server, Source: nil, Steps: steps, Plan: nil, GitCredentials: "none", GitHelper: "",
			Profile: "",
		},
		openAnswer: nil, renew: nil, begin: nil, finishAnswer: nil, renewals: 0, begun: nil,
		logs: map[string][][]byte{}, ends: map[string]protocol.StepEnd{}, finish: nil, finishes: 0,
	}
}

// answer returns an answer with the status and the JSON of v, or no body
// for nil.
func answer[T any](status int, v *T) *session.Response {
	var body []byte

	if v != nil {
		var err error

		if body, err = json.Marshal(v); err != nil {
			panic(err)
		}
	}

	return &session.Response{Status: status, Body: body, RetryAfter: 0}
}

// noBody is the answer 204.
func noBody() *session.Response {
	return &session.Response{Status: 204, Body: nil, RetryAfter: 0}
}

func refusal(status int, code string) *session.Response {
	return answer(status, &protocol.Error{Code: code, Message: "refused", Supported: ""})
}

// beginName returns the step name of a begin call, and false for another
// call.
func beginName(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/runner/v1/session/steps/")
	if !ok {
		return "", false
	}

	name, action, _ := strings.Cut(rest, "/")

	return name, action == "begin"
}

// Do implements session.Transport.
func (f *fakeServer) Do(_ context.Context, req *session.Request) (*session.Response, error) {
	if resp := f.runBegin(req); resp != nil {
		return resp, nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch req.Path {
	case protocol.PathSession:
		if f.openAnswer != nil {
			return f.openAnswer, nil
		}

		return answer(200, &protocol.SessionResponse{
			Session: "ses-1", ExpiresAt: time.Now(), Lease: protocol.FromStd(time.Minute),
			AssignmentVersion: f.version, Assignment: f.assignment,
		}), nil
	case protocol.PathSessionRenew:
		f.renewals++
		if f.renew != nil {
			if a := f.renew(f.renewals); a != nil {
				return a, nil
			}
		}

		return answer(200, &protocol.RenewResponse{ExpiresAt: time.Now(), Canceled: false, SessionOpen: false}), nil
	case protocol.PathSessionFinish:
		f.finishes++
		if f.finishAnswer != nil {
			return f.finishAnswer, nil
		}

		var fin protocol.Finish
		if !decode(req.Body, &fin) {
			return refusal(400, protocol.ErrorInvalid), nil
		}

		f.finish = &fin

		return noBody(), nil
	default:
		return f.step(req)
	}
}

// runBegin runs the begin hook for req and returns its answer, or nil when
// there is no hook and no answer. The hook runs without the lock, so that a
// renewal is answered while it waits.
func (f *fakeServer) runBegin(req *session.Request) *session.Response {
	name, ok := beginName(req.Path)
	if !ok {
		return nil
	}

	f.mu.Lock()
	hook := f.begin
	f.mu.Unlock()

	if hook == nil {
		return nil
	}

	return hook(name)
}

// step answers the calls of a step.
func (f *fakeServer) step(req *session.Request) (*session.Response, error) {
	rest, ok := strings.CutPrefix(req.Path, "/runner/v1/session/steps/")
	if !ok {
		return refusal(404, protocol.ErrorNotFound), nil
	}

	name, action, _ := strings.Cut(rest, "/")

	switch {
	case action == "begin":
		f.begun = append(f.begun, name)
	case action == "end":
		var end protocol.StepEnd
		if !decode(req.Body, &end) {
			return refusal(400, protocol.ErrorInvalid), nil
		}

		f.ends[name] = end
	case strings.HasPrefix(action, "logs/"):
		seq, ok := chunkNumber(action)
		if !ok || seq != len(f.logs[name]) {
			return refusal(400, protocol.ErrorInvalid), nil
		}

		f.logs[name] = append(f.logs[name], bytes.Clone(req.Body))
	default:
		return refusal(404, protocol.ErrorNotFound), nil
	}

	return noBody(), nil
}

// log returns the stored log of the step.
func (f *fakeServer) log(step string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return string(bytes.Join(f.logs[step], nil))
}

// lockedBuffer is a buffer that the logger of a run writes to.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write the test log: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// runConfig is the configuration of a run and the buffer of its messages.
type runConfig struct {
	*run.Config

	messages *lockedBuffer
}

// config returns the configuration of a run against f.
func config(t *testing.T, f *fakeServer) runConfig {
	t.Helper()

	messages := &lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	return runConfig{messages: messages, Config: &run.Config{
		Client:  &session.Client{Server: server, Transport: f, Release: "0.0.1", Now: nil},
		Handoff: handoff, WorkDir: t.TempDir(), Path: os.Getenv("PATH"), Stop: nil,
		Logger:     slog.New(slog.NewTextHandler(messages, nil)),
		Grace:      func(bool) time.Duration { return 2 * time.Second },
		RenewEvery: 50 * time.Millisecond, ChunkSize: 0, FlushEvery: 50 * time.Millisecond, Retry: time.Millisecond,
		Sleep: nil,
	}}
}

// step returns a step of the role hook with args.
func step(name string, args ...string) protocol.Step {
	return protocol.Step{
		Name: name, Role: "hook", Args: args, Env: nil, Dir: "", Timeout: 0, AllowFailure: false,
		ChangesResources: false, Outputs: nil,
	}
}

// selfStep returns a step that runs the test binary as the step program
// mode.
func selfStep(t *testing.T, name, mode string) protocol.Step {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	s := step(name, exe)
	s.Env = map[string]string{helperVar: mode}

	return s
}

// childOf returns the process ID that the step program printed.
func childOf(t *testing.T, log string) int {
	t.Helper()

	_, rest, ok := strings.Cut(log, "child=")
	if !ok {
		t.Fatalf("the log %q has no child", log)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0]))
	if err != nil {
		t.Fatalf("child pid in %q: %v", log, err)
	}

	return pid
}

// wantGone fails the test when the process pid still runs after a few
// seconds. A zombie counts as ended.
func wantGone(t *testing.T, pid int) {
	t.Helper()

	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for range 100 {
		if gone(pid) {
			return
		}

		<-tick.C
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Errorf("kill the process %d: %v", pid, err)
	}

	t.Fatalf("the process %d of the step still runs", pid)
}

func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}

	if runtime.GOOS != "linux" {
		return false
	}

	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}

	// The state follows the name in parentheses.
	_, rest, _ := strings.Cut(string(stat), ") ")

	return strings.HasPrefix(rest, "Z") || strings.HasPrefix(rest, "X")
}

// sum returns the hex SHA-256 of s.
func sum(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

// wantExit runs cfg and fails the test when the exit code is not want or
// when a message of the launcher holds the handoff token.
func wantExit(t *testing.T, cfg runConfig, want launcher.ExitCode) {
	t.Helper()

	got := run.Run(t.Context(), cfg.Config)

	messages := cfg.messages.String()
	if got != want {
		t.Fatalf("Run = %d, want %d; messages: %s", got, want, messages)
	}

	if strings.Contains(messages, handoff) {
		t.Fatal("the messages of the launcher hold the handoff token")
	}
}

// decode reports whether body decodes into v.
func decode[T any](body []byte, v *T) bool {
	return json.Unmarshal(body, v) == nil
}

// chunkNumber returns the chunk number of the action "logs/{seq}".
func chunkNumber(action string) (int, bool) {
	seq, err := strconv.Atoi(strings.TrimPrefix(action, "logs/"))

	return seq, err == nil
}
