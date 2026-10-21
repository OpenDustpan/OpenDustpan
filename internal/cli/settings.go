package cli

import "opendustpan.org/opendustpan/pkg/history"

// retentionDefault is the default of the retention settings: 400 days. A
// test proves it equals history.DefaultRetention.
const retentionDefault = "9600h"

// unsafeAllowDevBackupsFlag is the flag that lets a production installation
// restore a backup of a dev installation. The name says that the choice is
// unsafe. The flag has no variable and no configuration file key.
const unsafeAllowDevBackupsFlag = "unsafe-allow-restoring-dev-installation-backups-into-production"

// UnsafeAllowDevBackupsFlag is the name of the flag that lets a production
// installation restore a backup of a dev installation.
const UnsafeAllowDevBackupsFlag = unsafeAllowDevBackupsFlag

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
		File:      "",
		Kind:      KindString,
		Sensitive: false,
		URL:       false,
		StepInput: false,

		NoPasswordInFile: false,
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
		File:      "",
		Kind:      KindBool,
		Sensitive: false,
		URL:       false,
		StepInput: false,

		NoPasswordInFile: false,
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
		File:      "",
		Kind:      KindInt,
		Sensitive: false,
		URL:       false,
		StepInput: false,

		NoPasswordInFile: false,
	}
}

