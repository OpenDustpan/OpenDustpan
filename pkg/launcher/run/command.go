package run

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// ErrStepRefused reports a step that the launcher does not start. The
// phase fails with the reason.
var ErrStepRefused = errors.New("the launcher does not start the step")

// command returns the process of the step: its program, its arguments, its
// directory and its environment. It starts nothing.
func (r *phaseRun) command(step *protocol.Step) (*exec.Cmd, error) {
	// Defense in depth: the server stores only the roles tool and hook;
	// this check stays so that an unknown role from another release fails
	// closed and never runs as a hook.
	if step.Role != string(job.RoleTool) && step.Role != string(job.RoleHook) {
		return nil, fmt.Errorf("%w: the role of the step is not tool or hook", ErrStepRefused)
	}

	prog, err := r.program(step.Args)
	if err != nil {
		return nil, err
	}

	dir, err := r.stepDir(step.Dir)
	if err != nil {
		return nil, err
	}

	a := &r.sess.Assignment
	dustpan := map[string]string{
		launcher.EnvJob: a.Job, launcher.EnvPhase: a.Phase, launcher.EnvStep: step.Name, launcher.EnvUnit: a.Unit,
		launcher.EnvNamespace: a.Namespace, launcher.EnvServer: a.ServerURL,
	}

	env, err := launcher.BuildEnv(launcher.EnvInput{
		Path: r.cfg.Path, Home: r.home, Tmp: r.tmp, Dustpan: dustpan, Step: step.Env, Tool: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: the environment of the step is not valid: %w", ErrStepRefused, err)
	}

	// No shell: the program gets the argument list as it is. The path is
	// absolute, so no lookup runs at the start. The standard input is the
	// null device.
	//
	// The path is not a constant because the launcher exists to run the
	// program that a step names. Before this point the launcher checked it:
	// a known role (tool or hook), a clean absolute path or a bare name
	// found in an absolute directory of the PATH of the image (program,
	// lookPath), no NUL byte in any argument, a directory inside the
	// checkout (stepDir), and an environment from BuildEnv. It runs the
	// argument list with no shell. Each check has a hostile-input test in
	// refusal_test.go.
	// nosemgrep: go.lang.security.audit.dangerous-exec-cmd.dangerous-exec-cmd
	return &exec.Cmd{
		Path: prog, Args: step.Args, Env: env, Dir: dir, Stdin: nil, Stdout: nil, Stderr: nil, ExtraFiles: nil,
		SysProcAttr: sysProcAttr(), Process: nil, ProcessState: nil, Err: nil, Cancel: nil, WaitDelay: 0,
	}, nil
}

// program returns the absolute path of the program of the argument list
// args: a clean absolute path, or a bare name in an absolute directory of
// the PATH of the image.
//
// Defense in depth: the server checks the argument list of each step when
// it stores it; this check stays so that an assignment from another
// release never starts a relative path or reaches a shell.
func (r *phaseRun) program(args []string) (string, error) {
	if len(args) == 0 || args[0] == "" {
		return "", fmt.Errorf("%w: the step has no program", ErrStepRefused)
	}

	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return "", fmt.Errorf("%w: an argument of the step holds a NUL byte", ErrStepRefused)
		}
	}

	prog := args[0]

	if filepath.IsAbs(prog) {
		if filepath.Clean(prog) != prog {
			return "", fmt.Errorf("%w: the program of the step is not a clean absolute path", ErrStepRefused)
		}

		return prog, nil
	}

	if strings.ContainsRune(prog, '/') {
		return "", fmt.Errorf("%w: the program of the step is not a bare name or an absolute path", ErrStepRefused)
	}

	return lookPath(prog, r.cfg.Path)
}

// lookPath finds the program name in the absolute directories of path.
func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("%w: the program %s is not on the PATH of the image", ErrStepRefused, name)
}

// stepDir returns the working directory of the step: dir in the checkout.
// It refuses a directory outside the checkout, also through a link.
func (r *phaseRun) stepDir(dir string) (string, error) {
	if dir == "" {
		return r.checkout, nil
	}

	if !filepath.IsLocal(dir) {
		return "", fmt.Errorf("%w: the directory of the step is not inside the checkout", ErrStepRefused)
	}

	root, err := os.OpenRoot(r.checkout)
	if err != nil {
		return "", fmt.Errorf("%w: the checkout is not readable", ErrStepRefused)
	}

	info, err := root.Stat(dir)
	if closeErr := root.Close(); closeErr != nil {
		r.log.Warn("close the checkout", slog.Any("error", closeErr))
	}

	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: the directory of the step is not a directory inside the checkout", ErrStepRefused)
	}

	return filepath.Join(r.checkout, dir), nil
}
