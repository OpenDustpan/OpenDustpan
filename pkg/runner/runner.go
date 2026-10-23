// Package runner defines runner pools, runners and the rules of the runner
// protocol: the identities and tokens of a runner, the skew between the
// releases of the server and of a runner, and the ports of the
// coordinators that start a launcher for each claimed phase or sweep.
//
// A runner pool belongs to one namespace and has a fixed purpose: it runs
// the phases of jobs, or it runs sweeps with a read-only identity. A runner
// belongs to one pool. It is a long-lived process that the team hosts: a
// coordinator of the kind kubernetes, which starts one Kubernetes Job for
// each claimed phase, or of the kind machine, which starts one sandbox for
// each claimed phase on its own host. Inside the Job or the sandbox, the
// launcher runs the steps as argument lists, with no shell.
//
// The package is tool-neutral. It never imports a store adapter, a
// transport or a tool package. The server side of the protocol lives in an
// HTTP adapter, and calls the ports of this package and of package job.
package runner

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	// ErrInvalid reports a request with an empty or bad field.
	ErrInvalid = errors.New("runner request is not valid")
	// ErrNotFound reports a pool, a runner or a token that the store does
	// not know, or that the caller cannot see.
	ErrNotFound = errors.New("runner pool, runner or token not found")
	// ErrAlreadyExists reports a pool name that the namespace already has.
	ErrAlreadyExists = errors.New("runner pool already exists")
	// ErrImmutable reports a change to a field that cannot change after the
	// creation of a pool: its purpose and its namespace.
	ErrImmutable = errors.New("this field of the runner pool cannot change")
	// ErrPoolInUse reports a delete of a pool that a job that did not end,
	// or a sweep target, still names.
	ErrPoolInUse = errors.New("runner pool is in use")
	// ErrLimit reports a create above a limit of this package.
	ErrLimit = errors.New("too many runner pools, runners or join tokens")
	// ErrTokenUsed reports a join token or a handoff token that a request
	// already used. Each is good for one use.
	ErrTokenUsed = errors.New("token is already used")
	// ErrTokenExpired reports a join token or a handoff token after its
	// expiry.
	ErrTokenExpired = errors.New("token is expired")
	// ErrKeyExpired reports a runner key older than MaxKeyAge. The runner
	// must join again with a new join token.
	ErrKeyExpired = errors.New("runner key is expired")
	// ErrBadProof reports a request whose proof of possession does not
	// match the key of the runner or of the job session: a bad signature, a
	// time outside ProofWindow, or a body hash that does not match.
	ErrBadProof = errors.New("proof of possession is not valid")
	// ErrRetired reports a request of a runner that an administrator
	// retired.
	ErrRetired = errors.New("runner is retired")
	// ErrPurpose reports work for a pool of the other purpose: a job that
	// names a pool for sweeps, or a sweep target that names a pool for jobs.
	ErrPurpose = errors.New("runner pool has another purpose")
	// ErrConflict reports a request that conflicts with the state of the
	// store: a handoff token for a claim that has a job session already.
	ErrConflict = errors.New("runner request conflicts with the state of the store")
	// ErrRunnerOnline reports a join with the name of a runner that is not
	// offline and whose key is not expired. A join again is only for an
	// offline runner or a runner that must join again (Runner.CanRejoin).
	ErrRunnerOnline = errors.New("a runner with this name is online")
	// ErrReplay reports a proof whose nonce the server saw before for the
	// same subject, on a path that NeedsNonce names.
	ErrReplay = errors.New("proof of possession is a replay")
	// ErrSessionOpen reports a renewal by the runner of a claim whose
	// launcher opened its job session. The launcher renews the claim from
	// then on. The runner stops its renewals and leaves the Job or the
	// sandbox alone. It is not a lost claim.
	ErrSessionOpen = errors.New("the launcher of the claim holds a job session")
	// ErrClaimLost reports a handoff token, a session or a write for a
	// claim that is no longer the live claim of its operation. It wraps
	// durable.ErrClaimLost.
	ErrClaimLost = fmt.Errorf("claim of the runner is lost: %w", durable.ErrClaimLost)
	// ErrSessionWaitPassed reports a renewal of a claim that has no job
	// session after MaxSessionWait. The claim is lost. The store changes
	// nothing: the caller releases the claim through the job.Claimer or
	// the sweep claimer of the purpose of its pool.
	ErrSessionWaitPassed = fmt.Errorf("%w: no job session within the session wait limit", ErrClaimLost)
	// ErrVersionUnsupported reports a runner or a launcher release outside
	// the skew that the server accepts (CheckSkew).
	ErrVersionUnsupported = errors.New("runner release is not supported by this server")
	// ErrVersionAhead reports a runner or a launcher release that is newer
	// than the server, for example during a rolling deploy or after a
	// rollback of the server. The caller retries with backoff for
	// VersionAheadLimit at most (CheckVersionAhead). It does not wrap
	// ErrVersionUnsupported.
	ErrVersionAhead = errors.New("runner release is newer than this server")
)

