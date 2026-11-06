//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The covering tests of the passkey sign-in routes. TestBreakGlassPasskey
// drives the same two routes for a break-glass account.
const (
	signInCoveringTest     = "TestPasskeySignIn"
	breakGlassCoveringTest = "TestBreakGlassPasskey"
)

// passkeySignInAllowEntries returns the routes of the passkey sign-in. No CLI
// signs in with a passkey, so the steps of signInCoveringTest drive each one
// through the server of each store with a cookie jar and a software
// authenticator.
func passkeySignInAllowEntries() []AllowEntry {
	reason := "Console route: no CLI signs in with a passkey. The harness plays the browser with a cookie jar and a " +
		"software authenticator, and checks each answer."

	return []AllowEntry{
		{
			Method: "POST", Path: "/api/dustpan/v1/sign-in/passkey/begin", CoveringTest: signInCoveringTest,
			Reason: reason,
		},
		{
			Method: "POST", Path: "/api/dustpan/v1/sign-in/passkey/finish", CoveringTest: signInCoveringTest,
			Reason: reason,
		},
	}
}

// PasskeyOrigin returns the origin that the relying party of an env from
// NewEnvWithPasskeys checks: the host name localhost with the port of the
// server.
func (e *Env) PasskeyOrigin(tb testing.TB) string {
	tb.Helper()

	u, err := url.Parse(e.URL)
	if err != nil {
		tb.Fatalf("parse %s: %v", e.URL, err)
	}

	return "https://localhost:" + u.Port()
}

// withStore runs fn on the store of the server. The binary server owns its
// database, so for the binary the call stops it, runs fn on a handle that is
// closed before the binary starts again. The in-process server uses its own
// store.
func (e *Env) withStore(ctx context.Context, tb testing.TB, fn func(st namespace.Store) error) {
	tb.Helper()

	if e.binPath == "" {
		if err := fn(e.inprocess); err != nil {
			tb.Fatalf("use the store: %v", err)
		}

		return
	}

	e.StopServer(ctx, tb)
	e.writeStopped(ctx, tb, fn)
	e.StartServer(ctx, tb)
}

// SeedPasskey adds one passkey to the person principalID, as a register
// ceremony of that person does, and returns the credential of the software
// authenticator. With a role, it also grants the role on Namespace to the
// person, so that a token of the person can drive a CLI. No route can add a
// passkey for a person who has no password and no session, so this is a
// direct store write.
func (e *Env) SeedPasskey(
	ctx context.Context, tb testing.TB, principalID, role string,
) (*webauthntest.Authenticator, *webauthntest.Credential) {
	tb.Helper()

	auth := webauthntest.New("localhost", e.PasskeyOrigin(tb))

	cred, err := webauthntest.NewCredential([]byte(principalID), false)
	if err != nil {
		tb.Fatalf("make the credential: %v", err)
	}

	e.withStore(ctx, tb, func(st namespace.Store) error {
		return seedPasskey(ctx, st, e.PasskeyOrigin(tb), principalID, auth, cred, role)
	})

	return auth, cred
}

// errNoPasskeyRows reports a store that keeps no passkeys.
var errNoPasskeyRows = errors.New("the store keeps no passkeys or grants")

// seedPasskey runs the register ceremony of the person principalID on st.
func seedPasskey(
	ctx context.Context, st namespace.Store, origin, principalID string, auth *webauthntest.Authenticator,
	cred *webauthntest.Credential, role string,
) error {
	rows, ok := st.(interface {
		mfa.PasskeyStore
		mfasvc.Auditor
		access.Store
	})
	if !ok {
		return errNoPasskeyRows
	}

	rp, err := gowebauthn.New(origin)
	if err != nil {
		return fmt.Errorf("relying party: %w", err)
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: rows, RP: rp, Audit: rows, Now: nil, Log: slog.Default()})
	if err != nil {
		return fmt.Errorf("passkey service: %w", err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := mfasvc.Actor{
		Entry:       freshAdminEntry(at, mfasvc.Object("seed")),
		PrincipalID: principalID, SessionID: "ses-e2e-seed",
	}
	person := mfa.Person{ID: principalID, Name: principalID, DisplayName: principalID}

	options, err := svc.BeginRegistration(ctx, actor, person)
	if err != nil {
		return fmt.Errorf("begin the registration: %w", err)
	}

	challenge, err := webauthntest.ChallengeOf(options)
	if err != nil {
		return fmt.Errorf("challenge: %w", err)
	}

	response, err := auth.Register(cred, webauthntest.For(challenge))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	if _, err := svc.FinishRegistration(ctx, actor, person, "e2e-key", response); err != nil {
		return fmt.Errorf("finish the registration: %w", err)
	}

	if role == "" {
		return nil
	}

	return seedNamespaceGrant(ctx, st, rows, principalID, role, at)
}

// seedNamespaceGrant grants the role to the person on Namespace.
func seedNamespaceGrant(
	ctx context.Context, st namespace.Store, grants access.Store, principalID, role string, at time.Time,
) error {
	ns, err := st.GetNamespace(ctx, namespace.Name(Namespace))
	if err != nil {
		return fmt.Errorf("read the namespace: %w", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.NamespaceObject(Namespace),
		ID: "grt-e2e-passkey-" + principalID, Role: role, CreatedBy: freshAdminActor,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	fence := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}

	// The entry of a grant on a namespace is in the scope of that namespace.
	entry := freshAdminEntry(at, access.GrantRef(grant))
	entry.Namespace = ns.Name

	if err := grants.CreateGrant(ctx, grant, fence, entry, nil); err != nil {
		return fmt.Errorf("grant %s: %w", role, err)
	}

	return nil
}

// ServerRecords returns the entries of the audit log of the installation and
// the notifications of its outbox. It reads them from the database, because no
// route lists the outbox of the installation.
func (e *Env) ServerRecords(ctx context.Context, tb testing.TB) ([]audit.Entry, []notify.Notification) {
	tb.Helper()

	var (
		entries []audit.Entry
		notes   []notify.Notification
	)

	e.withStore(ctx, tb, func(st namespace.Store) error {
		var err error

		entries, notes, err = readServerRecords(ctx, st)

		return err
	})

	return entries, notes
}

// readServerRecords reads the entries of @server and the outbox of st.
func readServerRecords(ctx context.Context, st namespace.Store) ([]audit.Entry, []notify.Notification, error) {
	var (
		log    interface {
			List(ctx context.Context, scope namespace.Name, page audit.Page) (audit.Listing, error)
		}
		outbox notify.Outbox
	)

	switch s := st.(type) {
	case *sqlite.Store:
		log, outbox = s, s.Notifications()
	case *postgres.Store:
		log, outbox = s, s.Notifications()
	default:
		return nil, nil, fmt.Errorf("%w: %T", errNoPasskeyRows, st)
	}

	listing, err := log.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		return nil, nil, fmt.Errorf("list the audit log: %w", err)
	}

	notes, err := outbox.After(ctx, audit.ServerScope, 0, 100)
	if err != nil {
		return nil, nil, fmt.Errorf("read the outbox: %w", err)
	}

	return listing.Entries, notes, nil
}
