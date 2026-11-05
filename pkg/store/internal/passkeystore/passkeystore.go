// Package passkeystore holds the logic of the passkey tables that the SQL
// adapters share: the checks and the order of the writes of mfa.PasskeyStore.
// Each adapter gives it a Tx over its own queries, in the transaction that
// also appends the audit entry, and keeps its own SQL.
//
// Each write that changes the passkeys of a person reads the principal row
// first with Tx.LockPrincipal. In PostgreSQL the read takes the lock of the
// row. SQLite has one writer, so the transaction holds the database. The
// local user store takes the same lock, so a removal of the password and a
// removal of the last passkey run one after the other.
package passkeystore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// ErrNoRow is the error that a Tx returns when a read finds no row. The
// adapter translates the error of its driver into it.
var ErrNoRow = errors.New("no row")

// transportSeparator joins the transports of a passkey in its row.
const transportSeparator = ","

// Principal is the part of a principal row that the checks read.
type Principal struct {
	Kind       string
	BreakGlass bool
}

// LocalUser is the part of the local user row of a person that the checks
// read.
type LocalUser struct {
	PasswordHash string
	Demo         bool
}

// Row is a row of the passkey table.
type Row struct {
	ID             string
	PrincipalID    string
	Name           string
	Transports     string
	CredentialID   []byte
	PublicKey      []byte
	AAGUID         []byte
	SignCount      int64
	CreatedAtMs    int64
	LastUsedAtMs   int64
	SuspendedAtMs  int64
	BackupEligible bool
}

// ChallengeRow is a row of the challenge table.
type ChallengeRow struct {
	Digest      string
	Kind        string
	PrincipalID string
	PendingID   string
	SessionID   string
	CreatedAtMs int64
	ExpiresAtMs int64
}

// Tx holds the queries of one adapter in one transaction. Each read returns
// ErrNoRow when it finds no row. Each write that names a row returns the
// count of rows that it changed.
type Tx interface {
	Reads
	Writes
	ChallengeWrites
}

// Reads are the reads of a Tx.
type Reads interface {
	// LockPrincipal reads the principal id that exists (not deleted). In
	// PostgreSQL it locks the row until the end of the transaction.
	LockPrincipal(ctx context.Context, id string) (Principal, error)
	// LocalUserOf reads the local user of the person principalID.
	LocalUserOf(ctx context.Context, principalID string) (LocalUser, error)
	CountPasskeys(ctx context.Context, principalID string) (int64, error)
	// CountLivePasskeys counts the passkeys of the person that are not
	// suspended.
	CountLivePasskeys(ctx context.Context, principalID string) (int64, error)
	PasskeyByID(ctx context.Context, id string) (Row, error)
	PasskeyByCredentialID(ctx context.Context, credentialID []byte) (Row, error)
	ListPasskeys(ctx context.Context, principalID string) ([]Row, error)
}

// Writes are the writes of a Tx to the passkey table.
type Writes interface {
	CreatePasskey(ctx context.Context, r Row) (int64, error)
	DeletePasskey(ctx context.Context, id, principalID string) (int64, error)
	// RecordUse sets the counter and the last use of the passkey id when it
	// is not suspended and the counter does not go back.
	RecordUse(ctx context.Context, id string, signCount, atMs int64) (int64, error)
	Suspend(ctx context.Context, id string, atMs int64) (int64, error)
}

// ChallengeWrites are the writes of a Tx to the challenge table.
type ChallengeWrites interface {
	CreateChallenge(ctx context.Context, r ChallengeRow) (int64, error)
	DeleteExpiredChallenges(ctx context.Context, atMs int64) (int64, error)
	// TakeChallenge deletes the challenge digest and returns it.
	TakeChallenge(ctx context.Context, digest string) (ChallengeRow, error)
}

// RowOf returns the row of the passkey p.
func RowOf(p mfa.Passkey) Row {
	return Row{
		ID:             p.ID,
		PrincipalID:    p.PersonID,
		Name:           p.Name,
		Transports:     strings.Join(p.Transports, transportSeparator),
		CredentialID:   p.CredentialID,
		PublicKey:      p.PublicKey,
		AAGUID:         p.AAGUID[:],
		SignCount:      int64(p.SignCount),
		CreatedAtMs:    codec.Millis(p.CreatedAt),
		LastUsedAtMs:   0,
		SuspendedAtMs:  0,
		BackupEligible: p.BackupEligible,
	}
}