// ID prefixes of this package. The server makes each ID with a random part
// after the prefix.
const (
	// PoolIDPrefix starts the ID of a runner pool.
	PoolIDPrefix = "rp-"
	// RunnerIDPrefix starts the ID of a runner.
	RunnerIDPrefix = "rn-"
	// JoinTokenIDPrefix starts the ID of a join token. The ID is not the
	// secret.
	JoinTokenIDPrefix = "rjt-"
	// SessionIDPrefix starts the ID of a job session.
	SessionIDPrefix = "rjs-"
)

// Purpose is what the runners of a pool run. It is fixed at the creation of
// the pool.
type Purpose string

// The purposes of a pool.
const (
	// PurposeJobs runs the phases of runner jobs. A runner of the pool
	// claims only the durable kind job.RunnerKind of its pool.
	PurposeJobs Purpose = "jobs"
	// PurposeSweeps runs sweeps of unmanaged resources with a read-only
	// identity. A runner of the pool claims only SweepKind of its pool. No
	// job can name the pool.
	PurposeSweeps Purpose = "sweeps"
)

// Valid reports whether p is one of the purposes of this package.
func (p Purpose) Valid() bool {
	return p == PurposeJobs || p == PurposeSweeps
}

// SweepKindPrefix starts the durable kind of a sweep for a pool. The kind of a
// sweep for the pool with the ID p is SweepKindPrefix + p.
const SweepKindPrefix = "unmanaged.sweep."

// SweepKind returns the durable kind of the sweeps of the pool with the ID
// pool.
func SweepKind(pool string) durable.Kind {
	return durable.Kind(SweepKindPrefix + pool)
}

// ClaimKinds returns the durable kinds that a runner of p claims: the kind
// of the phases of the pool for PurposeJobs, and the kind of its sweeps for
// PurposeSweeps. It returns ErrInvalid for an unknown purpose. A runner never
// sends kinds: the server takes them from the pool of the runner, so a
// runner cannot claim the work of another pool or of the other purpose.
func ClaimKinds(p *Pool) ([]durable.Kind, error) {
	switch p.Purpose {
	case PurposeJobs:
		return []durable.Kind{job.RunnerKind(p.ID)}, nil
	case PurposeSweeps:
		return []durable.Kind{SweepKind(p.ID)}, nil
	default:
		return nil, fmt.Errorf("%w: unknown purpose %q", ErrInvalid, p.Purpose)
	}
}

// CheckJobPool checks that a runner job may name p: p belongs to the
// namespace ns of the unit and has PurposeJobs. It returns ErrInvalid for a
// pool of another namespace and ErrPurpose for a pool for sweeps. The runner
// executor calls it from job.Executor.Phases.
func CheckJobPool(p *Pool, ns namespace.Name) error {
	if p.Namespace != ns {
		return fmt.Errorf("%w: pool %s belongs to namespace %s, not %s", ErrInvalid, p.ID, p.Namespace, ns)
	}

	if p.Purpose != PurposeJobs {
		return fmt.Errorf("%w: pool %s runs %s, not jobs", ErrPurpose, p.ID, p.Purpose)
	}

	return nil
}

