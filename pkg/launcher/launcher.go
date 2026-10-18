// Package launcher defines the contract of the launcher: the static binary
// that a coordinator starts for each claimed phase or scan, in a Kubernetes
// Job or a machine sandbox. The launcher gets its handoff token through a
// handoff, opens a job session with a key that it makes in memory, checks
// out the source snapshot, and starts each step as an argument list with
// no shell. It sends the log of each step to the server, and gives the
// credentials of a tool step to the tool through a credentials helper or a
// file on a memory-backed volume. The image of the team holds only the
// tools.
//
// This package holds the rules that the launcher, the coordinators and the
// tests share: the command line, the exit codes, the environment of a step
// and the files of the credentials. It never imports a transport.
package launcher

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

var (
	// ErrUsage reports a command line that the launcher does not accept.
	ErrUsage = errors.New("launcher command line is not valid")
	// ErrEnv reports a variable that a step cannot set, or two values for
	// one variable.
	ErrEnv = errors.New("step environment is not valid")
)

// Binary is the name of the launcher binary.
const Binary = "dustpan-launcher"

// Helper names. The launcher puts links to itself with these names in a
// directory of the memory-backed volume. When the launcher starts with one
// of them as the base name of its first argument, it runs as that helper.
const (
	// HelperOpenTofu is the credentials helper of OpenTofu: OpenTofu starts
	// it as "terraform-credentials-dustpan get <host>".
	HelperOpenTofu = "terraform-credentials-dustpan"
	// HelperGit is the git credential helper of the mode forge: git starts
	// it as "git-credential-dustpan get".
	HelperGit = "git-credential-dustpan"
)

// Mode is what one start of the launcher binary does.
type Mode string

// The modes of the launcher binary.
const (
	// ModeRun runs one claimed phase or scan.
	ModeRun Mode = "run"
	// ModeCopy copies the launcher binary to a directory and ends. The init
	// container of a Kubernetes Job runs it, so the image of the team needs
	// no launcher.
	ModeCopy Mode = "copy"
	// ModeOpenTofuHelper answers the token of the server host to OpenTofu.
	ModeOpenTofuHelper Mode = "opentofu-helper"
	// ModeGitHelper answers a token of the forge connection to git.
	ModeGitHelper Mode = "git-helper"
)

// Default paths inside a Kubernetes pod or a sandbox.
const (
	// DefaultWorkDir holds the checkout and the outputs of the steps.
	DefaultWorkDir = "/dustpan/work"
	// DefaultSecretsDir is the memory-backed volume of the credential
	// files. The launcher refuses to run when it is not on tmpfs or ramfs.
	DefaultSecretsDir = "/dustpan/secrets"
	// DefaultBinDir holds the launcher binary that the init container
	// copied.
	DefaultBinDir = "/dustpan/bin"
)

// Args is the parsed command line of ModeRun.
type Args struct {
	Mode Mode
	// Server is the URL of the server, with https and no query.
	Server string
	// Handoff is how the launcher gets its handoff token.
	Handoff runner.Handoff
	// Coordinator is the URL of the coordinator for HandoffKubernetes, and
	// CAFile the file of the certificate authority of the coordinator.
	Coordinator string
	CAFile      string
	// TokenFile is the projected service account token for
	// HandoffKubernetes.
	TokenFile  string
	WorkDir    string
	SecretsDir string
	// Dest is the directory of ModeCopy.
	Dest string
	// HelperArgs are the arguments of a helper mode after the program
	// name.
	HelperArgs []string
}

// Command returns the command line that a coordinator gives to the
// launcher in the directory bin for ModeRun with a. It sets the flags of
// a that are not empty, and never the work and the secrets directories
// when they are the defaults.
func Command(bin string, a *Args) []string {
	args := []string{path.Join(bin, Binary), string(ModeRun), "--server", a.Server, "--handoff", string(a.Handoff)}

	for _, f := range []struct{ name, value, def string }{
		{"--coordinator", a.Coordinator, ""},
		{"--ca-file", a.CAFile, ""},
		{"--token-file", a.TokenFile, ""},
		{"--work-dir", a.WorkDir, DefaultWorkDir},
		{"--secrets-dir", a.SecretsDir, DefaultSecretsDir},
	} {
		if f.value != "" && f.value != f.def {
			args = append(args, f.name, f.value)
		}
	}

	return args
}

