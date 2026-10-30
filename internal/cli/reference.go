package cli

import (
	"strings"
)

// Reference returns the settings reference: the full text of
// docs/reference/configuration.md generated from Settings. The drift test
// fails when the file and the declarations differ; regenerate it with
// go test ./internal/cli/ -run TestConfigurationReference -update (also in
// make generate).
func Reference() string {
	var out strings.Builder

	writeServeSettings(&out)
	writeConfigFile(&out)
	writeSecrets(&out)
	writeStepInputs(&out)
	writeClients(&out)
	writeDev(&out)
	writeHost(&out)

	return out.String()
}

// writeServeSettings writes the title and the server settings table from
// the serve declarations.
func writeServeSettings(out *strings.Builder) {
	out.WriteString("# Configuration\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan serve` reads each setting from a flag, from an environment\n")
	out.WriteString("variable or from the configuration file. A flag wins over its variable, and\n")
	out.WriteString("a variable wins over the file. A variable that is empty or holds only\n")
	out.WriteString("spaces is not set, so the next layer wins. `opendustpan serve --help` prints\n")
	out.WriteString("the same list.\n")
	out.WriteString("\n")
	out.WriteString("## Server settings\n")
	out.WriteString("\n")
	out.WriteString("| Flag | Environment | File key | Default |\n")
	out.WriteString("| --- | --- | --- | --- |\n")

	for _, s := range ServeFlags() {
		file := "(none)"
		if s.File != "" {
			file = "`" + s.File + "`"
		}

		env := "(none)"
		if s.Env != "" {
			env = "`" + s.Env + "`"
		}

		out.WriteString("| `--" + s.Flag + "` | " + env + " | " + file + " | " + s.Reference + " |\n")
	}

	out.WriteString("\n")
	out.WriteString("With journaling on, the server refuses to complete an update with\n")
	out.WriteString("`succeeded` while the server has not stored a journal batch of the update. The\n")
	out.WriteString("server does not detect a missing last batch: if the last journal batch of an\n")
	out.WriteString("update is never sent, complete does not refuse it. Cancel the update when its\n")
	out.WriteString("batches stop arriving.\n")
	out.WriteString("\n")
}

// ConfigExample is the example configuration file of the reference: the
// JSON shape with # comments, which is valid YAML (ADR 63 decision 2). A
// test decodes it with ParseConfig.
const ConfigExample = `# opendustpan serve --config /etc/opendustpan/config.yaml
{
  # The name of this installation.
  "installation": "eu-west",
  "listen": ":8443",
  "public-url": "https://dustpan.example",
  "tls-cert": "/etc/opendustpan/tls.crt",
  "tls-key": "/etc/opendustpan/tls.key",
  "log-level": "info",
  "log-format": "json",
  # A bool is only true or false.
  "disable-pulumi-journaling": false,
  # An int is a decimal integer. 0 means the built-in limit.
  "max-state-bytes": 0,
  # The file holds the name of the token file, never a token.
  "admin-token-file": "/run/secrets/dustpan-admin-tokens"
}
`

// writeConfigFile writes the configuration file section: the rules of the
// decoder, the start log and the example.
func writeConfigFile(out *strings.Builder) {
	out.WriteString("## Configuration file\n")
	out.WriteString("\n")
	out.WriteString("`--config` (`DUSTPAN_CONFIG`) names one YAML file. Each key in the file is\n")
	out.WriteString("the file key of a setting in the table above, or `admin-token-file` for\n")
	out.WriteString("`DUSTPAN_ADMIN_TOKEN_FILE`. A secret is never in the file: the file can\n")
	out.WriteString("only name the file that holds it.\n")
	out.WriteString("\n")
	out.WriteString("The server refuses a file that:\n")
	out.WriteString("\n")
	out.WriteString("- is larger than 64 KiB,\n")
	out.WriteString("- has an anchor or an alias,\n")
	out.WriteString("- has a key two times,\n")
	out.WriteString("- has a key that no setting declares,\n")
	out.WriteString("- has a value of the wrong type.\n")
	out.WriteString("\n")
	out.WriteString("A bool is only `true` or `false`. A string setting takes only a string, so\n")
	out.WriteString("`no` stays the string `no`. An int setting takes only a decimal integer of\n")
	out.WriteString("0 or more. A refused file stops the start. The error names the key and the\n")
	out.WriteString("rule, never the value.\n")
	out.WriteString("\n")
	writeConfigValues(out)

	out.WriteString("At start, the server writes one INFO line for each setting, with its key,\n")
	out.WriteString("its value and its layer: `flag`, `env`, `file` or `default`. When more than\n")
	out.WriteString("one of `flag`, `env` and `file` sets a key, the server writes one WARN line\n")
	out.WriteString("for that key, also when the values are equal. For a sensitive setting, a\n")
	out.WriteString("line shows only `set` or `not set`, never the value. The sensitive settings\n")
	out.WriteString("are the database URL and the secrets. Each other URL setting refuses a URL\n")
	out.WriteString("with a user name or a password, so the log can show its value.\n")
	out.WriteString("\n")
	out.WriteString("At the log level `warn` or `error`, the server does not write these INFO\n")
	out.WriteString("lines: the level that the operator sets wins. The start audit entry (see\n")
	out.WriteString("\"Reload\") records the whole configuration at each level.\n")
	out.WriteString("\n")
	writeConfigReload(out)
	writeConfigExample(out)
}

