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
		Flag:                 flag,
		Env:                  env,
		Default:              def,
		Help:                 help,
		Reference:            reference,
		Doc:                  "",
		File:                 "",
		Kind:                 KindString,
		Sensitive:            false,
		URL:                  false,
		StaticCredentialHost: false,
		StepInput:            false,

		NoPasswordInFile: false,
	}
}

// boolSetting declares a bool setting: the flag, the variable, the --help
// text and the reference default. The default is always false: no opt-in
// without an explicit flag or variable.
func boolSetting(flag, env, help, reference string) *Setting {
	return &Setting{
		Flag:                 flag,
		Env:                  env,
		Default:              defaultFalse,
		Help:                 help,
		Reference:            reference,
		Doc:                  "",
		File:                 "",
		Kind:                 KindBool,
		Sensitive:            false,
		URL:                  false,
		StaticCredentialHost: false,
		StepInput:            false,

		NoPasswordInFile: false,
	}
}

// intSetting declares an int64 setting in decimal: the flag, the variable,
// the default, the --help text and the reference default.
func intSetting(flag, env, def, help, reference string) *Setting {
	return &Setting{
		Flag:                 flag,
		Env:                  env,
		Default:              def,
		Help:                 help,
		Reference:            reference,
		Doc:                  "",
		File:                 "",
		Kind:                 KindInt,
		Sensitive:            false,
		URL:                  false,
		StaticCredentialHost: false,
		StepInput:            false,

		NoPasswordInFile: false,
	}
}

