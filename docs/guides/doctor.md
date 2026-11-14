# Check an install with doctor

`opendustpan doctor` checks the settings of a production install and, if you
give it a server, the running server. It prints one line for each check. It
exits with 0 when no check fails, and with 1 when a check fails. A pipeline
reads the exit code.

`doctor` takes the flags of `serve` and reads them through the same layers:
the flag, the environment variable and the configuration file. Run it with the
same flags, variables and `--config` file as `serve`. It opens no database. It
does not read the key file: it asks the file system for the mode and the size
of the file. It refuses to start in a shell that holds `DUSTPAN_DEV`, as
`serve` does.

```sh
opendustpan doctor --config /etc/opendustpan/config.yaml
```

Each line starts with `pass`, `warn` or `fail`, then the name of the check:

```text
pass  key: the key file /etc/opendustpan/dustpan.key has mode 0600 and the size of a key
pass  key place: the key file is not next to the data
pass  database: the database is set
pass  admin token: no bootstrap admin token is set
warn  backup target: no backup target is set: set --backup-target (DUSTPAN_BACKUP_TARGET), for example dir:/var/backups/dustpan
```

No line shows a key, a token or a database URL.

## The checks of the settings

| Check | Result when it does not hold |
| --- | --- |
| The key-encryption key is set. A key file exists, has mode 0600 or stricter, the owner can read it, and has the size of a base64 key of 32 bytes. A file with mode 0000 fails, because `serve` cannot read it. | `fail` |
| The key file is not in the directory of the SQLite file or of the blobs. `doctor` resolves symbolic links first. | `warn` |
| The database is set. | `fail` |
| Each bootstrap admin token has at least 32 bytes. With no admin token, the line says so and passes. | `fail` |
| A backup target is set. | `warn` |
| The listen address is not loopback, and you set no TLS, and `--public-url` is not an `https` address of a proxy. | `warn` |
| With `--public-url`, you also set `--trusted-proxies`. | `warn` |

## The checks of a running server

Add `--server` (`DUSTPAN_SERVER`) and, for a server with a private CA,
`--ca-file`. `doctor` sends the token of the client commands
(`DUSTPAN_ADMIN_TOKEN` or `DUSTPAN_ADMIN_TOKEN_FILE`). The token needs the
actions that the routes below need.

| Check | Route | Result when it does not hold |
| --- | --- | --- |
| The capabilities document reports `installationKind` `production`. A missing field fails the check. | `GET /api/dustpan/v1/capabilities` | `fail` |
| The upgrade check gives no `cannot start` for this binary. | `GET /api/dustpan/v1/upgrade-check` | `warn` |
| The last successful backup ended less than 25 hours ago. | `GET /api/server/backups` | `warn` |

Without the token, the first check fails, and the other two say that they did
not run.

## Related

- The [configuration reference](../reference/configuration.md) lists the
  flags of `serve`, and so of `doctor`.
- `opendustpan key generate --out <path>` makes a key file that passes the
  first check. See the [security model](../explanation/security-model.md).
- The [backup guide](backup-restore-upgrade.md) shows how to set a backup
  target.