// writeConfigReload writes the reload part of the configuration file
// section (ADR 63 decisions 5, 9 and 10).
func writeConfigReload(out *strings.Builder) {
	out.WriteString("### Reload\n")
	out.WriteString("\n")
	out.WriteString("The server reloads the file on SIGHUP, and when its content changes. It\n")
	out.WriteString("reads the file every 10 seconds and compares the SHA-256 of the content.\n")
	out.WriteString("A flag and a variable change only at a restart. A reload checks the whole\n")
	out.WriteString("file with the rules of the start. When the file fails a rule, the server\n")
	out.WriteString("keeps the old configuration and writes one ERROR line that names the key\n")
	out.WriteString("and the rule. The server writes one INFO line for each changed value, and\n")
	out.WriteString("one audit entry with the channel `config-file` for the reload, with each\n")
	out.WriteString(
		"changed key. A changed setting takes effect at the next start. The metrics `dustpan_config_reloads_total` and\n",
	)
	out.WriteString("`dustpan_config_last_reload_success` count the reloads.\n")
	out.WriteString("\n")
	out.WriteString("A ConfigMap volume mounted with `subPath` never updates. Mount the whole\n")
	out.WriteString("volume, so the poll sees the change.\n")
	out.WriteString("\n")
	out.WriteString("At each start, the server also writes one audit entry with the channel\n")
	out.WriteString("`startup` and each setting, with the rule for sensitive settings. The\n")
	out.WriteString("[audit guide](../guides/audit-log.md) shows these entries.\n")
	out.WriteString("\n")
}

// writeConfigExample writes the example configuration file in the JSON
// shape with # comments.
func writeConfigExample(out *strings.Builder) {
	out.WriteString("This example uses the JSON shape with `#` comments. That text is valid\n")
	out.WriteString("YAML.\n")
	out.WriteString("\n")
	out.WriteString("```yaml\n")
	out.WriteString(ConfigExample)
	out.WriteString("```\n")
	out.WriteString("\n")
}

// writeConfigValues writes the rules for quoted strings and for a database
// URL with a password.
func writeConfigValues(out *strings.Builder) {
	out.WriteString("A string value that YAML reads as a number, a bool or a date needs quotes,\n")
	out.WriteString("for example `\"8080\"`, `\"true\"` or `\"2026-10-14\"`. The JSON shape of\n")
	out.WriteString("the example below quotes each string.\n")
	out.WriteString("\n")
	out.WriteString("The file refuses a `database` URL with a password. Set such a URL in\n")
	out.WriteString("`DUSTPAN_DATABASE`, because a secret comes only from a variable or from a\n")
	out.WriteString("file that a `*_FILE` key names. The `--database` flag refuses a URL with a\n")
	out.WriteString("password too: `serve` stops with an error before it opens the database.\n")
	out.WriteString("Use `DUSTPAN_DATABASE` instead.\n")
	out.WriteString("\n")
}

// secretSettings holds the secret rows of the reference in row order: the
// declarations of the values no flag takes.
//
//nolint:gochecknoglobals // The reference keeps one fixed row order; passing it as a parameter adds nothing.
var secretSettings = []*Setting{
	mustEnv("DUSTPAN_ADMIN_TOKEN"),
	mustEnv("DUSTPAN_ADMIN_TOKEN_FILE"),
	mustEnv("DUSTPAN_KEY_ENCRYPTION_KEY"),
	mustEnv("DUSTPAN_KEY_ENCRYPTION_KEY_FILE"),
	mustEnv("DUSTPAN_AUDIT_WEBHOOK_SECRET"),
	mustEnv("DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE"),
	mustEnv("DUSTPAN_LOG_WEBHOOK_SECRET"),
	mustEnv("DUSTPAN_LOG_WEBHOOK_SECRET_FILE"),
}

