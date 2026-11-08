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

// TestToolStepCredentialsFailClosed checks that a tool step whose assignment
// names a profile fails closed: this release cannot deliver the credentials
// of the profile, so the step fails the phase before its program starts. A
// tool step with no profile needs no credentials, and it runs.
func TestToolStepCredentialsFailClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		profile string
		refused bool
		reason  string
	}{
		{
			"a profile", "prod", true,
			`this launcher release cannot deliver the credentials of profile "prod"`,
		},
		{
			"a profile with a line feed", "pro\nd", true,
			`this launcher release cannot deliver the credentials of profile "prod"`,
		},
		{
			"a profile with a carriage return", "pro\rd", true,
			`this launcher release cannot deliver the credentials of profile "prod"`,
		},
		{"no profile", "", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "started")

			s := step("plan", "touch", marker)
			s.Role = string(job.RoleTool)

			f := newFake(s)
			f.assignment.Profile = tc.profile
			wantExit(t, config(t, f), launcher.ExitOK)

			if tc.refused {
				checkRefusedToolStep(t, f, marker, tc.reason)
			} else {
				checkToolStepRan(t, f, marker)
			}
		})
	}
}

// checkRefusedToolStep checks the failed phase of a tool step that the
// launcher refused before its program started.
func checkRefusedToolStep(t *testing.T, f *fakeServer, marker, reason string) {
	t.Helper()

	if f.finish == nil || f.finish.Succeeded || f.finish.Message != "step plan: "+reason {
		t.Fatalf("finish = %+v, want failed with %q", f.finish, reason)
	}

	if strings.ContainsAny(f.finish.Message, "\r\n") {
		t.Fatalf("the reason holds CR or LF: %q", f.finish.Message)
	}

	if got := f.log("plan"); got != "dustpan-launcher: "+reason+"\n" {
		t.Fatalf("log = %q, want the note of the refusal", got)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the program of the tool step started")
	}
}

// checkToolStepRan checks that the tool step started and that the phase
// reports the exit code of its program.
func checkToolStepRan(t *testing.T, f *fakeServer, marker string) {
	t.Helper()

	if f.finish == nil || !f.finish.Succeeded {
		t.Fatalf("finish = %+v, want the phase of the tool step to succeed", f.finish)
	}

	if end := f.ends["plan"]; end.ExitCode != 0 {
		t.Fatalf("end = %+v, want the exit code 0", end)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the program of the tool step did not start: %v", err)
	}
}

// TestUnsupportedStepFeaturesFailClosed checks the fail-closed rule for a
// declared output: a step with an output that a later phase of the job
// needs fails the phase before its program starts, because this release
// uploads no output. A step whose declared outputs no later phase reads
// runs, and its log holds the note that this release uploads no output.
func TestUnsupportedStepFeaturesFailClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outputs []protocol.Output
		refused bool
		reason  string
	}{
		{
			"an output that a later phase needs",
			[]protocol.Output{{Kind: "plan-apply", Path: "tfplan", Needed: true}},
			true,
			`this launcher release cannot upload output "plan-apply" that a later phase needs`,
		},
		{
			"an output whose kind holds a line feed",
			[]protocol.Output{{Kind: "plan\napply", Path: "tfplan", Needed: true}},
			true,
			`this launcher release cannot upload output "planapply" that a later phase needs`,
		},
		{
			"an output whose kind holds a carriage return",
			[]protocol.Output{{Kind: "plan\rapply", Path: "tfplan", Needed: true}},
			true,
			`this launcher release cannot upload output "planapply" that a later phase needs`,
		},
		{
			"an output that no later phase needs",
			[]protocol.Output{{Kind: "plan-display", Path: "tfplan.json", Needed: false}},
			false,
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "started")

			s := step("bad", "touch", marker)
			s.Outputs = tc.outputs

			f := newFake(s)
			wantExit(t, config(t, f), launcher.ExitOK)

			if tc.refused {
				checkRefusedOutputStep(t, f, marker, tc.reason)

				return
			}

			checkOutputStepRan(t, f, marker)
		})
	}
}

// checkRefusedOutputStep checks the failed phase of a step that the
// launcher refused because a later phase of the job needs one of its
// outputs.
func checkRefusedOutputStep(t *testing.T, f *fakeServer, marker, reason string) {
	t.Helper()

	if f.finish == nil || f.finish.Succeeded || f.finish.Message != "step bad: "+reason {
		t.Fatalf("finish = %+v, want failed with %q", f.finish, reason)
	}

	if got := f.log("bad"); got != "dustpan-launcher: "+reason+"\n" {
		t.Fatalf("log = %q, want the note of the refusal", got)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the program of the refused step started")
	}
}

// checkOutputStepRan checks that a step whose declared outputs no later
// phase reads started, that the phase reports the exit code 0, and that the
// log holds the note that the launcher uploads no output.
func checkOutputStepRan(t *testing.T, f *fakeServer, marker string) {
	t.Helper()

	if f.finish == nil || !f.finish.Succeeded {
		t.Fatalf("finish = %+v, want the phase of the step to succeed", f.finish)
	}

	if end := f.ends["bad"]; end.ExitCode != 0 {
		t.Fatalf("end = %+v, want the exit code 0", end)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the program of the step did not start: %v", err)
	}

	if !strings.Contains(f.log("bad"), "this launcher release does not upload declared outputs") {
		t.Fatalf("log = %q, want the note that the launcher uploads no output", f.log("bad"))
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
