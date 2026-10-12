package main

// Compatibility with the client commands in internal/cli/client (ADR 39
// decision 4). The client commands live in that package, where a depguard
// rule denies the store adapters, the blob adapters, the backup target code
// and the key-encryption key code. This file keeps the names the serve
// wiring and the command tests use, so the move changes no behavior and no
// output: every alias holds the same value as the client package.

import (
	"context"
	"io"

	"opendustpan.org/opendustpan/internal/cli/client"
)

const (
	adminTokenEnvVar       = client.AdminTokenEnvVar
	adminTokenFileEnvVar   = client.AdminTokenFileEnvVar
	backupServerEnvVar     = client.BackupServerEnvVar
	maxClientBodyBytes     = client.MaxClientBodyBytes
	verdictUpToDate        = client.VerdictUpToDate
	verdictUpgradePossible = client.VerdictUpgradePossible
	verdictCannotStart     = client.VerdictCannotStart
	keyWarning             = client.KeyWarning
)

var (
	errUsage                     = client.ErrUsage
	errBackupID                  = client.ErrBackupID
	errBackupNeedsServer         = client.ErrBackupNeedsServer
	errBackupNoToken             = client.ErrBackupNoToken
	errBackupRead                = client.ErrBackupRead
	errResponseTooLarge          = client.ErrResponseTooLarge
	errHealthNotReady            = client.ErrHealthNotReady
	errHealthFailed              = client.ErrHealthFailed
	errUpgradeVersionMismatch    = client.ErrUpgradeVersionMismatch
	errAdminTokenMissing         = client.ErrAdminTokenMissing
	errAdminTokenFilePermissions = client.ErrAdminTokenFilePermissions
)

type (
	backupStatusResponse  = client.BackupStatusResponse
	backupManifest        = client.BackupManifest
	restoreStatusResponse = client.RestoreStatusResponse
	restoreManifest       = client.RestoreManifest
	upgradeCheckAnswer    = client.UpgradeCheckAnswer
	checkBackupAnswer     = client.CheckBackupAnswer
	healthFlags           = client.HealthFlags
	restoreStartRequest   = client.RestoreStartRequest
)

// backupCommand runs `opendustpan backup` through the client package.
func backupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return client.BackupCommand(ctx, args, stdout, stderr)
}

// createNamespace runs `opendustpan namespace create` through the client package.
func createNamespace(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return client.CreateNamespace(ctx, args, stdout, stderr)
}

// restoreCommand runs `opendustpan restore` through the client package.
func restoreCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return client.RestoreCommand(ctx, args, stdout, stderr)
}

// healthURL answers the readiness address through the client package.
func healthURL(listen, tlsCert string) (string, error) {
	return client.HealthURL(listen, tlsCert)
}

// backupToken returns the access token through the client package.
func backupToken() (string, error) {
	return client.BackupToken()
}

// loadAdminTokensFrom reads the admin tokens through the client package.
func loadAdminTokensFrom(lookup func(key string) (string, bool)) ([]string, error) {
	return client.LoadAdminTokensFrom(lookup)
}
