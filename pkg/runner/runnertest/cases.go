package runnertest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

func testCreatePool(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	if f.pool.ID == "" || f.pool.Purpose != runner.PurposeJobs {
		t.Fatalf("CreatePool = %+v, want an ID and the purpose jobs", f.pool)
	}

	got, err := h.Pools.Pool(t.Context(), f.pool.ID)
	if err != nil || got.Name != "prod" || got.Namespace != "acme" {
		t.Fatalf("Pool = %+v, %v", got, err)
	}

	_, err = h.Pools.CreatePool(t.Context(), newPool("acme", "sweep", runner.PurposeSweeps), f.fence, entries(f.fence))
	if err != nil {
		t.Fatalf("CreatePool sweep: %v", err)
	}

	list, err := h.Pools.Pools(t.Context(), "acme")
	if err != nil || len(list) != 2 || list[0].Name != "prod" || list[1].Name != "sweep" {
		t.Fatalf("Pools = %+v, %v, want prod and sweep in order", list, err)
	}

	_, err = h.Pools.Pool(t.Context(), "rp-unknown")
	wantErr(t, "Pool of an unknown ID", err, runner.ErrNotFound)

	_, err = h.Pools.CreatePool(t.Context(), newPool("acme", "Bad Name", runner.PurposeJobs), f.fence, entries(f.fence))
	wantErr(t, "CreatePool with a bad name", err, runner.ErrInvalid)
}

func testPoolNameUnique(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	_, err := h.Pools.CreatePool(t.Context(), newPool("acme", "prod", runner.PurposeSweeps), f.fence, entries(f.fence))
	wantErr(t, "CreatePool of a name that exists", err, runner.ErrAlreadyExists)

	other, err := h.Domain.CreateNamespace(t.Context(), "other")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	_, err = h.Pools.CreatePool(t.Context(), newPool("other", "prod", runner.PurposeJobs), other, entries(other))
	if err != nil {
		t.Fatalf("CreatePool of the name in another namespace: %v", err)
	}
}

func testPurposeImmutable(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	next := f.pool
	next.Purpose = runner.PurposeSweeps

	_, err := h.Pools.UpdatePool(t.Context(), next, f.fence, entries(f.fence))
	wantErr(t, "UpdatePool of the purpose", err, runner.ErrImmutable)

	next = f.pool
	next.Lease = runner.MaxLease

	got, err := h.Pools.UpdatePool(t.Context(), next, f.fence, entries(f.fence))
	if err != nil || got.Lease != runner.MaxLease || got.Purpose != runner.PurposeJobs {
		t.Fatalf("UpdatePool of the lease = %+v, %v", got, err)
	}

	stored, err := h.Pools.Pool(t.Context(), f.pool.ID)
	if err != nil || stored.Purpose != runner.PurposeJobs {
		t.Fatalf("Pool after updates = %+v, %v", stored, err)
	}
}

func testPoolLimit(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	for i := 1; i < runner.MaxPoolsPerNamespace; i++ {
		name := fmt.Sprintf("pool-%02d", i)

		_, err := h.Pools.CreatePool(t.Context(), newPool("acme", name, runner.PurposeJobs), f.fence, entries(f.fence))
		if err != nil {
			t.Fatalf("CreatePool %d: %v", i, err)
		}
	}

	_, err := h.Pools.CreatePool(
		t.Context(),
		newPool("acme", "one-more", runner.PurposeJobs),
		f.fence,
		entries(f.fence),
	)
	wantErr(t, "CreatePool above the limit", err, runner.ErrLimit)
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	before, err := h.Domain.AuditEntries(t.Context(), "acme")
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	newer, err := h.Domain.BumpFence(t.Context(), f.fence)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	_, err = h.Pools.CreatePool(t.Context(), newPool("acme", "late", runner.PurposeJobs), f.fence, entries(f.fence))
	wantErr(t, "CreatePool with a stale fence", err, namespace.ErrFenced)

	err = h.Pools.DeletePool(t.Context(), f.pool.ID, f.fence, entries(f.fence))
	wantErr(t, "DeletePool with a stale fence", err, namespace.ErrFenced)

	after, err := h.Domain.AuditEntries(t.Context(), "acme")
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries %d after a stale fence, want %d", len(after), len(before))
	}

	if _, err := h.Pools.Pool(t.Context(), f.pool.ID); err != nil {
		t.Fatalf("Pool after a fenced delete: %v", err)
	}

	if err := h.Pools.DeletePool(t.Context(), f.pool.ID, newer, entries(newer)); err != nil {
		t.Fatalf("DeletePool with the new fence: %v", err)
	}
}

func testPoolInUse(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	if err := h.Domain.UsePool(t.Context(), f.fence, f.pool.ID); err != nil {
		t.Fatalf("UsePool: %v", err)
	}

	err := h.Pools.DeletePool(t.Context(), f.pool.ID, f.fence, entries(f.fence))
	wantErr(t, "DeletePool of a pool in use", err, runner.ErrPoolInUse)

	err = h.Pools.DeletePool(t.Context(), "rp-unknown", f.fence, entries(f.fence))
	wantErr(t, "DeletePool of an unknown pool", err, runner.ErrNotFound)
}

