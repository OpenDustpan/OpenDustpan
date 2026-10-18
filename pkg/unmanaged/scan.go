package unmanaged

import (
	"fmt"
	"hash/fnv"
	"path"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of a sweep target.
const (
	// MinEvery is the shortest time between two scheduled sweeps of a target.
	MinEvery = time.Hour
	// MaxEvery is the longest time between two scheduled sweeps of a target.
	MaxEvery = 30 * 24 * time.Hour
	// MaxTargets bounds the sweep targets of a namespace.
	MaxTargets = 100
	// MaxArgs bounds the arguments of the command of a target.
	MaxArgs = 256
	// MaxArgsBytes bounds the size of all the arguments of a command.
	MaxArgsBytes = 128 << 10
	// DefaultScanTimeout is how long a sweep can run on a runner when its
	// target gives no timeout.
	DefaultScanTimeout = 2 * time.Hour
	// MaxScanTimeout is the longest timeout of a sweep.
	MaxScanTimeout = 24 * time.Hour
	// maxNameLength bounds the name of a target and the ID of a pool.
	maxNameLength = 100
	// ScheduleTick is the period of the schedule row unmanaged.schedule. It
	// starts the sweeps whose time came, the daily state checks, and ends the
	// ignores and the pending actions whose time came.
	ScheduleTick = 15 * time.Minute
	// StateCheckPageTarget is the time that the state check aims at for one
	// page of MaxLookup contested keys: the read of the page from the index
	// and the write of its findings. A page above it logs a warning with the
	// namespace and the time, so an operator sees a slow index.
	StateCheckPageTarget = 2 * time.Second
)

// StateCheckOffset returns when, after midnight UTC, the daily state check of
// namespace ns starts. It hashes the name into the day, in steps of
// ScheduleTick, so the state checks of an installation with many namespaces
// spread over the day and do not all start at one tick. The offset of a
// namespace never changes, so its state check runs at the same time each day.
func StateCheckOffset(ns namespace.Name) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ns.String()))

	ticks := uint64(day / ScheduleTick)

	return time.Duration(h.Sum64()%ticks) * ScheduleTick
}

// TargetScope is one account scope that a target covers: an AWS account, an
// Azure subscription, a Google Cloud project or a Kubernetes cluster, and
// the regions to read. An empty Regions means every region.
type TargetScope struct {
	Provider string
	Scope    string
	Regions  []string
}

// Target is a sweep target: what to sweep, on which runner pool, with which
// command, and how often. A target names no credential: the command reads
// the cloud with the workload identity of the runner hosts of the pool, for
// example an instance role, a Kubernetes service account bound to a cloud
// role, or a managed identity. The server holds no cloud credential for a
// sweep and passes none. Command is an argument list that the launcher of
// the runner starts with no shell, as it starts a job step. It holds no
// secret (checkCommand). It writes the sweep report to the file that the
// runner names in the variable DUSTPAN_SWEEP_REPORT. Every is zero for a
// target that sweeps only on request.
type Target struct {
	Namespace namespace.Name
	Name      string
	Pool      string
	Scopes    []TargetScope
	Command   []string
	Every     time.Duration
	Timeout   time.Duration
	Enabled   bool
}

// Validate reports whether t is a valid target. It returns ErrInvalidTarget.
// It does not check that the pool belongs to the namespace of the target;
// the store checks that in its transaction.
func (t Target) Validate() error {
	if _, err := namespace.ParseName(t.Namespace.String()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidTarget, err)
	}

	switch {
	case !ident.Valid(t.Name, maxNameLength):
		return fmt.Errorf("%w: name %q", ErrInvalidTarget, t.Name)
	case !ident.Valid(t.Pool, maxNameLength):
		return fmt.Errorf("%w: a target names a runner pool", ErrInvalidTarget)
	}

	switch {
	case t.Every != 0 && (t.Every < MinEvery || t.Every > MaxEvery):
		return fmt.Errorf("%w: a schedule is from 1 hour to 30 days", ErrInvalidTarget)
	case t.Timeout < 0 || t.Timeout > MaxScanTimeout:
		return fmt.Errorf("%w: a timeout is at most 24 hours", ErrInvalidTarget)
	}

	if err := t.checkScopes(); err != nil {
		return err
	}

	return checkCommand(t.Command)
}

