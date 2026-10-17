package job

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Limits of the steps of one job. They bound the size of a job record and
// of the assignment that a runner gets.
const (
	MaxSteps     = 64
	MaxArgs      = 256
	MaxArgsBytes = 128 << 10
	MaxEnv       = 128
	MaxStepName  = 63
	// MaxStepTimeout bounds the timeout of one step.
	MaxStepTimeout = 24 * time.Hour
	// DefaultStepTimeout is the timeout of a step that names none.
	DefaultStepTimeout = time.Hour
)

// ReservedEnvPrefix starts the names of the variables that the runner sets
// for each step, for example the job ID. A step cannot set them.
const ReservedEnvPrefix = "DUSTPAN_"

// StepRole tells what a step is for.
type StepRole string

// The roles of a step.
const (
	// RoleHook runs before or after the tool, for example a version manager
	// or a check against an internal API.
	RoleHook StepRole = "hook"
	// RoleTool runs the tool, or a wrapper in place of the tool.
	RoleTool StepRole = "tool"
)

// OutputKind names a file that a step leaves for the runner to upload.
type OutputKind string

// The outputs of a step.
const (
	// OutputPlanDisplay is the plan as the tool writes it for people, as
	// JSON: the output of tofu show -json, or of pulumi preview --json.
	OutputPlanDisplay OutputKind = "plan-display"
	// OutputPlanApply is the plan that the apply phase reads, for example an
	// OpenTofu plan file. No person can download it.
	OutputPlanApply OutputKind = "plan-apply"
)

// Output is one file that a step leaves. Path is relative to the directory
// of the code location in the checkout.
type Output struct {
	Kind OutputKind
	Path string
}

// Step is one command of a runner job. The runner starts Args[0] with the
// arguments Args[1:], directly, with no shell: no expansion, no quoting, no
// pipe. Args[0] is a program name that the runner finds on its PATH, or an
// absolute path.
type Step struct {
	// Name is unique in the job: lowercase letters, digits and '-'.
	Name string
	Role StepRole
	// Phase is the phase that runs the step.
	Phase PhaseKind
	Args  []string
	// Env holds variables that are not secret. A name that the
	// credential-variable rule (IsCredentialEnv) matches is refused. Secret
	// values never pass through a variable.
	Env map[string]string
	// Dir is relative to the directory of the code location in the checkout. Empty
	// means that directory.
	Dir string
	// Timeout ends the step. Zero takes DefaultStepTimeout.
	Timeout time.Duration
	// AllowFailure lets the phase continue when the step exits with a code
	// other than 0.
	AllowFailure bool
	// ChangesResources marks a step that can change resources or write
	// state. The server sets it, never a caller: the StepPlanner of the
	// tool for its own steps, and MarkCustomSteps for the steps of a
	// blueprint or of the unit settings. The runner records the start of
	// such a step before it starts the process. When its claim ends after
	// that record, the phase fails and never runs again on its own.
	ChangesResources bool
	// Outputs are the files that the runner uploads after the step.
	Outputs []Output
}

// StepRecord is the stored outcome of one step under one claim.
type StepRecord struct {
	Name string
	// Token is the token of the durable claim that ran the step. After a
	// takeover, the records of the earlier claims stay.
	Token int64
	// ExitCode is the exit code of the process, or -1 when the process
	// ended on a signal or did not start.
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	// Log is the log version of the step.
	Log *LogRef
}

