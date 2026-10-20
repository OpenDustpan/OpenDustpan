package runnerapi_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestReleaseGoesThroughTheClaimer proves that a release of a claim with no
// job session runs the release rules of the claimer, that the operation then
// waits for the next claim, and that a claim with a job session stays.
func TestReleaseGoesThroughTheClaimer(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	first, second := e.newRunner("builder-1"), e.newRunner("builder-2")

	for _, r := range []*fakeRunner{first, second} {
		if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
			t.Fatalf("join = %d %s, want 200", status, body)
		}
	}

	e.startOperation(pool)

	work := first.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want work")
	}

	if status, body := first.call(opPath(work.Operation, "release"), nil); status != http.StatusNoContent {
		t.Fatalf("release = %d %s, want 204", status, body)
	}

	if got := e.claimer.released.Load(); got != 1 {
		t.Fatalf("the claimer released %d phases, want 1: a release goes through job.Claimer", got)
	}

	again := second.poll(2 * time.Second)
	if again == nil || again.Operation != work.Operation || again.Token != work.Token+1 {
		t.Fatalf("poll after the release = %+v, want the same operation with token %d", again, work.Token+1)
	}

	releaseWithSessionIsRefused(t, e, pool)
}

// releaseWithSessionIsRefused proves that the release of a claim with a job
// session answers 409 and does not reach the claimer.
func releaseWithSessionIsRefused(t *testing.T, e *env, pool string) {
	t.Helper()

	before := e.claimer.released.Load()
	r := e.newRunner("builder-3")
	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperationAt(pool, "job-2/0", 0)

	other := r.poll(2 * time.Second)
	if other == nil {
		t.Fatal("poll = 204, want work")
	}

	handoff := r.handoff(other.Operation)
	launcherKey := runner.Key{Public: mustKey(t), CreatedAt: time.Time{}}

	if _, err := e.store.runners.OpenSession(t.Context(), sha256Hex(handoff), launcherKey, time.Now()); err != nil {
		t.Fatalf("open session: %v", err)
	}

	status, body := r.call(opPath(other.Operation, "release"), nil)
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorConflict {
		t.Fatalf("release of a claim with a session = %d %s, want 409 conflict", status, body)
	}

	if got := e.claimer.released.Load(); got != before {
		t.Fatalf("the claimer released %d phases, want %d: a claim with a session is not released", got, before)
	}
}

// TestPollHonorsTheAssignmentVersion proves that a poll never claims a phase
// whose assignment version is above the version that the launcher reads.
func TestPollHonorsTheAssignmentVersion(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	const ahead = 3

	e.startOperationAt(pool, "job-9/0", ahead)

	if work := r.pollAt(0, ahead-1); work != nil {
		t.Fatalf("poll with version %d = %+v, want 204 for a phase of version %d", ahead-1, work, ahead)
	}

	work := r.pollAt(time.Second, ahead)
	if work == nil || work.Job != "job-9/0" {
		t.Fatalf("poll with version %d = %+v, want the phase job-9/0", ahead, work)
	}
}

// TestOnePollAtATime proves that a second poll of a runner, while the first
// is in flight, is refused with 429, and that a drain ends a long poll with
// no work.
func TestOnePollAtATime(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	var (
		wg      sync.WaitGroup
		waiting *protocol.Work
	)

	wg.Go(func() { waiting = r.poll(3 * time.Second) })

	pause(t, 300*time.Millisecond)

	status, body := r.call(
		protocol.PathPoll,
		protocol.PollRequest{Wait: 0, AssignmentVersion: runner.AssignmentVersion},
	)
	if status != http.StatusTooManyRequests || errorCode(t, body) != protocol.ErrorRateLimited {
		t.Fatalf("second poll = %d %s, want 429 rate-limited", status, body)
	}

	status, body = e.admin(http.MethodPost, "/api/runners/acme/runners/"+r.id+"/drain", nil)
	if status != http.StatusNoContent {
		t.Fatalf("drain = %d %s, want 204", status, body)
	}

	e.startOperation(pool)
	wg.Wait()

	if waiting != nil {
		t.Fatalf("poll of a runner that drained during the poll = %+v, want 204", waiting)
	}

	// The operation waits: a runner that did not drain takes it.
	other := e.newRunner("builder-2")
	if status, body = other.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	if work := other.poll(2 * time.Second); work == nil {
		t.Fatal("poll of a runner that did not drain = 204, want the operation that the draining runner left")
	}
}