// checkScopes reports whether t has 1 to MaxReportScopes valid scopes. It
// returns ErrInvalidTarget.
func (t Target) checkScopes() error {
	if len(t.Scopes) == 0 || len(t.Scopes) > MaxReportScopes {
		return fmt.Errorf("%w: 1 to %d scopes", ErrInvalidTarget, MaxReportScopes)
	}

	seen := map[[2]string]bool{}

	for _, s := range t.Scopes {
		if !validProvider(s.Provider) || s.Scope == "" || !shortText(s.Scope) {
			return fmt.Errorf("%w: scope %q of provider %q", ErrInvalidTarget, s.Scope, s.Provider)
		}

		if seen[[2]string{s.Provider, s.Scope}] {
			return fmt.Errorf("%w: scope %q of provider %q twice", ErrInvalidTarget, s.Scope, s.Provider)
		}

		seen[[2]string{s.Provider, s.Scope}] = true

		for _, r := range s.Regions {
			if r == "" || !shortText(r) {
				return fmt.Errorf("%w: a region of scope %q", ErrInvalidTarget, s.Scope)
			}
		}
	}

	return nil
}

// checkCommand applies the rules of a step to the command of a target: 1 to
// MaxArgs arguments, MaxArgsBytes in all, no NUL byte, a program that is a
// bare name or a clean absolute path, and no secret (CredentialArg).
func checkCommand(args []string) error {
	if len(args) == 0 || len(args) > MaxArgs {
		return fmt.Errorf("%w: a command has 1 to %d arguments", ErrInvalidTarget, MaxArgs)
	}

	total := 0

	for _, a := range args {
		total += len(a)
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("%w: an argument holds a NUL byte", ErrInvalidTarget)
		}

		if CredentialArg(a) {
			return fmt.Errorf("%w: an argument names a credential; a sweep reads the cloud with the identity of "+
				"the runner", ErrInvalidTarget)
		}
	}

	if total > MaxArgsBytes {
		return fmt.Errorf("%w: the arguments are above %d bytes", ErrInvalidTarget, MaxArgsBytes)
	}

	prog := args[0]
	bare := prog != "" && !strings.Contains(prog, "/")

	if !bare && (!path.IsAbs(prog) || path.Clean(prog) != prog) {
		return fmt.Errorf("%w: the program is a bare name or a clean absolute path", ErrInvalidTarget)
	}

	return nil
}

// credentialNames are the names that the credential-variable rule of a job
// step matches in full, in upper case with "_" for "-".
func credentialNames() []string {
	return []string{
		"PULUMI_ACCESS_TOKEN", "PULUMI_CONFIG_PASSPHRASE", "TF_HTTP_PASSWORD", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "ARM_CLIENT_SECRET", "GOOGLE_CREDENTIALS", "PASSWORD", "TOKEN", "SECRET",
		"PASSPHRASE", "API_KEY", "ACCESS_KEY", "SECRET_KEY", "PRIVATE_KEY", "CREDENTIALS",
	}
}

// credentialSuffixes are the suffixes that the credential-variable rule of a
// job step matches.
func credentialSuffixes() []string {
	return []string{
		"_TOKEN", "_SECRET", "_PASSWORD", "_PASSPHRASE", "_SECRET_KEY", "_ACCESS_KEY", "_PRIVATE_KEY",
		"_CREDENTIALS", "_API_KEY",
	}
}

// CredentialArg reports whether the argument a of a command names a
// credential, by the credential-variable rule of a job step: a flag such as
// "--password" or "--api-key=...", or an assignment such as
// "AWS_SECRET_ACCESS_KEY=...". The match ignores case, and "-" matches "_".
// A target with such an argument is refused, so the stored command, its
// audit entries and the log of the runner never hold a secret. The rule
// mirrors job.IsCredentialEnv until that package lands, and then calls it.
func CredentialArg(a string) bool {
	name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
	name = strings.ToUpper(strings.ReplaceAll(name, "-", "_"))

	if name == "" || strings.ContainsAny(name, " /:.") {
		return false
	}

	if slices.Contains(credentialNames(), name) || strings.HasPrefix(name, "TF_TOKEN_") {
		return true
	}

	return slices.ContainsFunc(credentialSuffixes(), func(sfx string) bool { return strings.HasSuffix(name, sfx) })
}

// ScanKind says what a sweep reads.
type ScanKind string

// The kinds of sweep.
const (
	// ScanCloud is a sweep of the cloud on a runner, with a sweep report.
	ScanCloud ScanKind = "cloud"
	// ScanState is a check of the resource index alone, in the server. It
	// finds resources left behind and managed twice.
	ScanState ScanKind = "state"
)

// ScanStatus is where a sweep is in its life.
type ScanStatus string

// The statuses of a sweep.
const (
	ScanQueued     ScanStatus = "queued"
	ScanDispatched ScanStatus = "dispatched"
	ScanRunning    ScanStatus = "running"
	ScanMatching   ScanStatus = "matching"
	ScanSucceeded  ScanStatus = "succeeded"
	ScanFailed     ScanStatus = "failed"
	ScanCanceled   ScanStatus = "canceled"
	ScanExpired    ScanStatus = "expired"
)

