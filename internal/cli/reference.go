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
	writeClients(&out)

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

		out.WriteString("| `--" + s.Flag + "` | `" + s.Env + "` | " + file + " | " + s.Reference + " |\n")
	}

	out.WriteString("\n")
}

// ConfigExample is the example configuration file of the reference: the
// JSON shape with # comments, which is valid YAML (ADR 63 decision 2). A
// test decodes it with ParseConfig.
const ConfigExample = `# opendustpan serve --config /etc/opendustpan/config.yaml
{
  # The region that this server is authoritative for.
  "region": "eu-west",
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
	out.WriteString("file that a `*_FILE` key names (ADR 63 decision 4). The `--database` flag\n")
	out.WriteString("still takes a URL with a password, but it is deprecated: `serve` logs a\n")
	out.WriteString("WARN line when it does. Use `DUSTPAN_DATABASE` instead.\n")
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
	mustEnv("DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY"),
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
	out.WriteString("openssl rand -base64 32\n")
	out.WriteString("```\n")
	out.WriteString("\n")
	out.WriteString("Without the variable, the server uses the key file in\n")
	out.WriteString("`--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), by default\n")
	out.WriteString("`dustpan.key` next to the SQLite database file, or `./dustpan.key` with\n")
	out.WriteString("PostgreSQL. On a terminal, `serve` asks for an explicit yes before it makes\n")
	out.WriteString("the file. Without a terminal, it makes the file only with\n")
	out.WriteString("`--generate-key-encryption-key`\n")
	out.WriteString("(`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true`). Without the prompt or the\n")
	out.WriteString("opt-in, `serve` stops before it opens the database and names each setting\n")
	out.WriteString("that helps. The server never makes a key silently. A made file holds 32\n")
	out.WriteString("random bytes in base64 with owner-only mode. Each start logs the path of the\n")
	out.WriteString("key file in use, never the key. An operator who loses the key file loses\n")
	out.WriteString("every secret that the key encrypts, and nobody can recover them. A key file\n")
	out.WriteString("next to the data does not protect a backup of that data.\n")
	out.WriteString("\n")
}

// writeClients writes the client commands section with one flags row per
// command spec, so the reference lists the same flags as the commands take.
func writeClients(out *strings.Builder) {
	out.WriteString("## Client commands\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan namespace create`, `opendustpan backup`, `opendustpan backup\n")
	out.WriteString("check`, `opendustpan upgrade check` and `opendustpan restore` call a running\n")
	out.WriteString("server. They read the server URL from `--server` or\n")
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
	out.WriteString("`opendustpan health` reads `--listen`, `--tls-cert` and their variables, as\n")
	out.WriteString("`serve` does. The [observe guide](../guides/observe.md) shows the probes.\n")
	out.WriteString("The [backup guide](../guides/backup-restore-upgrade.md) shows the flags of\n")
	out.WriteString("the backup commands.\n")
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
		{"opendustpan upgrade check", UpgradeCheckCommand(nil)},
		{"opendustpan health", HealthCommand(nil)},
	}

	rows := make([]clientRow, 0, len(specs))

	for _, spec := range specs {
		flags := make([]string, 0, len(spec.cmd.Settings))

		for _, s := range spec.cmd.Settings {
			flags = append(flags, "`--"+s.Flag+"`")
		}

		rows = append(rows, clientRow{command: spec.usage, flags: strings.Join(flags, ", ")})
	}

	return rows
}
