package run_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

func TestRunsTheStepsAndFinishes(t *testing.T) {
	t.Parallel()

	f := newFake(step("say", "echo", "hello", "world"), step("ok", "true"))
	wantExit(t, config(t, f), launcher.ExitOK)

	if strings.Join(f.begun, ",") != "say,ok" {
		t.Fatalf("begun = %v, want say,ok", f.begun)
	}

	if got := f.log("say"); got != "hello world\n" {
		t.Fatalf("log of say = %q", got)
	}

	end := f.ends["say"]
	if end.ExitCode != 0 || end.LogChunks != 1 || end.LogSHA256 != sum("hello world\n") ||
		end.FinishedAt.Before(end.StartedAt) {
		t.Fatalf("end of say = %+v", end)
	}

	if end := f.ends["ok"]; end.LogChunks != 0 || end.LogSHA256 != "" {
		t.Fatalf("end of ok = %+v, want no log", end)
	}

	if f.finish == nil || !f.finish.Succeeded {
		t.Fatalf("finish = %+v, want succeeded", f.finish)
	}
}

func TestAFailedStepFailsThePhase(t *testing.T) {
	t.Parallel()

	allowed := step("may-fail", "false")
	allowed.AllowFailure = true

	cases := []struct {
		name      string
		steps     []protocol.Step
		begun     string
		succeeded bool
		message   string
	}{
		{
			"failure stops",
			[]protocol.Step{step("fail", "false"), step("after", "true")},
			"fail", false,
			"step fail ended with the exit code 1",
		},
		{"allowed failure", []protocol.Step{allowed, step("after", "true")}, "may-fail,after", true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(tc.steps...)
			wantExit(t, config(t, f), launcher.ExitOK)

			if strings.Join(f.begun, ",") != tc.begun {
				t.Fatalf("begun = %v, want %s", f.begun, tc.begun)
			}

			if f.finish == nil || f.finish.Succeeded != tc.succeeded || f.finish.Message != tc.message {
				t.Fatalf("finish = %+v, want %v %q", f.finish, tc.succeeded, tc.message)
			}
		})
	}
}

// TestMasksAcrossChunkBorders sends a log with the handoff token in chunks
// of 3 bytes: no chunk and no join of chunks holds the token.
func TestMasksAcrossChunkBorders(t *testing.T) {
	t.Parallel()

	f := newFake(step("leak", "echo", "token", handoff, "end"))
	cfg := config(t, f)
	cfg.ChunkSize = 3

	wantExit(t, cfg, launcher.ExitOK)

	got := f.log("leak")
	if got != "token "+launcher.MaskText+" end\n" {
		t.Fatalf("log = %q, want the token masked", got)
	}

	end := f.ends["leak"]
	if end.LogChunks != len(f.logs["leak"]) || end.LogChunks < 2 || end.LogSHA256 != sum(got) {
		t.Fatalf("end = %+v with %d chunks, want the count and the hash of the stored log", end, len(f.logs["leak"]))
	}
}

// TestTheStepEnvironment checks that a step gets only the variables of the
// launcher contract, and never the environment of the launcher.
func TestTheStepEnvironment(t *testing.T) {
	t.Parallel()

	f := newFake(selfStep(t, "names", "names"))
	wantExit(t, config(t, f), launcher.ExitOK)

	want := "DUSTPAN_JOB_ID DUSTPAN_NAMESPACE DUSTPAN_PHASE DUSTPAN_SERVER_URL DUSTPAN_STEP DUSTPAN_UNIT HOME LANG " +
		helperVar + " PATH TMPDIR\n"
	// A test binary that was built with -cover prints this line to stderr when
	// it starts as the step program and GOCOVERDIR is not set. The launcher
	// contract hides GOCOVERDIR from the step, so the line is not a variable.
	got := strings.ReplaceAll(f.log("names"), "warning: GOCOVERDIR not set, no coverage data emitted\n", "")
	if got != want {
		t.Fatalf("variables = %q, want %q", got, want)
	}
}

// TestLeftProcessesEnd checks that a process that a step leaves running
// ends with the step.
func TestLeftProcessesEnd(t *testing.T) {
	t.Parallel()

	f := newFake(selfStep(t, "spawn", "spawn"))
	wantExit(t, config(t, f), launcher.ExitOK)

	wantGone(t, childOf(t, f.log("spawn")))

	if f.finish == nil || !f.finish.Succeeded {
		t.Fatalf("finish = %+v, want succeeded", f.finish)
	}
}

func TestACancelStopsTheProcessGroup(t *testing.T) {
	t.Parallel()

	f := newFake(selfStep(t, "wait", "spawn-wait"), step("after", "true"))
	f.renew = func(n int) *session.Response {
		if n < 3 {
			return nil
		}

		return answer(200, &protocol.RenewResponse{ExpiresAt: time.Now(), Canceled: true, SessionOpen: false})
	}

	wantExit(t, config(t, f), launcher.ExitOK)
	wantGone(t, childOf(t, f.log("wait")))

	if f.finish == nil || f.finish.Succeeded || f.finish.Message != "step wait: the job was canceled" {
		t.Fatalf("finish = %+v, want failed by the cancel", f.finish)
	}

	if len(f.begun) != 1 {
		t.Fatalf("begun = %v, want only the canceled step", f.begun)
	}
}

func TestALostClaimKillsTheStep(t *testing.T) {
	t.Parallel()

	f := newFake(selfStep(t, "wait", "spawn-wait"), step("after", "true"))
	f.renew = func(n int) *session.Response {
		if n < 3 {
			return nil
		}

		return refusal(409, protocol.ErrorClaimLost)
	}

	wantExit(t, config(t, f), launcher.ExitClaimLost)
	wantGone(t, childOf(t, f.log("wait")))

	if f.finishes != 0 || len(f.ends) != 0 {
		t.Fatalf("finishes = %d, ends = %v; want no report after a lost claim", f.finishes, f.ends)
	}
}

func TestTheLauncherStopsAStep(t *testing.T) {
	t.Parallel()

	timed := step("slow", "sleep", "30")
	timed.Timeout = protocol.FromStd(200 * time.Millisecond)

	cases := []struct {
		name    string
		step    protocol.Step
		stop    bool
		message string
	}{
		{"timeout", timed, false, "step slow: the step did not end in 200ms"},
		{"stop signal", step("slow", "sleep", "30"), true, "step slow: the launcher got a stop signal"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(tc.step)
			cfg := config(t, f)

			if tc.stop {
				stop := make(chan struct{})
				cfg.Stop = stop

				time.AfterFunc(200*time.Millisecond, func() { close(stop) })
			}

			started := time.Now()
			wantExit(t, cfg, launcher.ExitOK)

			if time.Since(started) > 10*time.Second {
				t.Fatalf("the step ran for %v", time.Since(started))
			}

			if f.finish == nil || f.finish.Succeeded || f.finish.Message != tc.message {
				t.Fatalf("finish = %+v, want %q", f.finish, tc.message)
			}

			if f.ends["slow"].ExitCode != -1 {
				t.Fatalf("end = %+v, want the exit code -1 of a signal", f.ends["slow"])
			}
		})
	}
}
