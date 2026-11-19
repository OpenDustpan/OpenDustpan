package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/cli/client"
)

// commandTable answers the command table of the binary: one row for each
// command with its path, synopsis, settings and run function. Longer paths
// come before their prefixes, so backup check matches before backup. Serve,
// dev and host create-temp-admin open stores: serve and dev run the server,
// and host create-temp-admin recovers a lost administrator through the database,
// because host shell access already controls the key and the database.
// Every other command is a client of the REST API.
func commandTable() cli.Table {
	return cli.Table{
		cli.NamespaceCreateCommand(client.CreateNamespace),
		cli.CollectionCreateCommand(cli.CollectionCreate),
		cli.CollectionListCommand(cli.CollectionList),
		cli.PlacementAddCommand(cli.PlacementAdd),
		cli.PlacementListCommand(cli.PlacementList),
		cli.PlacementDeleteCommand(cli.PlacementDelete),
		cli.UpgradeCheckCommand(func(
			ctx context.Context, args []string, stdout, stderr io.Writer,
		) error {
			return client.UpgradeCheck(ctx, args, stdout, stderr, version)
		}),
		cli.BackupCheckCommand(client.BackupCheck),
		cli.StoreCheckCommand(client.StoreCheck),
		cli.ServeCommand(func(ctx context.Context, args []string, _, stderr io.Writer) error {
			return serveCommand(ctx, args, stderr)
		}),
		cli.DevCommand(func(ctx context.Context, args []string, _, stderr io.Writer) error {
			return dev(ctx, args, stderr)
		}),
		cli.KeyGenerateCommand(),
		cli.DoctorCommand(func(ctx context.Context, args []string, stdout, stderr io.Writer) error {
			return client.Doctor(ctx, args, stdout, stderr, version)
		}),
		cli.HealthCommand(client.RunHealth),
		cli.BackupCommand(client.BackupCommand),
		cli.RestoreCommand(client.RestoreCommand),
		cli.HostCreateTempAdminCommand(hostCreateTempAdmin),
		cli.AuditChainStatusCommand(client.AuditChainStatus),
		cli.AuditChainApproveCommand(client.AuditChainApprove),
	}
}

// run dispatches args through the command table and keeps --version. A nil
// error means exit code 0, a non-nil error means exit code 1. A request for
// help (-h or --help) is not an error: at the top level it prints the usage
// line to stdout, and in a command the flag set has printed the flags.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	if args[0] == "--version" {
		_, err := fmt.Fprintln(stdout, version)
		if err != nil {
			return fmt.Errorf("write version: %w", err)
		}

		return nil
	}

	if args[0] == "--help" || args[0] == "-h" {
		if _, err := fmt.Fprintln(stdout, errUsage.Error()); err != nil {
			return fmt.Errorf("write usage: %w", err)
		}

		return nil
	}

	cmd, rest, ok := commandTable().Match(args)
	if !ok {
		return unknownCommand(commandTable(), args)
	}

	err := cmd.Run(ctx, rest, stdout, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	return err
}

// commandError is the error of a command line that names no command. It
// matches errUsage, so a caller that tests for the usage error keeps working.
type commandError struct {
	text string
}

// Error returns the short text with the one command that helps.
func (e *commandError) Error() string {
	return e.text
}

// Is reports that the error is the usage error.
func (e *commandError) Is(target error) bool {
	return target == errUsage
}

// unknownCommand returns the error for args that match no command. It says
// if the first word is not a command, or if it is the start of a command that
// needs more words.
func unknownCommand(table cli.Table, args []string) error {
	const help = ": run opendustpan --help for the list of commands"

	for _, cand := range table {
		if len(cand.Path) > 0 && cand.Path[0] == args[0] {
			return &commandError{text: fmt.Sprintf("the command %q is not complete or is not known", args[0]) + help}
		}
	}

	return &commandError{text: fmt.Sprintf("unknown command %q", args[0]) + help}
}
