package cli

// ServeSynopsis is the one-line synopsis of the serve command.
const ServeSynopsis = "Run the server with the built-in adapters."

// ServeFlags holds the flag names of the serve command in reference order:
// the configuration file, then the database, region, network, storage, key
// and limit settings.
func ServeFlags() []*Setting {
	return SettingsFor(
		"config",
		"database", "region",
		"listen", "console-dir", "allowed-origins", "tls-cert", "tls-key", "public-url",
		"log-level", "log-format",
		"blob-dir", "blob-store", "s3-endpoint", "s3-region", "backup-target",
		"key-encryption-key-file", "generate-key-encryption-key",
		"max-checkpoint-bytes", "checkpoint-delta-cutoff", "disable-pulumi-journaling",
		"max-secret-batch-bytes", "max-state-bytes", "max-body-bytes-in-flight",
		"soft-delete-retention",
	)
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
