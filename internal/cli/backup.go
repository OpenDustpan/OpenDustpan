package cli

// BackupCommand answers the backup row of the command table with run: a
// client of POST /api/dustpan/v1/backups that starts a backup and waits for it.
func BackupCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandBackup},
		Synopsis: "Back up the installation through the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