func testJoinTokenSingleUse(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	secret := joinToken(t, f, time.Hour)

	tok, p, err := h.Members.LookupJoin(t.Context(), hash(secret))
	if err != nil || p.ID != f.pool.ID || !tok.UsedAt.IsZero() {
		t.Fatalf("LookupJoin = %+v, %+v, %v", tok, p, err)
	}

	r, err := join(t, f, secret, "cluster-a", now)
	if err != nil || r.ID == "" || r.Pool != f.pool.ID || !r.Key.CreatedAt.Equal(now) {
		t.Fatalf("Join = %+v, %v", r, err)
	}

	_, err = join(t, f, secret, "cluster-b", now)
	wantErr(t, "a second Join with one token", err, runner.ErrTokenUsed)

	runners, err := h.Members.Runners(t.Context(), f.pool.ID)
	if err != nil || len(runners) != 1 {
		t.Fatalf("Runners = %+v, %v, want one runner", runners, err)
	}

	_, err = join(t, f, runner.JoinTokenPrefix+"unknown", "cluster-c", now)
	wantErr(t, "Join with an unknown token", err, runner.ErrNotFound)
}

func testJoinTokenExpiredRevoked(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	expired := joinToken(t, f, time.Minute)

	_, err := join(t, f, expired, "cluster-a", now.Add(time.Minute))
	wantErr(t, "Join at the expiry of the token", err, runner.ErrTokenExpired)

	revoked := joinToken(t, f, time.Hour)

	tokens, err := h.Pools.JoinTokens(t.Context(), f.pool.ID)
	if err != nil || len(tokens) != 2 {
		t.Fatalf("JoinTokens = %+v, %v", tokens, err)
	}

	for i := range tokens {
		if tokens[i].SHA256 != hash(revoked) {
			continue
		}

		if err = h.Pools.RevokeJoinToken(t.Context(), tokens[i].ID, f.fence, entries(f.fence)); err != nil {
			t.Fatalf("RevokeJoinToken: %v", err)
		}
	}

	_, err = join(t, f, revoked, "cluster-a", now)
	wantErr(t, "Join with a revoked token", err, runner.ErrTokenUsed)
}

func testJoinIsolation(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	secret := joinToken(t, f, time.Hour)

	req := joinRequest(t, secret, "laptop")
	req.Kind = runner.KindMachine
	req.Isolation = runner.IsolationProcess

	_, err := joinWith(t, f, secret, req, now)
	wantErr(t, "Join with process isolation that the pool does not list", err, runner.ErrInvalid)

	next := f.pool
	next.Isolation = []runner.Isolation{runner.IsolationProcess}

	if _, err := h.Pools.UpdatePool(t.Context(), next, f.fence, entries(f.fence)); err != nil {
		t.Fatalf("UpdatePool: %v", err)
	}

	if _, err := joinWith(t, f, secret, req, now); err != nil {
		t.Fatalf("Join with listed process isolation: %v", err)
	}
}

func testRejoinKeepsID(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	secret := joinToken(t, f, time.Hour)

	first, err := joinWith(t, f, secret, joinRequest(t, secret, "cluster-a"), now)
	if err != nil || first.Rejoined {
		t.Fatalf("Join = %+v, %v, want a new runner", first, err)
	}

	r := first.Runner
	next := newKey(t)
	next.CreatedAt = now.Add(runner.RotateKeyAfter)

	if _, err = rotate(t, f, r.ID, r.Key, next); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	later := now.Add(runner.RotateKeyAfter + runner.OfflineAfter + time.Second)
	secret = joinTokenAt(t, f, later, time.Hour)

	joined, err := joinWith(t, f, secret, joinRequest(t, secret, "cluster-a"), later)
	again := joined.Runner

	if err != nil || !joined.Rejoined || again.ID != r.ID || bytes.Equal(again.Key.Public, next.Public) {
		t.Fatalf("Join again = %+v, %v, want a join again of ID %s with a new key", joined, err, r.ID)
	}

	if len(again.PreviousKey.Public) != 0 || !again.RotatedAt.IsZero() || len(again.RotationKeys(later)) != 1 {
		t.Fatalf("Join again kept the previous key %x of %v, want none", again.PreviousKey.Public, again.RotatedAt)
	}

	_, err = rotate(t, f, r.ID, r.Key, again.Key)
	wantErr(t, "RotateKey with a key from before the join again", err, runner.ErrBadProof)

	if _, err = h.Members.Heartbeat(t.Context(), again.ID, again.Release, later); err != nil {
		t.Fatalf("Heartbeat after the join again: %v", err)
	}
}

