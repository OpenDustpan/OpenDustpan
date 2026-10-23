package runnertest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/runnertest"
)

// TestReferenceStore runs the suite against the reference adapter in
// memory, so each case of the suite runs before a SQL adapter exists.
func TestReferenceStore(t *testing.T) {
	t.Parallel()

	runnertest.Run(t, func(*testing.T) runnertest.Harness {
		m := newMem()

		return runnertest.Harness{Pools: m, Members: m, Sessions: m, Domain: m}
	})
}

// op is a durable operation of the reference adapter, with the expiry of
// its claim and the claim time that the session store recorded for it.
type op struct {
	kind      durable.Kind
	owner     string
	token     int64
	expires   time.Time
	claimedAt time.Time
	recorded  bool
	job       string
	phase     int
}

// nonceKey is a nonce of one subject.
type nonceKey struct {
	subject string
	nonce   string
}

// mem is the reference adapter: one lock over maps.
type mem struct {
	mu        sync.Mutex
	seq       int
	fences    map[namespace.Name]int64
	pools     map[string]runner.Pool
	inUse     map[string]bool
	tokens    map[string]runner.JoinToken
	runners   map[string]runner.Runner
	launches  map[string]runner.HandoffToken
	sessions  map[string]runner.Session
	ops       map[durable.ID]op
	entries   map[namespace.Name][]audit.Entry
	sessionOf map[durable.ID]string
	nonces    map[nonceKey]time.Time
}

func newMem() *mem {
	return &mem{
		mu: sync.Mutex{}, seq: 0, fences: map[namespace.Name]int64{}, pools: map[string]runner.Pool{},
		inUse: map[string]bool{}, tokens: map[string]runner.JoinToken{}, runners: map[string]runner.Runner{},
		launches: map[string]runner.HandoffToken{}, sessions: map[string]runner.Session{}, ops: map[durable.ID]op{},
		entries: map[namespace.Name][]audit.Entry{}, sessionOf: map[durable.ID]string{},
		nonces: map[nonceKey]time.Time{},
	}
}

// id returns a new ID with prefix.
func nextID(m *mem, prefix string) string {
	m.seq++

	return prefix + strconv.Itoa(m.seq)
}

// fenced checks fence and appends e.
func checkFence(m *mem, fence namespace.Fence) error {
	v, ok := m.fences[fence.Namespace]
	if !ok || v != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func appendAudit(m *mem, fence namespace.Fence, e []audit.Entry) {
	m.entries[fence.Namespace] = append(m.entries[fence.Namespace], e...)
}

// Domain.

func (m *mem) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

func (m *mem) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: m.fences[fence.Namespace]}, nil
}

// hour is the lease of the claims that Takeover makes.
const hour = time.Hour

func (m *mem) ClaimLease(
	_ context.Context, kind durable.Kind, owner string, lease time.Duration,
) (durable.Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := durable.ID(nextID(m, "op-"))
	expires := time.Now().Add(lease)
	m.ops[id] = op{
		kind: kind, owner: owner, token: 1, expires: expires, claimedAt: time.Time{}, recorded: false, job: "",
		phase: 0,
	}

	return durable.Claim{Operation: id, Owner: owner, Token: 1, ExpiresAt: expires}, nil
}

func (m *mem) Shorten(_ context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !liveClaim(m, c) {
		return durable.Claim{}, durable.ErrClaimLost
	}

	o := m.ops[c.Operation]
	o.expires = time.Now().Add(lease)
	m.ops[c.Operation] = o
	c.ExpiresAt = o.expires

	return c, nil
}

func (m *mem) Takeover(_ context.Context, c durable.Claim) (durable.Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	o, ok := m.ops[c.Operation]
	if !ok {
		return durable.Claim{}, durable.ErrNotFound
	}

	o.token++
	o.owner, o.claimedAt, o.recorded, o.expires = c.Owner, time.Time{}, false, time.Now().Add(hour)
	m.ops[c.Operation] = o

	return durable.Claim{Operation: c.Operation, Owner: c.Owner, Token: o.token, ExpiresAt: o.expires}, nil
}

func (m *mem) End(_ context.Context, c durable.Claim) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !liveClaim(m, c) {
		return durable.ErrClaimLost
	}

	delete(m.ops, c.Operation)

	return nil
}

func (m *mem) UsePool(_ context.Context, _ namespace.Fence, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.inUse[id] = true

	return nil
}