// Terminal reports whether s is an end status.
func (s ScanStatus) Terminal() bool {
	return s == ScanSucceeded || s == ScanFailed || s == ScanCanceled || s == ScanExpired
}

// ScanEvent is something that happens to a sweep.
type ScanEvent string

// The events of a sweep.
const (
	// ScanEventDispatch starts the durable operation that a runner of the
	// pool claims.
	ScanEventDispatch ScanEvent = "dispatch"
	// ScanEventClaim is the claim of a runner, or a takeover by another
	// runner after a lost claim.
	ScanEventClaim ScanEvent = "claim"
	// ScanEventRelease is a runner that gives the sweep back before an
	// upload.
	ScanEventRelease ScanEvent = "release"
	// ScanEventUploaded is the end of the upload of the report. The match
	// starts in the server.
	ScanEventUploaded ScanEvent = "uploaded"
	// ScanEventMatched is the end of the match. The findings are written.
	ScanEventMatched ScanEvent = "matched"
	// ScanEventFail is a command that failed, a report that did not parse,
	// or a match that failed.
	ScanEventFail ScanEvent = "fail"
	// ScanEventCancel is a cancel by a person.
	ScanEventCancel ScanEvent = "cancel"
	// ScanEventExpire is a sweep past its timeout.
	ScanEventExpire ScanEvent = "expire"
)

// scanTransitions returns the state table of a sweep. NextScan is the only
// place that reads it. A sweep of the kind ScanState goes from queued to
// matching with the event uploaded, because it has no runner and no report.
func scanTransitions() map[ScanStatus]map[ScanEvent]ScanStatus {
	return map[ScanStatus]map[ScanEvent]ScanStatus{
		ScanQueued: {
			ScanEventDispatch: ScanDispatched, ScanEventUploaded: ScanMatching, ScanEventCancel: ScanCanceled,
			ScanEventExpire: ScanExpired,
		},
		ScanDispatched: {
			ScanEventClaim: ScanRunning, ScanEventCancel: ScanCanceled, ScanEventExpire: ScanExpired,
		},
		ScanRunning: {
			ScanEventClaim: ScanRunning, ScanEventRelease: ScanDispatched, ScanEventUploaded: ScanMatching,
			ScanEventFail: ScanFailed, ScanEventCancel: ScanCanceled, ScanEventExpire: ScanExpired,
		},
		ScanMatching: {
			ScanEventMatched: ScanSucceeded, ScanEventFail: ScanFailed,
		},
	}
}

// NextScan returns the status after ev in status s of a sweep of kind k. It
// returns ErrTransition for an event that the table does not allow, for any
// event on an end status, and for a state check that would leave the server:
// it is never dispatched to a runner. A cancel during the match is not allowed: the
// match only reads the report and writes findings, and ends within its own
// limits.
func NextScan(k ScanKind, s ScanStatus, ev ScanEvent) (ScanStatus, error) {
	runnerOnly := s == ScanDispatched || s == ScanRunning || ev == ScanEventDispatch
	stateOnRunner := k == ScanState && runnerOnly
	cloudSkip := k == ScanCloud && s == ScanQueued && ev == ScanEventUploaded

	if stateOnRunner || cloudSkip {
		return s, fmt.Errorf("%w: %s on a %s sweep that is %s", ErrTransition, ev, k, s)
	}

	to, ok := scanTransitions()[s][ev]
	if !ok {
		return s, fmt.Errorf("%w: %s on a sweep that is %s", ErrTransition, ev, s)
	}

	return to, nil
}

// ScanCounts are the counts of a sweep that succeeded. Rejected counts the
// records that did not parse. OutOfScope counts the records that RecordScope
// put outside every scope of the header; the match drops them.
type ScanCounts struct {
	Records      int64
	Managed      int64
	Elsewhere    int64
	NotInAnyUnit int64
	LeftBehind   int64
	ManagedTwice int64
	Resolved     int64
	Rejected     int64
	OutOfScope   int64
}

// Scan is one sweep of one target, or one state check of a namespace.
// Target is empty for a state check. Scopes holds the scopes of the header
// of the report, after the upload. Reason is the reason of a failure.
// StartedBy is the principal subject of a person who started it, or empty
// for a sweep from the schedule.
type Scan struct {
	CreatedAt time.Time
	EndedAt   time.Time
	ID        string
	Namespace namespace.Name
	Target    string
	Kind      ScanKind
	Status    ScanStatus
	StartedBy string
	Reason    string
	Scopes    []ReportScope
	Counts    ScanCounts
}
