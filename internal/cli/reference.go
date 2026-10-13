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
	writeSecrets(&out)
	writeClients(&out)

	return out.String()
}

// writeServeSettings writes the title and the server settings table from
// the serve declarations.
func writeServeSettings(out *strings.Builder) {
	out.WriteString("# Configuration\n")
	out.WriteString("\n")
	out.WriteString("`opendustpan serve` reads each setting from a flag or from an environment\n")
	out.WriteString("variable. A flag wins over its variable. `opendustpan serve --help` prints the\n")
	out.WriteString("same list.\n")
	out.WriteString("\n")
	out.WriteString("## Server settings\n")
	out.WriteString("\n")
	out.WriteString("| Flag | Environment | Default |\n")
	out.WriteString("| --- | --- | --- |\n")

	for _, s := range ServeFlags() {
		out.WriteString("| `--" + s.Flag + "` | `" + s.Env + "` | " + s.Reference + " |\n")
	}

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
