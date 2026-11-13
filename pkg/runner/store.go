package runner

import (
	"context"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// PoolStore is one of three persistence ports of this package: PoolStore for pools and
// join tokens, MemberStore for runners and their keys, and SessionStore for
// handoff tokens and job sessions. Each SQL store adapter implements the
// three in its own SQL, on one database, and runs the conformance suite in
// package runnertest.
//
// Pools, runners and join tokens are data of their namespace: the backup of
// the namespace holds them, and every write checks the fence and appends
// the audit entries e in its transaction. Handoff tokens and job sessions
// live as long as a durable claim. They are control data, which a backup
// does not hold: after a restore, every launcher opens a new session or
// stops.
//
// Every method that writes returns namespace.ErrFenced for a stale fence,
// and changes nothing on an error: its audit entries roll back with it.
// The stores keep the SHA-256 of each secret, never the secret.
//
// PoolStore holds pools and join tokens.
type PoolStore interface {
	// CreatePool persists p and returns it. It returns ErrAlreadyExists
	// for a name that the namespace has, ErrLimit when the namespace has
	// MaxPoolsPerNamespace pools, and the error of Pool.Validate.
	CreatePool(ctx context.Context, p Pool, fence namespace.Fence, e []audit.Entry) (Pool, error)
	// UpdatePool replaces the settings of the pool p.ID with p, and returns
	// it. It returns ErrNotFound for an unknown pool, and the error of
	// CheckUpdate: ErrImmutable for a change of the purpose. A runner that
	// joined with an isolation that the new settings refuse keeps its key,
	// but claims nothing until an administrator retires it or changes the
	// pool again.
	UpdatePool(ctx context.Context, p Pool, fence namespace.Fence, e []audit.Entry) (Pool, error)
	// DeletePool deletes the pool id, its runners and its join tokens. It
	// returns ErrNotFound for an unknown pool, and ErrPoolInUse while a job
	// that is not terminal or a sweep target names it.
	DeletePool(ctx context.Context, id string, fence namespace.Fence, e []audit.Entry) error
	// Pool returns the pool id, or ErrNotFound.
	Pool(ctx context.Context, id string) (Pool, error)
	// Pools returns the pools of ns, ordered by name.
	Pools(ctx context.Context, ns namespace.Name) ([]Pool, error)

	// CreateJoinToken persists t for the pool t.Pool. t.SHA256 is the hash
	// of the secret, which only the caller sees, once. It returns
	// ErrNotFound for an unknown pool, ErrInvalid for an expiry that
	// JoinTTL refuses, and ErrLimit when the pool has MaxOpenJoinTokens
	// open tokens.
	CreateJoinToken(ctx context.Context, t JoinToken, fence namespace.Fence, e []audit.Entry) (JoinToken, error)
	// RevokeJoinToken revokes the join token id. It returns ErrNotFound for
	// an unknown token, and ErrTokenUsed for a token that a join used. A
	// revoke of a revoked token changes nothing.
	RevokeJoinToken(ctx context.Context, id string, fence namespace.Fence, e []audit.Entry) error
	// JoinTokens returns the join tokens of the pool, newest first.
	JoinTokens(ctx context.Context, pool string) ([]JoinToken, error)
}

// MemberStore is the persistence port for runners and their keys.
type MemberStore interface {
	// LookupJoin returns the join token whose secret has the hash sha256,
	// and its pool, so that the caller can read the fence of the namespace
	// of the pool before Join. It returns ErrNotFound for an unknown hash.
	// It changes nothing.
	LookupJoin(ctx context.Context, sha256 string) (JoinToken, Pool, error)
	// Join registers the runner of j.Request with the join token whose
	// secret has the hash j.TokenSHA256, at j.At. In one transaction it
	// checks the token with JoinToken.CheckUse, marks it used, and stores
	// the runner with j.Request.Key, created at j.At. It appends the audit
	// entries that e returns for the result, so the caller picks
	// EventRunnerJoined or EventRunnerRejoined (JoinEvent) from what the
	// store found, not from an earlier read.
	//
	// A runner name that the pool has, of a runner that is not retired, is
	// a join again (Joined.Rejoined): the runner keeps its ID and gets the
	// new key, only when Runner.CanRejoin is true at j.At, else Join
	// returns ErrRunnerOnline. A join again clears Runner.PreviousKey and
	// Runner.RotatedAt, so no key of the runner before the join works. In
	// the same transaction it expires every live claim of the runner that
	// has no job session, so a takeover gets those claims. A claim with a
	// job session stays: its launcher holds its own session key, renews
	// the claim through its session, and runs until it ends.
	//
	// It returns ErrNotFound for an unknown hash, ErrTokenUsed and
	// ErrTokenExpired from CheckUse, ErrInvalid for an isolation that the
	// pool does not allow, and ErrLimit when the pool has
	// MaxRunnersPerPool runners. The caller checks JoinRequest.Validate and
	// the proof of j.Request against JoinChallenge first.
	Join(ctx context.Context, j Joining, fence namespace.Fence, e JoinAudit) (Joined, error)

	// Runner returns the runner id, also a retired one, or ErrNotFound.
	Runner(ctx context.Context, id string) (Runner, error)
	// Runners returns the runners of the pool that are not retired, ordered
	// by name.
	Runners(ctx context.Context, pool string) ([]Runner, error)
	// Heartbeat records a request of the runner id at now with the release
	// rel, and returns the runner. It returns ErrNotFound for an unknown
	// runner, ErrRetired for a retired one, and ErrKeyExpired when the key
	// of the runner is older than MaxKeyAge. It appends no audit entry.
	Heartbeat(ctx context.Context, id string, rel Release, now time.Time) (Runner, error)
	// RotateKey replaces the key of the runner r.Runner with r.Next, whose
	// CreatedAt is the time of the rotation, and returns the runner. The
	// proof of the request is of the key with the ID r.Signer. In one
	// transaction it checks the request with Runner.CheckRotation: a proof
	// of the current key rotates, and a proof of Runner.PreviousKey in
	// RotateGrace is only a retry, with r.Next equal to the current key. It
	// keeps the old key as Runner.PreviousKey, with Runner.RotatedAt. A
	// request that changes nothing appends no audit entry. It returns
	// ErrBadProof from CheckRotation, ErrInvalid for a next key that is not
	// valid, and ErrNotFound, ErrRetired and ErrKeyExpired as Heartbeat
	// does. The caller checks the proof against Runner.RotationKeys and a
	// signature of RotateChallenge by r.Next first.
	RotateKey(ctx context.Context, r Rotation, fence namespace.Fence, e []audit.Entry) (Runner, error)
	// SetDraining sets or clears the drain of the runner id. A draining
	// runner claims no new work. It returns ErrNotFound and ErrRetired.
	SetDraining(ctx context.Context, id string, draining bool, fence namespace.Fence, e []audit.Entry) error
	// Retire retires the runner id at now. Its key then authenticates
	// nothing. In the same transaction it expires every live claim of the
	// runner, also a claim with a job session, so its claims go to a
	// takeover, and it ends every job session of the runner:
	// SessionStore.Session returns ErrRetired for them. It
	// returns ErrNotFound for an unknown runner. A retire of a retired
	// runner changes nothing. The runner management API accepts a retire
	// in maintenance mode, because a retire reduces the work of the runners.
	Retire(ctx context.Context, id string, now time.Time, fence namespace.Fence, e []audit.Entry) error
}

// SessionStore is the persistence port for handoff tokens and job sessions.
//
// The claim time of a claim of a runner is the database time of its
// RecordClaim, which the poll calls right after the claim. The store
// records it once for each claim. A claim with no record gets the IssuedAt
// of its first handoff token as its claim time, and a new handoff token for
// the same claim never moves it. MaxSessionWait counts from the claim time.
//
// A claim is live while its operation is running with the owner and the
// token of the claim, and its lease did not pass on the clock of the
// database. A lease that passed ends the claim for every method of this
// port, as an expiry does.
type SessionStore interface {
	// RecordClaim records the claim c of its runner, with the job and the
	// phase that it works on (job is empty for a sweep), and the claim time:
	// the time of the database. It runs in its own transaction. A second
	// call for the same claim changes nothing. It returns ErrClaimLost when
	// c is not live.
	RecordClaim(ctx context.Context, c durable.Claim, job string, phase int) error
	// LookupClaim returns what RecordClaim stored for the claim c, and
	// whether the claim has a job session. It returns ErrNotFound for a
	// claim with no record, and ErrClaimLost when c is not live.
	LookupClaim(ctx context.Context, c durable.Claim) (ClaimRecord, error)
	// IssueHandoffToken stores t for the live claim c of the runner
	// t.Runner, and records the claim time t.IssuedAt when c has no record. It
	// returns ErrClaimLost when c is not the live claim of its operation,
	// or when the owner of c is not t.Runner, so a runner never gets a
	// token for the claim of another runner. It returns ErrInvalid when
	// the operation is not of a kind of the pool of the runner
	// (ClaimKinds), and ErrConflict when the claim has a session already.
	// A second token for a claim with no session replaces the first: the
	// first then opens nothing (ErrNotFound). So a pod that waited past
	// HandoffTokenTTL can get a new one, until MaxSessionWait.
	IssueHandoffToken(ctx context.Context, t HandoffToken, c durable.Claim) error
	// OpenSession exchanges the handoff token whose secret has the hash
	// sha256 for a job session with the key k, at now. In one transaction
	// it checks the token with HandoffToken.CheckUse, checks that its claim
	// is live and that now is not later than MaxSessionWait after the
	// claim time, marks the token used and stores the session. It returns
	// ErrNotFound for an unknown hash, ErrTokenUsed and ErrTokenExpired
	// from CheckUse, and ErrClaimLost when the claim is not live or when
	// MaxSessionWait passed. A session at exactly MaxSessionWait opens.
	OpenSession(ctx context.Context, sha256 string, k Key, now time.Time) (Session, error)
	// RunnerRenew is the renewal of the claim c by its runner, before the
	// launcher opens a session. In one transaction, with the rules of
	// CheckRunnerRenew, it checks the fence (namespace.ErrFenced for a stale
	// fence) and then:
	//   - returns ErrClaimLost when c is not live, or when its owner is not
	//     the runner of the request;
	//   - returns ErrSessionOpen when the claim has a job session. It does
	//     not renew: the launcher renews the claim through its session.
	//   - returns ErrSessionWaitPassed (it wraps ErrClaimLost) when now is
	//     later than MaxSessionWait after the claim time and the claim has
	//     no session. It changes nothing: the caller releases the claim
	//     through job.Claimer or SweepClaimer, which apply their own rules.
	//   - else moves the expiry of c to the time of the database plus d and
	//     returns the claim.
	//
	// The argument now is only for the session wait limit. It returns
	// ErrClaimLost for a claim with no record (RecordClaim), and ErrInvalid
	// for a d that is not positive.
	RunnerRenew(
		ctx context.Context, c durable.Claim, fence namespace.Fence, d time.Duration, now time.Time,
	) (durable.Claim, error)
	// SessionRenew is the renewal of the claim of the job session id by its
	// launcher, which holds the claim from the exchange of its session. In one
	// transaction it checks the fence (namespace.ErrFenced for a stale fence)
	// and then:
	//   - returns ErrNotFound for an unknown session;
	//   - returns ErrRetired when an administrator retired the runner of the
	//     session;
	//   - returns ErrClaimLost when the claim of the session is no longer
	//     live, so the launcher of a claim that another claim replaced
	//     renews nothing;
	//   - else moves the expiry of the claim to the time of the database plus
	//     d, also while a cancel is requested, and returns the claim.
	//
	// The renewal during a cancel is the difference from durable.Store.Extend,
	// which refuses it: the launcher stops its step with the grace period of
	// the step (up to 10 minutes) and then writes its last log chunks and its
	// finish, so its claim must stay live until then. It returns ErrInvalid
	// for a d that is not positive.
	SessionRenew(ctx context.Context, id string, fence namespace.Fence, d time.Duration) (durable.Claim, error)
	// Session returns the session id, or ErrNotFound. It returns
	// ErrClaimLost when the claim of the session is no longer live, so a
	// launcher of a claim that another claim replaced writes nothing, and
	// ErrRetired when an administrator retired the runner of the session.
	Session(ctx context.Context, id string) (Session, error)

	// UseNonce records the nonce of a proof of subject, a runner ID, a
	// session ID or the hash of a handoff token, and keeps it until
	// expires (NonceExpiry). It returns ErrReplay when the store holds the
	// same nonce for subject and now is not after its expiry: up to and
	// at the expiry, so a replay at exactly the end of ProofWindow, which
	// CheckProof accepts, still fails. A nonce after its expiry is free
	// again. The protocol adapter calls it after CheckProof, on the paths
	// that protocol.NeedsNonce names, with the clock of its instance. The
	// store deletes a nonce only after its expiry on the clock of the
	// instance that deletes it. NonceTTL allows for MaxClockSkew between
	// the instances, so no instance accepts a proof whose nonce another
	// instance deleted. Nonces are control data, and a backup does not
	// hold them.
	UseNonce(ctx context.Context, subject, nonce string, expires, now time.Time) error
}

// SweepClaimer is the port through which the runner protocol claims the
// sweeps of a pool for sweeps, as job.Claimer claims the phases of a pool
// for jobs. A sweep is a durable operation of SweepKind. The design of
// sweeps owns the adapter. The protocol adds no rule to it.
type SweepClaimer interface {
	// Claim takes one sweep of req.Kinds that can run, and returns its
	// work and the fence of the namespace of the pool. It returns
	// durable.ErrNoWork when no sweep can run now, and
	// ErrInvalid for a request with no owner, a kind that is not a
	// SweepKind, or a lease that is not positive.
	Claim(ctx context.Context, req durable.ClaimRequest) (SweepClaim, error)
	// Renew moves the expiry of c to now plus d on the clock of the
	// store, and returns the new claim. It returns ErrClaimLost when c is
	// not live, and namespace.ErrFenced when the fence is stale.
	Renew(ctx context.Context, c durable.Claim, fence namespace.Fence, d time.Duration) (durable.Claim, error)
	// Release gives the sweep back before its report, so that the next
	// claim runs it from its start. It returns ErrClaimLost when c is not
	// live, and namespace.ErrFenced when the fence is stale.
	Release(ctx context.Context, c durable.Claim, fence namespace.Fence) error
	// Report stores the report of the sweep from r, and ends the claim and
	// the operation in one transaction: the upload of the report is the
	// end of a sweep. It returns ErrClaimLost when c is not live, and
	// ErrInvalid for a report above the limit of the design of sweeps. It
	// returns namespace.ErrFenced when the fence is stale.
	Report(ctx context.Context, c durable.Claim, fence namespace.Fence, r io.Reader) error
}

// SweepClaim is one claimed sweep: the claim, the fence of the namespace of
// the pool and the work of the coordinator.
type SweepClaim struct {
	Claim durable.Claim
	Fence namespace.Fence
	Work  Work
}

// Joining is the input of MemberStore.Join. The caller clears the secret
// of the join token in Request.Token: the store gets only its hash.
type Joining struct {
	TokenSHA256 string
	Request     JoinRequest
	At          time.Time
}

// Rotation is the input of MemberStore.RotateKey.
type Rotation struct {
	// Runner is the ID of the runner.
	Runner string
	// Signer is the ID of the key of the proof of the request (Key.ID).
	Signer string
	// Next is the new key. Its CreatedAt is the time of the rotation.
	Next Key
}

// Joined is the result of MemberStore.Join: the runner, and whether the
// join was a join again of a runner of the same name. For a join again,
// PreviousKeyID is the ID of the key that the runner had before the join,
// and EndedClaims holds the operation IDs of the claims that the join
// ended. Both are empty for a first join.
type Joined struct {
	Runner        Runner
	Rejoined      bool
	PreviousKeyID string
	EndedClaims   []string
}

// ClaimRecord is what RecordClaim stored for a claim.
type ClaimRecord struct {
	// Job is the ID of the job of the phase, or empty for a sweep.
	Job string
	// Phase is the index of the phase of the job.
	Phase int
	// ClaimedAt is the claim time, on the clock of the database.
	ClaimedAt time.Time
	// HasSession is true when the claim has a job session.
	HasSession bool
}

// JoinAudit returns the audit entries of a join, from its result. The
// store calls it in the transaction of the join, before the commit.
type JoinAudit func(j *Joined) []audit.Entry

// JoinEvent returns the event of the audit entry of the join j:
// EventRunnerRejoined for a join again, else EventRunnerJoined.
func JoinEvent(j *Joined) Event {
	if j.Rejoined {
		return EventRunnerRejoined
	}

	return EventRunnerJoined
}
