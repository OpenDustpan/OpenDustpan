package cli

// RestoreCommand answers the restore row of the command table with run: a
// client of POST /api/dustpan/v1/restores. The server restores from its own
// backup target into its own stores, so the command takes no --from,
// --database, --blob-dir or --blob-store.
func RestoreCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandRestore},
		Synopsis: "Restore a backup from the server target into the installation.",
		Settings: SettingsFor("server", "ca-file", "backup", "check-only", "force"),
		Run:      run,
	}
}
