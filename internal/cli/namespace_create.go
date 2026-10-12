package cli

// NamespaceCreateCommand answers the namespace create row of the command
// table with run: a client of POST /api/server/namespaces.
func NamespaceCreateCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandNamespace, commandCreate},
		Synopsis: "Create a namespace on the server.",
		Settings: SettingsFor("server", "ca-file"),
		Run:      run,
	}
}
