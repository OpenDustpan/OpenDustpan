package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// componentsCase is one case of TestServeComponents.
type componentsCase struct {
	name string
	args []string
	env  string
	// want holds the text that the start log must hold.
	want []string
	// noEffectLines is the number of lines about a setting that no selected
	// component reads.
	noEffectLines int
}

// startsWith returns a case that starts and logs want.
func startsWith(name, env string, args []string, noEffectLines int, want ...string) componentsCase {
	return componentsCase{name: name, args: args, env: env, want: want, noEffectLines: noEffectLines}
}

func TestServeComponents(t *testing.T) {
	cases := []componentsCase{
		startsWith("default runs every component", "", nil, 0, `components=api,worker`),
		startsWith("flag selects the worker", "", []string{"--components", "worker"}, 0, `components=worker`),
		startsWith("variable selects the api", "api", nil, 0, `components=api`),
		startsWith(
			"a setting that no selected component reads gives one line and does not stop the start",
			"", []string{"--components", "worker", "--console-dir", "/nonexistent"}, 1,
			`msg="setting has no effect: no selected component reads it" key=console-dir components=worker`,
		),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setServeEnv(t, testKeyBase64())

			if tc.env != "" {
				t.Setenv("DUSTPAN_COMPONENTS", tc.env)
			}

			logs, err := startServerLogs(t, serveArgs(t, tc.args)...)
			if err != nil {
				t.Fatalf("serve: %v", err)
			}

			checkStartLogs(t, logs, tc)
		})
	}
}

// checkStartLogs fails unless the start log holds the text of the case and the
// number of lines about settings that no component reads.
func checkStartLogs(t *testing.T, logs string, tc componentsCase) {
	t.Helper()

	for _, want := range tc.want {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not hold %q:\n%s", want, logs)
		}
	}

	if n := strings.Count(logs, "setting has no effect"); n != tc.noEffectLines {
		t.Errorf("no-effect lines = %d, want %d:\n%s", n, tc.noEffectLines, logs)
	}
}

// serveArgs returns the arguments of a serve test on a new database.
func serveArgs(t *testing.T, extra []string) []string {
	t.Helper()

	path := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	return append([]string{"--database", path, "--listen", "127.0.0.1:0", "--log-format", "text"}, extra...)
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeRefusesAnUnknownComponent(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	err := serve(t.Context(), serveArgs(t, []string{"--components", "api,scheduler"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "the valid components are all, api and worker") {
		t.Fatalf("serve: err = %v, want it to name the valid components", err)
	}
}
