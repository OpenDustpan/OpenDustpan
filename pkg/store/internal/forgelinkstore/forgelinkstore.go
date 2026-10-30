// Package forgelinkstore holds the rules of the Git host account link tables
// that the SQLite and PostgreSQL adapters share: the mapping between a row and
// a value of package forgelink, the check of a stored row, and the steps of an
// accept and of a new request. A row is untrusted when it is read, because a
// backup, a restore or another version of the server can have written it. Both
// adapters call the same functions, so they apply one rule.
package forgelinkstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
)

// ErrCorrupt reports a stored row that does not pass the checks of a link or a
// request. The adapters do not serve such a row. The error also wraps
// forgelink.ErrInvalid.
var ErrCorrupt = errors.New("stored Git host account link is not valid")

// RequestRow is one row of the table of link requests. Its fields have the names
// and the order of the generated row type, so that an adapter converts one to
// the other.
type RequestRow struct {
	TokenSha256      string
	PrincipalID      string
	CreatedAtMs      int64
	ExpiresAtMs      int64
	ProvedKind       string
	ProvedHost       string
	ProvedAccountID  string
	ProvedLogin      string
	ProvedDeliveryID string
	ProvedAtMs       int64
	UsedAtMs         int64
}

// LinkRow is one row of the table of links.
type LinkRow struct {
	Kind        string
	Host        string
	AccountID   string
	Login       string
	PrincipalID string
	LinkedAtMs  int64
}

// RequestRowOf returns the row of a new request r.
func RequestRowOf(r forgelink.Request) RequestRow {
	return RequestRow{
		TokenSha256: r.TokenSHA256, PrincipalID: r.Principal, CreatedAtMs: r.CreatedAt.UnixMilli(),
		ExpiresAtMs: r.ExpiresAt.UnixMilli(), ProvedKind: "", ProvedHost: "", ProvedAccountID: "", ProvedLogin: "",
		ProvedDeliveryID: "", ProvedAtMs: 0, UsedAtMs: 0,
	}
}

// fromMs returns the time of ms in UTC, and the zero time for 0.
func fromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// toMs returns the Unix milliseconds of t, and 0 for the zero time.
func toMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// ProofRow holds the columns of a proof.
type ProofRow struct {
	Kind       string
	Host       string
	AccountID  string
	Login      string
	DeliveryID string
}

// ProofRowOf returns the columns of the proof p.
func ProofRowOf(p forgelink.Proof) ProofRow {
	return ProofRow{
		Kind: string(p.Account.Kind), Host: p.Account.Host, AccountID: p.Account.ID, Login: p.Login,
		DeliveryID: p.DeliveryID,
	}
}

