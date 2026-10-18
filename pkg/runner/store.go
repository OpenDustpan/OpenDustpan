package runner

import (
	"context"
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
	// that is not terminal or a scan target names it.
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
	// the runner with j.Request.Key, created at j.At. A runner name that the pool has, of a runner
	// that is not retired, keeps its runner ID and gets the new key: this
	// is a join again. It returns ErrNotFound for an unknown hash,
	// ErrTokenUsed and ErrTokenExpired from CheckUse, ErrInvalid for an
	// isolation that the pool does not allow, and ErrLimit when the pool
	// has MaxRunnersPerPool runners. The caller checks JoinRequest.Validate
	// and the proof of j.Request against JoinChallenge first.
	Join(ctx context.Context, j Joining, fence namespace.Fence, e []audit.Entry) (Runner, error)

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
	// RotateKey replaces the key of the runner id with next, whose
	// CreatedAt is the time of the rotation, and returns the runner. It returns ErrNotFound, ErrRetired and
	// ErrKeyExpired as Heartbeat does. The caller checks a proof of the
	// old key and a signature of RotateChallenge by next first. A rotation
	// to the current key changes nothing, so a retry after a lost answer is
	// safe.
	RotateKey(ctx context.Context, id string, next Key, fence namespace.Fence, e []audit.Entry) (Runner, error)
	// SetDraining sets or clears the drain of the runner id. A draining
	// runner claims no new work. It returns ErrNotFound and ErrRetired.
	SetDraining(ctx context.Context, id string, draining bool, fence namespace.Fence, e []audit.Entry) error
	// Retire retires the runner id at now. Its key then authenticates
	// nothing, and its claims expire and go to a takeover. It returns
	// ErrNotFound for an unknown runner. A retire of a retired runner
	// changes nothing.
	Retire(ctx context.Context, id string, now time.Time, fence namespace.Fence, e []audit.Entry) error
}

// SessionStore is the persistence port for handoff tokens and job sessions.
type SessionStore interface {
	// IssueHandoffToken stores t for the live claim c of the runner t.Runner.
	// It returns ErrClaimLost when c is not the live claim of its
	// operation, ErrInvalid when the operation is not of a kind of the pool
	// of the runner (ClaimKinds), and ErrConflict when the claim has a
	// session already. A second token for a claim with no session replaces
	// the first, so a pod that waited past HandoffTokenTTL can get a new
	// one.
	IssueHandoffToken(ctx context.Context, t HandoffToken, c durable.Claim) error
	// OpenSession exchanges the handoff token whose secret has the hash
	// sha256 for a job session with the key k, at now. In one transaction
	// it checks the token with HandoffToken.CheckUse, checks that its claim
	// is live, marks the token used and stores the session. It returns
	// ErrNotFound for an unknown hash, ErrTokenUsed and ErrTokenExpired
	// from CheckUse, and ErrClaimLost when the claim is not live.
	OpenSession(ctx context.Context, sha256 string, k Key, now time.Time) (Session, error)
	// Session returns the session id, or ErrNotFound. It returns
	// ErrClaimLost when the claim of the session is no longer live, so a
	// launcher of a claim that another claim replaced writes nothing.
	Session(ctx context.Context, id string) (Session, error)
}

// Joining is the input of MemberStore.Join. The caller clears the secret
// of the join token in Request.Token: the store gets only its hash.
type Joining struct {
	TokenSHA256 string
	Request     JoinRequest
	At          time.Time
}
