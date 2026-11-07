package run_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestHostileStepsNeverStart sends steps that each check of the launcher
// refuses, as if the server had stored them. No process starts: the
// program would create the file marker.
func TestHostileStepsNeverStart(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "started")
	touch := []string{"touch", marker}

	withEnv := func(name, value string) protocol.Step {
		s := step("bad", touch...)
		s.Env = map[string]string{name: value}

		return s
	}

	withRole := func(role string) protocol.Step {
		s := step("bad", touch...)
		s.Role = role

		return s
	}

	withDir := func(dir string) protocol.Step {
		s := step("bad", touch...)
		s.Dir = dir

		return s
	}

	cases := []struct {
		name   string
		step   protocol.Step
		reason string
	}{
		{"relative program", step("bad", "./touch", marker), "not a bare name or an absolute path"},
		{"unclean absolute program", step("bad", "/usr/../usr/bin/touch", marker), "not a clean absolute path"},
		{"NUL in an argument", step("bad", "touch", marker+"\x00x"), "NUL byte"},
		{"no program", step("bad"), "no program"},
		{"unknown role", withRole("admin"), "not tool or hook"},
		{"program not on the PATH", step("bad", "no-such-program-here"), "not on the PATH"},
		{"step sets PATH", withEnv("PATH", "/tmp"), "environment of the step is not valid"},
		{"step sets a launcher variable", withEnv("DUSTPAN_JOB_ID", "x"), "environment of the step is not valid"},
		{"step sets a credential", withEnv("AWS_SECRET_ACCESS_KEY", "x"), "environment of the step is not valid"},
		{"directory outside the checkout", withDir("../.."), "not inside the checkout"},
		{"absolute directory", withDir("/etc"), "not inside the checkout"},
		{"missing directory", withDir("net"), "not a directory inside the checkout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(tc.step)
			wantExit(t, config(t, f), launcher.ExitOK)

			if f.finish == nil || f.finish.Succeeded || !strings.Contains(f.finish.Message, tc.reason) {
				t.Fatalf("finish = %+v, want failed with %q", f.finish, tc.reason)
			}

			if !strings.Contains(f.log("bad"), tc.reason) || f.ends["bad"].ExitCode != -1 {
				t.Fatalf("log = %q, end = %+v; want the reason and the exit code -1", f.log("bad"), f.ends["bad"])
			}

			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the program of a refused step started")
			}
		})
	}
}

// TestToolStepCredentialsFailClosed checks that a tool step fails closed:
// this release cannot get the credentials of a tool step, so the step fails
// the phase before its program starts.
func TestToolStepCredentialsFailClosed(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "started")

	s := step("plan", "touch", marker)
	s.Role = string(job.RoleTool)

	f := newFake(s)
	wantExit(t, config(t, f), launcher.ExitOK)

	if f.finish == nil || f.finish.Succeeded ||
		f.finish.Message != "step plan: this launcher release does not run a tool step" {
		t.Fatalf("finish = %+v, want the fail-closed reason of the tool step", f.finish)
	}

	if got := f.log("plan"); got != "dustpan-launcher: this launcher release does not run a tool step\n" {
		t.Fatalf("log = %q, want the note of the refusal", got)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the program of the tool step started")
	}
}

// TestUnsupportedStepFeaturesFailClosed checks that a step this release
// cannot serve fails the phase before its program starts: a tool step (no
// credentials) and a step with a declared output (no upload).
func TestUnsupportedStepFeaturesFailClosed(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "started")

	tool := step("bad", "touch", marker)
	tool.Role = string(job.RoleTool)

	output := step("bad", "touch", marker)
	output.Outputs = []protocol.Output{{Kind: "plan-display", Path: "tfplan.json"}}

	cases := []struct {
		name   string
		step   protocol.Step
		reason string
	}{
		{"tool step", tool, "this launcher release does not run a tool step"},
		{"declared output", output, "this launcher release does not upload declared outputs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(tc.step)
			wantExit(t, config(t, f), launcher.ExitOK)

			if f.finish == nil || f.finish.Succeeded || f.finish.Message != "step bad: "+tc.reason {
				t.Fatalf("finish = %+v, want failed with %q", f.finish, tc.reason)
			}

			if !strings.Contains(f.log("bad"), tc.reason) {
				t.Fatalf("log = %q, want the note of the refusal", f.log("bad"))
			}

			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the program of the refused step started")
			}
		})
	}
}

