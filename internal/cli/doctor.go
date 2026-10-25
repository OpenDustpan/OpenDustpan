package cli

// DoctorSynopsis is the one-line synopsis of the doctor command.
const DoctorSynopsis = "Check the settings and the running server of a production install."

// DoctorCommand answers the doctor row of the command table with run. It
// takes the settings of serve and reads them through the same layers: the
// flag, the environment and the configuration file. It opens no database and
// reads no key file. With --server, it asks the running server through the
// REST API.
func DoctorCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandDoctor},
		Synopsis: DoctorSynopsis,
		Settings: append(ServeFlags(), SettingsFor("server", "ca-file")...),
		Run:      run,
	}
}
