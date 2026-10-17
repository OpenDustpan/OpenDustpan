package main

// Recovery of a lost administrator through the host (break-glass): the host
// command makes a temporary administrator for one named person, with a
// fixed expiry and a personal access token. It opens the database named by
// --database itself, like serve does: it needs no listener, no identity
// provider and no DNS. The person comes from the command line, never from a
// configuration file, and the command reads no token from one either: host
// access is the credential, and the command prints the new secret once.
//
//	opendustpan host create-temp-admin [--database sqlite:dustpan.db] <person>
//
// The command makes a new person principal, grants it the
// site-administrator role on the installation, issues its token, and
// records the fixed expiry. The person must name no principal that exists:
// the command never reuses one, so an expiry never ends a normal account.
// Each step writes its audit entry. The exit code is 0 on success and 1 on
// any refusal.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Sentinel errors of the host command. Each error text names what failed
// and the one command that helps.
var (
	// errHostNeedsPerson reports a host run without exactly one person
	// name on the command line.
	errHostNeedsPerson = errors.New(
		"host create-temp-admin needs one person name: opendustpan host create-temp-admin <person>",
	)
	// ErrPersonExists reports a host run that names a person that a
	// principal already holds.
	ErrPersonExists = errors.New("person already exists")
	// errHostBadLifetime reports a token policy with no positive maximum
	// lifetime.
	errHostBadLifetime = errors.New("read token policy: maximum lifetime is not positive")
)

// hostSubject is the subject of the audit entries of the host command: the
// host itself acted, through no sign-in.
const hostSubject = tempadmin.HostSubject

// hostTokenName is the name of the personal token the host command issues.
// One name for every recovery token, so the token list shows their origin.
const hostTokenName = tempadmin.HostTokenName

// hostCaller is the caller the host command acts as: an administrator with
// no stored token, like a bootstrap administrator.
func hostCaller() identity.Principal {
	return identity.Principal{
		Subject:                hostSubject,
		DisplayName:            hostSubject,
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
	}
}

// hostEntry returns the audit entry of one host step: the host as the
// subject, through the API channel, with the action of the caller. The
// service of the step sets the time, the scope, the kind, the outcome and
// the object.
func hostEntry(action string) audit.Entry {
	return audit.Entry{
		FenceVersion: 0,
		At:           time.Time{},
		Namespace:    "",
		Subject:      hostSubject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         "",
		Outcome:      "",
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
	}
}

// hostFlags holds the settings of the host command.
type hostFlags struct {
	common
}

// hostCreateTempAdmin runs the host command: it opens the database and
// makes the person a temporary administrator, then prints the secret of
// the new token once with its fixed expiry.
func hostCreateTempAdmin(
	ctx context.Context, args []string, stdout, stderr io.Writer,
) error {
	var f hostFlags

	fs := flag.NewFlagSet("host create-temp-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return errHostNeedsPerson
	}

	person := strings.TrimSpace(rest[0])

	if err := principal.ValidateSubject(person); err != nil {
		return err
	}

	logger := slog.New(slog.DiscardHandler)

	st, err := openStore(ctx, f.database, logger)
	if err != nil {
		if errors.Is(err, sqlite.ErrInUse) {
			return fmt.Errorf(
				"host create-temp-admin: database %s is in use: stop the server that uses this file, then run the command again: %w",
				f.database,
				err,
			)
		}

		return err
	}

	defer st.close()

	made, err := createTempAdmin(ctx, st, person)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "person: %s\ntoken: %s\nexpires: %s\n",
		made.subject, made.secret, made.expires.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write host output: %w", err)
	}

	return nil
}

// hostMade is the result of a host create: the person, the secret of the
// new token shown once, and its fixed expiry.
type hostMade struct {
	subject string
	secret  string
	expires time.Time
}

