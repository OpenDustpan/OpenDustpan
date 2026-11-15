// Package localuserstore holds the logic of the local user tables that the SQL
// adapters share: the checks and the order of the writes of the ports of
// package localuser. Each adapter gives it a Tx over its own queries, in the
// transaction that also appends the audit entry, and keeps its own SQL.
//
// Each write that changes the credentials of a person reads the principal
// row first with Tx.LockPrincipal. In PostgreSQL the read takes the lock of
// the row. SQLite has one writer, so the transaction holds the database.
// So two writes that change the credentials of one person run one after the
// other.
package localuserstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/generationstore"
)

// ErrNoRow is the error that a Tx returns when a read finds no row. The
// adapter translates the error of its driver into it.
var ErrNoRow = errors.New("no row")

// errExpired reports a reset code that exists and expired. RedeemReset
// returns it, wrapped with localuser.ErrNotFound, so that the adapter can
// delete the code in its own transaction.
var errExpired = errors.New("the reset code expired")

// Principal is the part of a principal row that the checks read.
type Principal struct {
	Kind       string
	Subject    string
	BreakGlass bool
}

// User is a row of the local user table. SessionGeneration is set only by
// Reads.UserByName, from the same read as the hash.
type User struct {
	ID              string
	PrincipalID     string
	UserName        string
	PasswordHash    string
	CreatedBy       string
	PasswordSetAtMs int64
	CreatedAtMs     int64
	// SessionGeneration is the session generation of the person.
	SessionGeneration int64
	ChangeRequired    bool
	Demo              bool
}

// Reset is a row of the reset code table.
type Reset struct {
	ID                 string
	UserID             string
	PrincipalID        string
	Digest             string
	Purpose            string
	CreatedBy          string
	Reason             string
	CreatedAtMs        int64
	ExpiresAtMs        int64
	RevokeAccessTokens bool
}

// Tx holds the queries of one adapter in one transaction. Each read returns
// ErrNoRow when it finds no row. Each write that names a row returns the
// count of rows that it changed.
type Tx interface {
	Reads
	UserWrites
	ResetWrites
}

// Reads are the reads of a Tx.
type Reads interface {
	// LockPrincipal reads the principal id that exists (not deleted). In
	// PostgreSQL it locks the row until the end of the transaction.
	LockPrincipal(ctx context.Context, id string) (Principal, error)
	// InstallationKind reads the stored installation kind, and "" before the
	// first claim.
	InstallationKind(ctx context.Context) (string, error)
	// PrincipalBySubject returns the ID of the principal that exists with the
	// subject.
	PrincipalBySubject(ctx context.Context, subject string) (string, error)
	UserByID(ctx context.Context, id string) (User, error)
	UserByName(ctx context.Context, name string) (User, error)
	UserByPrincipal(ctx context.Context, principalID string) (User, error)
	CountUserConflicts(ctx context.Context, id, principalID, userName string) (int64, error)
	CountOtherUserName(ctx context.Context, id, userName string) (int64, error)
}

// UserWrites are the writes of a Tx to the local user table.
type UserWrites interface {
	CreateUser(ctx context.Context, u User) (int64, error)
	CreateDemoUser(ctx context.Context, u User) (int64, error)
	RenameUser(ctx context.Context, id, userName string) (int64, error)
	DeleteUser(ctx context.Context, id string) (int64, error)
	SetPassword(ctx context.Context, id string, w Write) (int64, error)
	SetChangeRequired(ctx context.Context, id string, required bool) (int64, error)
	RehashPassword(ctx context.Context, id, oldHash, newHash string) (int64, error)
}

