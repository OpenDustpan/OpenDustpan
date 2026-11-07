package zonegen

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	exitFailure = 1
	exitUsage   = 2
)

// Run is the body of the zonegen command. It answers the exit code. With
// -check it writes nothing and fails when the file at -out is not what a fresh
// run gives.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("zonegen", flag.ContinueOnError)
	flags.SetOutput(stderr)

	out := flags.String("out", "pkg/preferences/zones_gen.go", "path of the generated Go file")
	check := flags.Bool("check", false, "fail when the file is not what a fresh run gives; write nothing")

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}

	want, err := fresh(ctx)
	if err != nil {
		reportf(stderr, "%v", err)

		return exitFailure
	}

	if *check {
		have, rerr := readFile(*out)
		if rerr != nil || !bytes.Equal(have, want) {
			reportf(stderr, "zonegen: %s is out of date; run make zones-generate", *out)

			return exitFailure
		}

		return 0
	}

	if err := os.WriteFile(*out, want, 0o600); err != nil {
		reportf(stderr, "zonegen: write %s: %v", *out, err)

		return exitFailure
	}

	reportf(stdout, "zonegen: wrote %s", *out)

	return 0
}

// fresh answers the generated file for the archive of the toolchain.
func fresh(ctx context.Context) ([]byte, error) {
	path, err := ArchivePath(ctx)
	if err != nil {
		return nil, err
	}

	archive, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("zonegen: read archive: %w", err)
	}

	return Generate(archive)
}

// readFile reads the file at path through a root on its directory.
func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}

	data, err := root.ReadFile(filepath.Base(path))
	closeErr := root.Close()

	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close directory: %w", closeErr)
	}

	return data, nil
}

// report writes one line to w. A failed write to a terminal has no remedy.
func reportf(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format+"\n", args...); err != nil {
		return
	}
}
