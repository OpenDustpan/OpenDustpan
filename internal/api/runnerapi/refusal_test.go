package runnerapi_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// errorCode returns the code of a protocol error body.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()

	var e protocol.Error

	decode(t, body, &e)

	return e.Code
}

// TestRunnerRequestsAreRefused proves each way that a request of a runner
// fails: a proof that does not match, a replay, a skew of the clock or the
// release, and a runner that an administrator retired.
func TestRunnerRequestsAreRefused(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	stranger := e.newRunner("stranger")
	stranger.id = "rn-unknown"

	wait := protocol.PollRequest{Wait: 0, AssignmentVersion: runner.AssignmentVersion}

	tests := []struct {
		name   string
		send   func() (int, []byte)
		status int
		code   string
	}{
		{
			"a proof of another key",
			func() (int, []byte) { return r.callWith(otherKey, protocol.PathPoll, wait) },
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"a proof from the future",
			func() (int, []byte) {
				return r.rawSigned(
					r.priv,
					protocol.PathPoll,
					wait,
					"0.4.0",
					stamp{newNonce(t), time.Now().Add(2 * time.Minute)},
				)
			},
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"a nonce that is too short",
			func() (int, []byte) {
				return r.rawSigned(r.priv, protocol.PathPoll, wait, "0.4.0", stamp{"abc", time.Now()})
			},
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"an unknown runner",
			func() (int, []byte) { return stranger.call(protocol.PathPoll, wait) },
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"no proof header",
			func() (int, []byte) { return r.raw(protocol.PathPoll, wait, "", "0.4.0") },
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"no release header",
			func() (int, []byte) { return r.rawSigned(r.priv, protocol.PathPoll, wait, "", fresh(t)) },
			http.StatusBadRequest, protocol.ErrorInvalid,
		},
		{
			"a release two minors older than the allowed range",
			func() (int, []byte) {
				return r.rawSigned(r.priv, protocol.PathPoll, wait, "0.1.0", fresh(t))
			},
			http.StatusUpgradeRequired, protocol.ErrorVersionUnsupported,
		},
		{
			"a release newer than the server",
			func() (int, []byte) {
				return r.rawSigned(r.priv, protocol.PathPoll, wait, "0.5.0", fresh(t))
			},
			http.StatusUpgradeRequired, protocol.ErrorVersionAhead,
		},
		{
			"a body that is not the poll request",
			func() (int, []byte) { return r.call(protocol.PathPoll, map[string]any{"unknown": 1}) },
			http.StatusBadRequest, protocol.ErrorInvalid,
		},
		{
			"a claim that does not exist",
			func() (int, []byte) { return r.call(opPath("missing", "renew"), nil) },
			http.StatusNotFound, protocol.ErrorNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := tc.send()
			if status != tc.status || errorCode(t, body) != tc.code {
				t.Fatalf("status = %d %s, want %d %s", status, body, tc.status, tc.code)
			}
		})
	}
}

// TestReplayedPollIsRefused proves that the server keeps the nonce of a
// proof on a path that is not safe to repeat, and refuses the same proof
// again.
func TestReplayedPollIsRefused(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	wait := protocol.PollRequest{Wait: 0, AssignmentVersion: runner.AssignmentVersion}
	nonce, at := newNonce(t), time.Now()

	st := stamp{nonce, at}
	status, body := r.rawSigned(r.priv, protocol.PathPoll, wait, "0.4.0", st)

	if status != http.StatusNoContent {
		t.Fatalf("first poll = %d %s, want 204", status, body)
	}

	status, body = r.rawSigned(r.priv, protocol.PathPoll, wait, "0.4.0", st)
	if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
		t.Fatalf("replayed poll = %d %s, want 401 bad-proof", status, body)
	}

	// A heartbeat is safe to repeat: the server keeps no nonce for it.
	beat := protocol.Heartbeat{Claims: nil, Free: 1}
	st = stamp{newNonce(t), at}

	for range 2 {
		status, body = r.rawSigned(r.priv, protocol.PathHeartbeat, beat, "0.4.0", st)

		if status != http.StatusOK {
			t.Fatalf("repeated heartbeat = %d %s, want 200", status, body)
		}
	}
}

