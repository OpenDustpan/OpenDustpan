// Package runnertest is the conformance suite for runner.PoolStore,
// runner.MemberStore and runner.SessionStore implementations. Every store
// adapter runs it:
//
//	func TestRunnerConformance(t *testing.T) {
//		t.Parallel()
//		runnertest.Run(t, func(t *testing.T) runnertest.Harness { return newHarness(t) })
//	}
//
// The suite comes before the first SQL adapter. Its cases state the rules
// of the ports, and a reference adapter in memory, in the tests of this
// package, runs them. The cases of the runner protocol over HTTP come with
// its adapter.
package runnertest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// Harness is one fresh, empty database for one case. The stores share the
// database, as the store adapters of one installation do.
type Harness struct {
	Pools    runner.PoolStore
	Members  runner.MemberStore
	Sessions runner.SessionStore
	Domain   Domain
}

// Domain makes the rows that the stores need and that other ports own,
// and reads the audit entries that the stores append.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// Claim starts a durable operation of kind and claims it for owner,
	// as the runner protocol does at a poll, and returns the claim.
	Claim(ctx context.Context, kind durable.Kind, owner string) (durable.Claim, error)
	// ClaimLease is Claim with the lease of the claim. A claim is live
	// only while its lease did not pass on the clock of the database.
	ClaimLease(ctx context.Context, kind durable.Kind, owner string, lease time.Duration) (durable.Claim, error)
	// Shorten sets the expiry of the live claim c to the time of the
	// database plus lease, also when that is earlier than the expiry of c,
	// and returns the claim. A case claims with a long lease, does its
	// writes, and then shortens the lease to let the claim pass.
	Shorten(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error)
	// Takeover expires the claim c and claims its operation again, as a
	// takeover does, and returns the new claim.
	Takeover(ctx context.Context, c durable.Claim) (durable.Claim, error)
	// End completes the operation of the live claim c, as the end of a
	// phase does. The operation then names no pool.
	End(ctx context.Context, c durable.Claim) error
	// UsePool makes a job that is not terminal name the pool id, so a
	// delete of the pool fails.
	UsePool(ctx context.Context, fence namespace.Fence, id string) error
	// AuditEntries returns the audit entries of the namespace ns, in the
	// order of their append.
	AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"create stores a pool and get returns it", testCreatePool},
		{"a pool name is unique in its namespace", testPoolNameUnique},
		{"the purpose of a pool never changes", testPurposeImmutable},
		{"a namespace has at most 50 pools", testPoolLimit},
		{"a stale fence changes nothing and appends nothing", testStaleFence},
		{"a pool in use cannot be deleted", testPoolInUse},
		{"a join token registers one runner", testJoinTokenSingleUse},
		{"an expired or revoked join token registers nothing", testJoinTokenExpiredRevoked},
		{"the pool decides the isolation of a runner", testJoinIsolation},
		{"a join again with one name keeps the runner ID and clears the previous key", testRejoinKeepsID},
		{"a join again needs an offline runner and ends its claims", testRejoinNeedsOffline},
		{"a join again keeps the sessions of launchers", testRejoinKeepsSessions},
		{"a key older than 30 days must join again", testKeyExpired},
		{"a retired runner authenticates nothing", testRetired},
		{"a retire ends the sessions of the runner", testRetireEndsSessions},
		{"a key rotation replaces the key, and a retry changes nothing", testRotateKey},
		{"the previous key only repeats the last rotation", testRotateWithPreviousKey},
		{"a handoff token opens one session for a live claim", testHandoffTokenSingleUse},
		{"a handoff token needs a live claim of the pool", testHandoffTokenClaim},
		{"a handoff token is only for a claim of its own runner", testHandoffTokenOwner},
		{"a new handoff token replaces the old one until a session opens", testHandoffTokenReplace},
		{"a nonce is good for one proof up to and at its expiry", testNonce},
		{"a runner renews a claim until the session wait limit", testRunnerRenew},
		{"a renewal with a stale fence changes nothing", testRunnerRenewStaleFence},
		{"a session opens up to the session wait limit", testSessionNearWaitLimit},
		{"a session of a claim that a takeover replaced is lost", testSessionAfterTakeover},
		{"a recorded claim keeps the database time, the job and the phase", testRecordClaim},
		{"a claim ends when its lease passes", testClaimLeasePasses},
		{"a renewal moves the expiry from the database time", testRenewExpiryFromDatabase},
		{"a runner of another namespace is not found for a drain, retire or rotation", testOtherNamespace},
		{"a session of a runner that no longer exists is refused", testSessionOfDeletedRunner},
		{"each change appends its audit entry", testAuditEntries},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// now is the clock of the cases.
//
//nolint:gochecknoglobals // A fixed time, read only.
var now = time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)

// fixture is a namespace with one pool for jobs, in the harness h.
type fixture struct {
	h     Harness
	fence namespace.Fence
	pool  runner.Pool
}

// setUp creates the namespace acme with the pool prod.
func setUp(t *testing.T, h Harness) fixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	p, err := h.Pools.CreatePool(t.Context(), newPool("acme", "prod", runner.PurposeJobs), fence, entries(fence))
	if err != nil {
		t.Fatalf("CreatePool: %v", err)
	}

	return fixture{h: h, fence: fence, pool: p}
}