func testKeyExpired(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	got, err := h.Members.Heartbeat(t.Context(), r.ID, r.Release, now.Add(runner.MaxKeyAge))
	if err != nil || !got.LastSeenAt.Equal(now.Add(runner.MaxKeyAge)) {
		t.Fatalf("Heartbeat at the key age limit = %+v, %v", got, err)
	}

	_, err = h.Members.Heartbeat(t.Context(), r.ID, r.Release, now.Add(runner.MaxKeyAge+time.Second))
	wantErr(t, "Heartbeat after the key age limit", err, runner.ErrKeyExpired)

	late := newKey(t)
	late.CreatedAt = now.Add(runner.MaxKeyAge + time.Second)

	_, err = rotate(t, f, r.ID, r.Key, late)
	wantErr(t, "RotateKey after the key age limit", err, runner.ErrKeyExpired)
}

func testRetired(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	if err = h.Members.SetDraining(t.Context(), r.ID, true, f.fence, entries(f.fence)); err != nil {
		t.Fatalf("SetDraining: %v", err)
	}

	got, err := h.Members.Heartbeat(t.Context(), r.ID, r.Release, now)
	if err != nil || !got.Draining {
		t.Fatalf("Heartbeat of a draining runner = %+v, %v", got, err)
	}

	for range 2 {
		if err = h.Members.Retire(t.Context(), r.ID, now, f.fence, entries(f.fence)); err != nil {
			t.Fatalf("Retire: %v", err)
		}
	}

	_, err = h.Members.Heartbeat(t.Context(), r.ID, r.Release, now)
	wantErr(t, "Heartbeat of a retired runner", err, runner.ErrRetired)

	runners, err := h.Members.Runners(t.Context(), f.pool.ID)
	if err != nil || len(runners) != 0 {
		t.Fatalf("Runners after Retire = %+v, %v", runners, err)
	}

	secret := joinToken(t, f, time.Hour)

	fresh, err := joinWith(t, f, secret, joinRequest(t, secret, "cluster-a"), now)
	if err != nil || fresh.Rejoined || fresh.Runner.ID == r.ID {
		t.Fatalf("Join with the name of a retired runner = %+v, %v, want a new runner", fresh, err)
	}
}

func testRotateKey(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	at := now.Add(runner.RotateKeyAfter + time.Hour)
	next := newKey(t)
	next.CreatedAt = at

	got, err := rotate(t, f, r.ID, r.Key, next)
	if err != nil || !bytes.Equal(got.Key.Public, next.Public) || !got.Key.CreatedAt.Equal(at) {
		t.Fatalf("RotateKey = %+v, %v", got, err)
	}

	if !bytes.Equal(got.PreviousKey.Public, r.Key.Public) || !got.RotatedAt.Equal(at) {
		t.Fatalf("RotateKey kept the previous key %x at %v, want %x at %v", got.PreviousKey.Public, got.RotatedAt,
			r.Key.Public, at)
	}

	retry := next
	retry.CreatedAt = at.Add(time.Minute)

	again, err := rotate(t, f, r.ID, r.Key, retry)
	if err != nil || !again.Key.CreatedAt.Equal(at) || !bytes.Equal(again.PreviousKey.Public, r.Key.Public) {
		t.Fatalf("RotateKey retry = %+v, %v, want no change", again, err)
	}

	checkRotationKeys(t, &again, r.Key, at)

	if _, err := h.Members.Heartbeat(t.Context(), r.ID, r.Release, now.Add(runner.MaxKeyAge+time.Hour)); err != nil {
		t.Fatalf("Heartbeat after a rotation, past the age of the old key: %v", err)
	}
}

func testRotateWithPreviousKey(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	at := now.Add(runner.RotateKeyAfter)
	rotateTo := func(signer, k runner.Key, when time.Time) (runner.Runner, error) {
		k.CreatedAt = when

		return rotate(t, f, r.ID, signer, k)
	}

	second, third := newKey(t), newKey(t)
	if _, err = rotateTo(r.Key, second, at); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	_, err = rotateTo(r.Key, third, at.Add(time.Minute))
	wantErr(t, "RotateKey to another key with the previous key in the grace window", err, runner.ErrBadProof)

	_, err = rotateTo(newKey(t), third, at.Add(time.Minute))
	wantErr(t, "RotateKey with a key that the runner never had", err, runner.ErrBadProof)

	_, err = rotateTo(r.Key, second, at.Add(runner.RotateGrace+time.Second))
	wantErr(t, "RotateKey retry with the previous key after the grace window", err, runner.ErrBadProof)

	stored, err := h.Members.Runner(t.Context(), r.ID)
	if err != nil || !bytes.Equal(stored.Key.Public, second.Public) || !bytes.Equal(stored.PreviousKey.Public,
		r.Key.Public) {
		t.Fatalf("Runner after refused rotations = %+v, %v, want the second key, after the first", stored, err)
	}

	got, err := rotateTo(second, third, at.Add(runner.RotateGrace+time.Second))
	if err != nil || !bytes.Equal(got.Key.Public, third.Public) || !bytes.Equal(got.PreviousKey.Public,
		second.Public) {
		t.Fatalf("RotateKey with the current key = %+v, %v, want the third key, after the second", got, err)
	}

	_, err = rotateTo(r.Key, third, at.Add(runner.RotateGrace+2*time.Second))
	wantErr(t, "RotateKey with a key from before the previous key", err, runner.ErrBadProof)
}