var (
	stepNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ValidateSteps checks the steps of a runner job. It returns an error that
// wraps ErrInvalid and names the first step that fails, for:
//   - no step, or more than MaxSteps,
//   - a name that is empty, too long, not unique or not of lowercase
//     letters, digits and '-',
//   - an unknown role, or a phase other than plan or apply,
//   - no argument, more than MaxArgs, more than MaxArgsBytes in total, a
//     NUL byte, or a first argument that is not a bare name or an absolute
//     path,
//   - a variable name that is not valid, that starts with
//     ReservedEnvPrefix, that the credential-variable rule matches
//     (IsCredentialEnv), or more than MaxEnv variables,
//   - a directory or an output path that is absolute or leaves the
//     directory of the code location,
//   - a negative timeout or one above MaxStepTimeout,
//   - a step that changes resources in the plan phase,
//   - an output of an unknown kind.
func ValidateSteps(steps []Step) error {
	if len(steps) == 0 || len(steps) > MaxSteps {
		return fmt.Errorf("%w: a runner job needs 1 to %d steps, got %d", ErrInvalid, MaxSteps, len(steps))
	}

	names := make(map[string]bool, len(steps))
	total := 0

	for i := range steps {
		s := &steps[i]
		if err := s.validate(); err != nil {
			return fmt.Errorf("step %d %q: %w", i, s.Name, err)
		}

		if names[s.Name] {
			return fmt.Errorf("%w: step %d: name %q is not unique", ErrInvalid, i, s.Name)
		}

		names[s.Name] = true

		for _, a := range s.Args {
			total += len(a)
		}
	}

	if total > MaxArgsBytes {
		return fmt.Errorf("%w: the arguments of all steps have %d bytes, above %d", ErrInvalid, total, MaxArgsBytes)
	}

	return nil
}

// validate checks one step without the rules across steps.
func (s *Step) validate() error {
	if err := s.validateFields(); err != nil {
		return err
	}

	if s.Timeout < 0 || s.Timeout > MaxStepTimeout {
		return fmt.Errorf("%w: timeout must be 0 to %v", ErrInvalid, MaxStepTimeout)
	}

	if err := validateArgs(s.Args); err != nil {
		return err
	}

	if err := validateEnv(s.Env); err != nil {
		return err
	}

	return validateOutputs(s.Outputs)
}

// validateFields checks the name, role, phase and directory of one step.
func (s *Step) validateFields() error {
	switch {
	case len(s.Name) > MaxStepName || !stepNamePattern.MatchString(s.Name):
		return fmt.Errorf("%w: name must be 1 to %d lowercase letters, digits and '-'", ErrInvalid, MaxStepName)
	case s.Role != RoleHook && s.Role != RoleTool:
		return fmt.Errorf("%w: unknown role %q", ErrInvalid, s.Role)
	case s.Phase != PhasePlan && s.Phase != PhaseApply:
		return fmt.Errorf("%w: a step runs in the plan or the apply phase, not %q", ErrInvalid, s.Phase)
	case s.Phase == PhasePlan && s.ChangesResources:
		return fmt.Errorf("%w: a step of the plan phase cannot change resources", ErrInvalid)
	case !localPath(s.Dir, true):
		return fmt.Errorf("%w: directory %q must stay in the code directory", ErrInvalid, s.Dir)
	default:
		return nil
	}
}

// validateArgs checks the argument list of one step.
func validateArgs(args []string) error {
	if len(args) == 0 || len(args) > MaxArgs {
		return fmt.Errorf("%w: a step needs 1 to %d arguments, got %d", ErrInvalid, MaxArgs, len(args))
	}

	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("%w: an argument holds a NUL byte", ErrInvalid)
		}
	}

	prog := args[0]
	bare := prog != "" && !strings.Contains(prog, "/")
	absolute := path.IsAbs(prog) && path.Clean(prog) == prog

	if !bare && !absolute {
		return fmt.Errorf("%w: program %q must be a bare name or a clean absolute path", ErrInvalid, prog)
	}

	return nil
}

// validateEnv checks the variables of one step.
func validateEnv(env map[string]string) error {
	if len(env) > MaxEnv {
		return fmt.Errorf("%w: a step sets %d variables, above %d", ErrInvalid, len(env), MaxEnv)
	}

	for name, value := range env {
		switch {
		case !envNamePattern.MatchString(name):
			return fmt.Errorf("%w: variable name %q is not valid", ErrInvalid, name)
		case strings.HasPrefix(strings.ToUpper(name), ReservedEnvPrefix):
			return fmt.Errorf("%w: variable %q uses the reserved prefix %s", ErrInvalid, name, ReservedEnvPrefix)
		case IsCredentialEnv(name):
			return fmt.Errorf("%w: variable %q carries a credential: credentials never pass through a variable",
				ErrInvalid, name)
		case strings.ContainsRune(value, 0):
			return fmt.Errorf("%w: variable %q holds a NUL byte", ErrInvalid, name)
		}
	}

	return nil
}

