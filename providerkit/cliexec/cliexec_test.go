package cliexec_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/providerkit/cliexec"
)

func TestCommandValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  cliexec.Command
		ok   bool
	}{
		{name: "bare name", cmd: command("oncall", nil), ok: true},
		{name: "clean absolute path", cmd: command("/usr/local/bin/oncall", nil), ok: true},
		{name: "relative path", cmd: command("./oncall", nil), ok: false},
		{name: "unclean absolute path", cmd: command("/usr/local/../bin/oncall", nil), ok: false},
		{name: "empty program", cmd: command("", nil), ok: false},
		{name: "own variable", cmd: command("oncall", map[string]string{"ONCALL_TEAM": "ops"}), ok: true},
		{name: "PATH", cmd: command("oncall", map[string]string{"PATH": "/tmp"}), ok: false},
		{name: "launcher variable", cmd: command("oncall", map[string]string{"DUSTPAN_JOB_ID": "1"}), ok: false},
		{name: "lowercase variable", cmd: command("oncall", map[string]string{"team": "ops"}), ok: false},
		{name: "NUL in an argument", cmd: cliexec.Command{
			Program: "oncall", Args: []string{"a\x00b"}, Env: nil, Stdin: nil, Timeout: time.Minute,
		}, ok: false},
		{name: "no timeout", cmd: cliexec.Command{
			Program: "oncall", Args: nil, Env: nil, Stdin: nil, Timeout: 0,
		}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cmd.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}

			if !tt.ok && !errors.Is(err, cliexec.ErrInvalidCommand) {
				t.Fatalf("Validate() = %v, want ErrInvalidCommand", err)
			}
		})
	}
}

func command(program string, env map[string]string) cliexec.Command {
	return cliexec.Command{Program: program, Args: []string{"show"}, Env: env, Stdin: nil, Timeout: time.Minute}
}