// Parse parses the command line argv of the launcher binary, with argv[0]
// the program. A program whose base name is HelperOpenTofu or HelperGit
// runs as that helper. It returns an error that wraps ErrUsage for an
// unknown mode or flag, a flag with no value, a flag twice, a server that
// is not an https URL, an unknown handoff, or a Kubernetes handoff with no
// coordinator, CA file or token file.
func Parse(argv []string) (Args, error) {
	if len(argv) == 0 {
		return Args{}, fmt.Errorf("%w: no program name", ErrUsage)
	}

	switch path.Base(argv[0]) {
	case HelperOpenTofu:
		return helperArgs(ModeOpenTofuHelper, argv[1:]), nil
	case HelperGit:
		return helperArgs(ModeGitHelper, argv[1:]), nil
	}

	if len(argv) < minArgs {
		return Args{}, fmt.Errorf("%w: give a mode: %s or %s", ErrUsage, ModeRun, ModeCopy)
	}

	flags, err := parseFlags(argv[2:])
	if err != nil {
		return Args{}, err
	}

	switch Mode(argv[1]) {
	case ModeRun:
		return runArgs(flags)
	case ModeCopy:
		return copyArgs(flags)
	case ModeOpenTofuHelper, ModeGitHelper:
		return Args{}, fmt.Errorf("%w: mode %s starts through the name of its link only", ErrUsage, argv[1])
	default:
		return Args{}, fmt.Errorf("%w: unknown mode %q", ErrUsage, argv[1])
	}
}

// helperArgs returns the arguments of a helper mode.
func helperArgs(m Mode, rest []string) Args {
	return Args{
		Mode: m, Server: "", Handoff: "", Coordinator: "", CAFile: "", TokenFile: "",
		WorkDir: "", SecretsDir: "", Dest: "", HelperArgs: slices.Clone(rest),
	}
}

// minArgs is the program name and the mode.
const minArgs = 2

// knownFlags are the flags of ModeRun and ModeCopy.
//
//nolint:gochecknoglobals // A fixed list, read only.
var knownFlags = []string{
	"--server", "--handoff", "--coordinator", "--ca-file", "--token-file", "--work-dir", "--secrets-dir", "--dest",
}

// parseFlags parses "--name value" pairs.
func parseFlags(rest []string) (map[string]string, error) {
	flags := make(map[string]string, len(rest))

	for i := 0; i < len(rest); i += 2 {
		name := rest[i]
		if !slices.Contains(knownFlags, name) {
			return nil, fmt.Errorf("%w: unknown flag %q", ErrUsage, name)
		}

		if i+1 >= len(rest) || rest[i+1] == "" {
			return nil, fmt.Errorf("%w: flag %s needs a value", ErrUsage, name)
		}

		if _, ok := flags[name]; ok {
			return nil, fmt.Errorf("%w: flag %s is given twice", ErrUsage, name)
		}

		flags[name] = rest[i+1]
	}

	return flags, nil
}

// runArgs checks the flags of ModeRun.
func runArgs(flags map[string]string) (Args, error) {
	a := Args{
		Mode: ModeRun, Server: flags["--server"], Handoff: runner.Handoff(flags["--handoff"]),
		Coordinator: flags["--coordinator"], CAFile: flags["--ca-file"], TokenFile: flags["--token-file"],
		WorkDir: flags["--work-dir"], SecretsDir: flags["--secrets-dir"], Dest: "", HelperArgs: nil,
	}

	if a.WorkDir == "" {
		a.WorkDir = DefaultWorkDir
	}

	if a.SecretsDir == "" {
		a.SecretsDir = DefaultSecretsDir
	}

	if flags["--dest"] != "" {
		return Args{}, fmt.Errorf("%w: flag --dest is for mode %s only", ErrUsage, ModeCopy)
	}

	if err := checkHTTPS(a.Server, "--server"); err != nil {
		return Args{}, err
	}

	if err := checkHandoff(&a); err != nil {
		return Args{}, err
	}

	return a, nil
}