// mustEnv answers the declaration of the environment variable. It panics
// when no declaration names it: the reference lists only declared secrets,
// so a typo fails fast.
func mustEnv(name string) *Setting {
	setting := ByEnv(name)
	if setting == nil {
		panic("cli: secret " + name + " has no setting declaration")
	}

	return setting
}

// writeSecrets writes the secrets section from the secret declarations:
// values no flag takes, because flags show in process listings.
func writeSecrets(out *strings.Builder) {
	out.WriteString("## Secrets\n")
	out.WriteString("\n")
	out.WriteString("No flag takes these values, because flags show in process lists.\n")
	out.WriteString("\n")
	out.WriteString("| Environment | Value |\n")
	out.WriteString("| --- | --- |\n")
	out.WriteString("| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | The S3 credentials. |\n")

	for _, setting := range secretSettings {
		out.WriteString("| `" + setting.Env + "` | " + setting.Doc + " |\n")
	}

	out.WriteString("\n")
	out.WriteString("Make a key-encryption key with this command:\n")
	out.WriteString("\n")
	out.WriteString("```sh\n")
	out.WriteString("opendustpan key generate --out <path>\n")
	out.WriteString("```\n")
	out.WriteString("\n")
	out.WriteString("`serve` needs a key from the variable, or from the file in\n")
	out.WriteString("`--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`). It has\n")
	out.WriteString("no default key file and makes no key. Without a key, `serve` stops before\n")
	out.WriteString("it opens the database. Each start logs the path of the key file in use,\n")
	out.WriteString("never the key. A key file in the directory of the SQLite file or of the\n")
	out.WriteString("blobs makes `serve` log a warning, because a backup of the data holds that\n")
	out.WriteString("key too. An operator who loses the key file loses every secret that the\n")
	out.WriteString("key encrypts, and nobody can recover them.\n")
	out.WriteString("\n")
}

// writeStepInputs writes the section of the variables that the launcher sets
// for the process of a step, from the declarations that carry the step mark.
func writeStepInputs(out *strings.Builder) {
	out.WriteString("## Variables of a step\n")
	out.WriteString("\n")
	out.WriteString("The launcher sets these variables for the process of each step of a job. The\n")
	out.WriteString("server does not read them, and no flag or file key sets them. A step cannot\n")
	out.WriteString("set a variable with the prefix `DUSTPAN_`: the launcher refuses it.\n")
	out.WriteString("\n")
	out.WriteString("| Environment | Value |\n")
	out.WriteString("| --- | --- |\n")

	for _, setting := range Settings {
		if setting.StepInput {
			out.WriteString("| `" + setting.Env + "` | " + setting.Doc + " |\n")
		}
	}

	out.WriteString("\n")
}

// writeClients writes the client commands section with one flags row per
// command spec, so the reference lists the same flags as the commands take.
func writeClients(out *strings.Builder) {
	out.WriteString("## Client commands\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup\n")
	out.WriteString("check`, `opendustpan upgrade check`, `opendustpan restore` and the\n")
	out.WriteString("`opendustpan audit-chain` commands call a running server. They read the\n")
	out.WriteString("server URL from `--server` or\n")
	out.WriteString("`DUSTPAN_SERVER`, and the token from `DUSTPAN_ADMIN_TOKEN` or\n")
	out.WriteString("`DUSTPAN_ADMIN_TOKEN_FILE`. `--ca-file` adds a PEM bundle of CA certificates\n")
	out.WriteString("to the system store.\n")
	out.WriteString("\n")
	out.WriteString("Each client command takes only the flags in its row:\n")
	out.WriteString("\n")
	out.WriteString("| Command | Flags |\n")
	out.WriteString("| --- | --- |\n")

	for _, row := range clientRows() {
		out.WriteString("| `" + row.command + "` | " + row.flags + " |\n")
	}

	out.WriteString("\n")
	out.WriteString("`opendustpan key generate` calls no server and opens no database. It writes a\n")
	out.WriteString("new key-encryption key file to the path in `--out` and refuses a file that\n")
	out.WriteString("exists.\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan doctor` takes each flag of `serve`, and `--server` and `--ca-file`.\n")
	out.WriteString("It reads them through the same layers, and calls a server only when you set\n")
	out.WriteString("`--server`. The [doctor guide](../guides/doctor.md) lists the checks.\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as\n")
	out.WriteString("`serve` does. The [observe guide](../guides/observe.md) shows the probes.\n")
	out.WriteString("The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of\n")
	out.WriteString("the backup commands. The [audit log guide](../guides/audit-log.md) shows\n")
	out.WriteString("the audit chain commands.\n")
}

