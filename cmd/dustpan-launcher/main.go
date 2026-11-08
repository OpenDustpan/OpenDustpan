// Command dustpan-launcher is the launcher: the static binary that a
// coordinator starts for each claimed phase. It runs the steps of the phase
// as argument lists with no shell. See "Launcher" in the reference
// documentation.
package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/helper"
	"opendustpan.org/opendustpan/pkg/launcher/run"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
)

// version is the release of the build, set with -ldflags.
var version = "dev"

// helperFailed is the exit code of a helper call that failed.
const helperFailed = 1

func main() {
	os.Exit(int(launch(os.Args, os.Stderr)))
}

// launch runs one start of the binary and returns its exit code. Its
// messages go to stderr; they never hold a secret.
func launch(argv []string, stderr io.Writer) launcher.ExitCode {
	log := slog.New(slog.NewTextHandler(stderr, nil)).With(slog.String("program", launcher.Binary))

	if err := harden(); err != nil {
		log.Error("protect the launcher process", slog.Any("error", err))

		return launcher.ExitSandbox
	}

	args, err := launcher.Parse(argv)
	if err != nil {
		log.Error("read the command line", slog.Any("error", err))

		return launcher.ExitUsage
	}

	switch args.Mode {
	case launcher.ModeCopy:
		return copyMode(&args, log)
	case launcher.ModeOpenTofuHelper, launcher.ModeGitHelper:
		return helperMode(&args, log)
	case launcher.ModeRun:
		return runMode(&args, log)
	default:
		log.Error("unknown mode", slog.String("mode", string(args.Mode)))

		return launcher.ExitUsage
	}
}

// copyMode copies the binary to the destination directory.
func copyMode(args *launcher.Args, log *slog.Logger) launcher.ExitCode {
	exe, err := os.Executable()
	if err != nil {
		log.Error("find the launcher binary", slog.Any("error", err))

		return launcher.ExitInternal
	}

	if err = launcher.CopyBinary(exe, args.Dest); err != nil {
		log.Error("copy the launcher binary", slog.Any("error", err))

		return launcher.ExitInternal
	}

	return launcher.ExitOK
}

// helperMode answers one call of a tool to a credentials helper.
func helperMode(args *launcher.Args, log *slog.Logger) launcher.ExitCode {
	err := helper.Run(context.Background(), &helper.Call{
		Mode: args.Mode, Args: args.HelperArgs, Stdin: os.Stdin, Stdout: os.Stdout,
		Socket: os.Getenv(launcher.EnvHelperSocket), Dial: helper.UnixDialer,
	})
	if err != nil {
		log.Error("answer the credentials helper", slog.Any("error", err))

		return helperFailed
	}

	return launcher.ExitOK
}

// runMode runs one claimed phase.
func runMode(args *launcher.Args, log *slog.Logger) launcher.ExitCode {
	// No descriptor that the launcher inherits reaches a step. The handoff
	// pipe on descriptor 3 stays open until the launcher reads it.
	first := launcher.HandoffFD
	if args.Handoff == runner.HandoffPipe {
		first++
	}

	if err := closeOnExec(first); err != nil {
		log.Error("mark the inherited descriptors close-on-exec", slog.Any("error", err))

		return launcher.ExitSandbox
	}

	secrets, err := launcher.TmpfsVolume{}.Prepare(args.SecretsDir)
	if err != nil {
		log.Error("prepare the secrets directory", slog.Any("error", err))

		return launcher.ExitSandbox
	}

	defer removeSecrets(args.SecretsDir, secrets, log)

	handoff, code := readHandoff(args.Handoff, log)
	if code != launcher.ExitOK {
		return code
	}

	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	defer signal.Stop(signals)

	go func() {
		<-signals
		close(stop)
	}()

	transport, err := newTransport(args.Server)
	if err != nil {
		log.Error("read the server URL", slog.Any("error", err))

		return launcher.ExitUsage
	}

	client := &session.Client{Server: args.Server, Transport: transport, Release: release(), Now: nil}

	return run.Run(context.Background(), &run.Config{
		Client: client, Handoff: handoff, WorkDir: args.WorkDir, Path: os.Getenv("PATH"), Logger: log, Stop: stop,
		Grace: nil, RenewEvery: 0, ChunkSize: 0, FlushEvery: 0, Retry: 0, Sleep: nil,
	})
}

// readHandoff reads the handoff token through the handoff h. It reads a
// pipe only: the standard input for runner.HandoffStdin, and the file
// descriptor launcher.HandoffFD for runner.HandoffPipe.
func readHandoff(h runner.Handoff, log *slog.Logger) (string, launcher.ExitCode) {
	var f *os.File

	switch h {
	case runner.HandoffPipe:
		f = os.NewFile(launcher.HandoffFD, "handoff")
	case runner.HandoffStdin:
		f = os.Stdin
	case runner.HandoffKubernetes, runner.HandoffVsock:
		log.Error("this launcher release does not read the handoff", slog.String("handoff", string(h)))

		return "", launcher.ExitHandoff
	default:
		log.Error("unknown handoff")

		return "", launcher.ExitUsage
	}

	handoff, err := launcher.ReadHandoff(f)
	if err != nil {
		log.Error("read the handoff", slog.Any("error", err))

		return "", launcher.ExitHandoff
	}

	return handoff, launcher.ExitOK
}

// release returns the release that the launcher sends. A development build
// has no release, and sends 0.0.0, which a development server accepts.
func release() string {
	r, err := runner.ParseRelease(version)
	if err != nil {
		return runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}.String()
	}

	return r.String()
}

// removeSecrets removes the directory of the launcher in the secrets
// directory, through a root on the secrets directory.
func removeSecrets(secretsDir, made string, log *slog.Logger) {
	root, err := os.OpenRoot(secretsDir)
	if err != nil {
		log.Error("open the secrets directory", slog.Any("error", err))

		return
	}

	if err = root.RemoveAll(filepath.Base(made)); err != nil {
		log.Error("remove the directory of the launcher", slog.Any("error", err))
	}

	if err = root.Close(); err != nil {
		log.Error("close the secrets directory", slog.Any("error", err))
	}
}
