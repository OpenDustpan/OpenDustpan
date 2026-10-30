package runnerapi_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestFakeRunnerLifecycle drives the protocol as a runner does: it joins,
// polls for a phase, sends a heartbeat, gets the handoff token, renews the
// claim, and sees the end of the claim.
func TestFakeRunnerLifecycle(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	work := claimFirstPhase(t, e, r, pool)

	beat := r.heartbeat(
		work.Operation,
	)

	if len(beat.Lost) != 0 || len(beat.Cancel) != 0 || beat.RotateKey || beat.Draining {
		t.Fatalf("heartbeat = %+v, want nothing to report", beat)
	}

	handoff := r.handoff(work.Operation)
	renewed := r.renew(
		work.Operation,
	)

	if renewed.SessionOpen || renewed.Canceled || !renewed.ExpiresAt.After(time.Now()) {
		t.Fatalf("renew = %+v, want a later expiry", renewed)
	}

	endAfterSession(t, e, r, work.Operation, handoff)
}

// endAfterSession opens the job session of the launcher with the handoff
// token, as the session path will, ends the phase as the job service does,
// and proves that the runner then sees the claim lost.
func endAfterSession(t *testing.T, e *env, r *fakeRunner, operation, handoff string) {
	t.Helper()

	// The session paths of the protocol are not part of this slice.
	launcherKey := runner.Key{Public: mustKey(t), CreatedAt: time.Time{}}

	if _, err := e.store.runners.OpenSession(t.Context(), sha256Hex(handoff), launcherKey, time.Now()); err != nil {
		t.Fatalf("open session: %v", err)
	}

	if renewed := r.renew(operation); !renewed.SessionOpen {
		t.Fatalf("renew with a session = %+v, want sessionOpen", renewed)
	}

	op, err := e.store.operations.Get(t.Context(), durable.ID(operation))
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}

	if err = e.store.operations.Complete(t.Context(), op.Claim); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if beat := r.heartbeat(operation); len(beat.Lost) != 1 || beat.Lost[0] != operation {
		t.Fatalf("heartbeat after the end = %+v, want the claim lost", beat)
	}
}

// claimFirstPhase proves that a poll finds no work before an operation
// starts, and that the poll after the start gets the phase with the first
// claim token and the lease of the pool.
func claimFirstPhase(t *testing.T, e *env, r *fakeRunner, pool string) *protocol.Work {
	t.Helper()

	if work := r.poll(0); work != nil {
		t.Fatalf("poll with no operation = %+v, want 204", work)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil || work.Job != phaseKey || work.Token != 1 || work.Lease.Std() != runner.DefaultLease {
		t.Fatalf("poll = %+v, want the phase %s with token 1 and the default lease", work, phaseKey)
	}

	return work
}

// heartbeat sends a heartbeat that names the claims and returns the answer.
func (f *fakeRunner) heartbeat(claims ...string) protocol.HeartbeatResponse {
	f.e.t.Helper()

	status, body := f.call(protocol.PathHeartbeat, protocol.Heartbeat{Claims: claims, Free: 1})
	if status != http.StatusOK {
		f.e.t.Fatalf("heartbeat = %d %s, want 200", status, body)
	}

	var beat protocol.HeartbeatResponse

	decode(f.e.t, body, &beat)

	return beat
}

// handoff asks for the handoff token of the claim and returns its secret.
func (f *fakeRunner) handoff(operation string) string {
	f.e.t.Helper()

	status, body := f.call(opPath(operation, "handoff"), nil)

	var resp protocol.HandoffResponse

	decode(f.e.t, body, &resp)

	if status != http.StatusOK || !strings.HasPrefix(resp.HandoffToken, runner.HandoffTokenPrefix) {
		f.e.t.Fatalf("handoff = %d %s, want 200 with a dph_ token", status, body)
	}

	return resp.HandoffToken
}

// renew renews the claim as the runner and returns the answer.
func (f *fakeRunner) renew(operation string) protocol.RenewResponse {
	f.e.t.Helper()

	status, body := f.call(opPath(operation, "renew"), nil)
	if status != http.StatusOK {
		f.e.t.Fatalf("renew = %d %s, want 200", status, body)
	}

	var resp protocol.RenewResponse

	decode(f.e.t, body, &resp)

	return resp
}

func mustKey(t *testing.T) []byte {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return pub
}

// TestCancelReachesTheRunner proves the heartbeat and the renewal tell a
// runner about a cancel.
func TestCancelReachesTheRunner(t *testing.T) {
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

	if err := e.store.operations.Cancel(t.Context(), durable.ID(work.Operation)); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	beat := r.heartbeat(
		work.Operation,
	)

	if len(beat.Cancel) != 1 || beat.Cancel[0] != work.Operation || len(beat.Lost) != 0 {
		t.Fatalf("heartbeat during a cancel = %+v, want the claim in cancel", beat)
	}

	if renewed := r.renew(work.Operation); !renewed.Canceled {
		t.Fatalf("renew during a cancel = %+v, want canceled", renewed)
	}
}

// TestTwoRunnersClaimOnePhase proves that of two runners that poll at the
// same time for one phase, exactly one gets it.
func TestTwoRunnersClaimOnePhase(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			twoRunnersClaimOnePhase(t, newEnvOn(t, b.open))
		})
	}
}