// envSetting declares a setting with no flag: the variable and its
// reference description. Flags show in process listings, so tokens and keys
// never get one.
func envSetting(env, doc string) *Setting {
	return &Setting{
		Flag:      "",
		Env:       env,
		Default:   "",
		Help:      "",
		Reference: "",
		Doc:       doc,
		File:      "",
		Kind:      KindString,
		Sensitive: false,
		URL:       false,
		StepInput: false,

		NoPasswordInFile: false,
	}
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
		"config", "DUSTPAN_CONFIG", "",
		"YAML configuration file; a flag and a variable win over it (DUSTPAN_CONFIG)",
		referenceNone,
	),
	stringSetting(
		"database", "DUSTPAN_DATABASE", "sqlite:dustpan.db",
		"sqlite:<path> or postgres://... (DUSTPAN_DATABASE)",
		"`sqlite:dustpan.db` (or `postgres://...`)",
	).WithFile("database").WithSensitive().WithNoPasswordInFile(),
	stringSetting(
		"region", "DUSTPAN_REGION", "local",
		"region this server is authoritative for (DUSTPAN_REGION)",
		"`local`",
	).WithFile("region"),
	stringSetting(
		"components", "DUSTPAN_COMPONENTS", "all",
		"components this process runs: all, or a comma-separated list of api and worker (DUSTPAN_COMPONENTS)",
		"`all` (or a comma-separated list of `api` and `worker`). `api` runs each HTTP route and starts "+
			"durable operations, but never claims one. `worker` runs the durable operations, the schedules, "+
			"the audit shipper and the audit chain check. A process that runs `worker` without `api` answers 404 "+
			"on each route except the probes and the metrics route. An unknown name stops the start. "+
			"See the several instances guide (docs/guides/several-instances.md)",
	).WithFile("components"),
	stringSetting(
		"listen", "DUSTPAN_LISTEN", ":8080",
		"listen address of the server (DUSTPAN_LISTEN)",
		"`:8080`",
	).WithFile("listen"),
	stringSetting(
		"console-dir", "DUSTPAN_CONSOLE_DIR", "",
		"serve a built web console from this directory (DUSTPAN_CONSOLE_DIR)",
		referenceNone,
	).WithFile("console-dir"),
	stringSetting(
		"allowed-origins", "DUSTPAN_ALLOWED_ORIGINS", "",
		"comma-separated console origins allowed to call the API (DUSTPAN_ALLOWED_ORIGINS)",
		referenceNone+". A comma-separated list. Each origin is a scheme (`http` or `https`), a host and an "+
			"optional port, in lower case with an ASCII (punycode) host. It has no default port, path, query or "+
			"fragment. The server refuses `null` and `*`. A listed origin gets CORS headers on `/api/`, with "+
			"`Access-Control-Expose-Headers: Retry-After, ETag, Idempotent-Replayed` on answers to requests.",
	).WithFile("allowed-origins"),
	stringSetting(
		"tls-cert", "DUSTPAN_TLS_CERT", "",
		"certificate file for HTTPS, with --tls-key; the health probe uses HTTPS when it is set "+
			"(DUSTPAN_TLS_CERT)",
		"(none, with `--tls-key`)",
	).WithFile("tls-cert"),
	stringSetting(
		"tls-key", "DUSTPAN_TLS_KEY", "",
		"key file for HTTPS, with --tls-cert (DUSTPAN_TLS_KEY)",
		"(none, with `--tls-cert`)",
	).WithFile("tls-key"),
	stringSetting(
		"public-url", "DUSTPAN_PUBLIC_URL", "",
		"external base URL, for example https://dustpan.example (DUSTPAN_PUBLIC_URL)",
		referenceNone,
	).WithFile("public-url").WithURL(),
	stringSetting(
		"trusted-proxies", "DUSTPAN_TRUSTED_PROXIES", "",
		"comma-separated CIDRs of trusted proxies, "+
			"empty means none (DUSTPAN_TRUSTED_PROXIES)",
		"(none: the server uses the address of the TCP peer only)",
	).WithFile("trusted-proxies"),
	stringSetting(
		"trusted-proxy-header", "DUSTPAN_TRUSTED_PROXY_HEADER", "x-forwarded-for",
		"forwarding header the server reads from trusted proxies: x-forwarded-for or forwarded "+
			"(DUSTPAN_TRUSTED_PROXY_HEADER)",
		"`x-forwarded-for` (or `forwarded`)",
	).WithFile("trusted-proxy-header"),
	boolSetting(
		"ip-allowlists", "DUSTPAN_IP_ALLOWLISTS",
		"turn on IP allowlists: a namespace that has a list refuses requests from addresses outside it "+
			"(DUSTPAN_IP_ALLOWLISTS)",
		"(none: off). Set `true` to turn the feature on. A namespace opts in with its own list (ADR 55). "+
			"The server needs `--trusted-proxies` and `--trusted-proxy-header` when a proxy sits in front of it, "+
			"or every client reads as the address of the proxy. "+
			"See Recovery in docs/explanation/security-model.md for the recovery path.",
	).WithFile("ip-allowlists"),
	boolSetting(
		"ip-allowlists-include-bootstrap", "DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP",
		"apply IP allowlists to the bootstrap admin token too (DUSTPAN_IP_ALLOWLISTS_INCLUDE_BOOTSTRAP)",
		"(none: the bootstrap admin token is exempt, so an administrator cannot lock themselves out). "+
			"Set `true` to apply the lists to it as well. See Recovery in docs/explanation/security-model.md for the recovery path.",
	).WithFile("ip-allowlists-include-bootstrap"),
	stringSetting(
		"git-host-allowed-ranges", "DUSTPAN_GIT_HOST_ALLOWED_RANGES", "",
		"comma-separated CIDRs that a connection to a Git host may reach, in addition to public addresses "+
			"(DUSTPAN_GIT_HOST_ALLOWED_RANGES)",
		"(none: a connection reaches public addresses only). Set the range of a GitHub Enterprise Server on a "+
			"private network, for example `10.20.0.0/16`. The server refuses loopback, link-local, private and "+
			"other reserved addresses outside these ranges, when a person sets an address and at each call.",
	).WithFile("git-host-allowed-ranges"),
	stringSetting(
		"log-level", "DUSTPAN_LOG_LEVEL", "info",
		"log level: debug, info, warn or error (DUSTPAN_LOG_LEVEL)",
		"`info` (or `debug`, `warn`, `error`)",
	).WithFile("log-level"),
	stringSetting(
		"log-format", "DUSTPAN_LOG_FORMAT", "json",
		"log format: json or text (DUSTPAN_LOG_FORMAT)",
		"`json` (or `text`)",
	).WithFile("log-format"),
	stringSetting(
		"blob-dir", "DUSTPAN_BLOB_DIR", "",
		"directory for state content (DUSTPAN_BLOB_DIR, default blobs next to the database)",
		"`blobs` next to the SQLite file, or `./blobs` for PostgreSQL",
	).WithFile("blob-dir"),
	stringSetting(
		"blob-store", "DUSTPAN_BLOB_STORE", "fs",
		"blob store fs with --blob-dir, or s3://<bucket>[/<prefix>] (DUSTPAN_BLOB_STORE)",
		"`fs` (the directory in `--blob-dir`), or `s3://<bucket>[/<prefix>]`",
	).WithFile("blob-store").WithURL(),
	intSetting(
		"blob-cache-bytes", "DUSTPAN_BLOB_CACHE_BYTES", "0",
		"bytes of OpenTofu state blobs to keep in memory, 0 means no cache (DUSTPAN_BLOB_CACHE_BYTES)",
		"`0`: no cache. The cache keeps only blobs that are written once: OpenTofu state versions. "+
			"One blob takes at most one eighth of the cache",
	).WithFile("blob-cache-bytes"),
	stringSetting(
		"s3-endpoint", "DUSTPAN_S3_ENDPOINT", "",
		"S3 endpoint URL, empty means AWS (DUSTPAN_S3_ENDPOINT)",
		"(none: AWS)",
	).WithFile("s3-endpoint").WithURL(),
	stringSetting(
		"s3-region", "DUSTPAN_S3_REGION", "",
		"S3 region, empty means us-east-1 (DUSTPAN_S3_REGION)",
		"`us-east-1`",
	).WithFile("s3-region"),
	stringSetting(
		"backup-target", "DUSTPAN_BACKUP_TARGET", "",
		"backup target dir:/path or s3://<bucket>[/<prefix>], empty means no backups (DUSTPAN_BACKUP_TARGET)",
		"(none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]`",
	).WithFile("backup-target").WithURL(),
	stringSetting(
		"key-encryption-key-file", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE", "",
		"file holding the base64 key-encryption key, default dustpan.key"+
			" next to the database (DUSTPAN_KEY_ENCRYPTION_KEY_FILE). An operator who loses the key file "+
			"loses every secret that the key encrypts, and nobody can recover them",
		"`dustpan.key` next to the SQLite file, or `./dustpan.key` for PostgreSQL",
	).WithFile("key-encryption-key-file").
		WithDoc("A file that holds that value. Only its owner can read it."),
	boolSetting(
		"generate-key-encryption-key", "DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY",
		"make the key file with no prompt when no key exists, never overwriting a file that exists "+
			"(DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true). A lost key file means lost secrets",
		"(none: ask on a terminal, else refuse). Set `true` to make the key file with no prompt",
	).WithFile("generate-key-encryption-key").
		WithDoc("Set `true` to make the key file with no prompt when no key exists. " +
			"The server never overwrites a file that exists."),
	intSetting(
		"max-checkpoint-bytes", "DUSTPAN_MAX_CHECKPOINT_BYTES", "0",
		"largest checkpoint in bytes, 0 means the default (DUSTPAN_MAX_CHECKPOINT_BYTES)",
		"`0`: 32 MiB, the largest Pulumi checkpoint",
	).WithFile("max-checkpoint-bytes"),
	intSetting(
		"max-secret-batch-bytes", "DUSTPAN_MAX_SECRET_BATCH_BYTES", "0",
		"largest secrets batch in bytes, 0 means the default (DUSTPAN_MAX_SECRET_BATCH_BYTES)",
		"`0`: 32 MiB, the largest batch of Pulumi secrets",
	).WithFile("max-secret-batch-bytes"),
	intSetting(
		"max-state-bytes", "DUSTPAN_MAX_STATE_BYTES", "0",
		"largest OpenTofu state in bytes, 0 means the default (DUSTPAN_MAX_STATE_BYTES)",
		"`0`: 32 MiB, the largest OpenTofu state",
	).WithFile("max-state-bytes"),
	intSetting(
		"max-body-bytes-in-flight", "DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT", "0",
		"concurrent large bodies in bytes, 0 means the default (DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT)",
		"`0`: 128 MiB of large request bodies of the Pulumi CLI and OpenTofu at the same time. "+
			"The budget covers OpenTofu state uploads as well. An upload whose OpenTofu state has a large outputs "+
			"document asks for more budget as the document grows. One encrypted state with 16 MiB of outputs takes "+
			"about 72 MiB of the budget. Installations with large outputs should raise this value. An upload "+
			"that can never fit answers 413.",
	).WithFile("max-body-bytes-in-flight"),
	stringSetting(
		"soft-delete-retention", "DUSTPAN_SOFT_DELETE_RETENTION", "720h",
		"how long a deleted stack or workspace stays before its final delete, at least 1h "+
			"(DUSTPAN_SOFT_DELETE_RETENTION)",
		"`720h` (30 days, ADR 47). A Go duration of at least `1h`, for example `168h`. A delete records "+
			"the end of its retention, so a change applies to later deletes",
	).WithFile("soft-delete-retention"),
	intSetting(
		"checkpoint-delta-cutoff", "DUSTPAN_CHECKPOINT_DELTA_CUTOFF", "1048576",
		"checkpoint size in bytes from which the CLI sends delta uploads, "+
			"0 turns delta uploads off (DUSTPAN_CHECKPOINT_DELTA_CUTOFF)",
		"`1048576` (1 MiB). `0` turns delta checkpoint uploads off",
	).WithFile("checkpoint-delta-cutoff"),
	boolSetting(
		"disable-pulumi-journaling", "DUSTPAN_DISABLE_PULUMI_JOURNALING",
		"answer journalVersion 0 at the start of a Pulumi update, so the CLI saves checkpoints "+
			"(DUSTPAN_DISABLE_PULUMI_JOURNALING=true)",
		"(none: journaling on). Set `true` to make the Pulumi CLI save checkpoints",
	).WithFile("disable-pulumi-journaling"),
	boolSetting(
		"disable-host-recovery", "DUSTPAN_DISABLE_HOST_RECOVERY",
		"turn the host command off, so it makes no temporary administrator (DUSTPAN_DISABLE_HOST_RECOVERY=true)",
		"(none: the host command is on). Set `true` or `1` in the environment of the operator account to turn "+
			"it off. `false`, `0` and an empty value keep it on. The host command refuses any other value",
	),
	stringSetting(
		"recovery-mode", "DUSTPAN_RECOVERY_MODE", "bootstrap",
		"break-glass recovery mode of the installation: bootstrap, signed-request or none (DUSTPAN_RECOVERY_MODE)",
		"`bootstrap`: recovery through the bootstrap administrator or the host command. `signed-request`: "+
			"recovery with a request signed with an offline operator key. `none`: no break-glass, the operator "+
			"restores from a backup, and the host command refuses to run",
	),
	stringSetting(
		"reason", "", "",
		"reason for the temporary administrator, at most 500 characters, stored in the audit entry of the create",
		referenceNone,
	),
	stringSetting(
		"audit-chain", "DUSTPAN_AUDIT_CHAIN", "on",
		"hash chain of the audit log: on or off. A change starts the server in maintenance mode "+
			"until a person approves it (DUSTPAN_AUDIT_CHAIN)",
		"`on`. `off` turns the chain off. A change starts the server in maintenance mode until a person "+
			"runs `opendustpan audit-chain approve`",
	).WithFile("audit-chain"),
	boolSetting(
		"audit-checkpoints", "DUSTPAN_AUDIT_CHECKPOINTS",
		"sign a checkpoint of the audit chain of each scope every hour and store it in the blob store; "+
			"needs --audit-chain=on and --audit-signing-key-file (DUSTPAN_AUDIT_CHECKPOINTS=true)",
		"(none: off). Set `true` to sign checkpoints. Needs `--audit-chain=on` and a signing key file",
	).WithFile("audit-checkpoints"),
	stringSetting(
		"audit-signing-key-file", "DUSTPAN_AUDIT_SIGNING_KEY_FILE", "",
		"file holding the base64 32-byte Ed25519 seed that signs audit checkpoints "+
			"(DUSTPAN_AUDIT_SIGNING_KEY_FILE)",
		referenceNone,
	).WithFile("audit-signing-key-file").WithDoc("A file that holds the base64 32-byte Ed25519 seed. Only its owner can read it."),
	stringSetting(
		"log-sink", "DUSTPAN_LOG_SINK", "",
		"a target of the application log instead of stderr; repeat the flag for more targets. Each is "+
			"[<name>=]stderr, stdout, file:<path>, syslog+udp://, syslog+tcp:// or syslog+tls://<host>:<port>, "+
			"or webhook:<https URL> (DUSTPAN_LOG_SINK, targets separated by white space)",
		"(none: stderr). For example `stderr syslog+tls://logs.example:6514`",
	).WithFile("log-sink").WithSensitive(),
	stringSetting(
		"audit-sink", "DUSTPAN_AUDIT_SINK", "",
		"a sink that gets a copy of each audit entry; repeat the flag for more sinks. Each is "+
			"[<name>=]file:<path>, syslog+udp://, syslog+tcp:// or syslog+tls://<host>:<port>, "+
			"webhook:<https URL> or blob:<prefix> (DUSTPAN_AUDIT_SINK, sinks separated by white space)",
		"(none: no sink). For example `siem=syslog+tls://logs.example:6514 blob:audit`",
	).WithFile("audit-sink").WithSensitive(),
	stringSetting(
		"audit-verify-keys-file", "DUSTPAN_AUDIT_VERIFY_KEYS_FILE", "",
		"file of base64 Ed25519 public keys, one on each line, that may verify audit checkpoints "+
			"but never sign them, for example the key before a key change (DUSTPAN_AUDIT_VERIFY_KEYS_FILE)",
		referenceNone,
	).WithFile("audit-verify-keys-file"),
	stringSetting(
		"oidc-issuer", "DUSTPAN_OIDC_ISSUER", "",
		"issuer URL of the OpenID Connect provider of the console sign-in, with https; the sign-in also needs "+
			"--oidc-provider, --oidc-client-id, the client secret and --public-url. To use another issuer, "+
			"also set a new --oidc-provider (DUSTPAN_OIDC_ISSUER)",
		"(none: no console sign-in). The identity of a person does not hold the issuer. To use another "+
			"issuer, also set a new `--oidc-provider`. Else a person of the new issuer gets the principal of a "+
			"person of the old issuer with the same `sub`.",
	).WithFile("oidc-issuer").WithURL().WithSensitive(),
	stringSetting(
		"oidc-provider", "DUSTPAN_OIDC_PROVIDER", "",
		"ID of the OpenID Connect provider: 1 to 32 lower-case letters, digits and hyphens. It is part of the "+
			"identity of each person who signs in: the subject <provider ID>:<sub>. Never change the ID after "+
			"the first sign-in, and never use it for another issuer (DUSTPAN_OIDC_PROVIDER)",
		"(none). The ID is part of the identity of each person who signs in: the subject "+
			"`<provider ID>:<sub>`. A principal with that subject, a local one too, is the principal of that "+
			"person. Never change the ID after the first sign-in. A new issuer needs a new ID.",
	).WithFile("oidc-provider"),
	stringSetting(
		"oidc-display-name", "DUSTPAN_OIDC_DISPLAY_NAME", "",
		"name of the OpenID Connect provider that the console shows (DUSTPAN_OIDC_DISPLAY_NAME)",
		"(the provider ID)",
	).WithFile("oidc-display-name"),
	stringSetting(
		"oidc-client-id", "DUSTPAN_OIDC_CLIENT_ID", "",
		"client ID of the server at the OpenID Connect provider (DUSTPAN_OIDC_CLIENT_ID)",
		referenceNone,
	).WithFile("oidc-client-id"),
	stringSetting(
		"oidc-scopes", "DUSTPAN_OIDC_SCOPES", "profile,email",
		"comma-separated scopes to ask in addition to openid (DUSTPAN_OIDC_SCOPES)",
		"`profile,email`",
	).WithFile("oidc-scopes"),
	boolSetting(
		"oidc-no-auto-create", "DUSTPAN_OIDC_NO_AUTO_CREATE",
		"refuse the sign-in of a person who has no principal yet, instead of making one with no grant "+
			"(DUSTPAN_OIDC_NO_AUTO_CREATE)",
		"false",
	).WithFile("oidc-no-auto-create"),
	stringSetting(
		"server", "DUSTPAN_SERVER", "",
		"base URL of the server, for example https://dustpan.example (DUSTPAN_SERVER)",
		referenceNone,
	).WithURL(),
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
		unsafeAllowDevBackupsFlag, "",
		"let a production installation restore a backup of a dev installation; the restore deletes the dev "+
			"users and their data. We do not recommend this. Only this flag sets it: no variable, no "+
			"configuration file key and no API setting",
		"false. Only the flag sets it. No variable and no configuration file key sets it. "+
			"The server writes one warning to the log at each start with the flag. Without it, a production "+
			"installation refuses to restore a backup of a dev installation. A restore of any backup "+
			"deletes the dev users and their data from the staged data",
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
	).WithSensitive(),
	envSetting(
		"DUSTPAN_ADMIN_TOKEN_FILE",
		"A file that holds the tokens, one per line or comma-separated. Only its owner can read it. "+
			"When you set both, `DUSTPAN_ADMIN_TOKEN` wins.",
	).WithFile("admin-token-file"),
	envSetting(
		"DUSTPAN_OIDC_CLIENT_SECRET",
		"The client secret of the server at the OpenID Connect provider. Required when you set "+
			"`--oidc-issuer` and no secret file.",
	).WithSensitive(),
	envSetting(
		"DUSTPAN_OIDC_CLIENT_SECRET_FILE",
		"A file that holds the OpenID Connect client secret. Only its owner can read it. "+
			"When you set both, `DUSTPAN_OIDC_CLIENT_SECRET` wins.",
	),
	envSetting(
		"DUSTPAN_AUDIT_WEBHOOK_SECRET",
		"The key of the HMAC-SHA256 signature of each request of a `webhook:` audit sink. "+
			"Required when `--audit-sink` names a webhook and you set no secret file.",
	).WithSensitive(),
	envSetting(
		"DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE",
		"A file that holds the webhook secret. Only its owner can read it. "+
			"When you set both, `DUSTPAN_AUDIT_WEBHOOK_SECRET` wins.",
	),
	envSetting(
		"DUSTPAN_LOG_WEBHOOK_SECRET",
		"The key of the HMAC-SHA256 signature of each request of a `webhook:` log sink. "+
			"Required when `--log-sink` names a webhook and you set no secret file.",
	).WithSensitive(),
	envSetting(
		"DUSTPAN_LOG_WEBHOOK_SECRET_FILE",
		"A file that holds the log webhook secret. Only its owner can read it. "+
			"When you set both, `DUSTPAN_LOG_WEBHOOK_SECRET` wins.",
	),
	envSetting(
		"DUSTPAN_KEY_ENCRYPTION_KEY",
		"The base64 text of 32 random bytes. It can stay unset while the database holds no secrets.",
	).WithSensitive(),
	envSetting(
		DevDataDirEnv,
		"The parent of the data directory of `dev`. The data directory is `dev` in it. `--ephemeral` "+
			"ignores it, and `--data-dir` wins over it.",
	),
	envSetting(
		DevAdminTokenEnv,
		"The administrator token of `dev`, in place of a token that `dev` makes. At least 32 bytes. "+
			"`dev` clears the variable from its environment after the read, and `dev.env` takes "+
			"the token from the variable in the shell that loads it.",
	).WithSensitive(),

	stringSetting(
		history.PlanRetentionFlag, history.PlanRetentionEnv, retentionDefault,
		"how long a stored plan stays ("+history.PlanRetentionEnv+")",
		"`"+retentionDefault+"` (400 days). A Go duration",
	),
	stringSetting(
		history.JobLogRetentionFlag, history.JobLogRetentionEnv, retentionDefault,
		"how long a version of a job log stays ("+history.JobLogRetentionEnv+")",
		"`"+retentionDefault+"` (400 days). A Go duration",
	),
	stepInput("DUSTPAN_JOB_ID", "The ID of the job that the step belongs to."),
	stepInput("DUSTPAN_PHASE", "The name of the phase of the job that the step belongs to."),
	stepInput("DUSTPAN_STEP", "The name of the step."),
	stepInput("DUSTPAN_UNIT", "The unit that the job runs on."),
	stepInput("DUSTPAN_NAMESPACE", "The namespace of the unit."),
	stepInput("DUSTPAN_COMMIT", "The commit of the source that the job runs."),
	stepInput("DUSTPAN_SERVER_URL", "The URL of the server that the step calls."),
	stepInput(
		"DUSTPAN_SWEEP_REPORT",
		"Set for the command of a cloud sweep only: the path of the JSON Lines file that the command writes "+
			"its report to. The launcher uploads the file when the command ends.",
	),
}

// stepInput declares a variable that the launcher sets for the process of a
// step: the variable and its reference description. The server does not read
// it, so it has no flag, and it holds no credential.
func stepInput(env, doc string) *Setting {
	return envSetting(env, doc).WithStepInput()
}
