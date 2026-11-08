// Package goclient builds the Go client and its conformance adapter from an API
// description, for the conformance suite.
//
// Build runs the parser and the Go template set, writes the generated files
// into a directory, adds the hand-written transport and the message loop of
// the adapter, and compiles the adapter with the go tool. It writes only in the
// directory it gets.
package goclient

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/golang"
)

//go:embed files/*.txt
var handWritten embed.FS

// Module is the module path of the Go client.
const Module = "opendustpan.org/client"

// goDirective is the Go language version of the module of the client. The
// client uses no feature of a newer version.
const goDirective = "1.22"

// The modes of the files and directories that Build adds.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// ErrBuild means that the go tool failed to compile or to check the client.
var ErrBuild = errors.New("goclient: the go tool failed")

// buildEnv returns the variables of the harness that the go tool gets. The
// tool does not get the rest of the environment, because CI holds tokens in it.
func buildEnv() []string {
	return []string{"PATH", "HOME", "TMPDIR", "GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "SYSTEMROOT"}
}

// Options are the inputs of Build.
type Options struct {
	// Description is the API description.
	Description []byte
	// Operations are the IDs of the operations that the client covers.
	Operations []string
	// ServerRelease is the release of the server that the description comes
	// from.
	ServerRelease string
	// Dir is the directory to write into. Build creates it.
	Dir string
}

// Generated is the output of Generate.
type Generated struct {
	// Files are the generated files.
	Files []clientgen.File
	// ClientVersion is the version of the client.
	ClientVersion string
	// DescriptionVersion is the version of the description.
	DescriptionVersion string
}

// Built is the result of Build.
type Built struct {
	// Adapter is the path of the compiled adapter.
	Adapter string
	// ClientVersion is the version of the client.
	ClientVersion string
	// DescriptionVersion is the version of the description.
	DescriptionVersion string
}

// Generate parses the description and renders the Go files, with no write.
func Generate(opts Options) (Generated, error) {
	var out Generated

	api, err := clientgen.NewParser(opts.Operations...).Parse(opts.Description)
	if err != nil {
		return out, fmt.Errorf("parse the description: %w", err)
	}

	version, err := clientgen.ClientVersion(api.Version, 0)
	if err != nil {
		return out, fmt.Errorf("version of the client: %w", err)
	}

	files, err := golang.New().Render(api, clientgen.Options{
		Package: Module, ClientVersion: version, ServerRelease: opts.ServerRelease,
		ServerCommit: "development", Description: nil,
		GeneratorVersion: clientgen.GeneratorVersion, DescriptionSHA256: "",
	})
	if err != nil {
		return out, fmt.Errorf("render the Go client: %w", err)
	}

	out.Files, out.ClientVersion, out.DescriptionVersion = files, version, api.Version

	return out, nil
}

// Build generates the client into opts.Dir, adds the hand-written files and
// compiles the adapter.
func Build(ctx context.Context, opts Options) (Built, error) {
	var built Built

	gen, err := Generate(opts)
	if err != nil {
		return built, err
	}

	if err = clientgen.NewWriter(golang.New()).Write(opts.Dir, gen.Files); err != nil {
		return built, fmt.Errorf("write the generated files: %w", err)
	}

	if writeErr := writeHandWritten(opts.Dir); writeErr != nil {
		return built, writeErr
	}

	adapter := filepath.Join("bin", "adapter")

	for _, args := range [][]string{{"vet", "./..."}, {"build", "-o", adapter, "./cmd/conformance-adapter"}} {
		if toolErr := goTool(ctx, opts.Dir, args...); toolErr != nil {
			return built, toolErr
		}
	}

	built.Adapter = filepath.Join(opts.Dir, adapter)
	built.ClientVersion, built.DescriptionVersion = gen.ClientVersion, gen.DescriptionVersion

	return built, nil
}

// writeHandWritten adds go.mod, the transport and the message loop.
func writeHandWritten(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}

	defer root.Close() //nolint:errcheck // Nothing was written through the close.

	mod := "module " + Module + "\n\ngo " + goDirective + "\n"
	if err = root.WriteFile("go.mod", []byte(mod), fileMode); err != nil {
		return fmt.Errorf("write go.mod: %w", err)
	}

	parts := [][2]string{
		{"transport.go", "files/transport.go.txt"},
		{"cmd/conformance-adapter/main.go", "files/main.go.txt"},
	}

	for _, part := range parts {
		src, readErr := handWritten.ReadFile(part[1])
		if readErr != nil {
			return fmt.Errorf("read %s: %w", part[1], readErr)
		}

		if err = root.MkdirAll(filepath.Dir(part[0]), dirMode); err != nil {
			return fmt.Errorf("make the directory of %s: %w", part[0], err)
		}

		if err = root.WriteFile(part[0], src, fileMode); err != nil {
			return fmt.Errorf("write %s: %w", part[0], err)
		}
	}

	return nil
}

// goTool runs the go tool in dir with a short environment.
func goTool(ctx context.Context, dir string, args ...string) error {
	_, err := Run(ctx, dir, args...)

	return err
}

// Run runs the go tool in dir with the environment of Build, and returns its
// output. A test uses it to run a program of its own in the module that Build
// wrote, for example a program that reads a value of the client.
func Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("%w: find go: %w", ErrBuild, err)
	}

	cmd := exec.CommandContext(ctx, goBin, args...) //nolint:gosec // G204: go comes from PATH; the arguments are fixed.
	cmd.Dir = dir
	cmd.Env = Environ("GOFLAGS=-buildvcs=false", "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "CGO_ENABLED=0")

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	if err = cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%w: go %s: %w\n%s", ErrBuild, strings.Join(args, " "), err, out.String())
	}

	return out.Bytes(), nil
}

// Environ returns the allowlisted variables of this process, read by name,
// and then the extra variables. The result is never nil, so a child process
// does not inherit the rest of the environment.
func Environ(extra ...string) []string {
	names := buildEnv()
	env := make([]string, 0, len(names)+len(extra))

	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}

	return append(env, extra...)
}