// ResetWrites are the writes of a Tx to the reset code table and to the
// principal and its tokens.
type ResetWrites interface {
	CreateReset(ctx context.Context, r Reset) (int64, error)
	ResetByDigest(ctx context.Context, digest string) (Reset, error)
	DeleteReset(ctx context.Context, id string) (int64, error)
	DeleteResetsOfUser(ctx context.Context, userID string) error
	// BumpGeneration adds 1 to the session generation of the principal
	// with cause, through generationstore.Bump, and appends the entry of the
	// bump that names the change of this transaction. It returns the new
	// generation. An expected generation other than 0 makes the bump a
	// compare-and-swap that returns session.ErrGenerationChanged when the
	// stored generation is another one.
	BumpGeneration(ctx context.Context, principalID string, cause session.BumpCause, expected uint64) (uint64, error)
	RevokeAccessTokens(ctx context.Context, principalID string, atMs int64) (int64, error)
}

// Write is a password write: the hash, the time of the write in Unix
// milliseconds, and the flag that asks for a change at the next sign-in.
type Write struct {
	Hash           string
	SetAtMs        int64
	ChangeRequired bool
	// FromGeneration is the session generation that the write expects, or 0
	// for no condition.
	FromGeneration uint64
}

// PasswordWrite returns the Write of h and c at at.
func PasswordWrite(h localuser.PasswordHash, c localuser.PasswordChange, at time.Time) Write {
	return Write{
		Hash: string(h), SetAtMs: codec.Millis(at), ChangeRequired: c.ChangeRequired, FromGeneration: c.FromGeneration,
	}
}

// UserOf returns the domain record of a row.
func UserOf(r User) localuser.User {
	var setAt time.Time
	if r.PasswordSetAtMs != 0 {
		setAt = codec.FromMillis(r.PasswordSetAtMs)
	}

	return localuser.User{
		CreatedAt:      codec.FromMillis(r.CreatedAtMs),
		PasswordSetAt:  setAt,
		ID:             r.ID,
		PrincipalID:    r.PrincipalID,
		UserName:       r.UserName,
		CreatedBy:      r.CreatedBy,
		ChangeRequired: r.ChangeRequired,
		Demo:           r.Demo,
	}
}

// ResetOf returns the domain record of a row.
func ResetOf(r Reset) localuser.Reset {
	return localuser.Reset{
		CreatedAt:          codec.FromMillis(r.CreatedAtMs),
		ExpiresAt:          codec.FromMillis(r.ExpiresAtMs),
		ID:                 r.ID,
		UserID:             r.UserID,
		CreatedBy:          r.CreatedBy,
		Reason:             r.Reason,
		Purpose:            localuser.Purpose(r.Purpose),
		RevokeAccessTokens: r.RevokeAccessTokens,
	}
}

// CheckHash returns localuser.ErrInvalid when h is not a hash that the
// hasher reads, so that no write stores a hash that a sign-in refuses.
//
// Defense in depth: the service hashes each password with the hasher, which
// only makes valid hashes. This check stays so that a new caller cannot store
// a hash of another form, and the database refuses another prefix too.
func CheckHash(h localuser.PasswordHash) error {
	if _, err := argon2id.Decode(h); err != nil {
		return fmt.Errorf("store a password hash: %w", err)
	}

	return nil
}

// person reads and locks the principal id and checks that it is a person.
// It returns principal.ErrNotFound for no such principal or another kind.
func person(ctx context.Context, tx Tx, id string) (Principal, error) {
	p, err := tx.LockPrincipal(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return Principal{}, fmt.Errorf("%w: %s", principal.ErrNotFound, id)
	}

	if err != nil {
		return Principal{}, fmt.Errorf("select principal of a local user: %w", err)
	}

	if p.Kind != string(principal.KindPerson) {
		return Principal{}, fmt.Errorf("%w: the principal is a %s, not a person", principal.ErrNotFound, p.Kind)
	}

	return p, nil
}