// cancelDuringBegin sets f to answer every renewal with a cancel, and to
// answer the begin of the step only after the renewal loop recorded the
// cancel.
func cancelDuringBegin(f *fakeServer, _ runConfig) {
	delivered := make(chan struct{})
	f.begin = func(string) *session.Response {
		<-delivered

		return nil
	}
	f.renew = func(n int) *session.Response {
		if n >= 2 {
			select {
			case delivered <- struct{}{}:
			default:
			}
		}

		return answer(200, &protocol.RenewResponse{ExpiresAt: time.Time{}, Canceled: true, SessionOpen: false})
	}
}

// loseClaimAtTheBeginCall sets f to answer the begin call of the step with a
// lost claim. The step ends at that call, before the checks that refuse a
// step before its start run: this case covers the begin call, not the check
// of a lost claim in refuseStep.
func loseClaimAtTheBeginCall(f *fakeServer, _ runConfig) {
	f.begin = func(string) *session.Response {
		return refusal(409, protocol.ErrorClaimLost)
	}
}

// stopBeforeTheRun closes cfg.Stop before the run.
func stopBeforeTheRun(_ *fakeServer, cfg runConfig) {
	stop := make(chan struct{})
	close(stop)
	cfg.Stop = stop
}

// TestStopBeforeProcessStart checks that a cancel, a lost claim or a stop
// that arrives before a step starts stops the phase without starting the
// program.
func TestStopBeforeProcessStart(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "started")

	cases := []struct {
		name    string
		change  func(f *fakeServer, cfg runConfig)
		code    launcher.ExitCode
		message string
	}{
		{
			"cancel during the begin of the step",
			cancelDuringBegin,
			launcher.ExitOK,
			"step bad: the job was canceled",
		},
		{
			"claim loss at the begin call",
			loseClaimAtTheBeginCall,
			launcher.ExitClaimLost,
			"",
		},
		{
			"stop closed before the run",
			stopBeforeTheRun,
			launcher.ExitOK,
			"step bad: the launcher got a stop signal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The program does not exist. A launcher that starts the step
			// anyway fails at the start, so the reason of the phase differs
			// from the refusal and the test fails deterministically.
			f := newFake(step("bad", "dustpan-no-such-program", marker))
			cfg := config(t, f)
			tc.change(f, cfg)

			wantExit(t, cfg, tc.code)

			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the program of the stopped step started")
			}

			if tc.message == "" {
				if f.finishes != 0 {
					t.Fatalf("finishes = %d, want no report after a lost claim", f.finishes)
				}

				return
			}

			if f.finish == nil || f.finish.Succeeded || f.finish.Message != tc.message {
				t.Fatalf("finish = %+v, want failed with %q", f.finish, tc.message)
			}
		})
	}
}

