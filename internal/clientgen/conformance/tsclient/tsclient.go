// Package tsclient builds the TypeScript client and its conformance adapter
// from an API description, for the conformance suite.
//
// Build runs the parser and the TypeScript template set, writes the package
// into a directory, adds the hand-written adapter, and compiles the package
// with the TypeScript compiler. It writes only in the directory it gets. The
// compiler and Node come from the PATH of the caller: the package has no
// dependency, so the build needs no network.
package tsclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/typescript"
)

//go:embed files/adapter.mjs
var handWritten embed.FS

// Package is the npm name of the TypeScript client.
const Package = "@opendustpan/client"

// adapterFile is the name of the adapter in the package directory. It imports
// the compiled package from dist.
const adapterFile = "adapter.mjs"

// The modes of the files that Build adds.
const fileMode = 0o600

// ErrBuild means that the TypeScript compiler failed, or that a tool is
// missing.
var ErrBuild = errors.New("tsclient: the build failed")

// Options are the inputs of Build.
type Options struct {
	// Description is the API description.
	Description []byte
	// ServerRelease is the release of the server that the description comes
	// from.
	ServerRelease string
	// Dir is the directory to write into. Build creates it.
	Dir string
}

// Built is the result of Build.
type Built struct {
	// Command is the path of Node, and Args are the arguments that start the
	// adapter.
	Command string
	Args    []string
	// ClientVersion is the version of the client.
	ClientVersion string
	// DescriptionVersion is the version of the description.
	DescriptionVersion string
}

// Generate parses the description and renders the package, with no write.
func Generate(opts Options) ([]clientgen.File, string, error) {
	api, err := clientgen.NewParser().Parse(opts.Description)
	if err != nil {
		return nil, "", fmt.Errorf("parse the description: %w", err)
	}

	digest := sha256.Sum256(opts.Description)

	files, err := typescript.New().Render(api, clientgen.Options{
		Package: Package, ClientVersion: api.Version, ServerRelease: opts.ServerRelease,
		ServerCommit: "development", Description: opts.Description,
		GeneratorVersion: clientgen.GeneratorVersion, DescriptionSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return nil, "", fmt.Errorf("render the TypeScript client: %w", err)
	}

	return files, api.Version, nil
}

// Build writes the package and the adapter into opts.Dir and compiles them.
func Build(ctx context.Context, opts Options) (Built, error) {
	var built Built

	files, version, err := Generate(opts)
	if err != nil {
		return built, err
	}

	if err = clientgen.NewWriter(typescript.New()).Write(opts.Dir, files); err != nil {
		return built, fmt.Errorf("write the generated files: %w", err)
	}

	src, err := handWritten.ReadFile("files/" + adapterFile)
	if err != nil {
		return built, fmt.Errorf("read the adapter: %w", err)
	}

	if err = os.WriteFile(filepath.Join(opts.Dir, adapterFile), src, fileMode); err != nil {
		return built, fmt.Errorf("write the adapter: %w", err)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		return built, fmt.Errorf("%w: find node: %w", ErrBuild, err)
	}

	if compileErr := Compile(ctx, opts.Dir); compileErr != nil {
		return built, compileErr
	}

	built.Command, built.Args = node, []string{filepath.Join(opts.Dir, adapterFile)}
	built.ClientVersion, built.DescriptionVersion = version, version

	return built, nil
}

// Environ returns the allowlisted variables of this process, read by name. The
// result is never nil, so a child process does not inherit the rest of the
// environment, where CI holds tokens.
func Environ() []string {
	names := []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"}
	env := make([]string, 0, len(names))

	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}

	return env
}

// Compile runs the TypeScript compiler on the package in dir. It writes dist
// in dir.
func Compile(ctx context.Context, dir string) error {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		return fmt.Errorf("%w: find tsc: %w", ErrBuild, err)
	}

	// G204: tsc comes from PATH, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, tsc, "-p", "tsconfig.json") //nolint:gosec // See the line above.
	cmd.Dir = dir
	cmd.Env = Environ()

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	if err = cmd.Run(); err != nil {
		return fmt.Errorf("%w: tsc: %w\n%s", ErrBuild, err, strings.TrimSpace(out.String()))
	}

	return nil
}