func (m *mem) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.entries[ns]), nil
}

// PoolStore.

func (m *mem) CreatePool(
	_ context.Context,
	p runner.Pool,
	fence namespace.Fence,
	e []audit.Entry,
) (runner.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return runner.Pool{}, err
	}

	if err := p.Validate(); err != nil {
		return runner.Pool{}, err
	}

	n := 0

	for k := range m.pools {
		q := m.pools[k]
		if q.Namespace != fence.Namespace {
			continue
		}

		n++

		if q.Name == p.Name {
			return runner.Pool{}, runner.ErrAlreadyExists
		}
	}

	if n >= runner.MaxPoolsPerNamespace {
		return runner.Pool{}, runner.ErrLimit
	}

	p.ID, p.Namespace = nextID(m, runner.PoolIDPrefix), fence.Namespace
	m.pools[p.ID] = p
	appendAudit(m, fence, e)

	return p, nil
}

func (m *mem) UpdatePool(
	_ context.Context,
	p runner.Pool,
	fence namespace.Fence,
	e []audit.Entry,
) (runner.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return runner.Pool{}, err
	}

	old, ok := m.pools[p.ID]
	if !ok || old.Namespace != fence.Namespace {
		return runner.Pool{}, runner.ErrNotFound
	}

	if err := runner.CheckUpdate(&old, &p); err != nil {
		return runner.Pool{}, err
	}

	m.pools[p.ID] = p
	appendAudit(m, fence, e)

	return p, nil
}

func (m *mem) DeletePool(_ context.Context, id string, fence namespace.Fence, e []audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return err
	}

	p, ok := m.pools[id]
	if !ok || p.Namespace != fence.Namespace {
		return runner.ErrNotFound
	}

	if m.inUse[id] {
		return runner.ErrPoolInUse
	}

	delete(m.pools, id)

	for k := range m.runners {
		if m.runners[k].Pool == id {
			delete(m.runners, k)
		}
	}

	for k := range m.tokens {
		if m.tokens[k].Pool == id {
			delete(m.tokens, k)
		}
	}

	appendAudit(m, fence, e)

	return nil
}

func (m *mem) Pool(_ context.Context, id string) (runner.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.pools[id]
	if !ok {
		return runner.Pool{}, runner.ErrNotFound
	}

	return p, nil
}

func (m *mem) Pools(_ context.Context, ns namespace.Name) ([]runner.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []runner.Pool

	for k := range m.pools {
		p := m.pools[k]
		if p.Namespace == ns {
			out = append(out, p)
		}
	}

	slices.SortFunc(out, func(a, b runner.Pool) int { return compare(a.Name, b.Name) })

	return out, nil
}

func (m *mem) CreateJoinToken(
	_ context.Context, t runner.JoinToken, fence namespace.Fence, e []audit.Entry,
) (runner.JoinToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return runner.JoinToken{}, err
	}

	if _, ok := m.pools[t.Pool]; !ok {
		return runner.JoinToken{}, runner.ErrNotFound
	}

	if _, err := runner.JoinTTL(t.ExpiresAt.Sub(t.CreatedAt)); err != nil {
		return runner.JoinToken{}, err
	}

	open := 0

	for k := range m.tokens {
		o := m.tokens[k]
		if o.Pool == t.Pool && o.CheckUse(t.CreatedAt) == nil {
			open++
		}
	}

	if open >= runner.MaxOpenJoinTokens {
		return runner.JoinToken{}, runner.ErrLimit
	}

	t.ID = nextID(m, runner.JoinTokenIDPrefix)
	m.tokens[t.ID] = t
	appendAudit(m, fence, e)

	return t, nil
}

func (m *mem) RevokeJoinToken(_ context.Context, id string, fence namespace.Fence, e []audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return err
	}

	t, ok := m.tokens[id]

	switch {
	case !ok:
		return runner.ErrNotFound
	case !t.UsedAt.IsZero():
		return runner.ErrTokenUsed
	case !t.RevokedAt.IsZero():
		return nil
	}

	t.RevokedAt = t.CreatedAt
	m.tokens[id] = t
	appendAudit(m, fence, e)

	return nil
}

func (m *mem) JoinTokens(_ context.Context, pool string) ([]runner.JoinToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []runner.JoinToken

	for k := range m.tokens {
		t := m.tokens[k]
		if t.Pool == pool {
			out = append(out, t)
		}
	}

	return out, nil
}

