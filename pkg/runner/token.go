package runner

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// Join tokens. An administrator with the action
// job.ActionNamespaceManageRunners creates a join token for one pool. A
// runner uses it once to register its key. The store keeps the SHA-256 of
// the secret, never the secret.
const (
	// DefaultJoinTTL is the lifetime of a join token that names none.
	DefaultJoinTTL = time.Hour
	// MaxJoinTTL is the longest lifetime of a join token.
	MaxJoinTTL = 7 * 24 * time.Hour
	// JoinTokenPrefix starts the secret of a join token, so that a secret
	// scanner can find it.
	JoinTokenPrefix = "dpj_"
)

// JoinTTL returns the lifetime of a new join token for the asked lifetime
// d: DefaultJoinTTL for zero, else d. It returns ErrInvalid for a negative
// d, or one above MaxJoinTTL.
func JoinTTL(d time.Duration) (time.Duration, error) {
	switch {
	case d == 0:
		return DefaultJoinTTL, nil
	case d < 0 || d > MaxJoinTTL:
		return 0, fmt.Errorf("%w: a join token lives 1 second to %v", ErrInvalid, MaxJoinTTL)
	default:
		return d, nil
	}
}

// JoinToken is the record of a join token. It holds no secret.
type JoinToken struct {
	// ID has the prefix JoinTokenIDPrefix.
	ID   string
	Pool string
	// SHA256 is the hash of the secret, in hex.
	SHA256    string
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	// UsedAt and UsedBy record the one join of the token: the time and the
	// ID of the runner.
	UsedAt    time.Time
	UsedBy    string
	RevokedAt time.Time
}

// CheckUse checks that t can register a runner at now. It returns
// ErrTokenUsed for a token that a join used or that an administrator
// revoked, and ErrTokenExpired for a token after its expiry. The store
// calls it in the transaction of the join, and marks the token used in the
// same transaction, so two joins with one token give one runner.
func (t *JoinToken) CheckUse(now time.Time) error {
	switch {
	case !t.UsedAt.IsZero() || !t.RevokedAt.IsZero():
		return fmt.Errorf("%w: join token %s", ErrTokenUsed, t.ID)
	case !now.Before(t.ExpiresAt):
		return fmt.Errorf(
			"%w: join token %s expired at %s",
			ErrTokenExpired,
			t.ID,
			t.ExpiresAt.UTC().Format(time.RFC3339),
		)
	default:
		return nil
	}
}

// Runner keys. A runner makes an Ed25519 key pair at each join and at each
// rotation. The server keeps the public key. Every request of the runner
// carries a proof of possession of the private key, so the runner holds no
// bearer secret after its join.
const (
	// RotateKeyAfter is the age of a key at which the heartbeat answer asks
	// the runner to rotate it.
	RotateKeyAfter = 25 * 24 * time.Hour
	// MaxKeyAge is the age of a key after which the server refuses it with
	// ErrKeyExpired. The runner must then join again. The 5 days between
	// RotateKeyAfter and MaxKeyAge let a runner that was offline for a
	// short time rotate.
	MaxKeyAge = 30 * 24 * time.Hour
	// RotateGrace is the time after a rotation during which the server
	// still accepts the previous key, for a retry of that rotation only: a
	// rotation request whose next key is the current key. A runner that
	// lost the answer of its rotation sends it again with the old key, and
	// the server answers as for the first request and changes nothing
	// (Runner.CheckRotation).
	RotateGrace = 5 * time.Minute
)

// Key is a public key of a runner or of a launcher.
type Key struct {
	// Public is an Ed25519 public key.
	Public    []byte
	CreatedAt time.Time
}

// Validate returns ErrInvalid for a key that is not an Ed25519 public key.
func (k Key) Validate() error {
	if len(k.Public) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: a key is an Ed25519 public key of %d bytes", ErrInvalid, ed25519.PublicKeySize)
	}

	return nil
}

// ID returns the ID of the key: the hex SHA-256 of the public key. A
// proof names it.
func (k Key) ID() string {
	sum := sha256.Sum256(k.Public)

	return hex.EncodeToString(sum[:])
}

// ExpiredAt reports whether k is older than MaxKeyAge at now.
func (k Key) ExpiredAt(now time.Time) bool {
	return now.Sub(k.CreatedAt) > MaxKeyAge
}

// RotationDue reports whether the runner must rotate k at now: k is older
// than RotateKeyAfter.
func (k Key) RotationDue(now time.Time) bool {
	return now.Sub(k.CreatedAt) > RotateKeyAfter
}

