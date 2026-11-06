package cli

// StoreCheckCommand answers the store check row of the command table with
// run: a client of POST /api/dustpan/v1/store-checks and
// GET /api/dustpan/v1/store-checks/{id}. The server reads its own store, so
// the command takes a target name and never an address.
func StoreCheckCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandStore, commandCheck},
		Synopsis: "Ask the server to check its blob store or its backup target.",
		Settings: SettingsFor("server", "ca-file", "target"),
		Run:      run,
	}
}