func twoRunnersClaimOnePhase(t *testing.T, e *env) {
	t.Helper()

	pool := e.createPool()
	runners := []*fakeRunner{e.newRunner("builder-1"), e.newRunner("builder-2")}

	for _, r := range runners {
		if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
			t.Fatalf("join = %d %s", status, body)
		}
	}

	e.startOperation(pool)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for _, r := range runners {
		wg.Go(func() {
			if work := r.poll(500 * time.Millisecond); work != nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d runners got the phase, want exactly 1", wins)
	}
}

// TestLapsedLeaseMakesThePhaseClaimable proves that a claim that lapses is
// claimable again with a new token, and that the first runner then holds
// nothing: its heartbeat reports the claim lost and its renewal is refused.
func TestLapsedLeaseMakesThePhaseClaimable(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			lapsedLeaseMakesThePhaseClaimable(t, newEnvOn(t, b.open))
		})
	}
}

func lapsedLeaseMakesThePhaseClaimable(t *testing.T, e *env) {
	t.Helper()

	pool := e.createPool()
	first, second := e.newRunner("builder-1"), e.newRunner("builder-2")

	for _, r := range []*fakeRunner{first, second} {
		if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
			t.Fatalf("join = %d %s", status, body)
		}
	}

	e.startOperation(pool)

	old := first.poll(2 * time.Second)
	if old == nil {
		t.Fatal("first poll = 204, want work")
	}

	e.lapse(old.Operation)

	taken := second.poll(2 * time.Second)
	if taken == nil || taken.Operation != old.Operation || taken.Token != old.Token+1 {
		t.Fatalf("second poll after the lease lapsed = %+v, want the same operation with token %d", taken, old.Token+1)
	}

	if beat := first.heartbeat(old.Operation); len(beat.Lost) != 1 {
		t.Fatalf("heartbeat of the first runner = %+v, want the claim lost", beat)
	}

	status, body := first.call(opPath(old.Operation, "renew"), nil)
	if status != http.StatusConflict {
		t.Fatalf("renew by the first runner = %d %s, want 409", status, body)
	}
}

// replicaRunners is the number of runners of the replica test.
const replicaRunners = 6

// pause waits for d, or until the test ends.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
	case <-timer.C:
	}
}

// TestReplicasClaimOnePhase proves that of several runners that poll
// several replicas of the server at the same time, exactly one gets a
// phase. The replicas are separate connection pools on one PostgreSQL
// schema, as the instances of one installation are.
func TestReplicasClaimOnePhase(t *testing.T) {
	t.Parallel()

	if os.Getenv(postgresEnv) == "" {
		t.Skipf("set %s to run the replica test", postgresEnv)
	}

	schemaURL := newSchema(t)
	first, second := openPostgresAt(t, schemaURL), openPostgresAt(t, schemaURL)

	e := newEnvOn(t, func(*testing.T) stores { return first })
	_, replica, _ := serve(
		t,
		second,
		serveOptions{now: nil, authz: identity.AdminOnly{}, claimer: nil, recorder: nil, jobs: nil, logger: nil},
	)

	pool := e.createPool()
	servers := []*env{e, {t: t, store: second, server: replica, handler: nil, claimer: nil, fence: e.fence, clock: nil}}

	runners := make([]*fakeRunner, 0, replicaRunners)

	for i := range replicaRunners {
		r := e.newRunner(fmt.Sprintf("builder-%d", i))
		if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
			t.Fatalf("join = %d %s", status, body)
		}

		// Half of the runners poll the other replica.
		r.e = servers[i%len(servers)]
		runners = append(runners, r)
	}

	e.startOperation(pool)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for _, r := range runners {
		wg.Go(func() {
			if work := r.poll(500 * time.Millisecond); work != nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d runners got the phase, want exactly 1", wins)
	}
}