// handoff is a runner with a claim of its pool and a handoff token for it,
// issued at now.
type handoff struct {
	f      fixture
	r      runner.Runner
	c      durable.Claim
	tok    runner.HandoffToken
	secret string
}

// handoffSetUp joins a runner and claims an operation of the pool for it.
func handoffSetUp(t *testing.T, h Harness) handoff {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	tok, secret := handoffToken(t, c, r.ID, now)

	if err = h.Sessions.IssueHandoffToken(t.Context(), tok, c); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	return handoff{f: f, r: r, c: c, tok: tok, secret: secret}
}

func testHandoffTokenSingleUse(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	r, tok, secret := l.r, l.tok, l.secret

	s, err := h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), now)
	if err != nil || s.ID == "" || s.Operation != tok.Operation || s.Runner != r.ID {
		t.Fatalf("OpenSession = %+v, %v", s, err)
	}

	got, err := h.Sessions.Session(t.Context(), s.ID)
	if err != nil || got.Token != tok.Token {
		t.Fatalf("Session = %+v, %v", got, err)
	}

	_, err = h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), now)
	wantErr(t, "a second OpenSession with one handoff token", err, runner.ErrTokenUsed)

	_, err = h.Sessions.OpenSession(t.Context(), hash(runner.HandoffTokenPrefix+"unknown"), newKey(t), now)
	wantErr(t, "OpenSession with an unknown token", err, runner.ErrNotFound)
}

func testHandoffTokenClaim(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	f, r, secret := l.f, l.r, l.secret

	_, err := h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), now.Add(runner.HandoffTokenTTL))
	wantErr(t, "OpenSession at the expiry of the token", err, runner.ErrTokenExpired)

	sweep, err := h.Domain.Claim(t.Context(), runner.SweepKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	tok, _ := handoffToken(t, sweep, r.ID, now)

	err = h.Sessions.IssueHandoffToken(t.Context(), tok, sweep)
	wantErr(t, "IssueHandoffToken for a kind of the other purpose", err, runner.ErrInvalid)

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if _, err = h.Domain.Takeover(t.Context(), c); err != nil {
		t.Fatalf("Takeover: %v", err)
	}

	tok.Operation, tok.Token = string(c.Operation), c.Token

	err = h.Sessions.IssueHandoffToken(t.Context(), tok, c)
	wantErr(t, "IssueHandoffToken for a claim that a takeover replaced", err, runner.ErrClaimLost)
}

func testSessionAfterTakeover(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	r, secret := l.r, l.secret

	s, err := h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	if _, err = h.Domain.Takeover(t.Context(), durableClaim(s, r)); err != nil {
		t.Fatalf("Takeover: %v", err)
	}

	_, err = h.Sessions.Session(t.Context(), s.ID)
	wantErr(t, "Session after a takeover", err, runner.ErrClaimLost)
}

func testAuditEntries(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	secret := joinToken(t, f, time.Hour)

	if _, err := join(t, f, secret, "cluster-a", now); err != nil {
		t.Fatalf("Join: %v", err)
	}

	got, err := h.Domain.AuditEntries(t.Context(), "acme")
	if err != nil || len(got) != 3 {
		t.Fatalf("AuditEntries = %d, %v, want 3: the pool, the join token and the join", len(got), err)
	}

	for i := range got {
		if got[i].FenceVersion != f.fence.FailoverVersion {
			t.Errorf("entry %s has the fence version %d, want %d", got[i].Action, got[i].FenceVersion,
				f.fence.FailoverVersion)
		}
	}
}

func testRejoinNeedsOffline(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	first, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), first.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	online := now.Add(runner.OfflineAfter)

	_, err = join(t, f, joinTokenAt(t, f, online, time.Hour), "cluster-a", online)
	wantErr(t, "Join again of an online runner", err, runner.ErrRunnerOnline)

	offline := now.Add(runner.OfflineAfter + time.Second)
	secret := joinTokenAt(t, f, offline, time.Hour)

	joined, err := joinWith(t, f, secret, joinRequest(t, secret, "cluster-a"), offline)
	again := joined.Runner

	if err != nil || !joined.Rejoined || again.ID != first.ID || bytes.Equal(again.Key.Public, first.Key.Public) {
		t.Fatalf("Join again of an offline runner = %+v, %v, want a join again of ID %s with a new key", joined, err,
			first.ID)
	}

	if joined.PreviousKeyID != first.Key.ID() || !slices.Equal(joined.EndedClaims, []string{string(c.Operation)}) {
		t.Fatalf("Join again = previous key %q and ended claims %v, want %q and [%s]", joined.PreviousKeyID,
			joined.EndedClaims, first.Key.ID(), c.Operation)
	}

	tok, _ := handoffToken(t, c, first.ID, offline)

	err = h.Sessions.IssueHandoffToken(t.Context(), tok, c)
	wantErr(t, "IssueHandoffToken for a claim of the old key", err, runner.ErrClaimLost)
}