// MemberStore.

func tokenByHash(m *mem, sum string) (runner.JoinToken, bool) {
	for k := range m.tokens {
		t := m.tokens[k]
		if t.SHA256 == sum {
			return t, true
		}
	}

	var none runner.JoinToken

	return none, false
}

func (m *mem) LookupJoin(_ context.Context, sum string) (runner.JoinToken, runner.Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := tokenByHash(m, sum)
	if !ok {
		return runner.JoinToken{}, runner.Pool{}, runner.ErrNotFound
	}

	return t, m.pools[t.Pool], nil
}

func (m *mem) Join(
	_ context.Context, j runner.Joining, fence namespace.Fence, e runner.JoinAudit,
) (runner.Joined, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return runner.Joined{}, err
	}

	req, now := j.Request, j.At

	t, ok := tokenByHash(m, j.TokenSHA256)
	if !ok {
		return runner.Joined{}, runner.ErrNotFound
	}

	if err := t.CheckUse(now); err != nil {
		return runner.Joined{}, err
	}

	p := m.pools[t.Pool]
	if !p.Allows(req.Isolation) {
		return runner.Joined{}, fmt.Errorf(
			"%w: the pool does not allow the isolation %s",
			runner.ErrInvalid,
			req.Isolation,
		)
	}

	r := runnerNamed(m, p.ID, req.Name)
	rejoined := r.ID != ""
	previousKeyID := r.Key.ID()

	var ended []string

	switch {
	case !rejoined:
		r.ID = nextID(m, runner.RunnerIDPrefix)
		r.JoinedAt = now
	case !r.CanRejoin(now):
		return runner.Joined{}, runner.ErrRunnerOnline
	default:
		ended = expireWaitingClaims(m, r.ID)
	}

	key := req.Key
	key.CreatedAt = now
	r.Pool, r.Namespace, r.Name, r.Kind, r.Isolation = p.ID, p.Namespace, req.Name, req.Kind, req.Isolation
	r.Labels, r.Release, r.Key, r.JoinedWith, r.LastSeenAt = req.Labels, req.Release, key, t.ID, now
	r.PreviousKey, r.RotatedAt = runner.Key{Public: nil, CreatedAt: time.Time{}}, time.Time{}
	m.runners[r.ID] = r

	t.UsedAt, t.UsedBy = now, r.ID
	m.tokens[t.ID] = t

	out := runner.Joined{Runner: r, Rejoined: rejoined, PreviousKeyID: "", EndedClaims: ended}
	if rejoined {
		out.PreviousKeyID = previousKeyID
	}

	appendAudit(m, fence, e(&out))

	return out, nil
}

// runnerNamed returns the runner of pool called name that is not retired,
// or a zero runner.
func runnerNamed(m *mem, pool, name string) runner.Runner {
	for k := range m.runners {
		r := m.runners[k]
		if r.Pool == pool && r.Name == name && r.RetiredAt.IsZero() {
			return r
		}
	}

	var none runner.Runner

	return none
}

func (m *mem) Runner(_ context.Context, id string) (runner.Runner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.runners[id]
	if !ok {
		return runner.Runner{}, runner.ErrNotFound
	}

	return r, nil
}

func (m *mem) Runners(_ context.Context, pool string) ([]runner.Runner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []runner.Runner

	for k := range m.runners {
		r := m.runners[k]
		if r.Pool == pool && r.RetiredAt.IsZero() {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b runner.Runner) int { return compare(a.Name, b.Name) })

	return out, nil
}

// live returns the runner id when its key authenticates at now.
func liveRunner(m *mem, id string, now time.Time) (runner.Runner, error) {
	r, ok := m.runners[id]

	switch {
	case !ok:
		return runner.Runner{}, runner.ErrNotFound
	case !r.RetiredAt.IsZero():
		return runner.Runner{}, runner.ErrRetired
	case r.Key.ExpiredAt(now):
		return runner.Runner{}, runner.ErrKeyExpired
	default:
		return r, nil
	}
}

func (m *mem) Heartbeat(_ context.Context, id string, rel runner.Release, now time.Time) (runner.Runner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := liveRunner(m, id, now)
	if err != nil {
		return runner.Runner{}, err
	}

	r.Release, r.LastSeenAt = rel, now
	m.runners[id] = r

	return r, nil
}