// TestJoinAgainAuditNamesTheKeysAndClaims proves that the audit entry of a
// join again names the key before and after, and the claims that it ended.
func TestJoinAgainAuditNamesTheKeysAndClaims(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")
	again := e.newRunner("builder-1")
	firstToken, secondToken := e.joinToken(pool), e.joinToken(pool)

	if status, body := r.join(pool, firstToken); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want work")
	}

	e.clock.advance(runner.OfflineAfter + time.Minute)

	if status, body := again.join(pool, secondToken); status != http.StatusOK {
		t.Fatalf("join again = %d %s, want 200", status, body)
	}

	if again.id != r.id {
		t.Fatalf("join again gave the runner %s, want %s", again.id, r.id)
	}

	oldKey, newKey := runner.Key{
		Public:    r.pub,
		CreatedAt: time.Time{},
	}, runner.Key{
		Public:    again.pub,
		CreatedAt: time.Time{},
	}
	got := e.rejoinChanges()

	keyID, ended := got["keyID"], got["endedClaims"]
	if keyID == nil || keyID.Old == nil || *keyID.Old != oldKey.ID() || keyID.New != newKey.ID() {
		t.Fatalf("keyID change = %+v, want %s to %s", keyID, oldKey.ID(), newKey.ID())
	}

	if ended == nil || ended.New != work.Operation {
		t.Fatalf("endedClaims change = %+v, want %q", ended, work.Operation)
	}
}

// rejoinChanges returns the changes of the audit entry of the join again of
// acme, by key.
func (e *env) rejoinChanges() map[string]*audit.Change {
	e.t.Helper()

	listing, err := e.store.audit.List(e.t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		e.t.Fatalf("list audit: %v", err)
	}

	out := map[string]*audit.Change{}

	for i := range listing.Entries {
		changes := listing.Entries[i].Changes
		if !slices.ContainsFunc(changes, func(c audit.Change) bool {
			return c.Key == "event" && c.New == string(runner.EventRunnerRejoined)
		}) {
			continue
		}

		for j := range changes {
			out[changes[j].Key] = &changes[j]
		}
	}

	return out
}

// TestProofIsCheckedBeforeTheRunnerState proves that a caller without the
// key of a retired runner gets the answer for an unknown runner, and that the
// holder of the key learns that the runner is retired.
func TestProofIsCheckedBeforeTheRunnerState(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	status, body := e.admin(http.MethodPost, "/api/runners/acme/runners/"+r.id+"/retire", nil)
	if status != http.StatusNoContent {
		t.Fatalf("retire = %d %s, want 204", status, body)
	}

	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	beat := protocol.Heartbeat{Claims: nil, Free: 1}
	forged, forgedBody := r.callWith(otherKey, protocol.PathHeartbeat, beat)

	stranger := e.newRunner("stranger")
	stranger.id = "rn-unknown"
	unknown, unknownBody := stranger.call(protocol.PathHeartbeat, beat)

	if forged != http.StatusUnauthorized || unknown != http.StatusUnauthorized ||
		errorCode(t, forgedBody) != protocol.ErrorBadProof ||
		!bytes.Equal(forgedBody, unknownBody) {
		t.Fatalf("forged proof of a retired runner = %d %s, unknown runner = %d %s, want the same 401",
			forged, forgedBody, unknown, unknownBody)
	}

	if status, body := r.call(protocol.PathHeartbeat, beat); status != http.StatusForbidden ||
		errorCode(t, body) != protocol.ErrorRetired {
		t.Fatalf("heartbeat of a retired runner with its key = %d %s, want 403 retired", status, body)
	}

	if !strings.Contains(string(forgedBody), runner.ErrBadProof.Error()) {
		t.Fatalf("refusal = %s, want the sentence of the bad-proof sentinel", forgedBody)
	}
}
