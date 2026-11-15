package cli

// UpgradeCheckCommand answers the upgrade check row of the command table
// with run: a client of GET /api/dustpan/v1/upgrade-check.
func UpgradeCheckCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandUpgrade, commandCheck},
		Synopsis: "Ask the server for its version and print the upgrade verdict.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