func testRetireEndsSessions(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)

	s, err := h.Sessions.OpenSession(t.Context(), hash(l.secret), newKey(t), now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	if err = h.Members.Retire(t.Context(), l.r.ID, now, l.f.fence, entries(l.f.fence)); err != nil {
		t.Fatalf("Retire: %v", err)
	}

	_, err = h.Sessions.Session(t.Context(), s.ID)
	wantErr(t, "Session of a retired runner", err, runner.ErrRetired)
}

func testHandoffTokenOwner(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	f := l.f

	other, err := join(t, f, joinToken(t, f, time.Hour), "cluster-b", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), l.r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	stolen, _ := handoffToken(t, c, other.ID, now)

	err = h.Sessions.IssueHandoffToken(t.Context(), stolen, c)
	if !errors.Is(err, runner.ErrClaimLost) && !errors.Is(err, runner.ErrNotFound) {
		t.Fatalf("IssueHandoffToken for the claim of another runner = %v, want ErrClaimLost or ErrNotFound", err)
	}

	asOwner := c
	asOwner.Owner = other.ID

	err = h.Sessions.IssueHandoffToken(t.Context(), stolen, asOwner)
	if !errors.Is(err, runner.ErrClaimLost) && !errors.Is(err, runner.ErrNotFound) {
		t.Fatalf("IssueHandoffToken with a claim that names another owner = %v, want ErrClaimLost or ErrNotFound", err)
	}

	_, err = h.Sessions.OpenSession(t.Context(), stolen.SHA256, newKey(t), now)
	wantErr(t, "OpenSession with a refused token", err, runner.ErrNotFound)
}

func testHandoffTokenReplace(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	c := durable.Claim{
		Operation: durable.ID(l.tok.Operation),
		Owner:     l.r.ID,
		Token:     l.tok.Token,
		ExpiresAt: time.Time{},
	}

	secret := runner.HandoffTokenPrefix + randomHex(t)
	next := l.tok
	next.SHA256 = hash(secret)

	if err := h.Sessions.IssueHandoffToken(t.Context(), next, c); err != nil {
		t.Fatalf("IssueHandoffToken again: %v", err)
	}

	_, err := h.Sessions.OpenSession(t.Context(), hash(l.secret), newKey(t), now)
	wantErr(t, "OpenSession with the replaced token", err, runner.ErrNotFound)

	if _, err = h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), now); err != nil {
		t.Fatalf("OpenSession with the new token: %v", err)
	}

	late := next
	late.SHA256 = hash(runner.HandoffTokenPrefix + randomHex(t))

	err = h.Sessions.IssueHandoffToken(t.Context(), late, c)
	wantErr(t, "IssueHandoffToken for a claim with a session", err, runner.ErrConflict)
}

func testNonce(t *testing.T, h Harness) {
	t.Helper()

	nonce := randomHex(t)
	expires := now.Add(runner.NonceTTL)

	if err := h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires, now); err != nil {
		t.Fatalf("UseNonce: %v", err)
	}

	err := h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires, now.Add(time.Second))
	wantErr(t, "UseNonce of a nonce that the store holds", err, runner.ErrReplay)

	if err = h.Sessions.UseNonce(t.Context(), "rn-2", nonce, expires, now); err != nil {
		t.Fatalf("UseNonce of the nonce for another subject: %v", err)
	}

	if err = h.Sessions.UseNonce(t.Context(), "rn-1", randomHex(t), expires, now); err != nil {
		t.Fatalf("UseNonce of another nonce: %v", err)
	}

	err = h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires, now.Add(runner.ProofWindow))
	wantErr(t, "UseNonce at the end of the proof window", err, runner.ErrReplay)

	err = h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires, expires)
	wantErr(t, "UseNonce at the expiry", err, runner.ErrReplay)

	err = h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires.Add(runner.NonceTTL), expires.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("UseNonce after the expiry: %v", err)
	}
}

// checkRotationKeys checks the keys that r, rotated from old at at, gives
// for a proof.
func checkRotationKeys(t *testing.T, r *runner.Runner, old runner.Key, at time.Time) {
	t.Helper()

	inGrace := r.RotationKeys(at.Add(runner.RotateGrace))
	if len(inGrace) != len([]runner.Key{r.Key, old}) || !bytes.Equal(inGrace[1].Public, old.Public) {
		t.Fatalf("RotationKeys in the grace window = %d keys, want the new and the old", len(inGrace))
	}

	other := r.ProofKeys(at.Add(time.Minute))
	if len(other) != 1 || !bytes.Equal(other[0].Public, r.Key.Public) {
		t.Fatalf("ProofKeys of another request = %d keys, want the new key only", len(other))
	}

	if late := r.RotationKeys(at.Add(runner.RotateGrace + time.Second)); len(late) != 1 {
		t.Fatalf("RotationKeys after the grace window = %d keys, want the new key only", len(late))
	}
}

