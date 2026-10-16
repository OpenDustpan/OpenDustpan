package main

import (
	"context"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/cli/client"
)

// commandTable answers the command table of the binary: one row for each
// command with its path, synopsis, settings and run function. Longer paths
// come before their prefixes, so backup check matches before backup. Serve
// is the only command that opens stores; every other command is a client of
// the REST API (ADR 39 decision 1).
func commandTable() cli.Table {
	return cli.Table{
		cli.NamespaceCreateCommand(client.CreateNamespace),
		cli.UpgradeCheckCommand(func(
			ctx context.Context, args []string, stdout, stderr io.Writer,
		) error {
			return client.UpgradeCheck(ctx, args, stdout, stderr, version)
		}),
		cli.BackupCheckCommand(client.BackupCheck),
		cli.ServeCommand(func(ctx context.Context, args []string, _, stderr io.Writer) error {
			return serve(ctx, args, stderr)
		}),
		cli.HealthCommand(client.RunHealth),
		cli.BackupCommand(client.BackupCommand),
		cli.RestoreCommand(client.RestoreCommand),
		cli.AuditChainStatusCommand(client.AuditChainStatus),
		cli.AuditChainApproveCommand(client.AuditChainApprove),
	}
}

// run dispatches args through the command table and keeps --version. A nil
// error means exit code 0, a non-nil error means exit code 1.
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

	cmd, rest, ok := commandTable().Match(args)
	if !ok {
		return errUsage
	}

	return cmd.Run(ctx, rest, stdout, stderr)
}