// validateOutputs checks the outputs of one step.
func validateOutputs(outputs []Output) error {
	for _, o := range outputs {
		if o.Kind != OutputPlanDisplay && o.Kind != OutputPlanApply {
			return fmt.Errorf("%w: unknown output kind %q", ErrInvalid, o.Kind)
		}

		if o.Path == "" || !localPath(o.Path, false) {
			return fmt.Errorf("%w: output path %q must stay in the code directory", ErrInvalid, o.Path)
		}
	}

	return nil
}

// localPath reports whether p is a relative, clean path that stays in its
// base directory. An empty p is local only when allowEmpty is true.
func localPath(p string, allowEmpty bool) bool {
	if p == "" {
		return allowEmpty
	}

	if strings.ContainsRune(p, 0) || path.IsAbs(p) || strings.Contains(p, `\`) {
		return false
	}

	clean := path.Clean(p)

	return clean == p && clean != ".." && !strings.HasPrefix(clean, "../")
}

// credentialNames are the variables that tools and cloud SDKs read a
// credential from. The match ignores case.
//
//nolint:gochecknoglobals // A fixed list, read only.
var credentialNames = []string{
	"PULUMI_ACCESS_TOKEN", "PULUMI_CONFIG_PASSPHRASE", "TF_HTTP_PASSWORD", "TF_CLI_CONFIG_FILE",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "ARM_CLIENT_SECRET",
	"ARM_CLIENT_CERTIFICATE_PASSWORD", "GOOGLE_CREDENTIALS", "GOOGLE_APPLICATION_CREDENTIALS",
	"GITHUB_TOKEN", "VAULT_TOKEN",
}

// credentialPrefixes start the names of variables that carry a credential,
// for example TF_TOKEN_app_example_com.
//
//nolint:gochecknoglobals // A fixed list, read only.
var credentialPrefixes = []string{"TF_TOKEN_"}

// credentialSuffixes end the names of variables that carry a credential by
// convention.
//
//nolint:gochecknoglobals // A fixed list, read only.
var credentialSuffixes = []string{
	"_TOKEN", "_SECRET", "_PASSWORD", "_PASSPHRASE", "_SECRET_KEY", "_ACCESS_KEY", "_PRIVATE_KEY", "_CREDENTIALS",
	"_API_KEY",
}

// IsCredentialEnv is the credential-variable rule. It reports whether a
// step variable called name would carry a credential: a name in a fixed
// list of tool and cloud variables (PULUMI_ACCESS_TOKEN,
// AWS_SECRET_ACCESS_KEY and others), a name that starts with TF_TOKEN_, or
// a name that ends with _TOKEN, _SECRET, _PASSWORD, _PASSPHRASE,
// _SECRET_KEY, _ACCESS_KEY, _PRIVATE_KEY, _CREDENTIALS or _API_KEY. The
// match ignores case. ValidateSteps refuses such a variable: the job token
// and the secrets of a profile reach a tool only through a credentials
// helper or a file.
func IsCredentialEnv(name string) bool {
	upper := strings.ToUpper(name)

	if slices.Contains(credentialNames, upper) {
		return true
	}

	for _, p := range credentialPrefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}

	for _, sfx := range credentialSuffixes {
		if strings.HasSuffix(upper, sfx) {
			return true
		}
	}

	return false
}

// MarkCustomSteps returns a copy of steps from a blueprint or the settings
// of a unit, with ChangesResources set by the server: true for every step
// of the apply phase, false for every step of the plan phase. The value
// that the author of the steps gave does not count. Only the StepPlanner
// of a tool, which is code of the server, marks a step of the apply phase
// as safe.
func MarkCustomSteps(steps []Step) []Step {
	out := slices.Clone(steps)
	for i := range out {
		out[i].ChangesResources = out[i].Phase == PhaseApply
	}

	return out
}

// Limits of the edits of one write-back job.
const (
	MaxEdits       = 64
	MaxEditKeys    = 256
	MaxEditBytes   = 256 << 10
	maxEditKeyPath = 1024
)

// ciPaths are the paths of CI configuration at the root of a repository or
// in any directory. A write-back job never edits a file in them, also when
// the tool allows the name.
//
//nolint:gochecknoglobals // A fixed list, read only.
var ciPaths = []string{
	".github", ".gitlab", ".gitlab-ci.yml", ".circleci", ".buildkite", ".forgejo", ".gitea", ".woodpecker",
	".woodpecker.yml", ".drone.yml", ".travis.yml", "Jenkinsfile", "azure-pipelines.yml", "bitbucket-pipelines.yml",
	".tekton", ".git",
}

// ValidateEdits checks the edits of a write-back job whose code location
// has the directory dir. It returns an error that wraps ErrInvalid and
// names the first edit that fails, for:
//   - no edit, or more than MaxEdits,
//   - a path that fails the rules of a step directory (relative, clean,
//     in the code directory), or that names the same file twice,
//   - a path that editable refuses: only the config and variable files of
//     the unit, as the StepPlanner of its tool tells,
//   - a path in which a part of dir or of the path is CI configuration,
//     such as .github or .gitlab-ci.yml, whatever editable says,
//   - no key, more than MaxEditKeys, an empty key path, or more than
//     MaxEditBytes of values in total.
func ValidateEdits(dir string, edits []FileEdit, editable func(p string) bool) error {
	if len(edits) == 0 || len(edits) > MaxEdits {
		return fmt.Errorf("%w: a write-back job needs 1 to %d edits, got %d", ErrInvalid, MaxEdits, len(edits))
	}

	seen := make(map[string]bool, len(edits))
	total := 0

	for i := range edits {
		e := &edits[i]
		if err := validateEditPath(dir, e.Path, editable); err != nil {
			return fmt.Errorf("edit %d %q: %w", i, e.Path, err)
		}

		if seen[e.Path] {
			return fmt.Errorf("%w: edit %d: file %q is edited twice", ErrInvalid, i, e.Path)
		}

		seen[e.Path] = true

		n, err := validateEditKeys(e.Keys)
		if err != nil {
			return fmt.Errorf("edit %d %q: %w", i, e.Path, err)
		}

		total += n
	}

	if total > MaxEditBytes {
		return fmt.Errorf("%w: the values of all edits have %d bytes, above %d", ErrInvalid, total, MaxEditBytes)
	}

	return nil
}

// validateEditPath checks the path of one edit.
func validateEditPath(dir, p string, editable func(p string) bool) error {
	if p == "" || !localPath(p, false) || !localPath(dir, true) {
		return fmt.Errorf("%w: the path must stay in the code directory", ErrInvalid)
	}

	for part := range strings.SplitSeq(path.Join(dir, p), "/") {
		if slices.Contains(ciPaths, part) {
			return fmt.Errorf("%w: %q is CI configuration, which a write-back job never edits", ErrInvalid, part)
		}
	}

	if editable == nil || !editable(p) {
		return fmt.Errorf("%w: the file is not a config or variable file of the unit", ErrInvalid)
	}

	return nil
}

// validateEditKeys checks the keys of one edit and returns the size of its
// values.
func validateEditKeys(keys map[string][]byte) (int, error) {
	if len(keys) == 0 || len(keys) > MaxEditKeys {
		return 0, fmt.Errorf("%w: an edit needs 1 to %d keys, got %d", ErrInvalid, MaxEditKeys, len(keys))
	}

	n := 0

	for k, v := range keys {
		if k == "" || len(k) > maxEditKeyPath || strings.ContainsRune(k, 0) {
			return 0, fmt.Errorf("%w: key path %q is not valid", ErrInvalid, k)
		}

		n += len(v)
	}

	return n, nil
}
