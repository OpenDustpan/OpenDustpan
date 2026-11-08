package main

// Recovery of a lost administrator through the host: the host command makes
// a temporary administrator for one named person, with a fixed expiry and a
// personal access token. It opens the database named by --database itself,
// like serve does: it needs no listener, no identity provider and no DNS.
// This is the one command besides serve that opens the database: host shell
// access already controls the key and the database. The person comes from
// the command line, never from a configuration file, and the command reads
// no token from one either: host access is the credential, and the command
// prints the new secret once.
//
//	opendustpan host create-temp-admin --database sqlite:<path> --reason <text> <person>
//
// The command refuses when DUSTPAN_DISABLE_HOST_RECOVERY turns it off, when
// that variable holds a value that is not a bool, and when the recovery mode
// of the installation is none. It needs a reason: --reason, or a prompt on a
// terminal. It applies no migration: it refuses a database with a pending
// migration, so the server stays the one process that changes the schema.
// In one store transaction it makes a new person principal, grants it the
// site-administrator role on the installation, records the temporary
// administrator with the reason, and issues its token, each with its audit
// entry. The person must name no principal that exists: the
// command never reuses one, so an expiry never ends a normal account. The
// exit code is 0 on success and 1 on any refusal.

import (
	"bufio"
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

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Sentinel errors of the host command. Each error text names what failed
// and the one command that helps.
var (
	// errHostNeedsPerson reports a host run without exactly one person
	// name on the command line.
	errHostNeedsPerson = errors.New(
		"host create-temp-admin needs one person name: opendustpan host create-temp-admin --reason <text> <person>",
	)
	// ErrPersonExists reports a host run that names a person that a
	// principal already holds.
	ErrPersonExists = errors.New("person already exists")
	// errHostOff reports a host run while the operator turned the command
	// off.
	errHostOff = errors.New(
		"host create-temp-admin is off: unset DUSTPAN_DISABLE_HOST_RECOVERY on the host, then run the command again",
	)
	// errHostModeNone reports a host run on an installation with no
	// break-glass.
	errHostModeNone = errors.New(
		"host create-temp-admin is off: the recovery mode is none (no break-glass): " +
			"set DUSTPAN_RECOVERY_MODE=bootstrap on the host, then run the command again",
	)
	// errHostBadMode reports a recovery mode that the command does not know.
	errHostBadMode = errors.New("unknown recovery mode: set DUSTPAN_RECOVERY_MODE to bootstrap, signed-request or none")
	// errHostMigrationsPending reports a host run on a database with a
	// pending migration.
	errHostMigrationsPending = errors.New("host create-temp-admin applies no migration")
	// errHostNeedsReason reports a host run with no reason.
	errHostNeedsReason = errors.New(
		"host create-temp-admin needs a reason: opendustpan host create-temp-admin --reason <text> <person>",
	)
)

// Recovery modes of the installation. The host command runs in the modes
// bootstrap and signed-request, and refuses in the mode none.
const (
	modeBootstrap     = "bootstrap"
	modeSignedRequest = "signed-request"
	modeNone          = "none"
)

// hostSweepNote is the line that the host command writes to standard error
// when the sweep of ended temporary administrators does not end every record.
const hostSweepNote = "some ended temporary administrators did not expire: " +
	"run the command again, then check the server log"

// hostReviewNote is the line that the host command writes to standard error
// after a create: no store keeps break-glass reviews yet, so no review opens
// at the expiry.
const hostReviewNote = "note: no break-glass review opens for this temporary administrator yet: " +
	"review its audit entries by hand after the expiry"

// maxReasonBytes caps the reason that the host command reads from the
// prompt, so the read never takes all memory.
const maxReasonBytes = 4096

// hostSubject is the subject of the audit entries of the host command: the
// host itself acted, through no sign-in.
const hostSubject = tempadmin.HostSubject

// hostEntry returns the audit entry of one host step: the host as the
// subject, through the API channel, with the action of the caller. The
// service of the step sets the time, the scope, the kind, the outcome and
// the object.
func hostEntry(action string) audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            time.Time{},
		Namespace:     "",
		Subject:       hostSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          "",
		Outcome:       "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// hostFlags holds the settings of the host command.
type hostFlags struct {
	common

	reason string
	mode   string
}

// hostIO holds what the host command reads besides its arguments: the
// standard input of the reason prompt, whether it is a terminal, and the
// environment.
type hostIO struct {
	stdin    io.Reader
	terminal bool
	lookup   cli.LookupFunc
}

// hostCreateTempAdmin runs the host command: it opens the database and
// makes the person a temporary administrator, then prints the secret of
// the new token once with its fixed expiry.
func hostCreateTempAdmin(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	in := hostIO{stdin: os.Stdin, terminal: isTerminal(os.Stdin, stderr), lookup: os.LookupEnv}

	return hostCreateTempAdminWithIO(ctx, args, in, stdout, stderr)
}

// hostCreateTempAdminWithIO is hostCreateTempAdmin with an explicit
// standard input, terminal flag and environment, so tests drive each.
func hostCreateTempAdminWithIO(
	ctx context.Context, args []string, in hostIO, stdout, stderr io.Writer,
) error {
	var f hostFlags

	fs := flag.NewFlagSet("host create-temp-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)
	cli.ByFlag("reason").String(fs, &f.reason)

	mode := cli.ByFlag("recovery-mode")
	fs.StringVar(&f.mode, mode.Flag, cli.EnvWith(in.lookup, mode.Env, mode.Default), mode.Help)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if err := hostAllowed(in.lookup, f.mode); err != nil {
		return err
	}

	if strings.TrimSpace(f.database) == "" {
		return errNoDatabase
	}

	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return errHostNeedsPerson
	}

	person := strings.TrimSpace(rest[0])

	if err := principal.ValidateSubject(person); err != nil {
		return err
	}

	reason, err := hostReason(f.reason, in, stderr)
	if err != nil {
		return err
	}

	made, err := runHostCreate(ctx, f.database, hostInput{person: person, reason: reason}, stderr)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "person: %s\ntoken: %s\nexpires: %s\n",
		made.subject, made.secret, made.expires.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write host output: %w", err)
	}

	if _, err := fmt.Fprintln(stderr, hostReviewNote); err != nil {
		return fmt.Errorf("write host note: %w", err)
	}

	return nil
}