// createTempAdmin makes person a temporary administrator through the store
// st: it reads the token policy first, sweeps the records that ended, makes
// a new person principal, grants it the site-administrator role, records
// the temporary administrator, and issues its personal token. The record
// and the token share one lifetime: the token policy maximum when that is
// below the fixed temporary administrator lifetime, or that lifetime
// otherwise. Each step writes its audit entry. A step that fails after the
// person exists undoes the earlier steps before the error returns, so no
// live grant, person or record stays without a token.
func createTempAdmin(ctx context.Context, st store, person string) (hostMade, error) {
	var made hostMade

	temps, err := tempadmin.NewService(tempadmin.Config{Store: st, Now: nil})
	if err != nil {
		return made, fmt.Errorf("temporary administrator service: %w", err)
	}

	tokens, tokensErr := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if tokensErr != nil {
		return made, fmt.Errorf("access token service: %w", tokensErr)
	}

	lifetime, err := hostLifetime(ctx, tokens)
	if err != nil {
		return made, err
	}

	_, sweepErr := temps.Sweep(ctx, hostEntry(tempadmin.Action))
	if sweepErr != nil {
		return made, fmt.Errorf("sweep ended temporary administrators: %w", sweepErr)
	}

	principals, principalsErr := principal.NewService(
		principal.Config{Tokens: nil, Store: st, Guard: nil, Namespaces: nil, Now: nil, NewID: nil},
	)
	if principalsErr != nil {
		return made, fmt.Errorf("principal service: %w", principalsErr)
	}

	p, personErr := personOf(ctx, st, principals, person)
	if personErr != nil {
		return made, personErr
	}

	grantID, grantErr := grantSiteAdmin(ctx, st, p)
	if grantErr != nil {
		return made, errors.Join(grantErr, dropPerson(ctx, st, p))
	}

	expires, expiresErr := tempExpires(ctx, temps, p, person, lifetime)
	if expiresErr != nil {
		return made, errors.Join(expiresErr, revokeHostGrant(ctx, st, grantID), dropPerson(ctx, st, p))
	}

	tok, secret, createErr := issueHostToken(ctx, tokens, p, expires, person)
	if createErr != nil {
		return made, errors.Join(
			createErr,
			endHostRecord(ctx, st, p.ID),
			revokeHostGrant(ctx, st, grantID),
			dropPerson(ctx, st, p),
		)
	}

	return hostMade{subject: person, secret: secret.Reveal(), expires: tok.ExpiresAt}, nil
}

// hostLifetime answers the lifetime the host command uses for one record
// and its token: the token policy maximum when that is below the fixed
// temporary administrator lifetime, or that lifetime otherwise.
func hostLifetime(ctx context.Context, tokens *accesstoken.Service) (time.Duration, error) {
	policyMax, err := tokens.MaxLifetime(ctx)
	if err != nil {
		return 0, fmt.Errorf("read token policy: %w", err)
	}

	if policyMax <= 0 {
		return 0, errHostBadLifetime
	}

	return min(tempadmin.Lifetime, policyMax), nil
}

// tempExpires records the principal p as a temporary administrator for the
// shared lifetime and answers its expiry.
func tempExpires(
	ctx context.Context, temps *tempadmin.Service, p principal.Principal, person string, lifetime time.Duration,
) (time.Time, error) {
	rec, err := temps.CreateWithLifetime(ctx, hostEntry(tempadmin.Action), tempadmin.Input{
		Subject:     person,
		PrincipalID: p.ID,
		CreatedBy:   hostOperator(),
	}, lifetime)
	if err != nil {
		return time.Time{}, fmt.Errorf("record temporary administrator for %q: %w", person, err)
	}

	return rec.ExpiresAt, nil
}

// issueHostToken issues the personal recovery token of the principal p
// with the expiry expires.
func issueHostToken(
	ctx context.Context, tokens *accesstoken.Service, p principal.Principal, expires time.Time, person string,
) (accesstoken.Token, accesstoken.Secret, error) {
	tok, secret, err := tokens.Create(ctx, hostCaller(), hostEntry(string(identity.ActionServerManageTokens)),
		accesstoken.ScopePersonal, accesstoken.Input{PrincipalID: p.ID, Name: hostTokenName, ExpiresAt: expires})
	if err != nil {
		return accesstoken.Token{}, accesstoken.Secret{}, fmt.Errorf("issue access token for %q: %w", person, err)
	}

	return tok, secret, nil
}

// revokeHostGrant ends the grant grantID. No ID means the grant exists
// already and keeps working, so there is nothing to undo. A grant that is
// already gone stays gone.
func revokeHostGrant(ctx context.Context, st store, grantID string) error {
	if grantID == "" {
		return nil
	}

	entry := hostCleanupEntry(string(identity.ActionServerManageAccess), access.GrantIDRef(grantID))

	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if err := st.RevokeGrant(ctx, grantID, fence, entry, nil); err != nil &&
		!errors.Is(err, access.ErrNotFound) {
		return fmt.Errorf(
			"cleanup grant %q: DELETE /api/server/grants/{id} removes it: %w",
			grantID,
			err,
		)
	}

	return nil
}