func (m *mem) RotateKey(
	_ context.Context, rot runner.Rotation, fence namespace.Fence, e []audit.Entry,
) (runner.Runner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return runner.Runner{}, err
	}

	id, next := rot.Runner, rot.Next
	if err := next.Validate(); err != nil {
		return runner.Runner{}, err
	}

	if own, ok := m.runners[id]; ok && own.Namespace != fence.Namespace {
		return runner.Runner{}, runner.ErrNotFound
	}

	r, err := liveRunner(m, id, next.CreatedAt)
	if err != nil {
		return runner.Runner{}, err
	}

	rotate, err := r.CheckRotation(rot.Signer, next, next.CreatedAt)
	if err != nil || !rotate {
		return r, err
	}

	r.PreviousKey, r.RotatedAt, r.Key = r.Key, next.CreatedAt, next
	m.runners[id] = r
	appendAudit(m, fence, e)

	return r, nil
}

func (m *mem) SetDraining(_ context.Context, id string, draining bool, fence namespace.Fence, e []audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return err
	}

	r, ok := m.runners[id]

	switch {
	case !ok, r.Namespace != fence.Namespace:
		return runner.ErrNotFound
	case !r.RetiredAt.IsZero():
		return runner.ErrRetired
	}

	r.Draining = draining
	m.runners[id] = r
	appendAudit(m, fence, e)

	return nil
}

func (m *mem) Retire(_ context.Context, id string, now time.Time, fence namespace.Fence, e []audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return err
	}

	r, ok := m.runners[id]

	switch {
	case !ok, r.Namespace != fence.Namespace:
		return runner.ErrNotFound
	case !r.RetiredAt.IsZero():
		return nil
	}

	r.RetiredAt = now
	m.runners[id] = r
	expireAllClaims(m, id)
	appendAudit(m, fence, e)

	return nil
}

// expireAllClaims ends every live claim of the runner id, as an expiry
// does: the next claim of each operation gets a new token.
func expireAllClaims(m *mem, id string) {
	for k, o := range m.ops {
		if o.owner == id {
			releaseOp(m, k)
		}
	}
}

// expireWaitingClaims ends the live claims of the runner id that have no
// job session, and returns their operation IDs in order.
func expireWaitingClaims(m *mem, id string) []string {
	var ended []string

	for k, o := range m.ops {
		if o.owner == id && !hasSession(m, k, o.token) {
			releaseOp(m, k)

			ended = append(ended, string(k))
		}
	}

	slices.Sort(ended)

	return ended
}

// releaseOp ends the live claim of the operation id.
func releaseOp(m *mem, id durable.ID) {
	o := m.ops[id]
	o.token++
	o.owner, o.claimedAt, o.recorded, o.expires = "", time.Time{}, false, time.Time{}
	m.ops[id] = o
}

// hasSession reports whether the claim token of the operation id has a
// job session.
func hasSession(m *mem, id durable.ID, token int64) bool {
	sid, ok := m.sessionOf[id]

	return ok && m.sessions[sid].Token == token
}

// SessionStore.

func liveClaim(m *mem, c durable.Claim) bool {
	o, ok := m.ops[c.Operation]

	return ok && o.token == c.Token && o.owner == c.Owner && o.expires.After(time.Now())
}

func (m *mem) IssueHandoffToken(_ context.Context, t runner.HandoffToken, c durable.Claim) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !liveClaim(m, c) || c.Owner != t.Runner || m.ops[c.Operation].owner != t.Runner {
		return runner.ErrClaimLost
	}

	r, ok := m.runners[t.Runner]
	if !ok {
		return runner.ErrNotFound
	}

	p := m.pools[r.Pool]

	kinds, err := runner.ClaimKinds(&p)
	if err != nil {
		return err
	}

	if !slices.Contains(kinds, m.ops[c.Operation].kind) {
		return fmt.Errorf("%w: the operation is not of a kind of the pool", runner.ErrInvalid)
	}

	if hasSession(m, c.Operation, c.Token) {
		return runner.ErrConflict
	}

	for h := range m.launches {
		if o := m.launches[h]; o.Operation == t.Operation && o.Token == t.Token {
			delete(m.launches, h)
		}
	}

	m.launches[t.SHA256] = t
	recordClaimTime(m, c.Operation, t.IssuedAt, "", 0)

	return nil
}

