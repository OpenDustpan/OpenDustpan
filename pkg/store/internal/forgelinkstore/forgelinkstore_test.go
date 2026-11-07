package forgelinkstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/store/internal/forgelinkstore"
)

// fakeTx is a forgelinkstore.Tx over one request and one link. The flags make a
// statement change no row, as a writer that overtook the step would.
type fakeTx struct {
	request   forgelinkstore.RequestRow
	link      forgelinkstore.LinkRow
	hasLink   bool
	useFails  bool
	swapFails bool
	inserts   int
	replaces  int
	uses      int
}

// noLink is the zero link row: the account has no link.
func noLink() forgelinkstore.LinkRow {
	var none forgelinkstore.LinkRow

	return none
}

// newFakeTx returns a fakeTx that holds the request row and, when held is not
// the zero row, the link row.
func newFakeTx(request forgelinkstore.RequestRow, held forgelinkstore.LinkRow) *fakeTx {
	return &fakeTx{
		request: request, link: held, hasLink: held != noLink(), useFails: false, swapFails: false,
		inserts: 0, replaces: 0, uses: 0,
	}
}

func (x *fakeTx) Request(_ context.Context, _ string) (forgelinkstore.RequestRow, bool, error) {
	return x.request, true, nil
}

func (x *fakeTx) ProveRequest(_ context.Context, _ string, _ forgelinkstore.ProofRow, _ int64) (bool, error) {
	return true, nil
}

func (x *fakeTx) Link(_ context.Context, _ forgelink.Account) (forgelinkstore.LinkRow, bool, error) {
	return x.link, x.hasLink, nil
}

func (x *fakeTx) InsertLink(_ context.Context, _ forgelinkstore.LinkRow) error {
	x.inserts++

	return nil
}

func (x *fakeTx) ReplaceLink(_ context.Context, _ forgelinkstore.LinkRow, _ string) (bool, error) {
	x.replaces++

	return !x.swapFails, nil
}

func (x *fakeTx) UseRequest(_ context.Context, _, _ string, _ int64) (bool, error) {
	x.uses++

	return !x.useFails, nil
}

// start is the time of the fixtures.
func start() time.Time { return time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC) }

// provedRequest returns the row of a request of prn-ana that account 42 proved.
func provedRequest(t *testing.T) forgelinkstore.RequestRow {
	t.Helper()

	r, err := forgelink.NewRequest(strings.Repeat("a", forgelink.MinTokenLen), "prn-ana", start())
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	row := forgelinkstore.RequestRowOf(r)
	row.ProvedKind, row.ProvedHost, row.ProvedAccountID = string(forge.KindGitHub), "github.com", "42"
	row.ProvedLogin, row.ProvedDeliveryID, row.ProvedAtMs = "ana-gh", "d1", start().Add(time.Second).UnixMilli()

	return row
}

func accept(previous string) forgelink.Acceptance {
	var lost audit.Entry

	return forgelink.Acceptance{
		TokenSHA256: forgelink.HashToken(strings.Repeat("a", forgelink.MinTokenLen)), Principal: "prn-ana",
		Previous: previous, At: start().Add(time.Minute), Lost: lost,
	}
}

// heldBy returns the link row of account 42 that principal holds.
func heldBy(principal string) forgelinkstore.LinkRow {
	return forgelinkstore.LinkRow{
		Kind: "github", Host: "github.com", AccountID: "42", Login: "x", PrincipalID: principal, LinkedAtMs: 1,
	}
}

// TestApplyAcceptFailsWhenTheUseChangesNoRow proves the guard on the use of the
// request: when another writer used it first, the step fails, so the caller
// rolls the link back. The use is the statement that makes a token single use.
func TestApplyAcceptFailsWhenTheUseChangesNoRow(t *testing.T) {
	t.Parallel()

	tx := newFakeTx(provedRequest(t), noLink())
	tx.useFails = true

	_, err := forgelinkstore.ApplyAccept(t.Context(), tx, accept(""))
	if !errors.Is(err, forgelink.ErrUsed) || tx.uses != 1 {
		t.Fatalf("accept = %v after %d uses, want ErrUsed", err, tx.uses)
	}
}

// TestApplyAcceptFailsWhenTheReplaceChangesNoRow proves the guard on the replace:
// when the link changed after the read, the step is a conflict and the request
// is not used.
func TestApplyAcceptFailsWhenTheReplaceChangesNoRow(t *testing.T) {
	t.Parallel()

	tx := newFakeTx(provedRequest(t), heldBy("prn-bob"))
	tx.swapFails = true

	_, err := forgelinkstore.ApplyAccept(t.Context(), tx, accept("prn-bob"))
	if !errors.Is(err, forgelink.ErrConflict) || tx.replaces != 1 || tx.uses != 0 {
		t.Fatalf("accept = %v after %d replaces and %d uses, want ErrConflict and no use", err, tx.replaces, tx.uses)
	}
}

func TestApplyAcceptMakesALink(t *testing.T) {
	t.Parallel()

	tx := newFakeTx(provedRequest(t), noLink())

	got, err := forgelinkstore.ApplyAccept(t.Context(), tx, accept(""))
	if err != nil || got.Link.Principal != "prn-ana" || got.Replaced != "" || tx.inserts != 1 || tx.uses != 1 {
		t.Fatalf("accept = %+v, %v, %d inserts, %d uses", got, err, tx.inserts, tx.uses)
	}
}

func TestApplyAcceptReplacesTheLinkThatThePersonSaw(t *testing.T) {
	t.Parallel()

	tx := newFakeTx(provedRequest(t), heldBy("prn-bob"))

	got, err := forgelinkstore.ApplyAccept(t.Context(), tx, accept("prn-bob"))
	if err != nil || got.Replaced != "prn-bob" || tx.replaces != 1 || tx.uses != 1 {
		t.Fatalf("replace = %+v, %v, %d replaces, %d uses", got, err, tx.replaces, tx.uses)
	}
}

func TestApplyAcceptNeverReplacesSilently(t *testing.T) {
	t.Parallel()

	for _, previous := range []string{"", "prn-cy"} {
		tx := newFakeTx(provedRequest(t), heldBy("prn-bob"))

		_, err := forgelinkstore.ApplyAccept(t.Context(), tx, accept(previous))
		if !errors.Is(err, forgelink.ErrConflict) || tx.replaces != 0 || tx.uses != 0 {
			t.Errorf("accept with previous %q = %v, want ErrConflict and no write", previous, err)
		}
	}
}