// PasskeyOf returns the domain record of a row. A row is untrusted input: it
// returns mfa.ErrInvalid for a row that no write of this package makes, so
// that a changed row cannot reach a ceremony.
func PasskeyOf(r Row) (mfa.Passkey, error) {
	var none mfa.Passkey

	if r.SignCount < 0 || r.SignCount > math.MaxUint32 {
		return none, fmt.Errorf("%w: stored passkey counter is out of range", mfa.ErrInvalid)
	}

	if r.CreatedAtMs <= 0 || r.LastUsedAtMs < 0 || r.SuspendedAtMs < 0 {
		return none, fmt.Errorf("%w: stored passkey time is out of range", mfa.ErrInvalid)
	}

	var aaguid [16]byte
	if len(r.AAGUID) != len(aaguid) {
		return none, fmt.Errorf("%w: stored passkey AAGUID has %d bytes", mfa.ErrInvalid, len(r.AAGUID))
	}

	copy(aaguid[:], r.AAGUID)

	var ts []string
	if r.Transports != "" {
		ts = strings.Split(r.Transports, transportSeparator)
	}

	p := mfa.Passkey{
		CreatedAt:      codec.FromMillis(r.CreatedAtMs),
		LastUsedAt:     optionalTime(r.LastUsedAtMs),
		SuspendedAt:    optionalTime(r.SuspendedAtMs),
		ID:             r.ID,
		PersonID:       r.PrincipalID,
		Name:           r.Name,
		CredentialID:   slices.Clone(r.CredentialID),
		PublicKey:      slices.Clone(r.PublicKey),
		Transports:     ts,
		AAGUID:         aaguid,
		SignCount:      uint32(r.SignCount),
		BackupEligible: r.BackupEligible,
	}

	// Defense in depth: each write checks the passkey, and the CHECK
	// constraints of the table refuse most such rows. This check stays so
	// that a restored or changed row cannot bring in a value that skips them.
	if err := p.Validate(); err != nil {
		return none, fmt.Errorf("stored passkey: %w", err)
	}

	return p, nil
}

// optionalTime returns the time of ms, or the zero time for 0.
func optionalTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return codec.FromMillis(ms)
}

// person reads and locks the principal id and checks that it is a person.
// It returns mfa.ErrInvalid, wrapped with principal.ErrNotFound, for no such
// principal or another kind.
func person(ctx context.Context, tx Tx, id string) (Principal, error) {
	p, err := tx.LockPrincipal(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return Principal{}, fmt.Errorf("%w: %w: %s", mfa.ErrInvalid, principal.ErrNotFound, id)
	}

	if err != nil {
		return Principal{}, fmt.Errorf("select principal of a passkey: %w", err)
	}

	if p.Kind != string(principal.KindPerson) {
		return Principal{}, fmt.Errorf("%w: %w: the principal is a %s, not a person", mfa.ErrInvalid,
			principal.ErrNotFound, p.Kind)
	}

	return p, nil
}

// localUser reads the local user of the person principalID. found is false
// when the person has no local user.
func localUser(ctx context.Context, tx Tx, principalID string) (LocalUser, bool, error) {
	u, err := tx.LocalUserOf(ctx, principalID)
	if errors.Is(err, ErrNoRow) {
		var none LocalUser

		return none, false, nil
	}

	if err != nil {
		return LocalUser{}, false, fmt.Errorf("select local user of a passkey: %w", err)
	}

	return u, true, nil
}