// TestJoinIsRefused proves each way that a join fails.
func TestJoinIsRefused(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()

	used := e.joinToken(pool)
	first := e.newRunner("builder-1")

	if status, body := first.join(pool, used); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	tests := []struct {
		name   string
		join   func() (int, []byte)
		status int
		code   string
	}{
		{
			"a join token that a join used",
			func() (int, []byte) { return e.newRunner("builder-2").join(pool, used) },
			http.StatusUnauthorized, protocol.ErrorTokenUsed,
		},
		{
			"an unknown join token",
			func() (int, []byte) { return e.newRunner("builder-3").join(pool, "dpj_unknown") },
			http.StatusNotFound, protocol.ErrorNotFound,
		},
		{
			"a join token of another pool",
			func() (int, []byte) { return e.newRunner("builder-4").join("rp-other", e.joinToken(pool)) },
			http.StatusBadRequest, protocol.ErrorInvalid,
		},
		{
			"a name of a runner that is online",
			func() (int, []byte) {
				// The runner joins in this subtest, so the wait of the subtest for a
				// parallel slot cannot make it go offline before the second join.
				if status, body := e.newRunner("builder-8").join(pool, e.joinToken(pool)); status != http.StatusOK {
					return status, body
				}

				return e.newRunner("builder-8").join(pool, e.joinToken(pool))
			},
			http.StatusConflict, protocol.ErrorConflict,
		},
		{
			"a signature of another key",
			func() (int, []byte) {
				r := e.newRunner("builder-5")
				token := e.joinToken(pool)
				other := e.newRunner("builder-6")
				r.priv = other.priv

				return r.join(pool, token)
			},
			http.StatusUnauthorized, protocol.ErrorBadProof,
		},
		{
			"a release older than the allowed range",
			func() (int, []byte) {
				r := e.newRunner("builder-7")
				r.release = "0.1.0"

				return r.join(pool, e.joinToken(pool))
			},
			http.StatusUpgradeRequired, protocol.ErrorVersionUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := tc.join()
			if status != tc.status || errorCode(t, body) != tc.code {
				t.Fatalf("join = %d %s, want %d %s", status, body, tc.status, tc.code)
			}
		})
	}
}

// TestRetiredRunnerIsRefused proves that a runner that an administrator
// retired authenticates nothing, and that its claims go back to the pool.
func TestRetiredRunnerIsRefused(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want work")
	}

	status, body := e.admin(
		http.MethodPost,
		"/api/runners/acme/runners/"+r.id+"/retire",
		nil,
	)

	if status != http.StatusNoContent {
		t.Fatalf("retire = %d %s, want 204", status, body)
	}

	status, body = r.call(protocol.PathHeartbeat, protocol.Heartbeat{Claims: nil, Free: 1})
	if status != http.StatusForbidden || errorCode(t, body) != protocol.ErrorRetired {
		t.Fatalf("heartbeat of a retired runner = %d %s, want 403 retired", status, body)
	}

	other := e.newRunner("builder-2")
	if status, body = other.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	again := other.poll(
		2 * time.Second,
	)

	if again == nil || again.Operation != work.Operation || again.Token != work.Token+1 {
		t.Fatalf("poll after the retire = %+v, want the same operation with a new token", again)
	}
}

// TestKeyRotation proves a runner replaces its key, and that the old key
// then authenticates nothing but a retry of the same rotation.
func TestKeyRotation(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	rotate := func(pub ed25519.PublicKey, priv ed25519.PrivateKey) (int, []byte) {
		next := runner.Key{Public: pub, CreatedAt: time.Time{}}

		return r.call(protocol.PathRotate, protocol.RotateRequest{
			PublicKey: pub, NextProof: ed25519.Sign(priv, runner.RotateChallenge(r.id, next)),
		})
	}

	oldKey := r.priv

	if status, body := rotate(pub, priv); status != http.StatusNoContent {
		t.Fatalf("rotate = %d %s, want 204", status, body)
	}

	r.pub, r.priv = pub, priv

	status, body := r.call(
		protocol.PathHeartbeat,
		protocol.Heartbeat{Claims: nil, Free: 1},
	)

	if status != http.StatusOK {
		t.Fatalf("heartbeat with the new key = %d %s, want 200", status, body)
	}

	status, body = r.callWith(
		oldKey,
		protocol.PathHeartbeat,
		protocol.Heartbeat{Claims: nil, Free: 1},
	)

	if status != http.StatusUnauthorized {
		t.Fatalf("heartbeat with the old key = %d %s, want 401", status, body)
	}

	// A retry of the same rotation, signed by the previous key, changes nothing.
	status, body = r.callWith(oldKey, protocol.PathRotate, protocol.RotateRequest{
		PublicKey: pub,
		NextProof: ed25519.Sign(priv, runner.RotateChallenge(r.id, runner.Key{Public: pub, CreatedAt: time.Time{}})),
	})
	if status != http.StatusNoContent {
		t.Fatalf("retry of the rotation with the old key = %d %s, want 204", status, body)
	}
}