// TestALinkOutOfTheCheckoutIsRefused checks the directory of a step through
// a link that leaves the checkout.
func TestALinkOutOfTheCheckoutIsRefused(t *testing.T) {
	t.Parallel()

	s := step("bad", "true")
	s.Dir = "out"

	f := newFake(s)
	cfg := config(t, f)

	if err := os.MkdirAll(filepath.Join(cfg.WorkDir, "checkout"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.Symlink(t.TempDir(), filepath.Join(cfg.WorkDir, "checkout", "out")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	wantExit(t, cfg, launcher.ExitOK)

	if f.finish == nil || f.finish.Succeeded || !strings.Contains(f.finish.Message, "inside the checkout") {
		t.Fatalf("finish = %+v, want the directory refused", f.finish)
	}
}

// TestAHostileStepNameIsNeverSent checks that a step name that is not valid
// fails the phase before any call of the step.
func TestAHostileStepNameIsNeverSent(t *testing.T) {
	t.Parallel()

	f := newFake(step("../finish", "true"))
	wantExit(t, config(t, f), launcher.ExitOK)

	if len(f.begun) != 0 || f.finish == nil || f.finish.Succeeded {
		t.Fatalf("begun = %v, finish = %+v; want no call of the step and a failed phase", f.begun, f.finish)
	}
}

func TestUnsupportedAssignments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		change  func(f *fakeServer)
		code    launcher.ExitCode
		message string
	}{
		{
			"source", func(f *fakeServer) { f.assignment.Source = source() }, launcher.ExitSource,
			"cannot download a source snapshot",
		},
		{
			"plan",
			func(f *fakeServer) { f.assignment.Plan = &protocol.Plan{ID: "plan-1", Parts: nil} },
			launcher.ExitOK,
			"cannot read the plan",
		},
		{"newer assignment", func(f *fakeServer) { f.version = 2 }, launcher.ExitOK, "the version 2"},
		{"no step", func(f *fakeServer) { f.assignment.Steps = nil }, launcher.ExitOK, "no step"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(step("ok", "true"))
			tc.change(f)
			wantExit(t, config(t, f), tc.code)

			if len(f.begun) != 0 || f.finish == nil || f.finish.Succeeded ||
				!strings.Contains(f.finish.Message, tc.message) {
				t.Fatalf(
					"begun = %v, finish = %+v; want no step and a failed phase with %q",
					f.begun,
					f.finish,
					tc.message,
				)
			}
		})
	}

	f := newFake(step("ok", "true"))
	f.assignment.Sweep = "sweep-1"
	wantExit(t, config(t, f), launcher.ExitInternal)

	if f.finishes != 0 || len(f.begun) != 0 {
		t.Fatal("a sweep began a step or finished")
	}
}

func TestOpenAndReportFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(f *fakeServer)
		token  string
		code   launcher.ExitCode
	}{
		{"handoff not valid", nil, "not-a-token", launcher.ExitHandoff},
		{
			"token refused", func(f *fakeServer) { f.openAnswer = refusal(401, protocol.ErrorTokenUsed) }, handoff,
			launcher.ExitHandoff,
		},
		{
			"release refused", func(f *fakeServer) { f.openAnswer = refusal(426, protocol.ErrorVersionUnsupported) },
			handoff, launcher.ExitVersion,
		},
		{
			"server not available", func(f *fakeServer) { f.openAnswer = refusal(503, protocol.ErrorUnavailable) },
			handoff, launcher.ExitHandoff,
		},
		{
			"finish not stored", func(f *fakeServer) { f.finishAnswer = refusal(503, protocol.ErrorUnavailable) },
			handoff, launcher.ExitUnreported,
		},
		{
			"finish after a lost claim", func(f *fakeServer) { f.finishAnswer = refusal(409, protocol.ErrorClaimLost) },
			handoff, launcher.ExitClaimLost,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(step("ok", "true"))
			if tc.change != nil {
				tc.change(f)
			}

			cfg := config(t, f)
			cfg.Handoff = tc.token
			wantExit(t, cfg, tc.code)
		})
	}
}

func TestARelativeWorkDirIsRefused(t *testing.T) {
	t.Parallel()

	f := newFake(step("ok", "true"))
	cfg := config(t, f)
	cfg.WorkDir = "work"

	wantExit(t, cfg, launcher.ExitSandbox)
}

// source returns the source snapshot of an assignment.
func source() *protocol.Source {
	return &protocol.Source{
		Commit: "abc", ObjectFormat: "sha1", PackSHA256: "", PackSize: 0, Directory: "", Submodules: nil,
		LargeFiles: nil,
	}
}