// Limits of pools, runners and tokens. Installation settings can change
// them later.
const (
	// MaxPoolsPerNamespace bounds the pools of one namespace.
	MaxPoolsPerNamespace = 50
	// MaxRunnersPerPool bounds the runners of one pool that are not
	// retired.
	MaxRunnersPerPool = 1000
	// MaxOpenJoinTokens bounds the join tokens of one pool that are not
	// used, revoked or expired.
	MaxOpenJoinTokens = 100
	// DefaultConcurrency is the number of phases or sweeps that one runner
	// runs at a time, unless the pool sets more.
	DefaultConcurrency = 1
	// MaxConcurrency bounds the concurrency of a runner.
	MaxConcurrency = 64
	// MaxNameLength bounds the name of a pool and of a runner.
	MaxNameLength = 63
	// MaxLabels bounds the labels of a runner.
	MaxLabels = 32
)

// Leases of a claim. The lease of a pool is the time from a claim or a
// renewal to its expiry. A holder renews after a third of the lease, so one
// lost renewal does not lose the claim.
const (
	// DefaultLease is the lease of a pool that sets none.
	DefaultLease = 90 * time.Second
	// MinLease is the shortest lease of a pool.
	MinLease = 30 * time.Second
	// MaxLease is the longest lease of a pool. A longer lease makes a
	// takeover after a lost runner slower.
	MaxLease = 10 * time.Minute
)

// renewParts is the number of renewals in one lease.
const renewParts = 3

// RenewEvery returns the time between two renewals of a claim with the
// lease d: a third of d.
func RenewEvery(d time.Duration) time.Duration {
	return d / renewParts
}

