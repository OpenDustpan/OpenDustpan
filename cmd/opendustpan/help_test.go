package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestUsageNamesEveryCommand proves that the usage line names each command of
// the command table, so a new command shows in it.
func TestUsageNamesEveryCommand(t *testing.T) {
	t.Parallel()

	for _, cmd := range commandTable() {
		if want := "opendustpan " + strings.Join(cmd.Path, " "); !strings.Contains(errUsage.Error(), want) {
			t.Errorf("usage = %q, want it to name %q", errUsage.Error(), want)
		}
	}
}

// usageGroup is a group of the usage text and the commands under it.
type usageGroup struct {
	name     string
	commands []string
}

// TestUsageGroupsByIntent proves that the usage text lists the commands in the
// groups and the order of the decision, that each command of the table is
// under exactly one group, and that no line names a command that the table
// lacks.
func TestUsageGroupsByIntent(t *testing.T) {
	t.Parallel()

	want := []usageGroup{
		{"Try out", []string{"dev"}},
		{"Run in production", []string{"serve", "key generate", "doctor"}},
		{"Operate", []string{
			"health", "namespace create", "host create-temp-admin", "audit-chain status", "audit-chain approve",
		}},
		{"Back up", []string{"backup", "backup check", "restore"}},
		{"Upgrade", []string{"upgrade check"}},
	}

	got := parseUsage(t, errUsage.Error())

	if len(got) != len(want) {
		t.Fatalf("usage has %d groups, want %d:\n%s", len(got), len(want), errUsage)
	}

	for i := range want {
		if got[i].name != want[i].name || strings.Join(got[i].commands, ",") != strings.Join(want[i].commands, ",") {
			t.Errorf("group %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	seen := map[string]int{}

	for _, group := range got {
		for _, command := range group.commands {
			seen[command]++
		}
	}

	for _, cmd := range commandTable() {
		if name := strings.Join(cmd.Path, " "); seen[name] != 1 {
			t.Errorf("the usage lists %q %d times, want once", name, seen[name])
		}

		delete(seen, strings.Join(cmd.Path, " "))
	}

	for name := range seen {
		t.Errorf("the usage lists %q, which the command table lacks", name)
	}
}

// commandPath answers the path of the command in a usage line, without the
// program name and without the flags and the operands.
func commandPath(line string) string {
	path := []string{}

	for word := range strings.FieldsSeq(strings.TrimPrefix(line, "  opendustpan ")) {
		if strings.HasPrefix(word, "-") || strings.HasPrefix(word, "<") {
			break
		}

		path = append(path, word)
	}

	return strings.Join(path, " ")
}

// parseUsage splits the usage text into its groups. A group starts with a line
// that ends in a colon and has no indent. A command line has two spaces of
// indent and starts with the program name. The command is its path, with no
// operand.
func parseUsage(t *testing.T, text string) []usageGroup {
	t.Helper()

	var groups []usageGroup

	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "  opendustpan ") {
			if len(groups) == 0 {
				t.Fatalf("command line %q is before the first group", line)
			}

			groups[len(groups)-1].commands = append(groups[len(groups)-1].commands, commandPath(line))

			continue
		}

		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "usage") {
			groups = append(groups, usageGroup{name: strings.TrimSuffix(line, ":"), commands: nil})
		}
	}

	return groups
}

// TestHelpExitsZero proves that a request for help is not an error: the
// command answers no error, so the binary exits with status 0. The top-level
// request prints the usage line to standard output.
func TestHelpExitsZero(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"serve", "-h"},
		{"namespace", "create", "-h"},
		{"host", "create-temp-admin", "-h"},
		{"backup", "check", "--help"},
	} {
		var stdout, stderr bytes.Buffer

		if err := run(t.Context(), args, &stdout, &stderr); err != nil {
			t.Errorf("run %v: err = %v, want nil for a help request", args, err)
		}

		if len(args) == 1 && !strings.Contains(stdout.String(), "opendustpan serve") {
			t.Errorf("run %v: stdout = %q, want the usage line", args, stdout.String())
		}
	}
}