func testRejoinKeepsSessions(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	f, r := l.f, l.r

	s, err := h.Sessions.OpenSession(t.Context(), hash(l.secret), newKey(t), now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	waiting, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	tok, _ := handoffToken(t, waiting, r.ID, now)
	if err = h.Sessions.IssueHandoffToken(t.Context(), tok, waiting); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	offline := now.Add(runner.OfflineAfter + time.Second)
	if _, err = join(t, f, joinTokenAt(t, f, offline, time.Hour), "cluster-a", offline); err != nil {
		t.Fatalf("Join again: %v", err)
	}

	if got, lost := h.Sessions.Session(t.Context(), s.ID); lost != nil || got.ID != s.ID {
		t.Fatalf("Session of a launcher after a join again of its runner = %+v, %v, want it live", got, lost)
	}

	_, err = h.Sessions.RunnerRenew(t.Context(), l.c, runner.DefaultLease, offline)
	wantErr(t, "RunnerRenew of the claim with a session after a join again", err, runner.ErrSessionOpen)

	_, err = h.Sessions.RunnerRenew(t.Context(), waiting, runner.DefaultLease, offline)
	wantErr(t, "RunnerRenew of a claim with no session after a join again", err, runner.ErrClaimLost)

	_, err = h.Sessions.OpenSession(t.Context(), tok.SHA256, newKey(t), offline)
	wantErr(t, "OpenSession for a claim with no session after a join again", err, runner.ErrClaimLost)
}

func testRunnerRenew(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	f, r, c := l.f, l.r, l.c

	got, err := h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, now.Add(time.Minute))
	if err != nil || got.Token != c.Token {
		t.Fatalf("RunnerRenew = %+v, %v, want the claim of the same token", got, err)
	}

	_, err = h.Sessions.RunnerRenew(t.Context(), c, 0, now)
	wantErr(t, "RunnerRenew with no lease", err, runner.ErrInvalid)

	other := c
	other.Owner = "rn-other"

	_, err = h.Sessions.RunnerRenew(t.Context(), other, runner.DefaultLease, now)
	wantErr(t, "RunnerRenew of a claim with another owner", err, runner.ErrClaimLost)

	limit := now.Add(runner.MaxSessionWait)

	if _, err = h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, limit); err != nil {
		t.Fatalf("RunnerRenew at the session wait limit: %v", err)
	}

	late, _ := handoffToken(t, c, r.ID, now.Add(runner.MaxSessionWait-time.Minute))
	if err = h.Sessions.IssueHandoffToken(t.Context(), late, c); err != nil {
		t.Fatalf("IssueHandoffToken near the session wait limit: %v", err)
	}

	after := now.Add(runner.MaxSessionWait + time.Second)

	_, err = h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, after)
	wantErr(t, "RunnerRenew after the session wait limit", err, runner.ErrClaimLost)

	_, err = h.Sessions.OpenSession(t.Context(), late.SHA256, newKey(t), after)
	wantErr(t, "OpenSession of a released claim", err, runner.ErrClaimLost)

	_, err = h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, after)
	wantErr(t, "RunnerRenew of a released claim", err, runner.ErrClaimLost)

	renewed, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = h.Sessions.RunnerRenew(t.Context(), renewed, runner.DefaultLease, time.Now())
	wantErr(t, "RunnerRenew of a claim with no record", err, runner.ErrClaimLost)

	// The claim time of a recorded claim is the database time: the times
	// below are from the clock of the cases, which is the same clock.
	start := time.Now()

	if err = h.Sessions.RecordClaim(t.Context(), renewed, "", 0); err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}

	if _, err = h.Sessions.RunnerRenew(t.Context(), renewed, runner.DefaultLease, start); err != nil {
		t.Fatalf("RunnerRenew before a handoff token: %v", err)
	}

	tok, _ := handoffToken(t, renewed, r.ID, start.Add(runner.MaxSessionWait-time.Minute))
	if err = h.Sessions.IssueHandoffToken(t.Context(), tok, renewed); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	pastLimit := start.Add(runner.MaxSessionWait + time.Second)

	_, err = h.Sessions.OpenSession(t.Context(), tok.SHA256, newKey(t), pastLimit)
	wantErr(t, "OpenSession after the session wait limit from the recorded claim time", err, runner.ErrClaimLost)
}

