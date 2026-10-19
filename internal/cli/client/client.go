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

// ErrUsage answers the usage of every subcommand.
var ErrUsage = errors.New(
	"usage: opendustpan --version | opendustpan serve | opendustpan dev | opendustpan health | opendustpan namespace create <name> | " +
		"opendustpan upgrade check | opendustpan backup check | opendustpan backup | opendustpan restore | " +
		"opendustpan audit-chain status | opendustpan audit-chain approve",
)

// KeyEnvVar names the setting that holds the base64 key-encryption key. The
// reports name it, so the operator knows which setting protects the backup.
const KeyEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY"

// KeyFileEnvVar names the setting that holds the key file path. The key
// warning names it, so the operator knows which setting to check.
const KeyFileEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY_FILE"