// Pool is a runner pool: the runners of one namespace that run one purpose.
type Pool struct {
	// ID has the prefix PoolIDPrefix.
	ID        string
	Namespace namespace.Name
	// Name is unique in the namespace: lowercase letters, digits and '-',
	// at most MaxNameLength.
	Name string
	// Purpose is fixed at creation. A change fails with ErrImmutable.
	Purpose Purpose
	// Lease is the lease of each claim of a runner of the pool, from
	// MinLease to MaxLease. Zero takes DefaultLease.
	Lease time.Duration
	// Concurrency is the number of phases or sweeps that each runner of the
	// pool runs at a time, from 1 to MaxConcurrency. Zero takes
	// DefaultConcurrency.
	Concurrency int
	// Isolation lists the isolation kinds that a runner of the pool may
	// use. A runner that joins with another kind fails with ErrInvalid.
	// Empty allows every kind but IsolationProcess.
	Isolation []Isolation
	// GitCredentials tells what a step gets when its tool runs git, for
	// example to get an OpenTofu module.
	GitCredentials GitCredentials
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ReadOnly reports whether the runners of p run with a read-only identity:
// a pool for sweeps. The console and the guide tell the team to give the
// hosts of such a pool a role that cannot write. The server cannot check
// the role of a host.
func (p *Pool) ReadOnly() bool {
	return p.Purpose == PurposeSweeps
}

// EffectiveLease returns the lease of p, with DefaultLease for zero.
func (p *Pool) EffectiveLease() time.Duration {
	if p.Lease == 0 {
		return DefaultLease
	}

	return p.Lease
}

// EffectiveConcurrency returns the concurrency of p, with
// DefaultConcurrency for zero.
func (p *Pool) EffectiveConcurrency() int {
	if p.Concurrency == 0 {
		return DefaultConcurrency
	}

	return p.Concurrency
}

// Allows reports whether a runner of p may use the isolation kind k.
func (p *Pool) Allows(k Isolation) bool {
	if len(p.Isolation) == 0 {
		return k.Valid() && k != IsolationProcess
	}

	return slices.Contains(p.Isolation, k)
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidName reports whether s is a valid name of a pool or a runner.
func ValidName(s string) bool {
	return len(s) <= MaxNameLength && namePattern.MatchString(s)
}

// Validate checks the fields of p that a caller sets. It returns an error
// that wraps ErrInvalid for a name that is not valid, an unknown purpose, a
// lease outside MinLease to MaxLease, a concurrency outside 1 to
// MaxConcurrency, an unknown isolation kind, or an unknown git credential
// mode, or a custom git credential helper that is not a bare
// git-credential- name or a clean absolute path.
func (p *Pool) Validate() error {
	switch {
	case !ValidName(p.Name):
		return fmt.Errorf("%w: pool name must be 1 to %d lowercase letters, digits and '-'", ErrInvalid, MaxNameLength)
	case !p.Purpose.Valid():
		return fmt.Errorf("%w: unknown purpose %q", ErrInvalid, p.Purpose)
	case p.Lease != 0 && (p.Lease < MinLease || p.Lease > MaxLease):
		return fmt.Errorf("%w: lease must be %v to %v", ErrInvalid, MinLease, MaxLease)
	case p.Concurrency < 0 || p.Concurrency > MaxConcurrency:
		return fmt.Errorf("%w: concurrency must be 1 to %d", ErrInvalid, MaxConcurrency)
	}

	for _, k := range p.Isolation {
		if !k.Valid() {
			return fmt.Errorf("%w: unknown isolation %q", ErrInvalid, k)
		}
	}

	return p.GitCredentials.Validate()
}

// CheckUpdate checks a change of the pool old to next. It returns
// ErrImmutable for a change of the ID, the namespace or the purpose, and
// the error of next.Validate otherwise.
func CheckUpdate(old, next *Pool) error {
	switch {
	case old.ID != next.ID, old.Namespace != next.Namespace:
		return fmt.Errorf("%w: the ID and the namespace of a pool never change", ErrImmutable)
	case old.Purpose != next.Purpose:
		return fmt.Errorf("%w: the purpose of pool %s is %s and never changes: create a pool for %s",
			ErrImmutable, old.ID, old.Purpose, next.Purpose)
	default:
		return next.Validate()
	}
}

// GitCredentialMode tells what a step gets when its tool runs git.
type GitCredentialMode string

// The git credential modes.
const (
	// GitCredentialsNone gives no git credential to a step. A tool that
	// runs git reaches only public repositories. It is the default.
	GitCredentialsNone GitCredentialMode = "none"
	// GitCredentialsForge makes the launcher the git credential helper of
	// the step. It answers a short-lived read-only token of the forge
	// connection of the job, for the hosts of that connection only.
	GitCredentialsForge GitCredentialMode = "forge"
	// GitCredentialsCustom names a credential helper program of the team,
	// in the image of the step. Git starts it with no shell.
	GitCredentialsCustom GitCredentialMode = "custom"
)

// GitCredentials is the git credential setting of a pool.
type GitCredentials struct {
	// Mode is empty or GitCredentialsNone for no credential.
	Mode GitCredentialMode
	// Helper is the program of GitCredentialsCustom: a bare name that
	// starts with git-credential-, or a clean absolute path. Git config
	// gives a helper that starts with '!' to a shell, so such a value is
	// refused.
	Helper string
}

// Validate returns an error that wraps ErrInvalid for an unknown mode, a
// helper with a mode other than GitCredentialsCustom, or a helper that is
// not a bare git-credential- name or a clean absolute path.
func (g GitCredentials) Validate() error {
	switch g.Mode {
	case "", GitCredentialsNone, GitCredentialsForge:
		if g.Helper != "" {
			return fmt.Errorf("%w: a git credential helper needs the mode %s", ErrInvalid, GitCredentialsCustom)
		}

		return nil
	case GitCredentialsCustom:
		return validateHelper(g.Helper)
	default:
		return fmt.Errorf("%w: unknown git credential mode %q", ErrInvalid, g.Mode)
	}
}

// validateHelper checks the program of a custom git credential helper.
func validateHelper(h string) error {
	// Defense in depth: launcher.GitConfig refuses a control character too;
	// this check stays so that a pool never stores a helper that could add a
	// line to the git configuration of a step.
	if strings.ContainsFunc(h, isControl) {
		return fmt.Errorf("%w: git credential helper must not hold a control character", ErrInvalid)
	}

	bare := strings.HasPrefix(h, "git-credential-") && len(h) > len("git-credential-") &&
		!strings.ContainsAny(h, "/ \t!")
	absolute := strings.HasPrefix(h, "/") && cleanAbs(h) && !strings.ContainsAny(h, " \t!")

	if !bare && !absolute {
		return fmt.Errorf("%w: git credential helper %q must be a bare git-credential- name or a clean absolute path",
			ErrInvalid, h)
	}

	return nil
}

// cleanAbs reports whether p is an absolute path with no empty, '.' or '..'
// element and no trailing '/'.
func cleanAbs(p string) bool {
	if len(p) < 2 || strings.HasSuffix(p, "/") || strings.ContainsRune(p, 0) {
		return false
	}

	for part := range strings.SplitSeq(p[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}

	return true
}

// isControl reports whether r is a control character of the C0 range
// (0x00 to 0x1F) or DEL (0x7F).
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}
