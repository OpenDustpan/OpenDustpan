package cli

// BackupCheckCommand answers the backup check row of the command table with
// run: a client of GET /api/dustpan/v1/backups/{id}/check.
func BackupCheckCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandBackup, commandCheck},
		Synopsis: "Ask the server to check a backup in its target.",
		Settings: SettingsFor("server", "ca-file", "backup"),
		Run:      run,
	}
}