// lockedUser reads the user userID, then reads and locks its principal. It
// returns localuser.ErrNotFound for no such user. It reads the user again
// after the lock, so that the row it returns is the one that the lock
// guards.
func lockedUser(ctx context.Context, tx Tx, userID string) (User, Principal, error) {
	u, err := userByID(ctx, tx, userID)
	if err != nil {
		return User{}, Principal{}, err
	}

	p, err := person(ctx, tx, u.PrincipalID)
	if errors.Is(err, principal.ErrNotFound) {
		return User{}, Principal{}, fmt.Errorf("%w: the principal of the user is gone", localuser.ErrNotFound)
	}

	if err != nil {
		return User{}, Principal{}, err
	}

	u, err = userByID(ctx, tx, userID)
	if err != nil {
		return User{}, Principal{}, err
	}

	return u, p, nil
}

// userByID reads the user id, or returns localuser.ErrNotFound.
func userByID(ctx context.Context, tx Tx, id string) (User, error) {
	u, err := tx.UserByID(ctx, id)
	if errors.Is(err, ErrNoRow) {
		return User{}, fmt.Errorf("%w: %s", localuser.ErrNotFound, id)
	}

	if err != nil {
		return User{}, fmt.Errorf("select local user: %w", err)
	}

	return u, nil
}

// CreateUser stores u with no password. The caller checked u with Validate.
func CreateUser(ctx context.Context, tx Tx, u localuser.User) error {
	// Defense in depth: the service creates no demo record; only the demo
	// seed does. This check stays so that a new caller cannot.
	if u.Demo {
		return fmt.Errorf("%w: only the demo seed makes the demo user", localuser.ErrInvalid)
	}

	if _, err := person(ctx, tx, u.PrincipalID); err != nil {
		return err
	}

	n, err := tx.CreateUser(ctx, User{
		ID:                u.ID,
		PrincipalID:       u.PrincipalID,
		UserName:          u.UserName,
		PasswordHash:      "",
		CreatedBy:         u.CreatedBy,
		PasswordSetAtMs:   0,
		CreatedAtMs:       codec.Millis(u.CreatedAt),
		ChangeRequired:    false,
		Demo:              false,
		SessionGeneration: 0,
	})
	if err != nil {
		return fmt.Errorf("insert local user: %w", err)
	}

	if n == 1 {
		return nil
	}

	return conflict(ctx, tx, u.ID, u.PrincipalID, u.UserName)
}

// conflict names why an insert of a user inserted nothing.
func conflict(ctx context.Context, tx Tx, id, principalID, userName string) error {
	count, err := tx.CountUserConflicts(ctx, id, principalID, userName)
	if err != nil {
		return fmt.Errorf("select local user conflicts: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("insert local user: no row and no conflict: %w", localuser.ErrUnavailable)
	}

	return localuser.ErrAlreadyExists
}

// ByName returns the credential of the user whose stored user name is name:
// the user, its hash and the session generation of its person, from one
// read. A name that fails ValidateUserName finds no user.
func ByName(ctx context.Context, tx Tx, name string) (localuser.Credential, error) {
	var none localuser.Credential

	// Defense in depth: the service checks the name first. This check stays
	// so that no lookup sends a name that no row can hold.
	if err := localuser.ValidateUserName(name); err != nil {
		return none, localuser.ErrNotFound
	}

	u, err := tx.UserByName(ctx, name)
	if errors.Is(err, ErrNoRow) {
		return none, localuser.ErrNotFound
	}

	if err != nil {
		return none, fmt.Errorf("select local user by name: %w", err)
	}

	// Defense in depth: the CHECK of the table keeps a stored generation at
	// 2 or more, and a missing row reads as 1. This check stays so that a
	// sign-in never seals a generation that no write could make.
	g, err := generationstore.FromRow(u.SessionGeneration)
	if err != nil {
		return none, fmt.Errorf("%w: %w", localuser.ErrUnavailable, err)
	}

	return localuser.Credential{
		User: UserOf(u), Hash: localuser.PasswordHash(u.PasswordHash), SessionGeneration: g,
	}, nil
}

// ByPrincipal returns the user of the principal principalID.
func ByPrincipal(ctx context.Context, tx Tx, principalID string) (localuser.User, error) {
	u, err := tx.UserByPrincipal(ctx, principalID)
	if errors.Is(err, ErrNoRow) {
		return localuser.User{}, localuser.ErrNotFound
	}

	if err != nil {
		return localuser.User{}, fmt.Errorf("select local user by principal: %w", err)
	}

	return UserOf(u), nil
}

// Rename sets the user name of the user userID to name.
func Rename(ctx context.Context, tx Tx, userID, name string) error {
	if err := localuser.ValidateUserName(name); err != nil {
		return err
	}

	u, _, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return err
	}

	if u.Demo {
		return localuser.ErrDemoFixed
	}

	taken, err := tx.CountOtherUserName(ctx, userID, name)
	if err != nil {
		return fmt.Errorf("select local user name: %w", err)
	}

	if taken > 0 {
		return localuser.ErrAlreadyExists
	}

	n, err := tx.RenameUser(ctx, userID, name)
	if err != nil {
		return fmt.Errorf("rename local user: %w", err)
	}

	if n != 1 {
		return localuser.ErrNotFound
	}

	return nil
}

