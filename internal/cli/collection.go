package cli

// CollectionCreateCommand answers the collection create row of the command
// table with run: a client of POST /api/dustpan/v1/namespaces/{namespace}/collections.
func CollectionCreateCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandCollection, commandCreate},
		Synopsis: "Create a collection in a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}

// CollectionListCommand answers the collection list row of the command table
// with run: a client of GET /api/dustpan/v1/namespaces/{namespace}/collections.
func CollectionListCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandCollection, commandList},
		Synopsis: "List the collections of a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
