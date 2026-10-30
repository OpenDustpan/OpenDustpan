// Package client holds the REST API clients of the opendustpan command.
//
// Every command here takes --server and a token from the environment, and
// never opens the database, a blob store, a backup target or the
// key-encryption key: only the serve wiring in cmd/opendustpan opens the
// data (ADR 39 decision 1). The name internal/cli/client says where the
// commands live: cli groups the command wiring, client holds the commands
// that are clients of the server, and serve stays in cmd/opendustpan. A
// depguard rule denies this package the store adapters, the blob adapters,
// the backup target code and the key-encryption key code, so a future
// client command cannot reopen the offline paths that ADR 39 deleted.
package client

import (
	"errors"
)

// ErrUsage answers the usage of every subcommand. It lists the commands in
// groups by intent. The names of the commands do not change.
var ErrUsage = errors.New(
	"usage: opendustpan <command> [flags] | opendustpan --version\n" +
		"\n" +
		"Try out:\n" +
		"  opendustpan dev\n" +
		"\n" +
		"Run in production:\n" +
		"  opendustpan serve\n" +
		"  opendustpan key generate --out <path>\n" +
		"  opendustpan doctor\n" +
		"\n" +
		"Operate:\n" +
		"  opendustpan health\n" +
		"  opendustpan namespace create <name>\n" +
		"  opendustpan host create-temp-admin <person>\n" +
		"  opendustpan audit-chain status\n" +
		"  opendustpan audit-chain approve\n" +
		"\n" +
		"Back up:\n" +
		"  opendustpan backup\n" +
		"  opendustpan backup check\n" +
		"  opendustpan restore\n" +
		"\n" +
		"Check the store:\n" +
		"  opendustpan store check\n" +
		"\n" +
		"Upgrade:\n" +
		"  opendustpan upgrade check\n" +
		"\n" +
		"Run opendustpan <command> --help for the flags of a command",
)

// KeyEnvVar names the setting that holds the base64 key-encryption key. The
// reports name it, so the operator knows which setting protects the backup.
const KeyEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY"

// KeyFileEnvVar names the setting that holds the key file path. The key
// warning names it, so the operator knows which setting to check.
const KeyFileEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY_FILE"
