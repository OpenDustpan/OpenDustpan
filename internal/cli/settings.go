package cli

// defaultFalse is the default of the bool declarations: no opt-in without
// an explicit flag or variable.
const defaultFalse = "false"

// referenceNone marks a reference default of no value: the setting stays
// empty unless the operator sets it.
const referenceNone = "(none)"

// stringSetting declares a string setting: the flag, the variable, the
// default, the --help text and the reference default.
func stringSetting(flag, env, def, help, reference string) *Setting {
	return &Setting{
		Flag:      flag,
		Env:       env,
		Default:   def,
		Help:      help,
		Reference: reference,
		Doc:       "",
		Kind:      KindString,
	}
}

// boolSetting declares a bool setting: the flag, the variable, the --help
// text and the reference default. The default is always false: no opt-in
// without an explicit flag or variable.
func boolSetting(flag, env, help, reference string) *Setting {
	return &Setting{
		Flag:      flag,
		Env:       env,
		Default:   defaultFalse,
		Help:      help,
		Reference: reference,
		Doc:       "",
		Kind:      KindBool,
	}
}

// intSetting declares an int64 setting in decimal: the flag, the variable,
// the default, the --help text and the reference default.
func intSetting(flag, env, def, help, reference string) *Setting {
	return &Setting{
		Flag:      flag,
		Env:       env,
		Default:   def,
		Help:      help,
		Reference: reference,
		Doc:       "",
		Kind:      KindInt,
	}
}

// envSetting declares a setting with no flag: the variable and its
// reference description. Flags show in process listings, so tokens and keys
// never get one.
func envSetting(env, doc string) *Setting {
	return &Setting{Flag: "", Env: env, Default: "", Help: "", Reference: "", Doc: doc, Kind: KindString}
}

