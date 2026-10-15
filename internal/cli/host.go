package cli

// HostCreateTempAdminCommand answers the host row of the command table with
// run: a direct store command, not a client of the REST API. It reaches the
// database without the public listener, the identity provider or DNS, so an
// operator with host access recovers after every administrator is lost. The
// person comes from the command line, never from a configuration file, and
// no token is read from one either: the command mints the credential.
func HostCreateTempAdminCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandHost, commandCreateTempAdmin},
		Synopsis: "Create a temporary administrator for one named person.",
		Settings: SettingsFor("database"),
		Run:      run,
	}
}
