package main

import (
	"os"
	"strings"
	"testing"
)

// TestDemoScriptKeepsThePasswordOutOfFiles proves that scripts/demo.sh sends
// the output of dev --demo to a pipe and never to the server log, that the
// reader of the pipe keeps the demo line out of the log, and that the script
// checks the log for the demo line before it reports success.
func TestDemoScriptKeepsThePasswordOutOfFiles(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../scripts/demo.sh")
	if err != nil {
		t.Fatalf("read the script: %v", err)
	}

	script := string(raw)

	start := strings.Index(script, "./bin/opendustpan dev --demo")
	if start < 0 {
		t.Fatal("the script does not start dev --demo")
	}

	command, _, _ := strings.Cut(script[start:], "server_pid=$!")

	if !strings.Contains(command, `>"$DEMO_TMP/server.fifo" 2>&1`) {
		t.Errorf("the output of dev --demo does not go to the pipe:\n%s", command)
	}

	if strings.Contains(command, "SERVER_LOG") {
		t.Errorf("the output of dev --demo goes to the server log:\n%s", command)
	}

	for _, want := range []string{
		`"Demo user: "*)`,
		`printf '%s\n' "$line" >&2`,
		`*) printf '%s\n' "$line" >>"$SERVER_LOG" ;;`,
		`if grep -q '^Demo user: ' "$SERVER_LOG"; then`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q", want)
		}
	}
}