func testSessionNearWaitLimit(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)
	f, r, c := l.f, l.r, l.c
	limit := now.Add(runner.MaxSessionWait)

	tok, _ := handoffToken(t, c, r.ID, limit.Add(-time.Minute))
	if err := h.Sessions.IssueHandoffToken(t.Context(), tok, c); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	s, err := h.Sessions.OpenSession(t.Context(), tok.SHA256, newKey(t), limit)
	if err != nil {
		t.Fatalf("OpenSession at the session wait limit: %v", err)
	}

	_, err = h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, limit.Add(time.Minute))
	wantErr(t, "RunnerRenew after the limit of a claim with a session", err, runner.ErrSessionOpen)

	if _, err = h.Sessions.Session(t.Context(), s.ID); err != nil {
		t.Fatalf("Session after a renewal by the runner past the limit: %v", err)
	}

	slow, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	first, _ := handoffToken(t, slow, r.ID, now)
	if err = h.Sessions.IssueHandoffToken(t.Context(), first, slow); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	next, _ := handoffToken(t, slow, r.ID, limit.Add(-time.Minute))
	if err = h.Sessions.IssueHandoffToken(t.Context(), next, slow); err != nil {
		t.Fatalf("IssueHandoffToken again: %v", err)
	}

	_, err = h.Sessions.OpenSession(t.Context(), next.SHA256, newKey(t), limit.Add(time.Second))
	wantErr(t, "OpenSession after the session wait limit with a new token", err, runner.ErrClaimLost)
}

// window is a span of the clock of the cases that holds the claim time of a
// claim: lo is at most the claim time, and hi is at least the claim time.
type window struct {
	lo, hi time.Time
}

// bracket records the claim c as the poll does, and returns the window of
// its claim time.
func bracket(t *testing.T, h Harness, c durable.Claim, jobID string, phase int) window {
	t.Helper()

	lo := time.Now().Truncate(time.Millisecond)

	if err := h.Sessions.RecordClaim(t.Context(), c, jobID, phase); err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}

	return window{lo: lo, hi: time.Now()}
}

func testRecordClaim(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = h.Sessions.LookupClaim(t.Context(), c)
	wantErr(t, "LookupClaim of a claim with no record", err, runner.ErrNotFound)

	w := bracket(t, h, c, "job-1", 2)

	rec, err := h.Sessions.LookupClaim(t.Context(), c)
	if err != nil || rec.Job != "job-1" || rec.Phase != 2 || rec.HasSession {
		t.Fatalf("LookupClaim = %+v, %v, want job-1, phase 2 and no session", rec, err)
	}

	if rec.ClaimedAt.Before(w.lo) || rec.ClaimedAt.After(w.hi) {
		t.Fatalf("claim time %v is not between %v and %v: it must be the time of the database", rec.ClaimedAt, w.lo,
			w.hi)
	}

	recordClaimIsOnce(t, h, recordedClaim{runner: r, claim: c, record: rec, hi: w.hi})
}

// recordedClaim is a claim of a runner with its record and the end of the
// window of its claim time.
type recordedClaim struct {
	runner runner.Runner
	claim  durable.Claim
	record runner.ClaimRecord
	hi     time.Time
}

// recordClaimIsOnce proves that a second RecordClaim and a session change
// nothing of the record rec of the claim c, and that a claim of another owner
// has no record.
func recordClaimIsOnce(t *testing.T, h Harness, in recordedClaim) {
	t.Helper()

	r, c, rec, hi := in.runner, in.claim, &in.record, in.hi

	if err := h.Sessions.RecordClaim(t.Context(), c, "job-2", 5); err != nil {
		t.Fatalf("RecordClaim again: %v", err)
	}

	tok, secret := handoffToken(t, c, r.ID, hi)
	if err := h.Sessions.IssueHandoffToken(t.Context(), tok, c); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	if _, err := h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), hi); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	again, err := h.Sessions.LookupClaim(t.Context(), c)
	if err != nil || again.Job != "job-1" || again.Phase != 2 || !again.ClaimedAt.Equal(rec.ClaimedAt) ||
		!again.HasSession {
		t.Fatalf("LookupClaim after a second record and a session = %+v, %v, want the first record with a session",
			again, err)
	}

	stranger := c
	stranger.Owner = "rn-other"

	err = h.Sessions.RecordClaim(t.Context(), stranger, "", 0)
	wantErr(t, "RecordClaim of a claim with another owner", err, runner.ErrClaimLost)

	_, err = h.Sessions.LookupClaim(t.Context(), stranger)
	wantErr(t, "LookupClaim of a claim with another owner", err, runner.ErrClaimLost)
}

// shortLease is the lease of a claim that the cases let pass, and
// leaseWait is the time to wait until it passed.
const (
	shortLease = 500 * time.Millisecond
	leaseWait  = 800 * time.Millisecond
)