// checkHandoff checks the flags of the handoff of a.
func checkHandoff(a *Args) error {
	switch a.Handoff {
	case runner.HandoffKubernetes:
		if a.CAFile == "" || a.TokenFile == "" {
			return fmt.Errorf("%w: handoff %s needs --coordinator, --ca-file and --token-file", ErrUsage, a.Handoff)
		}

		return checkHTTPS(a.Coordinator, "--coordinator")
	case runner.HandoffStdin, runner.HandoffPipe:
		if a.Coordinator != "" || a.CAFile != "" || a.TokenFile != "" {
			return fmt.Errorf("%w: --coordinator, --ca-file and --token-file are for handoff %s only",
				ErrUsage, runner.HandoffKubernetes)
		}

		return nil
	default:
		return fmt.Errorf("%w: unknown handoff %q", ErrUsage, a.Handoff)
	}
}

// copyArgs checks the flags of ModeCopy.
func copyArgs(flags map[string]string) (Args, error) {
	if len(flags) != 1 || flags["--dest"] == "" || !path.IsAbs(flags["--dest"]) {
		return Args{}, fmt.Errorf("%w: mode %s takes only --dest with an absolute path", ErrUsage, ModeCopy)
	}

	return Args{
		Mode: ModeCopy, Server: "", Handoff: "", Coordinator: "", CAFile: "", TokenFile: "",
		WorkDir: "", SecretsDir: "", Dest: path.Clean(flags["--dest"]), HelperArgs: nil,
	}, nil
}

// checkHTTPS returns ErrUsage when s is not an https URL with a host and
// no user, query or fragment.
func checkHTTPS(s, flag string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: %s must be an https URL with no user, query or fragment", ErrUsage, flag)
	}

	return nil
}

// ExitCode is the exit code of the launcher in ModeRun. A coordinator reads
// it when the launcher ends, to decide what to do with the claim. The codes
// stay below 126, so they never clash with the codes of a shell or of a
// signal.
type ExitCode int

// The exit codes of the launcher.
const (
	// ExitOK: the phase or the scan ended and the launcher reported it,
	// also a phase that failed. The server has the outcome.
	ExitOK ExitCode = 0
	// ExitUsage: the command line is not valid.
	ExitUsage ExitCode = 64
	// ExitHandoff: the launcher got no handoff token, or the exchange for a
	// job session failed. No step began.
	ExitHandoff ExitCode = 65
	// ExitVersion: the server refused the release of the launcher. No step
	// began.
	ExitVersion ExitCode = 66
	// ExitSandbox: the place is not safe to run in, for example the
	// credentials directory is not on a memory-backed volume. No step
	// began.
	ExitSandbox ExitCode = 67
	// ExitSource: the source snapshot failed its check, or the checkout
	// failed. No step began. The launcher reported the failure.
	ExitSource ExitCode = 68
	// ExitClaimLost: the claim ended under the launcher: another claim
	// replaced it, or it expired. The launcher stopped its step and
	// reported nothing more.
	ExitClaimLost ExitCode = 69
	// ExitUnreported: the launcher could not report the end of the phase
	// to the server after its retries. The claim expires, and the rules
	// of a lost claim apply.
	ExitUnreported ExitCode = 70
	// ExitInternal: an error in the launcher.
	ExitInternal ExitCode = 71
)

// Disposition is what a coordinator does with a claim after its launcher
// ends.
type Disposition string

// The dispositions.
const (
	// DispositionNone: the server has the outcome, or the claim is lost.
	// The coordinator only deletes the Job or the sandbox.
	DispositionNone Disposition = "none"
	// DispositionRelease: no step began. The coordinator releases the
	// claim, so that another runner can take it.
	DispositionRelease Disposition = "release"
	// DispositionExpire: the coordinator does nothing to the claim. It
	// expires, and the server applies the rule of a lost claim, which
	// fails the phase when a step that changes resources began.
	DispositionExpire Disposition = "expire"
)

// DispositionFor returns what a coordinator does with the claim after its
// launcher ends with code c. A code that this package does not know, or an
// end on a signal, takes DispositionExpire: the server decides with the
// records of the steps.
func DispositionFor(c ExitCode) Disposition {
	switch c {
	case ExitOK, ExitClaimLost:
		return DispositionNone
	case ExitUsage, ExitHandoff, ExitVersion, ExitSandbox:
		return DispositionRelease
	case ExitSource, ExitUnreported, ExitInternal:
		return DispositionExpire
	default:
		return dispositionUnknown
	}
}

// dispositionUnknown is the disposition of an exit code that this package
// does not know.
const dispositionUnknown = DispositionExpire