// Settings holds one declaration for each setting of the binary: the flag,
// the environment variable, the default and the help. The same declaration
// registers the flag, prints --help and generates the settings reference,
// so the three never drift. A flag wins over its variable.
//
// Three flags share one declaration across commands with one help text that
// covers each use: --listen and --tls-cert (serve and health) and --backup
// (backup check and restore). Every other flag keeps the help text it had
// before the table.
//
//nolint:gochecknoglobals // The table is the single declaration list the issue orders; callers never mutate it.
var Settings = []*Setting{
	stringSetting(
		"database", "DUSTPAN_DATABASE", "sqlite:dustpan.db",
		"sqlite:<path> or postgres://... (DUSTPAN_DATABASE)",
		"`sqlite:dustpan.db` (or `postgres://...`)",
	),
	stringSetting(
		"region", "DUSTPAN_REGION", "local",
		"region this server is authoritative for (DUSTPAN_REGION)",
		"`local`",
	),
	stringSetting(
		"listen", "DUSTPAN_LISTEN", ":8080",
		"listen address of the server (DUSTPAN_LISTEN)",
		"`:8080`",
	),
	stringSetting(
		"console-dir", "DUSTPAN_CONSOLE_DIR", "",
		"serve a built web console from this directory (DUSTPAN_CONSOLE_DIR)",
		referenceNone,
	),
	stringSetting(
		"allowed-origins", "DUSTPAN_ALLOWED_ORIGINS", "",
		"comma-separated console origins allowed to call the API (DUSTPAN_ALLOWED_ORIGINS)",
		referenceNone,
	),
	stringSetting(
		"tls-cert", "DUSTPAN_TLS_CERT", "",
		"certificate file for HTTPS, with --tls-key; the health probe uses HTTPS when it is set "+
			"(DUSTPAN_TLS_CERT)",
		"(none, with `--tls-key`)",
	),
	stringSetting(
		"tls-key", "DUSTPAN_TLS_KEY", "",
		"key file for HTTPS, with --tls-cert (DUSTPAN_TLS_KEY)",
		"(none, with `--tls-cert`)",
	),
	stringSetting(
		"public-url", "DUSTPAN_PUBLIC_URL", "",
		"external base URL, for example https://dustpan.example (DUSTPAN_PUBLIC_URL)",
		referenceNone,
	),
	stringSetting(
		"trusted-proxies", "DUSTPAN_TRUSTED_PROXIES", "",
		"comma-separated CIDRs of trusted proxies, "+
			"empty means none (DUSTPAN_TRUSTED_PROXIES)",
		"(none: the server uses the address of the TCP peer only)",
	),
	stringSetting(
		"trusted-proxy-header", "DUSTPAN_TRUSTED_PROXY_HEADER", "x-forwarded-for",
		"forwarding header the server reads from trusted proxies: x-forwarded-for or forwarded "+
			"(DUSTPAN_TRUSTED_PROXY_HEADER)",
		"`x-forwarded-for` (or `forwarded`)",
	),
	boolSetting(
		"ip-allowlists", "DUSTPAN_IP_ALLOWLISTS",
		"turn on IP allowlists: a namespace that has a list refuses requests from addresses outside it "+
			"(DUSTPAN_IP_ALLOWLISTS)",
		"(none: off). Set `true` to turn the feature on. A namespace opts in with its own list (ADR 55). "+
			"The server needs `--trusted-proxies` and `--trusted-proxy-header` when a proxy sits in front of it, "+
			"or every client reads as the address of the proxy.",
	),
	boolSetting(
		"ip-allowlists-include-bootstrap", "DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP",
		"apply IP allowlists to the bootstrap admin token too (DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP)",
		"(none: the bootstrap admin token is exempt, so an administrator cannot lock themselves out). "+
			"Set `true` to apply the lists to it as well.",
	),
	stringSetting(
		"log-level", "DUSTPAN_LOG_LEVEL", "info",
		"log level: debug, info, warn or error (DUSTPAN_LOG_LEVEL)",
		"`info` (or `debug`, `warn`, `error`)",
	),
	stringSetting(
		"log-format", "DUSTPAN_LOG_FORMAT", "json",
		"log format: json or text (DUSTPAN_LOG_FORMAT)",
		"`json` (or `text`)",
	),
	stringSetting(
		"blob-dir", "DUSTPAN_BLOB_DIR", "",
		"directory for state content (DUSTPAN_BLOB_DIR, default blobs next to the database)",
		"`blobs` next to the SQLite file, or `./blobs` for PostgreSQL",
	),
	stringSetting(
		"blob-store", "DUSTPAN_BLOB_STORE", "fs",
		"blob store fs with --blob-dir, or s3://<bucket>[/<prefix>] (DUSTPAN_BLOB_STORE)",
		"`fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]`",
	),
	stringSetting(
		"s3-endpoint", "DUSTPAN_S3_ENDPOINT", "",
		"S3 endpoint URL, empty means AWS (DUSTPAN_S3_ENDPOINT)",
		"(none: AWS)",
	),
	stringSetting(
		"s3-region", "DUSTPAN_S3_REGION", "",
		"S3 region, empty means us-east-1 (DUSTPAN_S3_REGION)",
		"`us-east-1`",
	),
	stringSetting(
		"backup-target", "DUSTPAN_BACKUP_TARGET", "",
		"backup target dir:/path or s3://<bucket>[/<prefix>], empty means no backups (DUSTPAN_BACKUP_TARGET)",
		"(none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]`",
	),
	stringSetting(
		"key-encryption-key-file", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE", "",
		"file holding the base64 key-encryption key, default dustpan.key"+
			" next to the database (DUSTPAN_KEY_ENCRYPTION_KEY_FILE). An operator who loses the key file "+
			"loses every secret that the key encrypts, and nobody can recover them",
		"`dustpan.key` next to the SQLite file, or `./dustpan.key` for PostgreSQL",
	).WithDoc("A file that holds that value. Only its owner can read it."),
	boolSetting(
		"generate-key-encryption-key", "DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY",
		"make the key file with no prompt when no key exists, never overwriting a file that exists "+
			"(DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true). A lost key file means lost secrets",
		"(none: ask on a terminal, else refuse). Set `true` to make the key file with no prompt",
	).WithDoc("Set `true` to make the key file with no prompt when no key exists. " +
		"The server never overwrites a file that exists."),
	intSetting(
		"max-checkpoint-bytes", "DUSTPAN_MAX_CHECKPOINT_BYTES", "0",
		"largest checkpoint in bytes, 0 means the default (DUSTPAN_MAX_CHECKPOINT_BYTES)",
		"`0`: 32 MiB, the largest Pulumi checkpoint",
	),
	intSetting(
		"max-secret-batch-bytes", "DUSTPAN_MAX_SECRET_BATCH_BYTES", "0",
		"largest secrets batch in bytes, 0 means the default (DUSTPAN_MAX_SECRET_BATCH_BYTES)",
		"`0`: 32 MiB, the largest batch of Pulumi secrets",
	),
	intSetting(
		"max-state-bytes", "DUSTPAN_MAX_STATE_BYTES", "0",
		"largest OpenTofu state in bytes, 0 means the default (DUSTPAN_MAX_STATE_BYTES)",
		"`0`: 32 MiB, the largest OpenTofu state",
	),
	intSetting(
		"max-body-bytes-in-flight", "DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT", "0",
		"concurrent large bodies in bytes, 0 means the default (DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT)",
		"`0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. "+
			"The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs "+
			"document asks for more budget as the document grows. One sealed state with 16 MiB of outputs takes "+
			"about 72 MiB of the budget. Installations with large outputs should raise this value. An upload "+
			"that can never fit answers 413.",
	),
	intSetting(
		"checkpoint-delta-cutoff", "DUSTPAN_CHECKPOINT_DELTA_CUTOFF", "1048576",
		"checkpoint size in bytes from which the CLI sends delta uploads, "+
			"0 turns delta uploads off (DUSTPAN_CHECKPOINT_DELTA_CUTOFF)",
		"`1048576` (1 MiB). `0` turns delta checkpoint uploads off",
	),
	boolSetting(
		"disable-pulumi-journaling", "DUSTPAN_DISABLE_PULUMI_JOURNALING",
		"answer journalVersion 0 at the start of a Pulumi update, so the CLI saves checkpoints "+
			"(DUSTPAN_DISABLE_PULUMI_JOURNALING=true)",
		"(none: journaling on). Set `true` to make the Pulumi CLI save checkpoints",
	),
	stringSetting(
		"server", "DUSTPAN_SERVER", "",
		"base URL of the server, for example https://dustpan.example (DUSTPAN_SERVER)",
		referenceNone,
	),
	stringSetting(
		"ca-file", "", "",
		"PEM bundle of extra CA certificates to trust with the system store",
		referenceNone,
	),
	stringSetting(
		"backup", "", "",
		"backup ID to check or restore, for example bkp-aaa",
		referenceNone,
	),
	boolSetting(
		"check-only", "",
		"run the restore checks and apply nothing",
		"false",
	),
	boolSetting(
		"force", "",
		"cancel active updates and runs first, then restore",
		"false",
	),
	envSetting(
		"DUSTPAN_ADMIN_TOKEN",
		"Required when you set no token file. At least 32 bytes. Comma-separated tokens sign in as "+
			"distinct administrators: `admin`, `admin-2`, and so on. Give each CI job or runner that writes "+
			"OpenTofu state its own token, because the server checks the lock holder by subject. Each token "+
			"is a full administrator, and a change to the list needs a restart. The server clears the "+
			"variable from its environment after the read.",
	),
	envSetting(
		"DUSTPAN_ADMIN_TOKEN_FILE",
		"A file that holds the tokens, one per line or comma-separated. Only its owner can read it. "+
			"When you set both, `DUSTPAN_ADMIN_TOKEN` wins.",
	),
	envSetting(
		"DUSTPAN_KEY_ENCRYPTION_KEY",
		"The base64 text of 32 random bytes. It can stay unset while the database holds no secrets.",
	),
}