// Delete deletes the user userID and each reset code of the user, and adds
// 1 to the session generation of the person, so that each session that the
// password made ends. The principal stays. The delete removes the password
// too, so it applies the rule of RemovePassword: it fails closed with
// localuser.ErrLastCredential while the user has a password. This code counts
// no passkey, so it refuses the delete also when the person has a passkey.
func Delete(ctx context.Context, tx Tx, userID string) error {
	u, _, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return err
	}

	if u.PasswordHash != "" {
		return fmt.Errorf("%w: the delete removes the password", localuser.ErrLastCredential)
	}

	if delErr := tx.DeleteResetsOfUser(ctx, userID); delErr != nil {
		return fmt.Errorf("delete reset codes of local user: %w", delErr)
	}

	n, err := tx.DeleteUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("delete local user: %w", err)
	}

	if n != 1 {
		return localuser.ErrNotFound
	}

	_, err = bump(ctx, tx, u.PrincipalID, 0)

	return err
}

// credentialChecks returns the error of a password write for u and p: the
// demo person (localuser.ErrDemoFixed) or a break-glass person
// (localuser.ErrBreakGlass).
//
// Defense in depth: the service refuses both too. These checks stay so that
// a new caller cannot store a password for either.
func credentialChecks(u User, p Principal) error {
	if p.BreakGlass {
		return localuser.ErrBreakGlass
	}

	if u.Demo {
		return localuser.ErrDemoFixed
	}

	return nil
}

// storePassword applies w to u, and adds 1 to the session generation of the
// person, so that each session ends. It returns the new generation. A
// w.FromGeneration other than 0 makes the bump a compare-and-swap.
func storePassword(ctx context.Context, tx Tx, u User, w Write) (uint64, error) {
	n, err := tx.SetPassword(ctx, u.ID, w)
	if err != nil {
		return 0, fmt.Errorf("set local user password: %w", err)
	}

	if n != 1 {
		return 0, localuser.ErrNotFound
	}

	return bump(ctx, tx, u.PrincipalID, w.FromGeneration)
}

// bump adds 1 to the session generation of the principal with the cause
// session.BumpPassword, through the one bump of the session generation: the
// adapter appends the entry session:generation-bumped that names the change
// of this transaction. It returns the new generation. A generation other
// than expected, when expected is not 0, fails with
// localuser.ErrGenerationChanged.
func bump(ctx context.Context, tx Tx, principalID string, expected uint64) (uint64, error) {
	g, err := tx.BumpGeneration(ctx, principalID, session.BumpPassword, expected)

	switch {
	case errors.Is(err, session.ErrPrincipalNotFound):
		return 0, fmt.Errorf("bump session generation: %w", principal.ErrNotFound)
	case errors.Is(err, session.ErrGenerationChanged):
		return 0, fmt.Errorf("bump session generation: %w: %w", localuser.ErrGenerationChanged, err)
	case err != nil:
		return 0, fmt.Errorf("bump session generation: %w", err)
	default:
		return g, nil
	}
}