// newPool returns a pool with no ID, which the store sets.
func newPool(ns namespace.Name, name string, purpose runner.Purpose) runner.Pool {
	return runner.Pool{
		ID: "", Namespace: ns, Name: name, Purpose: purpose, Lease: 0, Concurrency: 0, Isolation: nil,
		GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: now, UpdatedAt: now,
	}
}

// entries returns one audit entry of a change by alice in the namespace
// of fence.
func entries(fence namespace.Fence) []audit.Entry {
	return []audit.Entry{{
		At: now, Namespace: fence.Namespace, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-runners", Object: "namespace:" + string(fence.Namespace), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  fence.FailoverVersion,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}}
}

// joinToken creates a join token of the pool at now that expires after
// ttl, and returns its secret.
func joinToken(t *testing.T, f fixture, ttl time.Duration) string {
	t.Helper()

	return joinTokenAt(t, f, now, ttl)
}

// joinTokenAt creates a join token of the pool, created at the time at,
// that expires after ttl, and returns its secret.
func joinTokenAt(t *testing.T, f fixture, at time.Time, ttl time.Duration) string {
	t.Helper()

	secret := runner.JoinTokenPrefix + randomHex(t)
	tok := runner.JoinToken{
		ID: "", Pool: f.pool.ID, SHA256: hash(secret), CreatedBy: "alice", CreatedAt: at,
		ExpiresAt: at.Add(ttl), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}

	if _, err := f.h.Pools.CreateJoinToken(t.Context(), tok, f.fence, entries(f.fence)); err != nil {
		t.Fatalf("CreateJoinToken: %v", err)
	}

	return secret
}

// join registers the runner name with a new key, at at.
func join(t *testing.T, f fixture, secret, name string, at time.Time) (runner.Runner, error) {
	t.Helper()

	j, err := joinWith(t, f, secret, joinRequest(t, secret, name), at)

	return j.Runner, err
}

// joinWith registers the runner of req, at at, as the protocol adapter
// does: it checks the request, gives the store the hash of the secret and
// no secret, and appends the audit entry of the event that the result
// names.
func joinWith(t *testing.T, f fixture, secret string, req runner.JoinRequest, at time.Time) (runner.Joined, error) {
	t.Helper()

	if err := req.Validate(); err != nil {
		t.Fatalf("JoinRequest.Validate: %v", err)
	}

	req.Token = ""

	var seen []runner.Joined

	got, err := f.h.Members.Join(t.Context(), runner.Joining{TokenSHA256: hash(secret), Request: req, At: at},
		f.fence, func(j *runner.Joined) []audit.Entry {
			seen = append(seen, *j)
			e := entries(f.fence)
			e[0].Action = string(runner.ActionRunnerJoin)

			return e
		})

	if err == nil && (len(seen) != 1 || seen[0].Runner.ID != got.Runner.ID || seen[0].Rejoined != got.Rejoined) {
		t.Fatalf("Join called its audit function with %+v, want once with the result %+v", seen, got)
	}

	return got, err
}

// joinRequest returns a join request of a Kubernetes runner with a new key.
func joinRequest(t *testing.T, secret, name string) runner.JoinRequest {
	t.Helper()

	return runner.JoinRequest{
		Token: secret, Name: name, Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes, Labels: nil,
		Release: release, Key: newKey(t), Proof: nil,
	}
}

// release is the release of the runners of the cases.
//
//nolint:gochecknoglobals // A fixed value of the cases, read only.
var release = runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""}

// newKey makes a new Ed25519 public key.
func newKey(t *testing.T) runner.Key {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	return runner.Key{Public: pub, CreatedAt: time.Time{}}
}

// randomBytes is the size of a random secret of the cases.
const randomBytes = 16

// randomHex returns randomBytes random bytes in hex.
func randomHex(t *testing.T) string {
	t.Helper()

	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}

	return hex.EncodeToString(b)
}

// hash returns the hex SHA-256 of a secret, as the stores keep it.
func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))

	return hex.EncodeToString(sum[:])
}

// handoffToken returns a new handoff token of runnerID for the claim c,
// issued at at, and its secret.
func handoffToken(t *testing.T, c durable.Claim, runnerID string, at time.Time) (runner.HandoffToken, string) {
	t.Helper()

	secret := runner.HandoffTokenPrefix + randomHex(t)

	return runner.HandoffToken{
		SHA256: hash(secret), Operation: string(c.Operation), Token: c.Token, Runner: runnerID, IssuedAt: at,
		ExpiresAt: at.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}, secret
}

// rotate rotates the runner id to next, with a proof of signer.
func rotate(t *testing.T, f fixture, id string, signer, next runner.Key) (runner.Runner, error) {
	t.Helper()

	r := runner.Rotation{Runner: id, Signer: signer.ID(), Next: next}

	return f.h.Members.RotateKey(t.Context(), r, f.fence, entries(f.fence))
}

// durableClaim returns the durable claim of the session s of the runner r.
func durableClaim(s runner.Session, r runner.Runner) durable.Claim {
	return durable.Claim{Operation: durable.ID(s.Operation), Owner: r.ID, Token: s.Token, ExpiresAt: time.Time{}}
}

// wantErr fails t when err does not wrap want.
func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", what, err, want)
	}
}