// hostAllowed returns why the host command must not run: the off switch
// in the environment that lookup reads is on or holds a value that is not
// a bool, or the recovery mode is none or unknown. It opens no store.
func hostAllowed(lookup cli.LookupFunc, mode string) error {
	off, err := cli.HostRecoveryOff(lookup)
	if err != nil {
		return fmt.Errorf("host create-temp-admin: %w", err)
	}

	if off {
		return errHostOff
	}

	switch strings.TrimSpace(mode) {
	case modeBootstrap, modeSignedRequest:
		return nil
	case modeNone:
		return errHostModeNone
	default:
		return fmt.Errorf("%w: %q", errHostBadMode, mode)
	}
}

// hostReason answers the reason of the run: the flag value, or a line that
// the operator types at a prompt on a terminal. A run with no reason, or
// with a reason that tempadmin.ValidateReason refuses, fails.
func hostReason(flagValue string, in hostIO, stderr io.Writer) (string, error) {
	reason := strings.TrimSpace(flagValue)

	if reason == "" && in.terminal {
		if _, err := fmt.Fprint(stderr, "Reason for the temporary administrator: "); err != nil {
			return "", fmt.Errorf("write the reason prompt: %w", err)
		}

		line, err := bufio.NewReader(io.LimitReader(in.stdin, maxReasonBytes)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read the reason: %w", err)
		}

		reason = strings.TrimSpace(line)
	}

	if reason == "" {
		return "", errHostNeedsReason
	}

	if err := tempadmin.ValidateReason(reason); err != nil {
		return "", fmt.Errorf("host create-temp-admin: %w", err)
	}

	return reason, nil
}