// dropPerson deletes the person p. A person that is already gone stays
// gone: the delete also ends its grants, so no live grant stays.
func dropPerson(ctx context.Context, st store, p principal.Principal) error {
	entry := hostCleanupEntry(string(identity.ActionServerManagePrincipals), principal.Ref(p.ID))

	if err := st.DeletePrincipal(ctx, p.ID, entry); err != nil && !errors.Is(err, principal.ErrNotFound) {
		return fmt.Errorf(
			"cleanup person %q: DELETE /api/server/principals/%s with a site administrator token: %w",
			p.Subject,
			p.ID,
			err,
		)
	}

	return nil
}

// endHostRecord ends the temporary administrator record of the principal
// principalID. A record that is already ended or gone stays as it is.
func endHostRecord(ctx context.Context, st store, principalID string) error {
	entry := hostCleanupEntry(tempadmin.Action, tempadmin.Ref(principalID))

	if err := st.ExpireTempAdmin(ctx, principalID, entry); err != nil &&
		!errors.Is(err, tempadmin.ErrNotFound) && !errors.Is(err, tempadmin.ErrExpired) {
		return fmt.Errorf(
			"cleanup temporary administrator record for principal %q: it remains and expires at its expiry time: %w",
			principalID,
			err,
		)
	}

	return nil
}

// hostCleanupEntry returns the audit entry of one undo step of the host
// command: the host as the subject, in the server scope, with the action
// and the object of the caller.
func hostCleanupEntry(action, object string) audit.Entry {
	return audit.Entry{
		FenceVersion: 0,
		At:           time.Now().UTC().Truncate(time.Millisecond),
		Namespace:    audit.ServerScope,
		Subject:      hostSubject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       object,
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
	}
}

// hostOperator names the OS user that runs the host command, for the
// CreatedBy of the temporary administrator record.
func hostOperator() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}

	if name := os.Getenv("USER"); name != "" {
		return name
	}

	return "unknown"
}

// personOf makes a new person principal with the subject person. A subject
// that a principal already holds is refused: reusing it would end a normal
// account at the expiry of the record, and a later enable would hand its
// grants back. A bootstrap name never reaches the store:
// principal.ValidateSubject refuses it first.
func personOf(
	ctx context.Context, st store, principals *principal.Service, person string,
) (principal.Principal, error) {
	if _, err := st.GetPrincipalBySubject(ctx, person); err == nil {
		return principal.Principal{}, fmt.Errorf(
			"person %q already exists: pass a name that no principal holds: "+
				"opendustpan host create-temp-admin <person>: %w",
			person,
			ErrPersonExists,
		)
	} else if !errors.Is(err, principal.ErrNotFound) {
		return principal.Principal{}, fmt.Errorf("read principal %q: %w", person, err)
	}

	made, createErr := principals.CreatePrincipal(
		ctx,
		hostEntry(string(identity.ActionServerManagePrincipals)),
		principal.Input{Subject: person, DisplayName: "", Kind: principal.KindPerson},
	)
	if createErr != nil {
		return principal.Principal{}, fmt.Errorf("create principal %q: %w", person, createErr)
	}

	return made, nil
}

// grantSiteAdmin grants the person p the site-administrator role on the
// installation. It answers the ID of the new grant, or no ID when the
// grant exists already and keeps working.
func grantSiteAdmin(ctx context.Context, st store, p principal.Principal) (string, error) {
	authz, authzErr := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: nil, Units: nil, Principals: nil},
	)
	if authzErr != nil {
		return "", fmt.Errorf("access authorizer: %w", authzErr)
	}

	grants, grantsErr := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Now: nil, NewID: nil, BootstrapAdmins: nil,
	})
	if grantsErr != nil {
		return "", fmt.Errorf("access service: %w", grantsErr)
	}

	made, grantErr := grants.Grant(
		ctx,
		hostEntry(string(identity.ActionServerManageAccess)),
		hostCaller(),
		access.GrantInput{
			Object:  identity.ServerObject(),
			Role:    access.RoleSiteAdministrator,
			Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
		},
	)
	if grantErr != nil {
		if errors.Is(grantErr, access.ErrAlreadyExists) {
			return "", nil
		}

		return "", fmt.Errorf("grant site-administrator to %q: %w", p.Subject, grantErr)
	}

	return made.ID, nil
}
