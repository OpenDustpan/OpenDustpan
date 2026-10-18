package cliexectest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/providerkit/cliexec"
	"opendustpan.org/providerkit/cliexec/cliexectest"
)

// TestSuiteAgainstReferenceRunner runs the suite against a minimal runner on
// os/exec, so the suite itself has a test. The CLI engine brings its own
// adapter, which runs the same suite.
func TestSuiteAgainstReferenceRunner(t *testing.T) {
	t.Parallel()

	cliexectest.Run(t, func(t *testing.T, maxOutput int64) cliexec.Runner {
		t.Helper()

		return referenceRunner{max: maxOutput, path: os.Getenv("PATH")}
	})
}

type referenceRunner struct {
	max  int64
	path string
}

var errOverLimit = errors.New("over the output limit")

// limitBuffer keeps output up to max bytes. A write past the limit fails
// and stops the run.
type limitBuffer struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	max  int64
	over bool
	stop context.CancelFunc
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if int64(b.buf.Len()+len(p)) > b.max {
		b.over = true
		b.stop()

		return 0, errOverLimit
	}

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("keep output: %w", err)
	}

	return n, nil
}

func (r referenceRunner) Run(ctx context.Context, cmd cliexec.Command) (cliexec.Result, error) {
	none := cliexec.Result{ExitCode: 0, Stdout: nil, Stderr: nil}
	if err := cmd.Validate(); err != nil {
		return none, fmt.Errorf("check the command: %w", err)
	}

	dir, err := os.MkdirTemp("", "cliexec-run-*")
	if err != nil {
		return none, fmt.Errorf("make the run directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }() //nolint:errcheck // A leftover temporary directory does not change the result.

	runCtx, cancel := context.WithTimeout(ctx, cmd.Timeout)
	defer cancel()

	stdout := &limitBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}, max: r.max, over: false, stop: cancel}
	stderr := &limitBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}, max: r.max, over: false, stop: cancel}

	c := exec.CommandContext(runCtx, cmd.Program, cmd.Args...) //nolint:gosec // A checked argument list.
	c.Env = []string{"PATH=" + r.path, "HOME=" + dir, "TMPDIR=" + dir}
	for _, name := range slices.Sorted(mapKeys(cmd.Env)) {
		c.Env = append(c.Env, name+"="+cmd.Env[name])
	}

	c.Dir = dir
	c.Stdin = bytes.NewReader(cmd.Stdin)
	c.Stdout, c.Stderr = stdout, stderr
	c.SysProcAttr = new(syscall.SysProcAttr)
	c.SysProcAttr.Setpgid = true
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second

	err = c.Run()
	res := cliexec.Result{ExitCode: c.ProcessState.ExitCode(), Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}

	return res, classify(ctx, runCtx, stdout.over || stderr.over, err)
}

// classify maps the end of a run to the errors of the port.
func classify(ctx, runCtx context.Context, over bool, err error) error {
	var exitErr *exec.ExitError

	switch {
	case over:
		return cliexec.ErrTooLarge
	case ctx.Err() != nil:
		return fmt.Errorf("run: %w", ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return cliexec.ErrTimeout
	case err == nil, errors.As(err, &exitErr):
		return nil
	default:
		return fmt.Errorf("run: %w", err)
	}
}

func mapKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