// SetPassword applies the password write w to the user userID and returns
// the new session generation. A w.FromGeneration other than 0 makes the
// write a compare-and-swap on the session generation.
func SetPassword(ctx context.Context, tx Tx, userID string, w Write) (uint64, error) {
	if err := CheckHash(localuser.PasswordHash(w.Hash)); err != nil {
		return 0, err
	}

	u, p, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return 0, err
	}

	if err := credentialChecks(u, p); err != nil {
		return 0, err
	}

	return storePassword(ctx, tx, u, w)
}

// SetChangeRequired sets or clears the flag of the user userID.
func SetChangeRequired(ctx context.Context, tx Tx, userID string, required bool) error {
	u, _, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return err
	}

	if u.Demo {
		return localuser.ErrDemoFixed
	}

	n, err := tx.SetChangeRequired(ctx, userID, required)
	if err != nil {
		return fmt.Errorf("set local user change required: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("%w: the user has no password", localuser.ErrNotFound)
	}

	return nil
}

// Rehash replaces the hash old of the user userID with h, only while the
// stored hash is still old.
func Rehash(ctx context.Context, tx Tx, userID string, old, h localuser.PasswordHash) error {
	if err := CheckHash(h); err != nil {
		return err
	}

	u, p, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return err
	}

	// Defense in depth: a break-glass person has no password to rehash.
	// This check stays so that a rehash never stores a hash for one.
	if p.BreakGlass {
		return localuser.ErrBreakGlass
	}

	n, err := tx.RehashPassword(ctx, u.ID, string(old), string(h))
	if err != nil {
		return fmt.Errorf("rehash local user password: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("%w: the stored hash changed", localuser.ErrNotFound)
	}

	return nil
}

// RemovePassword deletes the password of the user userID when the person
// keeps another credential. This code counts no passkey, so it cannot know
// whether the person keeps another credential: the removal fails closed with
// localuser.ErrLastCredential after the other checks, also when the person
// has a passkey.
func RemovePassword(ctx context.Context, tx Tx, userID string) error {
	u, _, err := lockedUser(ctx, tx, userID)
	if err != nil {
		return err
	}

	if u.Demo {
		return localuser.ErrDemoFixed
	}

	if u.PasswordHash == "" {
		return fmt.Errorf("%w: the user has no password", localuser.ErrNotFound)
	}

	return localuser.ErrLastCredential
}

// SeedDemoPassword stores h as the password of the demo person at at. It
// reads the installation kind in the transaction and refuses another kind
// than dev. It makes the demo user record when it does not exist, with the ID
// that newID returns.
func SeedDemoPassword(ctx context.Context, tx Tx, h localuser.PasswordHash, at time.Time) error {
	if err := CheckHash(h); err != nil {
		return err
	}

	kind, err := tx.InstallationKind(ctx)
	if err != nil {
		return fmt.Errorf("%w: the installation kind cannot be read", localuser.ErrDemoOnly)
	}

	if kind != installation.KindDev.String() {
		return fmt.Errorf("%w: the installation kind is %q", localuser.ErrDemoOnly, kind)
	}

	principalID, err := tx.PrincipalBySubject(ctx, localuser.DemoSubject)
	if errors.Is(err, ErrNoRow) {
		return fmt.Errorf("%w: no demo person %s", principal.ErrNotFound, localuser.DemoSubject)
	}

	if err != nil {
		return fmt.Errorf("select demo person: %w", err)
	}

	p, err := person(ctx, tx, principalID)
	if err != nil {
		return err
	}

	// Defense in depth: the seed never makes a break-glass demo person.
	// This check stays so that the store never stores a password for one.
	if p.BreakGlass {
		return localuser.ErrBreakGlass
	}

	u, err := demoUser(ctx, tx, principalID, at)
	if err != nil {
		return err
	}

	_, err = storePassword(
		ctx,
		tx,
		u,
		PasswordWrite(h, localuser.PasswordChange{ChangeRequired: false, FromGeneration: 0}, at),
	)

	return err
}