func testClaimLeasePasses(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	waiting, err := h.Domain.ClaimLease(t.Context(), job.RunnerKind(f.pool.ID), r.ID, shortLease)
	if err != nil {
		t.Fatalf("ClaimLease: %v", err)
	}

	at := bracket(t, h, waiting, "job-1", 0).hi

	tok, secret := handoffToken(t, waiting, r.ID, at)
	if err = h.Sessions.IssueHandoffToken(t.Context(), tok, waiting); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	withSession, err := h.Domain.ClaimLease(t.Context(), job.RunnerKind(f.pool.ID), r.ID, shortLease)
	if err != nil {
		t.Fatalf("ClaimLease: %v", err)
	}

	bracket(t, h, withSession, "job-2", 0)

	open, openSecret := handoffToken(t, withSession, r.ID, at)
	if err = h.Sessions.IssueHandoffToken(t.Context(), open, withSession); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	s, err := h.Sessions.OpenSession(t.Context(), hash(openSecret), newKey(t), at)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	timer := time.NewTimer(leaseWait)
	select {
	case <-t.Context().Done():
		t.Fatal("the case ended before the lease passed")
	case <-timer.C:
	}

	_, err = h.Sessions.RunnerRenew(t.Context(), waiting, runner.DefaultLease, time.Now())
	wantErr(t, "RunnerRenew after the lease passed", err, runner.ErrClaimLost)

	late, _ := handoffToken(t, waiting, r.ID, time.Now())

	err = h.Sessions.IssueHandoffToken(t.Context(), late, waiting)
	wantErr(t, "IssueHandoffToken after the lease passed", err, runner.ErrClaimLost)

	_, err = h.Sessions.OpenSession(t.Context(), hash(secret), newKey(t), time.Now())
	wantErr(t, "OpenSession after the lease passed", err, runner.ErrClaimLost)

	_, err = h.Sessions.RunnerRenew(t.Context(), withSession, runner.DefaultLease, time.Now())
	wantErr(t, "RunnerRenew of a claim with a session after the lease passed", err, runner.ErrClaimLost)

	_, err = h.Sessions.Session(t.Context(), s.ID)
	wantErr(t, "Session after the lease passed", err, runner.ErrClaimLost)

	err = h.Sessions.RecordClaim(t.Context(), waiting, "job-1", 0)
	wantErr(t, "RecordClaim after the lease passed", err, runner.ErrClaimLost)
}

func testRenewExpiryFromDatabase(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	c, err := h.Domain.Claim(t.Context(), job.RunnerKind(f.pool.ID), r.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	lo := bracket(t, h, c, "", 0).lo

	// The clock of the caller is far in the past. The expiry still comes
	// from the clock of the database.
	got, err := h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, lo)
	if err != nil {
		t.Fatalf("RunnerRenew: %v", err)
	}

	earliest, latest := lo.Add(runner.DefaultLease), time.Now().Add(runner.DefaultLease)
	if got.ExpiresAt.Before(earliest) || got.ExpiresAt.After(latest) {
		t.Fatalf("RunnerRenew expiry = %v, want between %v and %v: the database time plus the lease", got.ExpiresAt,
			earliest, latest)
	}

	skewed, err := h.Sessions.RunnerRenew(t.Context(), c, runner.DefaultLease, lo.Add(time.Minute))
	if err != nil {
		t.Fatalf("RunnerRenew with a clock a minute ahead: %v", err)
	}

	if skewed.ExpiresAt.Equal(lo.Add(time.Minute + runner.DefaultLease)) {
		t.Fatalf("RunnerRenew expiry = %v, which is the clock of the caller plus the lease", skewed.ExpiresAt)
	}
}

func testOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	r, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	other, err := h.Domain.CreateNamespace(t.Context(), "globex")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	err = h.Members.SetDraining(t.Context(), r.ID, true, other, entries(other))
	wantErr(t, "SetDraining with the fence of another namespace", err, runner.ErrNotFound)

	err = h.Members.Retire(t.Context(), r.ID, now, other, entries(other))
	wantErr(t, "Retire with the fence of another namespace", err, runner.ErrNotFound)

	next := newKey(t)
	next.CreatedAt = now.Add(runner.RotateKeyAfter)

	_, err = h.Members.RotateKey(t.Context(), runner.Rotation{Runner: r.ID, Signer: r.Key.ID(), Next: next}, other,
		entries(other))
	wantErr(t, "RotateKey with the fence of another namespace", err, runner.ErrNotFound)

	stored, err := h.Members.Runner(t.Context(), r.ID)
	if err != nil || stored.Draining || !stored.RetiredAt.IsZero() || !bytes.Equal(stored.Key.Public, r.Key.Public) {
		t.Fatalf("Runner after refused changes = %+v, %v, want it unchanged", stored, err)
	}

	got, err := h.Domain.AuditEntries(t.Context(), "globex")
	if err != nil || len(got) != 0 {
		t.Fatalf("AuditEntries of the other namespace = %d, %v, want none", len(got), err)
	}
}

func testSessionOfDeletedRunner(t *testing.T, h Harness) {
	t.Helper()

	l := handoffSetUp(t, h)

	s, err := h.Sessions.OpenSession(t.Context(), hash(l.secret), newKey(t), now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	if err = h.Domain.End(t.Context(), l.c); err != nil {
		t.Fatalf("End: %v", err)
	}

	if err = h.Pools.DeletePool(t.Context(), l.f.pool.ID, l.f.fence, entries(l.f.fence)); err != nil {
		t.Fatalf("DeletePool: %v", err)
	}

	_, err = h.Sessions.Session(t.Context(), s.ID)
	wantErr(t, "Session of a runner that the delete of its pool removed", err, runner.ErrRetired)
}