// clientRow is one row of the client flags table: the command and its flags
// in backtick form.
type clientRow struct {
	command string
	flags   string
}

// clientRows answers the client flags table from the command specs, so the
// reference lists the same flags as the commands take.
func clientRows() []clientRow {
	specs := []struct {
		usage string
		cmd   Command
	}{
		{"opendustpan namespace create <name>", NamespaceCreateCommand(nil)},
		{"opendustpan backup", BackupCommand(nil)},
		{"opendustpan backup check --backup <id>", BackupCheckCommand(nil)},
		{"opendustpan restore --backup <id>", RestoreCommand(nil)},
		{"opendustpan store check --target <blobs|backup>", StoreCheckCommand(nil)},
		{"opendustpan upgrade check", UpgradeCheckCommand(nil)},
		{"opendustpan audit-chain status", AuditChainStatusCommand(nil)},
		{"opendustpan audit-chain approve", AuditChainApproveCommand(nil)},
		{"opendustpan health", HealthCommand(nil)},
		{"opendustpan key generate", KeyGenerateCommand()},
	}

	rows := make([]clientRow, 0, len(specs))

	for _, spec := range specs {
		flags := make([]string, 0, len(spec.cmd.Settings))

		for _, s := range spec.cmd.Settings {
			flags = append(flags, "`--"+s.Flag+"`")
		}

		list := strings.Join(flags, ", ")
		if list == "" {
			list = referenceNone
		}

		rows = append(rows, clientRow{command: spec.usage, flags: list})
	}

	return rows
}

// writeHost writes the host command section from the settings of the host
// row, so the reference lists the same settings as the command reads.
func writeHost(out *strings.Builder) {
	out.WriteString("\n")
	out.WriteString("## Host command\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan host create-temp-admin` opens the database itself, as `serve`\n")
	out.WriteString("does, and makes a temporary administrator for one named person. It reads\n")
	out.WriteString("these settings. The [host recovery guide](../guides/host-recovery.md) shows\n")
	out.WriteString("the steps.\n")
	out.WriteString("\n")
	out.WriteString("| Flag | Environment | Default |\n")
	out.WriteString("| --- | --- | --- |\n")

	for _, s := range HostCreateTempAdminCommand(nil).Settings {
		flag, env := referenceNone, referenceNone

		if s.Kind != KindBool {
			flag = "`--" + s.Flag + "`"
		}

		if s.Env != "" {
			env = "`" + s.Env + "`"
		}

		out.WriteString("| " + flag + " | " + env + " | " + s.Reference + " |\n")
	}
}

// writeDev writes the dev command section from the dev declarations, so the
// reference lists the same flags as the command takes.
func writeDev(out *strings.Builder) {
	out.WriteString("\n")
	out.WriteString("## Dev command\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan dev` runs a server for a trial or for CI, with no setup. It reads\n")
	out.WriteString("each setting from a flag only. No variable of `serve` and no configuration\n")
	out.WriteString("file sets a flag of `dev`, also when `serve` has a flag with the same name.\n")
	out.WriteString("The [dev server guide](../guides/dev-server.md) shows the steps.\n")
	out.WriteString("\n")
	out.WriteString("| Flag | Default |\n")
	out.WriteString("| --- | --- |\n")

	for _, s := range DevFlags() {
		out.WriteString("| `--" + s.Flag + "` | " + s.Reference + " |\n")
	}

	out.WriteString("\n")
	out.WriteString("`dev` reads only these two variables:\n")
	out.WriteString("\n")
	out.WriteString("| Environment | Description |\n")
	out.WriteString("| --- | --- |\n")

	for _, s := range DevEnvs() {
		out.WriteString("| `" + s.Env + "` | " + s.Doc + " |\n")
	}

	out.WriteString("\n")
	out.WriteString("At start, `dev` clears each variable of a secret setting of `serve` from its\n")
	out.WriteString("environment, and never reads `DUSTPAN_ADMIN_TOKEN` or\n")
	out.WriteString("`DUSTPAN_ADMIN_TOKEN_FILE`.\n")
}