// demoUser returns the demo user record of the principal, and makes it when
// it does not exist.
func demoUser(ctx context.Context, tx Tx, principalID string, at time.Time) (User, error) {
	u, err := tx.UserByPrincipal(ctx, principalID)
	if err == nil {
		if !u.Demo {
			return User{}, fmt.Errorf(
				"%w: the demo person has a local user that is not the demo user",
				localuser.ErrInvalid,
			)
		}

		return u, nil
	}

	if !errors.Is(err, ErrNoRow) {
		return User{}, fmt.Errorf("select demo user: %w", err)
	}

	u = User{
		ID:                ident.NewID(localuser.UserIDPrefix),
		PrincipalID:       principalID,
		UserName:          localuser.DemoUserName,
		PasswordHash:      "",
		CreatedBy:         "system",
		PasswordSetAtMs:   0,
		CreatedAtMs:       codec.Millis(at),
		ChangeRequired:    false,
		Demo:              true,
		SessionGeneration: 0,
	}

	n, err := tx.CreateDemoUser(ctx, u)
	if err != nil {
		return User{}, fmt.Errorf("insert demo user: %w", err)
	}

	if n != 1 {
		return User{}, conflict(ctx, tx, u.ID, u.PrincipalID, u.UserName)
	}

	return u, nil
}

// CreateReset stores r with the digest d, deletes each earlier code of the
// user, and for a reset ends the sessions of the person and revokes the
// access tokens when r asks for it. The caller checked r with Validate. It
// adds the changes of ResetChanges to e, so that the entry records the
// choice and what the store revoked.
func CreateReset(ctx context.Context, tx Tx, r localuser.Reset, d localuser.Digest, e *audit.Entry) error {
	revoked, err := insertReset(ctx, tx, r, d)
	if err != nil {
		return err
	}

	e.Changes = append(e.Changes, ResetChanges(r, revoked)...)

	return nil
}

// ResetChanges returns the audit changes of a reset code: the purpose, the
// choice to revoke the access tokens, and the count of the tokens that the
// store revoked.
func ResetChanges(r localuser.Reset, revoked int64) []audit.Change {
	return []audit.Change{
		{Key: "purpose", Old: nil, New: string(r.Purpose), Layer: "", TakesEffect: ""},
		{
			Key:         "revokeAccessTokens",
			Old:         nil,
			New:         strconv.FormatBool(r.RevokeAccessTokens),
			Layer:       "",
			TakesEffect: "",
		},
		{Key: "revokedAccessTokens", Old: nil, New: strconv.FormatInt(revoked, 10), Layer: "", TakesEffect: ""},
	}
}