// Handoff tokens and job sessions. At the claim of a phase or a sweep, the
// coordinator asks the server for one handoff token for the claim. It gives
// the token to the launcher through its Handoff. The launcher exchanges it
// once for a job session bound to a key that the launcher makes in memory.
const (
	// HandoffTokenTTL is the lifetime of a handoff token. A Kubernetes Job
	// that waits for a node longer than this asks for a new one through
	// the coordinator.
	HandoffTokenTTL = 10 * time.Minute
	// HandoffTokenPrefix starts the secret of a handoff token.
	HandoffTokenPrefix = "dph_"
)

// HandoffToken is the record of a handoff token. It holds no secret. It is
// good for one exchange, for the claim that it names, and only while that
// claim is live.
type HandoffToken struct {
	SHA256 string
	// Operation and Token name the durable claim of the phase or the sweep.
	Operation string
	Token     int64
	// Runner is the ID of the runner that asked for the token.
	Runner string
	// IssuedAt is the time of the request for the token. The first token
	// of a claim records the claim time, from which MaxSessionWait counts.
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// CheckUse checks that t can open a job session at now. It returns
// ErrTokenUsed after one exchange, and ErrTokenExpired after its expiry.
// The store also returns job.ErrClaimLost when the claim of t is not live.
func (t *HandoffToken) CheckUse(now time.Time) error {
	switch {
	case !t.UsedAt.IsZero():
		return fmt.Errorf("%w: handoff token of operation %s", ErrTokenUsed, t.Operation)
	case !now.Before(t.ExpiresAt):
		return fmt.Errorf("%w: handoff token of operation %s", ErrTokenExpired, t.Operation)
	default:
		return nil
	}
}

// Session is a job session: the hold of one launcher on one claim. It
// lives as long as the claim. Every request of the launcher carries a proof
// of possession of the key of the session.
type Session struct {
	// ID has the prefix SessionIDPrefix.
	ID        string
	Operation string
	Token     int64
	Runner    string
	Key       Key
	CreatedAt time.Time
}

// Proofs of possession. A request of a runner or of a launcher carries the
// ID of its runner or session, the time, a random nonce and an Ed25519
// signature of ProofInput. The proof covers the hash of the body, so a
// replay can only send the same request again. The server keeps the nonce
// of each proof on the paths that are not safe to repeat (the handoff, the
// poll, the session exchange and the rotation) until NonceExpiry, and
// refuses a second proof with the same nonce with ErrReplay. Every other
// request of the protocol is safe to repeat: a log chunk has a sequence
// number, a heartbeat and a renewal change nothing twice, and a step begin
// or end of a claim records once.
const (
	// ProofWindow is the largest difference between the time of a proof
	// and the clock of the server. CheckProof accepts a proof at exactly
	// ProofWindow.
	ProofWindow = time.Minute
	// MaxClockSkew is the largest difference between the clocks of two
	// instances of the server that the nonce store allows for. Durable
	// claims use the clock of the database and need no such bound. A
	// proof is checked on the clock of the instance that gets it, so the
	// nonce store needs one. The instances keep their clocks in step with
	// NTP, which keeps them much closer than this.
	MaxClockSkew = time.Minute
	// NonceTTL is the time after the time of a proof for which the server
	// keeps its nonce: ProofWindow, and MaxClockSkew for an instance whose
	// clock is behind the clock of the instance that prunes the store.
	// Until then, an instance that accepts the proof also finds its nonce.
	NonceTTL = ProofWindow + MaxClockSkew
	// proofContext starts the signed text, so that a signature of another
	// protocol never counts as a proof.
	proofContext = "opendustpan-runner-proof-v1"
)

// NonceExpiry returns the time until which the server keeps the nonce of
// p: the time of the proof plus NonceTTL. The store refuses the nonce
// again up to and at this time (SessionStore.UseNonce).
func NonceExpiry(p Proof) time.Time {
	return p.At.Add(NonceTTL)
}

// Proof is the proof of possession of one request.
type Proof struct {
	// Subject is the ID of the runner or of the job session.
	Subject string
	At      time.Time
	// Nonce is 16 random bytes or more, in hex.
	Nonce     string
	Signature []byte
}

// Signed is the part of a request that a proof signs.
type Signed struct {
	Method string
	// PathQuery is the path of the request with its query, as sent.
	PathQuery string
	// BodySHA256 is the SHA-256 of the body. An empty body has the hash of
	// no bytes. For a streamed upload (a log chunk, a plan part or a sweep
	// report), it is the value of the header Dustpan-Content-SHA256. The
	// server checks the proof with that value before it reads the body,
	// streams the body to the blob store, and then compares the hash of
	// what it got with the signed value (CheckContentHash).
	BodySHA256 []byte
}

// CheckContentHash compares the signed hash of a streamed upload with the
// SHA-256 of the body that the server got. It returns ErrBadProof when
// they differ. The server then deletes what it wrote.
func CheckContentHash(signed, got []byte) error {
	if len(signed) != sha256.Size || !bytes.Equal(signed, got) {
		return fmt.Errorf("%w: the body does not have the signed hash", ErrBadProof)
	}

	return nil
}

// ProofInput returns the text that a proof signs: the context, the
// subject, the method, the path with its query, the hex SHA-256 of the
// body, the time in Unix milliseconds and the nonce, one on each line.
func ProofInput(p Proof, r Signed) []byte {
	var b bytes.Buffer

	for _, s := range []string{
		proofContext, p.Subject, r.Method, r.PathQuery, hex.EncodeToString(r.BodySHA256),
		strconv.FormatInt(p.At.UnixMilli(), 10), p.Nonce,
	} {
		b.WriteString(s)
		b.WriteByte('\n')
	}

	return b.Bytes()
}

// MinNonce is the shortest nonce of a proof, in hex characters.
const MinNonce = 32

// CheckProof checks the proof p of the request r with the key k at now. It
// returns ErrBadProof for a time outside ProofWindow, a nonce shorter than
// MinNonce, or a signature that does not match. The caller checks that
// p.Subject names the runner or the session of k.
func CheckProof(k Key, p Proof, r Signed, now time.Time) error {
	if d := now.Sub(p.At); d > ProofWindow || d < -ProofWindow {
		return fmt.Errorf("%w: the time of the proof is %v from the server clock", ErrBadProof, d.Round(time.Second))
	}

	if len(p.Nonce) < MinNonce {
		return fmt.Errorf("%w: the nonce has fewer than 32 hex characters", ErrBadProof)
	}

	if err := k.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrBadProof, err)
	}

	if !ed25519.Verify(k.Public, ProofInput(p, r), p.Signature) {
		return fmt.Errorf("%w: the signature does not match the key", ErrBadProof)
	}

	return nil
}

