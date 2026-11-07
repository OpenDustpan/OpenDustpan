package cli

// PlacementAddCommand answers the placement add row of the command table with
// run: a client of POST /api/dustpan/v1/namespaces/{namespace}/placement-rules.
func PlacementAddCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandPlacement, commandAdd},
		Synopsis: "Add a placement rule to a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// PlacementListCommand answers the placement list row of the command table
// with run: a client of GET /api/dustpan/v1/namespaces/{namespace}/placement-rules.
func PlacementListCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandPlacement, commandList},
		Synopsis: "List the placement rules of a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// PlacementDeleteCommand answers the placement delete row of the command table
// with run: a client of DELETE on one placement rule of a namespace.
func PlacementDeleteCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandPlacement, commandDelete},
		Synopsis: "Delete a placement rule of a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
