package cli

// ServeSynopsis is the one-line synopsis of the serve command.
const ServeSynopsis = "Run the server with the built-in adapters."

// ServeFlags holds the flag names of the serve command in reference order:
// the database, region, network, storage, key and limit settings.
func ServeFlags() []*Setting {
	return SettingsFor(
		"database", "region",
		"listen", "console-dir", "allowed-origins", "tls-cert", "tls-key", "public-url",
		"log-level", "log-format", "log-sink",
		"blob-dir", "blob-store", "s3-endpoint", "s3-region", "backup-target",
		"key-encryption-key-file", "generate-key-encryption-key",
		"max-checkpoint-bytes", "checkpoint-delta-cutoff", "disable-pulumi-journaling",
		"max-secret-batch-bytes", "max-state-bytes", "max-body-bytes-in-flight",
		"audit-sink",
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