// Request returns the request that r holds and checks it again with
// forgelink.Request.Validate. It returns ErrCorrupt, which also wraps
// forgelink.ErrInvalid, for a row that fails, or that has a time below 0.
func Request(r RequestRow) (forgelink.Request, error) {
	if r.CreatedAtMs <= 0 || r.ProvedAtMs < 0 || r.UsedAtMs < 0 {
		return forgelink.Request{}, fmt.Errorf("%w: %w: a time of the request is not after 1970", ErrCorrupt,
			forgelink.ErrInvalid)
	}

	req := forgelink.Request{
		TokenSHA256: r.TokenSha256, Principal: r.PrincipalID, CreatedAt: fromMs(r.CreatedAtMs),
		ExpiresAt: fromMs(r.ExpiresAtMs),
		Proved: forgelink.Proof{
			Account: forgelink.Account{Kind: forge.Kind(r.ProvedKind), Host: r.ProvedHost, ID: r.ProvedAccountID},
			Login:   r.ProvedLogin, DeliveryID: r.ProvedDeliveryID,
		},
		ProvedAt: fromMs(r.ProvedAtMs), UsedAt: fromMs(r.UsedAtMs),
	}

	if err := req.Validate(); err != nil {
		return forgelink.Request{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return req, nil
}

// Link returns the link that r holds and checks it again with
// forgelink.Link.Validate. It returns ErrCorrupt for a row that fails.
func Link(r LinkRow) (forgelink.Link, error) {
	l := forgelink.Link{
		Account: forgelink.Account{Kind: forge.Kind(r.Kind), Host: r.Host, ID: r.AccountID}, Login: r.Login,
		Principal: r.PrincipalID, LinkedAt: fromMs(r.LinkedAtMs),
	}

	if err := l.Validate(); err != nil {
		return forgelink.Link{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return l, nil
}

// Links returns the links of rows, in the order of rows. A row that fails its
// checks fails the call (fail closed): no row after it is served.
func Links(rows []LinkRow) ([]forgelink.Link, error) {
	out := make([]forgelink.Link, 0, len(rows))

	for i := range rows {
		l, err := Link(rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, l)
	}

	return out, nil
}

// LinkRowOf returns the row of l.
func LinkRowOf(l forgelink.Link) LinkRow {
	return LinkRow{
		Kind: string(l.Account.Kind), Host: l.Account.Host, AccountID: l.Account.ID, Login: l.Login,
		PrincipalID: l.Principal, LinkedAtMs: toMs(l.LinkedAt),
	}
}

// CheckNewRequest checks the argument of CreateRequest before the transaction:
// the request passes Validate and has no proof and no accept.
func CheckNewRequest(r forgelink.Request) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("create link request: %w", err)
	}

	if r.IsProved() || !r.UsedAt.IsZero() {
		return fmt.Errorf("%w: a new link request has no proof and no accept", forgelink.ErrInvalid)
	}

	return nil
}

// CheckProof checks the argument of ProveRequest before the transaction.
func CheckProof(p forgelink.Proof) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("prove link request: %w", err)
	}

	return nil
}

// CheckAccount checks an account that a read or a delete names.
func CheckAccount(a forgelink.Account) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("account: %w", err)
	}

	return nil
}

// RequestTx is what ApplyProof needs from the transaction of an adapter.
type RequestTx interface {
	// Request returns the row of the request with the hash, and false when none.
	Request(ctx context.Context, hash string) (RequestRow, bool, error)
	// ProveRequest writes the proof p at nowMs only while the request has no proof
	// and no use and has not expired, and reports whether it changed a row.
	ProveRequest(ctx context.Context, hash string, p ProofRow, nowMs int64) (bool, error)
}

// ApplyProof records the proof p on the request with the hash in tx at now. It
// returns the request with its proof, ErrNotFound, forgelink.ErrUsed and
// forgelink.ErrExpired from forgelink.Request.Check, and forgelink.ErrConflict
// when another account proved the request. A repeat of the proof of the same
// account changes nothing.
func ApplyProof(
	ctx context.Context, tx RequestTx, hash string, p forgelink.Proof, now time.Time,
) (forgelink.Request, error) {
	req, err := readRequest(ctx, tx, hash)
	if err != nil {
		return forgelink.Request{}, err
	}

	if err := req.Check(now); err != nil {
		return forgelink.Request{}, err
	}

	if req.IsProved() {
		return sameProof(req, p)
	}

	proved, err := tx.ProveRequest(ctx, hash, ProofRowOf(p), now.UnixMilli())
	if err != nil {
		return forgelink.Request{}, err
	}

	// A writer that proved the request after the read above makes the statement
	// change no row. The request then holds that proof, and the same rule applies.
	req, err = readRequest(ctx, tx, hash)
	if err != nil {
		return forgelink.Request{}, err
	}

	if !proved {
		if !req.IsProved() {
			return forgelink.Request{}, forgelink.ErrExpired
		}

		return sameProof(req, p)
	}

	return req, nil
}

// readRequest reads the request with the hash and checks the row again.
func readRequest(ctx context.Context, tx RequestTx, hash string) (forgelink.Request, error) {
	row, found, err := tx.Request(ctx, hash)
	if err != nil {
		return forgelink.Request{}, err
	}

	if !found {
		return forgelink.Request{}, forgelink.ErrNotFound
	}

	req, err := Request(row)
	if err != nil {
		return forgelink.Request{}, fmt.Errorf("read link request: %w", err)
	}

	return req, nil
}

