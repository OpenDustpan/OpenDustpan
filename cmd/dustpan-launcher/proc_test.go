package main

import (
	"os"
	"os/exec"
	"testing"
)

// command returns the process of the program at the absolute path prog with
// the argument list args, as the launcher builds a step: no shell, no
// lookup.
func command(prog string, args ...string) *exec.Cmd {
	return &exec.Cmd{
		Path: prog, Args: append([]string{prog}, args...), Env: os.Environ(), Dir: "", Stdin: nil, Stdout: nil,
		Stderr: nil, ExtraFiles: nil, SysProcAttr: nil, Process: nil, ProcessState: nil, Err: nil, Cancel: nil,
		WaitDelay: 0,
	}
}

// goCommand returns the process of the go tool with args.
func goCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find the go tool: %v", err)
	}

	return command(goBin, args...)
}