// Variables that the launcher sets for a step. A step cannot set a name
// with the prefix job.ReservedEnvPrefix.
const (
	EnvJob        = "DUSTPAN_JOB_ID"
	EnvPhase      = "DUSTPAN_PHASE"
	EnvStep       = "DUSTPAN_STEP"
	EnvUnit       = "DUSTPAN_UNIT"
	EnvNamespace  = "DUSTPAN_NAMESPACE"
	EnvCommit     = "DUSTPAN_COMMIT"
	EnvServer     = "DUSTPAN_SERVER_URL"
	EnvScanReport = "DUSTPAN_SCAN_REPORT"
)

// Variables of the tool that the launcher sets for a step of the role tool
// only. Each holds a path or a URL, never a credential.
const (
	// EnvOpenTofuConfig names the CLI configuration file that names the
	// credentials helper.
	EnvOpenTofuConfig = "TF_CLI_CONFIG_FILE"
	// EnvPulumiHome names the Pulumi home directory with the credentials
	// file.
	EnvPulumiHome = "PULUMI_HOME"
	// EnvPulumiBackend names the URL of the server for the Pulumi CLI.
	EnvPulumiBackend = "PULUMI_BACKEND_URL"
	// EnvGitConfig names the git configuration file that names the git
	// credential helper.
	EnvGitConfig = "GIT_CONFIG_GLOBAL"
)

// EnvInput is what BuildEnv needs to make the environment of one step.
type EnvInput struct {
	// Path is the PATH of the launcher process: the PATH of the image.
	Path string
	// Home and Tmp are directories in the work directory.
	Home string
	Tmp  string
	// Dustpan are the variables with the prefix job.ReservedEnvPrefix, as
	// the launcher sets them for the step.
	Dustpan map[string]string
	// Step are the variables of the step from the spec.
	Step map[string]string
	// Tool are the variables of the tool and of the credential files, for
	// a step of the role tool. Each holds a path to the memory-backed
	// volume or a URL. Empty for any other step.
	Tool map[string]string
}

// BuildEnv returns the environment of one step, as NAME=value strings in
// order of their names. The launcher never passes its own environment to
// a step: a step gets PATH, HOME, TMPDIR, LANG, the variables of the
// launcher, its own variables and, for a tool step, the variables of the
// tool. It returns an error that wraps ErrEnv for a step variable that
// sets PATH, HOME, TMPDIR or LANG, that has the reserved prefix, that the
// credential-variable rule matches, or that a launcher variable also sets,
// and for a launcher variable without the reserved prefix.
func BuildEnv(in EnvInput) ([]string, error) {
	env := map[string]string{"PATH": in.Path, "HOME": in.Home, "TMPDIR": in.Tmp, "LANG": "C.UTF-8"}

	for name, v := range in.Dustpan {
		if !strings.HasPrefix(name, "DUSTPAN_") {
			return nil, fmt.Errorf("%w: launcher variable %q has no prefix DUSTPAN_", ErrEnv, name)
		}

		env[name] = v
	}

	for name, v := range in.Tool {
		if _, ok := env[name]; ok {
			return nil, fmt.Errorf("%w: tool variable %q is set twice", ErrEnv, name)
		}

		env[name] = v
	}

	for name, v := range in.Step {
		if err := checkStepVar(name, env); err != nil {
			return nil, err
		}

		env[name] = v
	}

	out := make([]string, 0, len(env))
	for name, v := range env {
		out = append(out, name+"="+v)
	}

	slices.Sort(out)

	return out, nil
}

// checkStepVar checks one variable of a step against the variables that the
// launcher already set in env.
func checkStepVar(name string, env map[string]string) error {
	upper := strings.ToUpper(name)

	switch {
	case slices.Contains([]string{"PATH", "HOME", "TMPDIR", "LANG"}, upper):
		return fmt.Errorf("%w: a step cannot set %s", ErrEnv, name)
	case strings.HasPrefix(upper, "DUSTPAN_"):
		return fmt.Errorf("%w: variable %q uses the reserved prefix DUSTPAN_", ErrEnv, name)
	case job.IsCredentialEnv(name):
		return fmt.Errorf("%w: variable %q carries a credential", ErrEnv, name)
	}

	if _, ok := env[name]; ok {
		return fmt.Errorf("%w: variable %q is set by the launcher", ErrEnv, name)
	}

	return nil
}