// envSetting declares a setting with no flag: the variable and its
// reference description. Flags show in process listings, so tokens and keys
// never get one.
func envSetting(env, doc string) *Setting {
	return &Setting{
		Flag:                 "",
		Env:                  env,
		Default:              "",
		Help:                 "",
		Reference:            "",
		Doc:                  doc,
		File:                 "",
		Kind:                 KindString,
		Sensitive:            false,
		URL:                  false,
		StaticCredentialHost: false,
		StepInput:            false,

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
		"database",
		"DUSTPAN_DATABASE",
		"",
		"sqlite:<path> or postgres://..., required (DUSTPAN_DATABASE)",
		"(none: `serve` stops without it). `sqlite:<path>` or `postgres://...` for PostgreSQL 18 or later. The server checks the failover settings of the URL, for example `target_session_attrs` and `pool_max_conn_lifetime`. It sets a default for each one that the URL does not name: see [Database settings for a failover](../guides/several-instances.md#database-settings-for-a-failover)",
	).WithFile("database").WithStaticCredentialHost().WithSensitive().WithNoPasswordInFile(),
	stringSetting(
		"installation", "DUSTPAN_INSTALLATION", "local",
		"name of this installation, the primary installation of the namespaces it creates (DUSTPAN_INSTALLATION)",
		"`local`",
	).WithFile("installation"),
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
			"optional port, in lower case with an ASCII (punycode) host. The host is an IP address or a DNS name of "+
			"letters, digits and hyphens. The port is a number from 1 to 65535. It has no default port, user "+
			"information, path, query or fragment. The server refuses `null` and `*`. A listed origin gets CORS headers on `/api/`, with "+
			"`Access-Control-Expose-Headers: Retry-After, ETag, Idempotent-Replayed` on answers to requests.",
	).WithFile("allowed-origins"),
	stringSetting(
		"console-content-type-options",
		"DUSTPAN_CONSOLE_CONTENT_TYPE_OPTIONS",
		"",
		"X-Content-Type-Options header of the console files: a value, or off (see the reference) (DUSTPAN_CONSOLE_CONTENT_TYPE_OPTIONS)",
		"(none: the server sends `nosniff`). A value is sent as given. `off` (in any case) sends no header. It applies to the console files only.",
	).WithFile("console-content-type-options"),
	stringSetting(
		"console-frame-options",
		"DUSTPAN_CONSOLE_FRAME_OPTIONS",
		"",
		"X-Frame-Options header of the console files: a value, or off (see the reference) (DUSTPAN_CONSOLE_FRAME_OPTIONS)",
		"(none: the server sends `DENY`). A value is sent as given. `off` (in any case) sends no header. It applies to the console files only.",
	).WithFile("console-frame-options"),
	stringSetting(
		"console-csp",
		"DUSTPAN_CONSOLE_CSP",
		"",
		"Content-Security-Policy header of the console files: a value, or off (see the reference) (DUSTPAN_CONSOLE_CSP)",
		"(none: the server sends `default-src 'self'; connect-src 'self' <each allowed origin>; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`). `connect-src` follows `--allowed-origins`: a console that calls another API origin needs that origin listed there. A value is sent as given. `off` (in any case) sends no header. It applies to the console files only.",
	).WithFile("console-csp"),
	stringSetting(
		"console-hsts",
		"DUSTPAN_CONSOLE_HSTS",
		"",
		"Strict-Transport-Security header of the console files: a value, or off (see the reference) (DUSTPAN_CONSOLE_HSTS)",
		"(none: the server sends `max-age=31536000` when it serves TLS, and nothing without TLS). A value is sent as given. `off` (in any case) sends no header. The server never sends it without `--tls-cert`. It applies to the console files only.",
	).WithFile("console-hsts"),
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
		"issuer-base-url", "DUSTPAN_ISSUER_BASE_URL", "",
		"host of the OIDC issuer URL of each namespace, for example https://issuer.example.com; "+
			"empty means --public-url (DUSTPAN_ISSUER_BASE_URL)",
		"(none: the issuer uses `--public-url`. With neither setting, the issuer is off)",
	).WithFile("issuer-base-url").WithURL(),
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
		"(none: off). Set `true` to turn the feature on. A namespace opts in with its own list. "+
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
			"private network, for example `10.20.0.0/16`. A range opens private-network addresses only. "+
			"These are RFC 1918 and unique local addresses. An IPv4 range has at least 8 bits, and an IPv6 range "+
			"has at least 32 bits. The server always refuses loopback, link-local, cloud metadata, unspecified and "+
			"multicast addresses, also inside a range. It checks this when a person sets an address and at each call.",
	).WithFile("git-host-allowed-ranges"),
	boolSetting(
		"allow-plain-connections", "DUSTPAN_ALLOW_PLAIN_CONNECTIONS",
		"allow LDAP and SMTP connections with no TLS, except to loopback (DUSTPAN_ALLOW_PLAIN_CONNECTIONS)",
		"(none: off). Set `true` only in a trusted network. An LDAP bind or an SMTP login then sends its "+
			"password with no encryption. The settings resource also holds it, and a value from a flag, a "+
			"variable or this file locks it",
	).WithFile("egress.allowPlainConnections"),
	stringSetting(
		"branding-preset", "DUSTPAN_BRANDING_PRESET", "",
		"name of the branding preset of the installation, for example high-contrast (DUSTPAN_BRANDING_PRESET)",
		"(none: the console shows its standard palette). A name of lower-case letters, digits and hyphens, "+
			"at most 32 characters. The settings resource also holds it, and a value from a flag, a variable or "+
			"this file locks it",
	).WithFile("branding.preset"),
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
	).WithFile("blob-store").WithStaticCredentialHost().WithURL(),
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
	).WithFile("s3-endpoint").WithStaticCredentialHost().WithURL(),
	stringSetting(
		"s3-region", "DUSTPAN_S3_REGION", "",
		"S3 region, empty means us-east-1 (DUSTPAN_S3_REGION)",
		"`us-east-1`",
	).WithFile("s3-region"),
	stringSetting(
		"backup-target", "DUSTPAN_BACKUP_TARGET", "",
		"backup target dir:/path or s3://<bucket>[/<prefix>], empty means no backups (DUSTPAN_BACKUP_TARGET)",
		"(none: no backups), or `dir:/path` or `s3://<bucket>[/<prefix>]`",
	).WithFile("backup-target").WithStaticCredentialHost().WithURL(),
	stringSetting(
		"key-encryption-key-file", "DUSTPAN_KEY_ENCRYPTION_KEY_FILE", "",
		"file holding the base64 key-encryption key, required without DUSTPAN_KEY_ENCRYPTION_KEY"+
			" (DUSTPAN_KEY_ENCRYPTION_KEY_FILE). To make a key file, run: opendustpan key generate --out <path>. "+
			"An operator who loses the key file "+
			"loses every secret that the key encrypts, and nobody can recover them",
		"(none: `serve` stops without a key). Make a file with `opendustpan key generate --out <path>`",
	).WithFile("key-encryption-key-file").
		WithDoc("A file that holds that value. Only its owner can read it."),
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
		"`720h` (30 days). A Go duration of at least `1h`, for example `168h`. A delete records "+
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
		"out", "", "",
		"path of the new key file, required (key generate). The command refuses a file that exists",
		referenceNone,
	),
	stringSetting(
		"reason", "", "",
		"reason for the temporary administrator, at most 500 characters, stored in the audit entry of the create",
		referenceNone,
	),
	stringSetting(
		"audit-chain", "DUSTPAN_AUDIT_CHAIN", "on",
		"hash chain of the audit log: on or off. A change starts the server in maintenance mode "+
			"until a person approves it in the console (DUSTPAN_AUDIT_CHAIN)",
		"`on`. `off` turns the chain off. A change starts the server in maintenance mode until a person "+
			"approves it in the console",
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
	).WithFile("log-sink").WithStaticCredentialHost().WithSensitive(),
	stringSetting(
		"audit-sink", "DUSTPAN_AUDIT_SINK", "",
		"a sink that gets a copy of each audit entry; repeat the flag for more sinks. Each is "+
			"[<name>=]file:<path>, syslog+udp://, syslog+tcp:// or syslog+tls://<host>:<port>, "+
			"webhook:<https URL>, otlp:<https URL> or blob:<prefix> (DUSTPAN_AUDIT_SINK, sinks separated by white space)",
		"(none: no sink). For example `siem=syslog+tls://logs.example:6514 blob:audit`",
	).WithFile("audit-sink").WithStaticCredentialHost().WithSensitive(),
	stringSetting(
		"audit-verify-keys-file", "DUSTPAN_AUDIT_VERIFY_KEYS_FILE", "",
		"file of base64 Ed25519 public keys, one on each line, that may verify audit checkpoints "+
			"but never sign them, for example the key before a key change (DUSTPAN_AUDIT_VERIFY_KEYS_FILE)",
		referenceNone,
	).WithFile("audit-verify-keys-file"),
	stringSetting(
		"destination-ports", "DUSTPAN_DESTINATION_PORTS", "",
		"comma-separated ports, besides 443, that a notification destination may use; a webhook or chat "+
			"destination on any other port is refused when a person saves it and again before each send "+
			"(DUSTPAN_DESTINATION_PORTS)",
		"(none: port 443 only). For example `8443,9443`",
	).WithFile("destination-ports"),
	stringSetting(
		"oidc-issuer", "DUSTPAN_OIDC_ISSUER", "",
		"issuer URL of the OpenID Connect provider of the console sign-in, with https; the sign-in also needs "+
			"--oidc-provider, --oidc-client-id, the client secret and --public-url. To use another issuer, "+
			"also set a new --oidc-provider (DUSTPAN_OIDC_ISSUER)",
		"(none: no console sign-in). The identity of a person does not hold the issuer. To use another "+
			"issuer, also set a new `--oidc-provider`. Else a person of the new issuer gets the principal of a "+
			"person of the old issuer with the same `sub`.",
	).WithFile("oidc-issuer").WithStaticCredentialHost().WithURL().WithSensitive(),
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
	stringSetting(
		"target", "", "",
		"store to check: blobs for the live blob store, backup for the backup target. The server reads its own "+
			"store; the command never takes an address",
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
		"DUSTPAN_AUDIT_OTLP_HEADERS",
		"Extra headers of each request of an `otlp:` audit sink, as `Name=Value` items separated by "+
			"commas or line breaks. Percent-encode a value. Optional.",
	).WithSensitive(),
	envSetting(
		"DUSTPAN_AUDIT_OTLP_HEADERS_FILE",
		"A file that holds those headers. Only its owner can read it. "+
			"When you set both, `DUSTPAN_AUDIT_OTLP_HEADERS` wins.",
	),
	envSetting(
		"DUSTPAN_LOG_OTLP_HEADERS",
		"Extra headers of each request of an `otlp:` log sink, in the same form. Optional.",
	).WithSensitive(),
	envSetting(
		"DUSTPAN_LOG_OTLP_HEADERS_FILE",
		"A file that holds those headers. Only its owner can read it. "+
			"When you set both, `DUSTPAN_LOG_OTLP_HEADERS` wins.",
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
		"how long a stored plan stays, from 24h to 87600h ("+history.PlanRetentionEnv+")",
		"`"+retentionDefault+"` (400 days). A Go duration from `24h` to `87600h`. Each plan records its end of "+
			"retention when it is made, so a change applies to later plans",
	).WithFile(history.PlanRetentionFlag),
	stringSetting(
		history.JobLogRetentionFlag, history.JobLogRetentionEnv, retentionDefault,
		"how long a version of a job log stays, from 24h to 87600h ("+history.JobLogRetentionEnv+")",
		"`"+retentionDefault+"` (400 days). A Go duration from `24h` to `87600h`. Each log version records its "+
			"end of retention when it is made, so a change applies to later versions",
	).WithFile(history.JobLogRetentionFlag),
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
	stepInput(
		"DUSTPAN_HELPER_SOCKET",
		"Set for a tool step only: the path of the Unix socket on which the launcher answers its helpers "+
			"for OpenTofu and git. The variable holds a path.",
	),
}

// stepInput declares a variable that the launcher sets for the process of a
// step: the variable and its reference description. The server does not read
// it, so it has no flag, and it holds no credential.
func stepInput(env, doc string) *Setting {
	return envSetting(env, doc).WithStepInput()
}