// insertReset does the writes of CreateReset and returns the count of the
// access tokens that it revoked.
func insertReset(ctx context.Context, tx Tx, r localuser.Reset, d localuser.Digest) (int64, error) {
	u, p, err := lockedUser(ctx, tx, r.UserID)
	if err != nil {
		return 0, err
	}

	if checkErr := credentialChecks(u, p); checkErr != nil {
		return 0, checkErr
	}

	if delErr := tx.DeleteResetsOfUser(ctx, u.ID); delErr != nil {
		return 0, fmt.Errorf("delete earlier reset codes: %w", delErr)
	}

	n, err := tx.CreateReset(ctx, Reset{
		ID:                 r.ID,
		UserID:             u.ID,
		PrincipalID:        u.PrincipalID,
		Digest:             d.String(),
		Purpose:            string(r.Purpose),
		CreatedBy:          r.CreatedBy,
		Reason:             r.Reason,
		CreatedAtMs:        codec.Millis(r.CreatedAt),
		ExpiresAtMs:        codec.Millis(r.ExpiresAt),
		RevokeAccessTokens: r.RevokeAccessTokens,
	})
	if err != nil {
		return 0, fmt.Errorf("insert reset code: %w", err)
	}

	if n != 1 {
		return 0, localuser.ErrAlreadyExists
	}

	if r.Purpose != localuser.PurposeReset {
		return 0, nil
	}

	if _, bumpErr := bump(ctx, tx, u.PrincipalID, 0); bumpErr != nil {
		return 0, bumpErr
	}

	if !r.RevokeAccessTokens {
		return 0, nil
	}

	revoked, err := tx.RevokeAccessTokens(ctx, u.PrincipalID, codec.Millis(r.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("revoke access tokens of the person: %w", err)
	}

	return revoked, nil
}

// RedeemReset uses the reset code of rd: it deletes the code, stores the
// hash as the password of its user, applies the change and ends the
// sessions of the person. It returns an error that matches
// localuser.ErrNotFound and Expired when the code expired: the adapter then
// deletes the code in its own transaction, because this one rolls back.
func RedeemReset(ctx context.Context, tx Tx, rd localuser.Redeem) (localuser.Reset, error) {
	if err := CheckHash(rd.Hash); err != nil {
		return localuser.Reset{}, err
	}

	r, err := tx.ResetByDigest(ctx, rd.Digest.String())
	if errors.Is(err, ErrNoRow) {
		return localuser.Reset{}, localuser.ErrNotFound
	}

	if err != nil {
		return localuser.Reset{}, fmt.Errorf("select reset code: %w", err)
	}

	if r.ExpiresAtMs <= codec.Millis(rd.At) {
		return localuser.Reset{}, fmt.Errorf("%w: %w", localuser.ErrNotFound, errExpired)
	}

	u, p, err := lockedUser(ctx, tx, r.UserID)
	if err != nil {
		return localuser.Reset{}, err
	}

	if checkErr := credentialChecks(u, p); checkErr != nil {
		return localuser.Reset{}, checkErr
	}

	// The delete decides between two redeems of one code at the same time:
	// the second finds no row.
	n, err := tx.DeleteReset(ctx, r.ID)
	if err != nil {
		return localuser.Reset{}, fmt.Errorf("delete reset code: %w", err)
	}

	if n != 1 {
		return localuser.Reset{}, localuser.ErrNotFound
	}

	if _, storeErr := storePassword(ctx, tx, u, PasswordWrite(rd.Hash, rd.Change, rd.At)); storeErr != nil {
		return localuser.Reset{}, storeErr
	}

	return ResetOf(r), nil
}

// UserOfReset returns the user of the reset code with the digest d when the
// code exists and did not expire at or before at. It writes nothing.
func UserOfReset(ctx context.Context, tx Tx, d localuser.Digest, at time.Time) (localuser.User, error) {
	r, err := tx.ResetByDigest(ctx, d.String())
	if errors.Is(err, ErrNoRow) {
		return localuser.User{}, localuser.ErrNotFound
	}

	if err != nil {
		return localuser.User{}, fmt.Errorf("select reset code: %w", err)
	}

	if r.ExpiresAtMs <= codec.Millis(at) {
		return localuser.User{}, fmt.Errorf("%w: %w", localuser.ErrNotFound, errExpired)
	}

	u, err := userByID(ctx, tx, r.UserID)
	if err != nil {
		return localuser.User{}, err
	}

	return UserOf(u), nil
}

// Expired reports whether err is the error of RedeemReset for a code that
// expired.
func Expired(err error) bool {
	return errors.Is(err, errExpired)
}
