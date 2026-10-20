package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestDevBackupFlagComesFromTheFlagOnly sends the setting that lets a
// production installation restore a dev backup through every other layer
// (layer 1d). A test sets every variable that serve declares to true, adds the
// names that a variable of this setting could have, and puts a key with the
// name of the flag in the configuration file. The setting stays off. The key
// in the file is an error. The flag turns it on.
//
//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestDevBackupFlagComesFromTheFlagOnly(t *testing.T) {
	name := cli.UnsafeAllowDevBackupsFlag

	setting := cli.ByFlag(name)
	if setting.Env != "" || setting.File != "" {
		t.Fatalf("setting --%s has the variable %q and the file key %q, want neither", name, setting.Env, setting.File)
	}

	env := map[string]string{}

	for _, s := range cli.Settings {
		if s.Env != "" {
			env[s.Env] = "true"
		}
	}

	upper := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	for _, extra := range []string{upper, "DUSTPAN_" + upper, "DUSTPAN_ALLOW_DEV_BACKUPS", "DUSTPAN_ALLOW_DEV_BACKUPS_INTO_PRODUCTION"} {
		env[extra] = "true"
	}

	lookup := func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}

	parse := func(t *testing.T, args ...string) *serveFlags {
		t.Helper()

		var f serveFlags

		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		f.register(fs)

		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse: %v", err)
		}

		f.args = args
		f.lookup = cli.SnapshotEnv(lookup, cli.DeclaredEnvs())

		if err := f.resolveLayers(fs, lookup); err != nil {
			t.Fatalf("resolve: %v", err)
		}

		return &f
	}

	t.Run("every variable set, no flag", func(t *testing.T) {
		if f := parse(t); f.allowDevBackups {
			t.Fatal("a variable turned the setting on, want the flag as the only source")
		}
	})

	t.Run("the flag", func(t *testing.T) {
		if f := parse(t, "--"+name); !f.allowDevBackups {
			t.Fatal("the flag left the setting off")
		}
	})

	t.Run("a key in the configuration file is an error", func(t *testing.T) {
		for _, text := range []string{name + ": true\n", `"` + name + `": true` + "\n"} {
			_, err := cli.ParseConfig([]byte(text))
			if !errors.Is(err, cli.ErrConfigUnknown) {
				t.Fatalf("parse config %q: err = %v, want ErrConfigUnknown", text, err)
			}
		}

		setServeEnv(t, testKeyBase64())

		err := serve(t.Context(), []string{"--config", writeConfig(t, name+": true\n")}, os.Stderr)
		if !errors.Is(err, cli.ErrConfigUnknown) {
			t.Fatalf("serve with the key in the file = %v, want ErrConfigUnknown", err)
		}
	})
}