// JoinChallenge returns the text that a runner signs with its new key to
// join: the context, the server URL, the pool, the runner name and the hex
// SHA-256 of the join token. A signature for one server or pool does not
// count for another.
func JoinChallenge(serverURL, pool, name, joinToken string) []byte {
	sum := sha256.Sum256([]byte(joinToken))

	return fmt.Appendf(
		nil,
		"%s-join\n%s\n%s\n%s\n%s\n",
		proofContext,
		serverURL,
		pool,
		name,
		hex.EncodeToString(sum[:]),
	)
}

// SessionChallenge returns the text that a launcher signs with its new
// session key to open a job session: the context, the server URL, the hex
// SHA-256 of the handoff token and the hex SHA-256 of the public key. A
// signature for one server or one handoff token does not count for
// another.
func SessionChallenge(serverURL, handoffToken string, k Key) []byte {
	tok := sha256.Sum256([]byte(handoffToken))
	pub := sha256.Sum256(k.Public)

	return fmt.Appendf(nil, "%s-session\n%s\n%s\n%s\n", proofContext, serverURL, hex.EncodeToString(tok[:]),
		hex.EncodeToString(pub[:]))
}

// RotateChallenge returns the text that both the old and the new key of a
// runner sign to rotate: the context, the runner ID and the hex SHA-256 of
// the new public key.
func RotateChallenge(runnerID string, next Key) []byte {
	sum := sha256.Sum256(next.Public)

	return fmt.Appendf(nil, "%s-rotate\n%s\n%s\n", proofContext, runnerID, hex.EncodeToString(sum[:]))
}

// RejoinArgs returns the command that the console and the guide show for a
// runner that must join again: the runner binary, the server URL, the pool
// and the runner name. The command reads the new join token from its
// standard input, so the token never lands in the shell history or in a
// variable.
func RejoinArgs(binary, serverURL, pool, name string) []string {
	return []string{binary, "join", "--server", serverURL, "--pool", pool, "--name", name, "--token-stdin"}
}
