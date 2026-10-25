package client

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
)

// devEnvVar is set by the dev.env file of opendustpan dev. Doctor refuses a
// shell that holds it, as serve does.
const devEnvVar = "DUSTPAN_DEV"

// The result of one check. A fail makes the exit code 1. A warn does not.
const (
	statusPass = "pass"
	statusWarn = "warn"
	statusFail = "fail"
)

var (
	// ErrDoctorDevEnvironment reports doctor in a shell that holds DUSTPAN_DEV.
	ErrDoctorDevEnvironment = errors.New(
		"DUSTPAN_DEV is set: the dev.env file of opendustpan dev sets it. Start doctor in a new shell",
	)
	// ErrDoctorFailed reports a doctor run in which at least one check failed.
	ErrDoctorFailed = errors.New("doctor found a failed check: fix each line that starts with fail")
)

// The names of the checks, as the report prints them.
const (
	nameKey           = "key"
	nameKeyPlace      = "key place"
	nameDatabase      = "database"
	nameAdminToken    = "admin token"
	nameBackupTarget  = "backup target"
	nameListen        = "listen address"
	nameProxies       = "trusted proxies"
	nameServerKind    = "server kind"
	nameServerUpgrade = "server upgrade"
	nameServerBackup  = "server backup"
)

// checkResult is the result of one check: a status, the name of the check and
// one line of detail. The detail holds no secret.
type checkResult struct {
	status string
	name   string
	detail string
}

// doctorSettings holds the effective settings of serve that doctor checks,
// read through the layers: the flag, the environment and the configuration
// file.
type doctorSettings struct {
	fs     *flag.FlagSet
	file   cli.ConfigFile
	lookup cli.LookupFunc
}

// value answers the effective value of the flag name, without space around it.
func (d doctorSettings) value(name string) string {
	f := d.fs.Lookup(name)
	if f == nil {
		return ""
	}

	return strings.TrimSpace(f.Value.String())
}

// Doctor runs `opendustpan doctor`: it reads the settings of serve through the
// same layers as serve does and prints one line for each check, for people.
// It exits with 0 when no check fails and with 1 when a check fails. It opens
// no database and reads no key file: it asks the file system for the mode and
// the size of the key file only. With --server, it also asks the running
// server through the REST API. binaryVersion is the stamped version of the
// binary, for the check of the upgrade.
func Doctor(ctx context.Context, args []string, stdout, stderr io.Writer, binaryVersion string) error {
	if _, held := os.LookupEnv(devEnvVar); held {
		return ErrDoctorDevEnvironment
	}

	settings, err := readDoctorSettings(args, stderr)
	if err != nil {
		return err
	}

	results := localChecks(settings)

	if server := settings.value("server"); server != "" {
		results = append(results, remoteChecks(ctx, settings, server, binaryVersion, stderr)...)
	}

	return reportDoctor(stdout, results)
}

// readDoctorSettings parses the flags of serve and of doctor and resolves
// each setting through the layers. It refuses a configuration file that does
// not parse and a password in a flag, as serve does.
func readDoctorSettings(args []string, stderr io.Writer) (doctorSettings, error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	settings := append(cli.ServeFlags(), cli.SettingsFor("server", "ca-file")...)
	registerAll(fs, settings)

	if err := fs.Parse(args); err != nil {
		return doctorSettings{}, fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return doctorSettings{}, ErrUsage
	}

	var file cli.ConfigFile

	if path := strings.TrimSpace(fs.Lookup("config").Value.String()); path != "" {
		data, err := cli.ReadConfigBytes(path)
		if err != nil {
			return doctorSettings{}, fmt.Errorf("read the configuration file: %w", err)
		}

		if file, err = cli.ParseConfig(data); err != nil {
			return doctorSettings{}, fmt.Errorf("read the configuration file: %w", err)
		}
	}

	resolved, err := cli.Resolve(fs, settings, os.LookupEnv, file)
	if err != nil {
		return doctorSettings{}, fmt.Errorf("resolve settings: %w", err)
	}

	if err := cli.RefuseFlagPassword(fs, resolved); err != nil {
		return doctorSettings{}, fmt.Errorf("check the flags: %w", err)
	}

	return doctorSettings{fs: fs, file: file, lookup: cli.LayeredLookup(os.LookupEnv, file)}, nil
}

// registerAll registers each setting as a flag by its kind. The values stay in
// the flag set: doctor reads them there, after the layers have set them.
func registerAll(fs *flag.FlagSet, settings []*cli.Setting) {
	for _, setting := range settings {
		if setting.Kind == cli.KindBool {
			setting.Bool(fs, new(bool))

			continue
		}

		if setting.Kind == cli.KindInt {
			setting.Int64(fs, new(int64))

			continue
		}

		setting.String(fs, new(string))
	}
}

// reportDoctor prints one line for each check and answers ErrDoctorFailed
// when a check failed.
func reportDoctor(stdout io.Writer, results []checkResult) error {
	failed := 0

	for _, r := range results {
		if r.status == statusFail {
			failed++
		}

		if _, err := fmt.Fprintf(stdout, "%s  %s: %s\n", r.status, r.name, r.detail); err != nil {
			return fmt.Errorf("write the report: %w", err)
		}
	}

	if failed > 0 {
		return fmt.Errorf("%w (%d of %d checks failed)", ErrDoctorFailed, failed, len(results))
	}

	return nil
}
