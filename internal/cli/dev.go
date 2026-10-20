package cli

import "slices"

// DevSynopsis is the one-line synopsis of the dev command.
const DevSynopsis = "Run a server for a trial or for CI, with no setup."

// DevDataDirEnv names the variable that holds the parent of the data
// directory of dev, for example the volume of the container image. The data
// directory is the subdirectory dev of it. The variable is not the setting of
// a flag: --data-dir names the data directory itself.
const DevDataDirEnv = "DUSTPAN_DATA_DIR"

// DevAdminTokenEnv names the variable that holds the administrator token
// that dev uses in place of a token that it makes.
const DevAdminTokenEnv = "DUSTPAN_DEV_ADMIN_TOKEN" //nolint:gosec // The name of a variable, not a credential.

// devAdminTokenFileEnv names the variable of the file of the administrator
// token of serve. dev never reads it, and clears it at start.
const devAdminTokenFileEnv = "DUSTPAN_ADMIN_TOKEN_FILE" //nolint:gosec // The name of a variable, not a credential.

// devListenDefault is the default listen address of dev: the loopback
// address, so a trial is not open to the network.
const devListenDefault = "127.0.0.1:8080"

// devSettings are the settings of dev. They are not in Settings: dev reads
// its settings from flags only, so a declaration here has no variable, no
// file key and no layer. Each setting that serve also has copies the text of
// the serve declaration.
//
//nolint:gochecknoglobals // A frozen declaration table, as Settings is. Never mutate it.
var devSettings = []*Setting{
	stringSetting(
		"data-dir", "", "",
		"directory for the data of dev, default a directory for each user",
		"(none). The default is a directory for each user. On Linux and other Unix systems, it is "+
			"`$XDG_DATA_HOME/opendustpan/dev`, or `~/.local/share/opendustpan/dev` when `XDG_DATA_HOME` is not set. "+
			"On macOS, it is `~/Library/Application Support/opendustpan/dev`. "+
			"On Windows, it is `%LocalAppData%\\opendustpan\\dev`. "+
			"With `"+DevDataDirEnv+"` set, it is the directory `dev` in the one that the variable names. "+
			"Not with `--ephemeral`",
	),
	boolSetting(
		"ephemeral", "",
		"use a new temporary directory and delete it at a clean stop",
		"(none: the data stays). A stop deletes the data, and with it the state that tracks the cloud "+
			"resources of the trial. Not with `--data-dir`",
	),
	stringSetting(
		"listen", "", devListenDefault,
		"listen address of the server",
		"`"+devListenDefault+"`. Another address adds a line to the banner and to the log. "+
			"With no `--tls`, the line also says that the token and the sign-in code cross the network in plain text",
	),
	boolSetting(
		"tls", "",
		"serve HTTPS with one self-signed certificate, and change no trust store",
		"(none: HTTP). The `cloud` block of OpenTofu needs it. The certificate lives 30 days",
	),
	stringSetting(
		"hostname", "", "",
		"name that the certificate holds, in addition to the local names, can repeat",
		"(none). The certificate holds `localhost`, `127.0.0.1`, `::1`, the host of `--listen` when it is not "+
			"a wildcard, the name of the host and each `--hostname`. Repeat the flag for each name",
	),
	stringSetting(
		"admin-token-file", "", "",
		"file that holds the administrator token, in place of a token that dev makes",
		"(none: dev makes a token and keeps it in the token file). Only its owner can read it",
	),
	DevByFlagFromServe("console-dir"),
	DevByFlagFromServe("log-level"),
	DevByFlagFromServe("log-format"),
	DevByFlagFromServe("disable-pulumi-journaling"),
	DevByFlagFromServe("max-checkpoint-bytes"),
	DevByFlagFromServe("max-secret-batch-bytes"),
	DevByFlagFromServe("max-state-bytes"),
	DevByFlagFromServe("max-body-bytes-in-flight"),
	DevByFlagFromServe("blob-cache-bytes"),
}

// DevByFlagFromServe answers a copy of the serve declaration of the flag
// with no variable and no file key, for the settings that dev shares with
// serve. It panics when serve has no such flag.
func DevByFlagFromServe(name string) *Setting {
	copied := *ByFlag(name)
	copied.Env = ""
	copied.File = ""

	return &copied
}

// DevFlags answers the flags of the dev command in reference order.
func DevFlags() []*Setting {
	return slices.Clone(devSettings)
}

// DevByFlag answers the declaration of the flag of dev. It panics when dev
// has no such flag, as ByFlag does for serve.
func DevByFlag(name string) *Setting {
	for _, s := range devSettings {
		if s.Flag == name {
			return s
		}
	}

	panic("cli: dev flag --" + name + " has no setting declaration")
}

// DevCommand answers the dev row of the command table with run: dev opens
// the stores as serve does, so only cmd/opendustpan passes its run function.
func DevCommand(run RunFunc) Command {
	return Command{
		Path:     []string{commandDev},
		Synopsis: DevSynopsis,
		Settings: DevFlags(),
		Run:      run,
	}
}

// DevEnvs answers the declarations of the two variables that dev reads.
func DevEnvs() []*Setting {
	return []*Setting{ByEnv(DevDataDirEnv), ByEnv(DevAdminTokenEnv)}
}

// DevSecretEnvs answers the variables that dev clears from its process
// environment at start: each variable of a secret setting of serve, and the
// file of the administrator token. An operator shell or a CI job often
// exports them for serve, and dev must never use them.
func DevSecretEnvs() []string {
	var names []string

	for _, s := range Settings {
		if s.Env != "" && s.Sensitive {
			names = append(names, s.Env)
		}
	}

	names = append(names, devAdminTokenFileEnv)
	slices.Sort(names)

	return slices.Compact(names)
}