// AddPasskey stores p. The caller checked p with Validate.
func AddPasskey(ctx context.Context, tx Tx, p mfa.Passkey) error {
	// Defense in depth: the service makes each passkey with no use and no
	// suspension. This check stays so that a new caller cannot store a
	// passkey that skips the counter rule.
	if !p.LastUsedAt.IsZero() || !p.SuspendedAt.IsZero() {
		return fmt.Errorf("%w: a new passkey has no use and no suspension", mfa.ErrInvalid)
	}

	if _, err := person(ctx, tx, p.PersonID); err != nil {
		return err
	}

	u, found, err := localUser(ctx, tx, p.PersonID)
	if err != nil {
		return err
	}

	// Defense in depth: the service refuses a passkey of the demo person
	// too. This check stays so that a new caller cannot add one.
	if found && u.Demo {
		return localuser.ErrDemoFixed
	}

	count, err := tx.CountPasskeys(ctx, p.PersonID)
	if err != nil {
		return fmt.Errorf("count passkeys: %w", err)
	}

	if count >= mfa.MaxPasskeys {
		return fmt.Errorf("%w: the person holds %d passkeys", mfa.ErrLimit, count)
	}

	n, err := tx.CreatePasskey(ctx, RowOf(p))
	if err != nil {
		return fmt.Errorf("insert passkey: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("%w: the passkey ID or the credential ID is taken", mfa.ErrAlreadyExists)
	}

	return nil
}

// Passkeys returns the passkeys of the person personID in the order of the
// add.
func Passkeys(ctx context.Context, tx Tx, personID string) ([]mfa.Passkey, error) {
	rows, err := tx.ListPasskeys(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}

	out := make([]mfa.Passkey, 0, len(rows))

	for i := range rows {
		p, err := PasskeyOf(rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// ByCredentialID returns the passkey with the credential ID id, or
// mfa.ErrNotFound.
func ByCredentialID(ctx context.Context, tx Tx, id []byte) (mfa.Passkey, error) {
	// Defense in depth: the relying party adapter reads a credential ID of
	// at most this size. This check stays so that no lookup sends a value
	// that no row can hold.
	if len(id) == 0 || len(id) > mfa.MaxCredentialIDBytes {
		return mfa.Passkey{}, mfa.ErrNotFound
	}

	r, err := tx.PasskeyByCredentialID(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return mfa.Passkey{}, mfa.ErrNotFound
	}

	if err != nil {
		return mfa.Passkey{}, fmt.Errorf("select passkey by credential ID: %w", err)
	}

	return PasskeyOf(r)
}

// RecordUse applies the use u. It returns regressed true, with no error,
// when it suspended the passkey: the adapter then appends the entry of the
// suspension, commits, and returns mfa.ErrCounterRegressed.
func RecordUse(ctx context.Context, tx Tx, u mfa.PasskeyUse) (bool, error) {
	if u.PasskeyID == "" || u.At.IsZero() {
		return false, fmt.Errorf("%w: a use names a passkey and a time", mfa.ErrInvalid)
	}

	at := codec.Millis(u.At)

	n, err := tx.RecordUse(ctx, u.PasskeyID, int64(u.SignCount), at)
	if err != nil {
		return false, fmt.Errorf("record passkey use: %w", err)
	}

	if n == 1 {
		return false, nil
	}

	r, err := tx.PasskeyByID(ctx, u.PasskeyID)
	if errors.Is(err, ErrNoRow) {
		return false, mfa.ErrNotFound
	}

	if err != nil {
		return false, fmt.Errorf("select passkey: %w", err)
	}

	if r.SuspendedAtMs != 0 {
		return false, mfa.ErrSuspended
	}

	n, err = tx.Suspend(ctx, u.PasskeyID, at)
	if err != nil {
		return false, fmt.Errorf("suspend passkey: %w", err)
	}

	if n != 1 {
		return false, mfa.ErrSuspended
	}

	return true, nil
}

// RemovePasskey deletes the passkey id of the person personID. A local user
// with no password, and a break-glass person, keep at least one passkey.
func RemovePasskey(ctx context.Context, tx Tx, personID, id string) error {
	p, err := person(ctx, tx, personID)
	if errors.Is(err, principal.ErrNotFound) {
		return mfa.ErrNotFound
	}

	if err != nil {
		return err
	}

	removed, err := owned(ctx, tx, personID, id)
	if err != nil {
		return err
	}

	u, found, err := localUser(ctx, tx, personID)
	if err != nil {
		return err
	}

	if found && u.Demo {
		return localuser.ErrDemoFixed
	}

	if needsPasskey(p, u, found) {
		if lastErr := leavesLive(ctx, tx, personID, removed); lastErr != nil {
			return lastErr
		}
	}

	n, err := tx.DeletePasskey(ctx, id, personID)
	if err != nil {
		return fmt.Errorf("delete passkey: %w", err)
	}

	if n != 1 {
		return mfa.ErrNotFound
	}

	return nil
}

// owned returns the row of the passkey id, or mfa.ErrNotFound unless it
// exists and belongs to the person personID.
func owned(ctx context.Context, tx Tx, personID, id string) (Row, error) {
	var none Row

	r, err := tx.PasskeyByID(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return none, mfa.ErrNotFound
	}

	if err != nil {
		return none, fmt.Errorf("select passkey: %w", err)
	}

	// Defense in depth: the delete names the person too, and the service
	// passes the person of the session. This check stays so that the count of
	// the removal is of the person of the passkey, also for a new caller or a
	// delete that names no person.
	if r.PrincipalID != personID {
		return none, mfa.ErrNotFound
	}

	return r, nil
}

// needsPasskey reports whether the person p keeps at least one passkey: a
// break-glass person, and a local user u with no password. found is false
// when the person has no local user.
func needsPasskey(p Principal, u LocalUser, found bool) bool {
	return p.BreakGlass || (found && u.PasswordHash == "")
}

// leavesLive returns mfa.ErrLastCredential when the removal of the passkey
// removed leaves the person personID with no passkey that is not suspended. A
// suspended passkey cannot sign in, so it does not count. When no passkey of
// the person is live, each removal fails closed. The caller holds the lock of
// the principal.
func leavesLive(ctx context.Context, tx Tx, personID string, removed Row) error {
	live, err := tx.CountLivePasskeys(ctx, personID)
	if err != nil {
		return fmt.Errorf("count passkeys that are not suspended: %w", err)
	}

	if removed.SuspendedAtMs == 0 {
		live--
	}

	if live < 1 {
		return mfa.ErrLastCredential
	}

	return nil
}

// ChallengeRowOf returns the row of the challenge c with the digest d.
func ChallengeRowOf(c mfa.Challenge, d mfa.Digest) ChallengeRow {
	return ChallengeRow{
		Digest:      d.String(),
		Kind:        string(c.Kind),
		PrincipalID: c.PersonID,
		PendingID:   c.PendingID,
		SessionID:   c.SessionID,
		CreatedAtMs: codec.Millis(c.CreatedAt),
		ExpiresAtMs: codec.Millis(c.ExpiresAt),
	}
}

// ChallengeOf returns the domain record of a row. It returns mfa.ErrInvalid
// for a row that fails the checks of a challenge.
func ChallengeOf(r ChallengeRow) (mfa.Challenge, error) {
	c := mfa.Challenge{
		CreatedAt: codec.FromMillis(r.CreatedAtMs),
		ExpiresAt: codec.FromMillis(r.ExpiresAtMs),
		Kind:      mfa.Ceremony(r.Kind),
		PersonID:  r.PrincipalID,
		PendingID: r.PendingID,
		SessionID: r.SessionID,
	}

	// Defense in depth: each write checks the challenge, and the CHECK
	// constraints of the table refuse most such rows. This check stays so
	// that a changed row cannot start a ceremony of another kind.
	if err := c.Validate(); err != nil {
		return mfa.Challenge{}, fmt.Errorf("stored challenge: %w", err)
	}

	return c, nil
}

// CreateChallenge deletes the challenges that expired at the time of c, and
// stores c with the digest d. The caller checked c with Validate.
func CreateChallenge(ctx context.Context, tx Tx, c mfa.Challenge, d mfa.Digest) error {
	if _, err := tx.DeleteExpiredChallenges(ctx, codec.Millis(c.CreatedAt)); err != nil {
		return fmt.Errorf("delete expired challenges: %w", err)
	}

	n, err := tx.CreateChallenge(ctx, ChallengeRowOf(c, d))
	if err != nil {
		return fmt.Errorf("insert challenge: %w", err)
	}

	if n != 1 {
		return mfa.ErrAlreadyExists
	}

	return nil
}

// Taken is the result of a take of a challenge. Refused is the error that
// the port returns for the take, after the commit of the delete:
// mfa.ErrNotFound for no challenge or one that expired, and mfa.ErrInvalid
// for a row that fails the checks of a challenge.
type Taken struct {
	Refused   error
	Challenge mfa.Challenge
}

// TakeChallenge deletes the challenge d and returns it. A challenge that
// expired at or before at, or a row that fails the checks, is deleted too, so
// the adapter commits and then returns Taken.Refused. The error is an error
// of the transaction, which rolls back.
func TakeChallenge(ctx context.Context, tx Tx, d mfa.Digest, at time.Time) (Taken, error) {
	r, err := tx.TakeChallenge(ctx, d.String())
	var none mfa.Challenge

	if errors.Is(err, ErrNoRow) {
		return Taken{Refused: mfa.ErrNotFound, Challenge: none}, nil
	}

	if err != nil {
		return Taken{}, fmt.Errorf("take challenge: %w", err)
	}

	if r.ExpiresAtMs <= codec.Millis(at) {
		return Taken{Refused: fmt.Errorf("%w: the challenge expired", mfa.ErrNotFound), Challenge: none}, nil
	}

	// A row that fails the checks is deleted too, and its error is the
	// refusal of the take.
	c, refused := ChallengeOf(r)

	return Taken{Refused: refused, Challenge: c}, nil
}
