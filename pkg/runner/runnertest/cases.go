package runnertest

import (
	"bytes"
	"errors"
	"fmt"
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

	first, err := join(t, f, joinToken(t, f, time.Hour), "cluster-a", now)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	later := now.Add(31 * 24 * time.Hour)

	again, err := join(t, f, joinTokenAt(t, f, later, time.Hour), "cluster-a", later)
	if err != nil || again.ID != first.ID || bytes.Equal(again.Key.Public, first.Key.Public) {
		t.Fatalf("Join again = %+v, %v, want ID %s with a new key", again, err, first.ID)
	}

	if _, err := h.Members.Heartbeat(t.Context(), again.ID, again.Release, later); err != nil {
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

	_, err = h.Members.RotateKey(t.Context(), r.ID, late, f.fence, entries(f.fence))
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

	if runners, err := h.Members.Runners(t.Context(), f.pool.ID); err != nil || len(runners) != 0 {
		t.Fatalf("Runners after Retire = %+v, %v", runners, err)
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

	got, err := h.Members.RotateKey(t.Context(), r.ID, next, f.fence, entries(f.fence))
	if err != nil || !bytes.Equal(got.Key.Public, next.Public) || !got.Key.CreatedAt.Equal(at) {
		t.Fatalf("RotateKey = %+v, %v", got, err)
	}

	if !bytes.Equal(got.PreviousKey.Public, r.Key.Public) || !got.RotatedAt.Equal(at) {
		t.Fatalf("RotateKey kept the previous key %x at %v, want %x at %v", got.PreviousKey.Public, got.RotatedAt,
			r.Key.Public, at)
	}

	retry := next
	retry.CreatedAt = at.Add(time.Minute)

	again, err := h.Members.RotateKey(t.Context(), r.ID, retry, f.fence, entries(f.fence))
	if err != nil || !again.Key.CreatedAt.Equal(at) || !bytes.Equal(again.PreviousKey.Public, r.Key.Public) {
		t.Fatalf("RotateKey retry = %+v, %v, want no change", again, err)
	}

	checkRotationKeys(t, &again, r.Key, at)

	if _, err := h.Members.Heartbeat(t.Context(), r.ID, r.Release, now.Add(runner.MaxKeyAge+time.Hour)); err != nil {
		t.Fatalf("Heartbeat after a rotation, past the age of the old key: %v", err)
	}
}

// handoff is a runner with a claim of its pool and a handoff token for it.
type handoff struct {
	f      fixture
	r      runner.Runner
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

	secret := runner.HandoffTokenPrefix + randomHex(t)
	tok := runner.HandoffToken{
		SHA256: hash(secret), Operation: string(c.Operation), Token: c.Token, Runner: r.ID,
		ExpiresAt: now.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

	if err = h.Sessions.IssueHandoffToken(t.Context(), tok, c); err != nil {
		t.Fatalf("IssueHandoffToken: %v", err)
	}

	return handoff{f: f, r: r, tok: tok, secret: secret}
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

	tok := runner.HandoffToken{
		SHA256: hash(runner.HandoffTokenPrefix + randomHex(t)), Operation: string(sweep.Operation), Token: sweep.Token,
		Runner: r.ID, ExpiresAt: now.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

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

	_, err = joinExpecting(t, f, runner.Joining{
		TokenSHA256: hash(secret), Request: joinRequest(t, secret, "cluster-a"), At: offline, Rejoin: "",
	})
	wantErr(t, "Join again that expects a new runner", err, runner.ErrConflict)

	again, err := join(t, f, secret, "cluster-a", offline)
	if err != nil || again.ID != first.ID || bytes.Equal(again.Key.Public, first.Key.Public) {
		t.Fatalf("Join again of an offline runner = %+v, %v, want ID %s with a new key", again, err, first.ID)
	}

	tok := runner.HandoffToken{
		SHA256: hash(runner.HandoffTokenPrefix + randomHex(t)), Operation: string(c.Operation), Token: c.Token,
		Runner: first.ID, ExpiresAt: offline.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

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

	stolen := runner.HandoffToken{
		SHA256: hash(runner.HandoffTokenPrefix + randomHex(t)), Operation: string(c.Operation), Token: c.Token,
		Runner: other.ID, ExpiresAt: now.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

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

	if err = h.Sessions.UseNonce(t.Context(), "rn-1", nonce, expires.Add(runner.NonceTTL), expires); err != nil {
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
