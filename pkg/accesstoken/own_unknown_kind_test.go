package accesstoken_test

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/principal"
)

// setKindOfRow changes the stored kind of the token id with a second
// connection, as a row of another version would be.
func setKindOfRow(t *testing.T, path, id, kind string) {
	t.Helper()

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	}()

	const query = "UPDATE access_tokens SET kind = ? WHERE id = ?"
	if _, execErr := conn.ExecContext(t.Context(), query, kind, id); execErr != nil {
		t.Fatalf("set stored token kind: %v", execErr)
	}
}

// ownOp runs the own-scope operation op ("revoke" or "rotate") on the token id
// as the person p.
func ownOp(t *testing.T, f *fixture, op string, p principal.Principal, id string) error {
	t.Helper()

	if op == "revoke" {
		return f.svc.Revoke(t.Context(), as(p), caller(), accesstoken.ScopeOwn, id)
	}

	_, _, err := f.svc.Rotate(t.Context(), as(p), caller(), accesstoken.RotateInput{
		OldEndsAt: nil, ExpiresAt: time.Time{}, ID: id, Scope: accesstoken.ScopeOwn,
	})

	return err
}

// TestOwnRouteAnswersOwnerBeforeKind checks, against the real store, that
// the own scope checks the owner of a row of an unknown kind before it
// answers about the kind: the owner gets ErrUnknownKind, and another person
// gets ErrNotFound, the same answer as for an ID that does not exist. Each
// row of the table is a hostile stored kind.
func TestOwnRouteAnswersOwnerBeforeKind(t *testing.T) {
	t.Parallel()

	kinds := []string{
		string(oldKind),
		"",
		"Personal",
		"PERSONAL",
		"personal ",
		" personal",
		"personal\x00",
		"../personal",
	}

	for _, op := range []string{"revoke", "rotate"} {
		for _, kind := range kinds {
			t.Run(op+"/"+strconv.Quote(kind), func(t *testing.T) {
				t.Parallel()
				checkOwnerBeforeKind(t, op, kind)
			})
		}
	}
}

// checkOwnerBeforeKind stores a token of a person with the kind, and runs op
// on it as the owner and as another person.
func checkOwnerBeforeKind(t *testing.T, op, kind string) {
	t.Helper()

	f := newFixture(t, nil)
	tok, _ := f.create(t, f.person, "laptop")

	bob, err := f.principals.CreatePrincipal(t.Context(), caller(),
		principal.Input{Subject: "bob@example.com", DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	setKindOfRow(t, f.path, tok.ID, kind)

	// Another person: the answer of an ID that does not exist.
	err = ownOp(t, &f, op, bob, tok.ID)
	if err == nil || !errors.Is(err, accesstoken.ErrNotFound) || errors.Is(err, accesstoken.ErrUnknownKind) ||
		strings.Contains(err.Error(), "unknown") {
		t.Errorf("%s as a non-owner: err = %v, want ErrNotFound only", op, err)
	}

	// The owner learns the kind.
	if ownErr := ownOp(t, &f, op, f.person, tok.ID); !errors.Is(ownErr, accesstoken.ErrUnknownKind) {
		t.Errorf("%s as the owner: err = %v, want ErrUnknownKind", op, ownErr)
	}
}

// TestOwnRouteChecksOwnerBeforeKindOfDecodedRow checks the layer after the
// store: a store that skips the decode check and gives back a token of an
// unknown kind still gets the owner check first.
func TestOwnRouteChecksOwnerBeforeKindOfDecodedRow(t *testing.T) {
	t.Parallel()

	for _, kind := range []accesstoken.Kind{oldKind, "", "Personal", "personal "} {
		t.Run(strconv.Quote(string(kind)), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			tok, _ := f.create(t, f.service, "deploy")

			var revokes atomic.Int32

			svc := serviceOver(t, &f, revokeCountStore{Store: f.store, kind: kind, revokes: &revokes})

			err := svc.Revoke(t.Context(), as(f.person), caller(), accesstoken.ScopeOwn, tok.ID)
			if !errors.Is(err, accesstoken.ErrNotFound) || errors.Is(err, accesstoken.ErrUnknownKind) {
				t.Errorf("revoke of another principal's row of kind %q: err = %v, want ErrNotFound only", kind, err)
			}
		})
	}
}