// recordClaimTime records the claim time at of the live claim of the
// operation id, with its job and phase, when it has no record.
func recordClaimTime(m *mem, id durable.ID, at time.Time, jobID string, phase int) {
	if o := m.ops[id]; !o.recorded {
		o.claimedAt, o.recorded, o.job, o.phase = at, true, jobID, phase
		m.ops[id] = o
	}
}

func (m *mem) RecordClaim(_ context.Context, c durable.Claim, jobID string, phase int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !liveClaim(m, c) {
		return runner.ErrClaimLost
	}

	recordClaimTime(m, c.Operation, time.Now(), jobID, phase)

	return nil
}

func (m *mem) LookupClaim(_ context.Context, c durable.Claim) (runner.ClaimRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !liveClaim(m, c) {
		return runner.ClaimRecord{}, runner.ErrClaimLost
	}

	o := m.ops[c.Operation]
	if !o.recorded {
		return runner.ClaimRecord{}, runner.ErrNotFound
	}

	return runner.ClaimRecord{
		Job: o.job, Phase: o.phase, ClaimedAt: o.claimedAt, HasSession: hasSession(m, c.Operation, c.Token),
	}, nil
}

func (m *mem) OpenSession(_ context.Context, sum string, k runner.Key, now time.Time) (runner.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.launches[sum]
	if !ok {
		return runner.Session{}, runner.ErrNotFound
	}

	if err := t.CheckUse(now); err != nil {
		return runner.Session{}, err
	}

	op := durable.ID(t.Operation)
	if !liveClaim(m, durable.Claim{Operation: op, Owner: t.Runner, Token: t.Token, ExpiresAt: time.Time{}}) {
		return runner.Session{}, runner.ErrClaimLost
	}

	if !m.ops[op].recorded || now.Sub(m.ops[op].claimedAt) > runner.MaxSessionWait {
		return runner.Session{}, fmt.Errorf("%w: no job session %v after the claim", runner.ErrClaimLost,
			runner.MaxSessionWait)
	}

	t.UsedAt = now
	m.launches[sum] = t

	s := runner.Session{
		ID: nextID(m, runner.SessionIDPrefix), Operation: t.Operation, Token: t.Token, Runner: t.Runner,
		Key: runner.Key{Public: k.Public, CreatedAt: now}, CreatedAt: now,
	}
	m.sessions[s.ID] = s
	m.sessionOf[op] = s.ID

	return s, nil
}

func (m *mem) RunnerRenew(
	_ context.Context, c durable.Claim, fence namespace.Fence, d time.Duration, now time.Time,
) (durable.Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := checkFence(m, fence); err != nil {
		return durable.Claim{}, err
	}

	if d <= 0 {
		return durable.Claim{}, fmt.Errorf("%w: a renewal needs a positive lease", runner.ErrInvalid)
	}

	if !liveClaim(m, c) {
		return durable.Claim{}, runner.ErrClaimLost
	}

	if !m.ops[c.Operation].recorded {
		return durable.Claim{}, fmt.Errorf("%w: the claim has no record", runner.ErrClaimLost)
	}

	err := runner.CheckRunnerRenew(m.ops[c.Operation].claimedAt, hasSession(m, c.Operation, c.Token), now)
	if errors.Is(err, runner.ErrClaimLost) {
		return durable.Claim{}, runner.ErrSessionWaitPassed
	}

	if err != nil {
		return durable.Claim{}, err
	}

	o := m.ops[c.Operation]
	o.expires = time.Now().Add(d)
	m.ops[c.Operation] = o
	c.ExpiresAt = o.expires

	return c, nil
}

func (m *mem) Session(_ context.Context, id string) (runner.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return runner.Session{}, runner.ErrNotFound
	}

	// A runner that no longer exists authenticates nothing, as a retired
	// runner does.
	if rn, found := m.runners[s.Runner]; !found || !rn.RetiredAt.IsZero() {
		return runner.Session{}, runner.ErrRetired
	}

	c := durable.Claim{Operation: durable.ID(s.Operation), Owner: s.Runner, Token: s.Token, ExpiresAt: time.Time{}}
	if !liveClaim(m, c) {
		return runner.Session{}, runner.ErrClaimLost
	}

	return s, nil
}

func (m *mem) UseNonce(_ context.Context, subject, nonce string, expires, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := nonceKey{subject: subject, nonce: nonce}
	if until, ok := m.nonces[k]; ok && !now.After(until) {
		return runner.ErrReplay
	}

	m.nonces[k] = expires

	return nil
}

// compare orders two strings.
func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
