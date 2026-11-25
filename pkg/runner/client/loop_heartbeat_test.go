package client_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// noAnswerReason is the reason of the record of a transport that gave no
// answer and no error.
const noAnswerReason = "the transport gave no answer and no error"

// nilOnPath answers the calls to its path with no answer and no error, and
// each other call from srv.
type nilOnPath struct {
	srv  *fakeServer
	path string
}

// Do implements client.Transport.
func (n nilOnPath) Do(ctx context.Context, req *session.Request) (*client.Answer, error) {
	if req.Path == n.path || strings.HasPrefix(req.Path, "/runner/v1/claims/") && n.path == protocol.PathClaimRenew {
		var none *client.Answer

		return none, nil
	}

	return n.srv.Do(ctx, req)
}

// nilLoop returns a loop whose transport answers path with no answer and no
// error, and a log capture.
func nilLoop(t *testing.T, srv *fakeServer, ticks *fakeTicks, path string) (*client.Loop, logCapture) {
	t.Helper()

	logs := newLogCapture()
	cfg := testConfig(t, srv, ticks)
	cfg.Transport = nilOnPath{srv: srv, path: path}
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return l, logs
}

func TestLoopRefusesANilRenewalAnswer(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {ok(work(lease))}})
	l, logs := nilLoop(t, srv, ticks, protocol.PathClaimRenew)

	go ticks.fire(t.Context(), t, runner.RenewEvery(lease))

	res := boundedRun(t, l)
	if !errors.Is(res.stopped, client.ErrInvalid) || !errors.Is(res.run, client.ErrInvalid) {
		t.Errorf("Run = %v, work stopped with %v; want ErrInvalid for both", res.run, res.stopped)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, noAnswerReason) {
		t.Error("no refusal record of the renewal with no answer")
	}
}

func TestLoopRefusesANilHeartbeatAnswer(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {noWork()}})
	l, logs := nilLoop(t, srv, ticks, protocol.PathHeartbeat)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrInvalid) {
		t.Errorf("Run = %v, want ErrInvalid", res.run)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, noAnswerReason) {
		t.Error("no refusal record of the heartbeat with no answer")
	}
}

// endedMessage is the record of a heartbeat answer that ends the loop with
// the code retired or bad-proof.
const endedMessage = "the server does not accept the runner: the loop ends"

func TestLoopEndsOnAHeartbeatAnsweredRetiredOrBadProof(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code   string
		status int
		want   error
	}{
		{protocol.ErrorRetired, http.StatusForbidden, client.ErrRetired},
		{protocol.ErrorBadProof, http.StatusUnauthorized, client.ErrBadProof},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			ticks := newFakeTicks()
			srv := newFakeServer(map[string][]answer{
				protocol.PathPoll:      {noWork()},
				protocol.PathHeartbeat: {refusal(tc.status, tc.code, 0)},
			})
			l, logs := loggedLoop(t, srv, ticks)

			go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

			if res := boundedRun(t, l); !errors.Is(res.run, tc.want) {
				t.Errorf("Run = %v, want %v", res.run, tc.want)
			}

			if !logs.hasAttrs(endedMessage, map[string]string{"error.code": tc.code}) {
				t.Errorf("no record that the loop ends on %s", tc.code)
			}

			if logs.has(slog.LevelWarn, refusedMessage, unknownCodeMessage) {
				t.Errorf("the code %s was logged as a code that the loop does not know", tc.code)
			}
		})
	}
}