// runHostCreate opens the database with no migration and makes the
// temporary administrator of in.
func runHostCreate(ctx context.Context, database string, in hostInput, stderr io.Writer) (hostMade, error) {
	st, err := openStore(ctx, database, slog.New(slog.DiscardHandler), openNoMigrate, "")
	if errors.Is(err, schema.ErrMigrationsPending) {
		return hostMade{}, fmt.Errorf(
			"%w: database %s has pending migrations: back up the database, "+
				"start opendustpan serve --database %s with this binary to apply them, then run the command again: %w",
			errHostMigrationsPending, database, database, err,
		)
	}

	if err != nil {
		if errors.Is(err, sqlite.ErrInUse) {
			return hostMade{}, fmt.Errorf(
				"host create-temp-admin: database %s is in use: stop the server that uses this file, then run the command again: %w",
				database,
				err,
			)
		}

		return hostMade{}, err
	}

	defer st.close()

	return createTempAdmin(ctx, st, in, stderr)
}

// hostInput names one run: the person and the reason.
type hostInput struct {
	person string
	reason string
}

// hostMade is the result of a host create: the person, the secret of the
// new token shown once, and its fixed expiry.
type hostMade struct {
	subject string
	secret  string
	expires time.Time
}

// createTempAdmin makes in.person a temporary administrator through the
// store st. It sweeps the records that ended first, then makes the person,
// its site-administrator grant, its record and its token in one store
// transaction, so a crash leaves all of them or none. The record and the
// token share one lifetime: the token policy maximum when that is below
// the fixed temporary administrator lifetime, or that lifetime otherwise.
func createTempAdmin(ctx context.Context, st store, in hostInput, stderr io.Writer) (hostMade, error) {
	temps, err := tempadmin.NewService(tempadmin.Config{Store: st, Reviews: nil, Now: nil, Logger: nil})
	if err != nil {
		return hostMade{}, fmt.Errorf("temporary administrator service: %w", err)
	}

	if _, sweepErr := temps.Sweep(ctx, hostEntry(tempadmin.Action)); sweepErr != nil {
		if !errors.Is(sweepErr, tempadmin.ErrSweepIncomplete) {
			return hostMade{}, fmt.Errorf("sweep ended temporary administrators: %w", sweepErr)
		}

		if _, werr := fmt.Fprintln(stderr, hostSweepNote); werr != nil {
			return hostMade{}, fmt.Errorf("write sweep note: %w", werr)
		}
	}

	if _, readErr := st.GetPrincipalBySubject(ctx, in.person); readErr == nil {
		return hostMade{}, personExists(in.person)
	} else if !errors.Is(readErr, principal.ErrNotFound) {
		return hostMade{}, fmt.Errorf("read principal %q: %w", in.person, readErr)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil},
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		return hostMade{}, fmt.Errorf("access token service: %w", err)
	}

	issuer, err := accesstoken.NewHostIssuer(tokens, st)
	if err != nil {
		return hostMade{}, fmt.Errorf("host token issuer: %w", err)
	}

	got, err := issuer.Recover(ctx, hostEntry(""), accesstoken.HostInput{
		Subject: in.person, Reason: in.reason, CreatedBy: hostOperator(),
	})
	if errors.Is(err, principal.ErrAlreadyExists) {
		return hostMade{}, personExists(in.person)
	}

	if err != nil {
		return hostMade{}, err
	}

	return hostMade{subject: in.person, secret: got.Secret.Reveal(), expires: got.Token.ExpiresAt}, nil
}

// personExists returns the refusal of a person name that a principal
// already holds. Reusing it would end a normal account at the expiry of the
// record, and a later enable would hand its grants back.
func personExists(person string) error {
	return fmt.Errorf(
		"person %q already exists: pass a name that no principal holds: "+
			"opendustpan host create-temp-admin --reason <text> <person>: %w",
		person,
		ErrPersonExists,
	)
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
