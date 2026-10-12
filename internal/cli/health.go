package cli

// HealthCommand answers the health row of the command table with run: a
// client of GET /health/ready on the local server.
func HealthCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandHealth},
		Synopsis: "Probe the local server and print ready on a 2xx answer.",
		Settings: SettingsFor("listen", "tls-cert"),
		Run:      run,
	}
}
