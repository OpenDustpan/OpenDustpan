// Package cli is the command line of the client generator. The program
// cmd/clientgen only calls Run, so that the tests run the whole command
// without a child process.
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/typescript"
)

// The exit codes of Run.
const (
	// ExitOK means that the command did what it was asked.
	ExitOK = 0
	// ExitFailed means that the command failed, or that -check found a difference.
	ExitFailed = 1
	// ExitUsage means that the arguments are not valid.
	ExitUsage = 2
)

// DefaultTypeScriptPackage is the npm name of the TypeScript client.
const DefaultTypeScriptPackage = "@opendustpan/client"

// DefaultServerRelease is the server release that a run records when the
// caller gives none. A published package carries the real release or commit.
const DefaultServerRelease = "development"

// ErrTooLarge means that the description is longer than the parser accepts.
var ErrTooLarge = errors.New("the description is longer than the limit")

// target is one template set that the command can write.
type target struct {
	lang    func() clientgen.Language
	pkgName string
}

// targets maps the name of a target to its template set and its default
// package name.
func targets() map[string]target {
	return map[string]target{
		"typescript": {lang: typescript.New, pkgName: DefaultTypeScriptPackage},
	}
}

// targetNames lists the names of the targets, in order.
func targetNames() []string {
	names := make([]string, 0, len(targets()))
	for name := range targets() {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// config holds the flags of one run.
type config struct {
	target, description, out, pkg, release, version string
	check                                           bool
}

// sayf writes one line of the report. A failed write to the terminal has no
// better handling than to go on.
func sayf(w io.Writer, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format+"\n", args...); err != nil {
		return
	}
}

// parse reads the flags. It returns the exit code and false when the command
// must stop.
func parse(args []string, stderr io.Writer) (config, int, bool) {
	var c config

	fs := flag.NewFlagSet("clientgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.target, "target", "", "the target language: "+strings.Join(targetNames(), ", "))
	fs.StringVar(&c.description, "description", "", "the path of the API description (openapi.json)")
	fs.StringVar(&c.out, "out", "", "the output directory")
	fs.StringVar(&c.pkg, "package", "", "the package name; the default depends on the target")
	fs.StringVar(&c.release, "server-release", DefaultServerRelease,
		"the server release, or the commit before the first release, that the description comes from")
	fs.StringVar(&c.version, "client-version", "", "the client version; the default is info.version of the description")
	fs.BoolVar(&c.check, "check", false,
		"write nothing; exit with 1 and list the differences if the output directory is not what a run gives")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return c, ExitOK, false
		}

		return c, ExitUsage, false
	}

	if fs.NArg() != 0 || c.target == "" || c.description == "" || c.out == "" {
		sayf(stderr, "clientgen: -target, -description and -out are required, and no argument follows the flags")

		return c, ExitUsage, false
	}

	if _, ok := targets()[c.target]; !ok {
		sayf(stderr, "clientgen: unknown target %q. Use one of: %s", c.target, strings.Join(targetNames(), ", "))

		return c, ExitUsage, false
	}

	return c, ExitOK, true
}

// Run runs the command with the arguments after the program name. It writes
// the report to stdout and the errors to stderr, and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	c, code, ok := parse(args, stderr)
	if !ok {
		return code
	}

	t := targets()[c.target]
	if c.pkg == "" {
		c.pkg = t.pkgName
	}

	lang := t.lang()

	files, err := generate(lang, c.description, clientgen.Options{
		Package: c.pkg, ClientVersion: c.version, ServerRelease: c.release,
		GeneratorVersion: clientgen.GeneratorVersion, DescriptionSHA256: "",
	})
	if err != nil {
		sayf(stderr, "clientgen: %v", err)

		return ExitFailed
	}

	if c.check {
		return report(lang, c.out, files, stdout, stderr)
	}

	if err = clientgen.NewWriter(lang).Write(c.out, files); err != nil {
		sayf(stderr, "clientgen: %v", err)

		return ExitFailed
	}

	sayf(stdout, "clientgen: wrote %d files to %s", len(files), c.out)

	return ExitOK
}

// generate reads and parses the description, and renders the files.
func generate(lang clientgen.Language, path string, opts clientgen.Options) ([]clientgen.File, error) {
	raw, err := readLimited(path)
	if err != nil {
		return nil, err
	}

	api, err := clientgen.NewParser().Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("read the description: %w", err)
	}

	digest := sha256.Sum256(raw)
	opts.DescriptionSHA256 = hex.EncodeToString(digest[:])

	if opts.ClientVersion == "" {
		opts.ClientVersion = api.Version
	}

	files, err := lang.Render(api, opts)
	if err != nil {
		return nil, fmt.Errorf("render the %s client: %w", lang.Name(), err)
	}

	return files, nil
}

// readLimited reads the description, and never more than the parser accepts.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the caller of the command names the file.
	if err != nil {
		return nil, fmt.Errorf("open the description: %w", err)
	}

	defer f.Close() //nolint:errcheck // Nothing was written through the close.

	raw, err := io.ReadAll(io.LimitReader(f, clientgen.MaxDescriptionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the description: %w", err)
	}

	if len(raw) > clientgen.MaxDescriptionBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, clientgen.MaxDescriptionBytes)
	}

	return raw, nil
}

// report compares the files with the directory, and lists the differences.
func report(lang clientgen.Language, dir string, files []clientgen.File, stdout, stderr io.Writer) int {
	diffs, err := clientgen.Diff(lang, dir, files)
	if err != nil {
		sayf(stderr, "clientgen: %v", err)

		return ExitFailed
	}

	if len(diffs) == 0 {
		sayf(stdout, "clientgen: %s is what a run gives", dir)

		return ExitOK
	}

	for _, d := range diffs {
		sayf(stderr, "clientgen: %s", d)
	}

	sayf(stderr, "clientgen: %s is not what a run gives. Run: make clients-generate", dir)

	return ExitFailed
}
