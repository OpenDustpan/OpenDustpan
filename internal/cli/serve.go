package cli

// ServeSynopsis is the one-line synopsis of the serve command.
const ServeSynopsis = "Run the server with the built-in adapters."

// ServeFlags holds the flag names of the serve command in reference order:
// the configuration file, then the database, installation, network, storage, key
// and limit settings.
func ServeFlags() []*Setting {
	return SettingsFor(append([]string{
		"config",
		"database",
		"installation",
		"components",
		"listen",
		"console-dir",
		"allowed-origins",
		"console-content-type-options",
		"console-frame-options",
		"console-csp",
		"console-hsts",
		"tls-cert",
		"tls-key",
		"public-url",
		"issuer-base-url",
		"trusted-proxies",
		"trusted-proxy-header",
		"ip-allowlists",
		"ip-allowlists-include-bootstrap",
		"git-host-allowed-ranges",
		"allow-plain-connections",
		"branding-preset",
		"log-level",
		"log-format",
		"log-sink",
		"blob-dir",
		"blob-store",
		"blob-cache-bytes",
		"s3-endpoint",
		"s3-region",
		"backup-target",
		UnsafeAllowDevBackupsFlag,
		"key-encryption-key-file",
		"max-checkpoint-bytes",
		"checkpoint-delta-cutoff",
		"disable-pulumi-journaling",
		"max-secret-batch-bytes",
		"max-state-bytes",
		"max-body-bytes-in-flight",
		"audit-chain",
		"audit-checkpoints",
		"audit-signing-key-file",
		"audit-verify-keys-file",
		"soft-delete-retention",
		"plan-retention",
		"job-log-retention",
		"audit-sink",
		"audit-sink-allowed-ranges",
		"destination-ports",
		"oidc-issuer",
		"oidc-provider",
		"oidc-display-name",
		"oidc-client-id",
		"oidc-scopes",
		"oidc-no-auto-create",
	}, ldapServeFlags()...)...)
}

// ldapServeFlags answers the flags of the static LDAP sign-in provider, in
// reference order. They close the list of the serve flags.
func ldapServeFlags() []string {
	return []string{
		"ldap-url",
		"ldap-provider",
		"ldap-display-name",
		"ldap-start-tls",
		"ldap-ca-file",
		"ldap-allowed-ranges",
		"ldap-bind-dn",
		"ldap-base-dn",
		"ldap-user-filter",
		"ldap-user-name-attribute",
		"ldap-email-attribute",
		"ldap-name-attribute",
		"ldap-subject",
		"ldap-disabled-attribute",
		"ldap-disabled-mask",
		"ldap-disabled-values",
		"ldap-groups",
		"ldap-group-base-dn",
		"ldap-group-filter",
		"ldap-group-member-attribute",
		"ldap-group-member-of-attribute",
		"ldap-group-name-attribute",
		"ldap-no-auto-create",
	}
}

// ServeCommand answers the serve row of the command table with run: serve
// opens the stores, so only cmd/opendustpan passes its run function.
func ServeCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandServe},
		Synopsis: ServeSynopsis,
		Settings: ServeFlags(),
		Run:      run,
	}
}

// ServeSettings answers every setting that serve reads, in log order: the
// serve flags, then the settings that only an environment variable or the
// configuration file sets. Serve logs each of them at start (ADR 63
// decision 7).
func ServeSettings() []*Setting {
	return append(ServeFlags(),
		mustEnv("DUSTPAN_ADMIN_TOKEN"),
		mustEnv("DUSTPAN_ADMIN_TOKEN_FILE"),
		mustEnv("DUSTPAN_KEY_ENCRYPTION_KEY"),
	)
}