// sameProof returns req when it was proved by the account of p, and ErrConflict
// when another account proved it.
func sameProof(req forgelink.Request, p forgelink.Proof) (forgelink.Request, error) {
	if req.Proved.Account != p.Account {
		return forgelink.Request{}, fmt.Errorf("%w: another Git host account proved this request", forgelink.ErrConflict)
	}

	return req, nil
}

// Tx is what the steps below need from the transaction of an adapter. The
// transaction serializes the writes: SQLite has one writer, and PostgreSQL holds
// the lock of the server audit scope. So the reads and the writes of a step see
// no other write in between.
type Tx interface {
	RequestTx
	// Link returns the row of the link of the account, and false when none.
	Link(ctx context.Context, a forgelink.Account) (LinkRow, bool, error)
	// InsertLink writes a new link.
	InsertLink(ctx context.Context, r LinkRow) error
	// ReplaceLink writes r over the link of its account only while that link
	// names previous, and reports whether it changed a row.
	ReplaceLink(ctx context.Context, r LinkRow, previous string) (bool, error)
	// UseRequest sets the use of the request at atMs for the principal that
	// started it, only while it has a proof, no use and no expiry, and reports
	// whether it changed a row.
	UseRequest(ctx context.Context, hash, principal string, atMs int64) (bool, error)
}

// ApplyAccept makes the link of the request with the hash for principal in tx.
// It reads the request, applies forgelink.Request.CheckAccept, compares the
// current link of the account with previous, writes the link, and marks the
// request as used. It returns the link and the principal that lost a link, or
// empty. It returns ErrNotFound, the errors of CheckAccept, and ErrConflict.
//
// Defense in depth: each write below is a statement that changes a row only
// while its conditions hold, and a statement that changes none fails the step.
// So a step that another writer overtook never makes a link.
func ApplyAccept(
	ctx context.Context, tx Tx, hash, principal, previous string, now time.Time,
) (forgelink.Link, string, error) {
	row, found, err := tx.Request(ctx, hash)
	if err != nil {
		return forgelink.Link{}, "", err
	}

	if !found {
		return forgelink.Link{}, "", forgelink.ErrNotFound
	}

	req, err := Request(row)
	if err != nil {
		return forgelink.Link{}, "", fmt.Errorf("accept link request: %w", err)
	}

	if err := req.CheckAccept(principal, now); err != nil {
		return forgelink.Link{}, "", err
	}

	link := req.LinkOf(now)

	if err := link.Validate(); err != nil {
		return forgelink.Link{}, "", fmt.Errorf("accept link request: %w", err)
	}

	replaced, err := putLink(ctx, tx, link, previous)
	if err != nil {
		return forgelink.Link{}, "", err
	}

	used, err := tx.UseRequest(ctx, hash, principal, now.UnixMilli())
	if err != nil {
		return forgelink.Link{}, "", err
	}

	if !used {
		return forgelink.Link{}, "", forgelink.ErrUsed
	}

	return link, replaced, nil
}

// putLink writes l and applies the rule that a link never replaces another link
// silently: with previous empty, the account must have no link, and with
// previous set, the stored link must name previous.
func putLink(ctx context.Context, tx Tx, l forgelink.Link, previous string) (string, error) {
	current, found, err := tx.Link(ctx, l.Account)
	if err != nil {
		return "", err
	}

	switch {
	case !found && previous == "":
		return "", tx.InsertLink(ctx, LinkRowOf(l))
	case !found || previous == "" || current.PrincipalID != previous:
		return "", fmt.Errorf("%w: the link of the account is not the link that the person saw", forgelink.ErrConflict)
	}

	replaced, err := tx.ReplaceLink(ctx, LinkRowOf(l), previous)
	if err != nil {
		return "", err
	}

	if !replaced {
		return "", fmt.Errorf("%w: the link of the account changed", forgelink.ErrConflict)
	}

	return previous, nil
}
