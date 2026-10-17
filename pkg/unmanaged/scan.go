package unmanaged

import (
	"fmt"
	"path"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of a scan target.
const (
	// MinEvery is the shortest time between two scheduled scans of a target.
	MinEvery = time.Hour
	// MaxEvery is the longest time between two scheduled scans of a target.
	MaxEvery = 30 * 24 * time.Hour
	// MaxTargets bounds the scan targets of a namespace.
	MaxTargets = 100
	// MaxArgs bounds the arguments of the command of a target.
	MaxArgs = 256
	// MaxArgsBytes bounds the size of all the arguments of a command.
	MaxArgsBytes = 128 << 10
	// DefaultScanTimeout is how long a scan can run on a runner when its
	// target gives no timeout.
	DefaultScanTimeout = 2 * time.Hour
	// MaxScanTimeout is the longest timeout of a scan.
	MaxScanTimeout = 24 * time.Hour
	// maxNameLength bounds the name of a target and the ID of a pool.
	maxNameLength = 100
)

// CredentialSource says how the command of a scan gets its cloud
// credentials. The server never stores a cloud credential for a scan.
type CredentialSource string

// The credential sources of a scan.
const (
	// CredentialsRunner is the workload identity of the runner hosts of the
	// pool, for example an instance role, a Kubernetes service account
	// bound to a cloud role, or a managed identity. The server passes no
	// credential. The team gives that identity read-only access.
	CredentialsRunner CredentialSource = "runner"
	// CredentialsProfile is the read-only entry of a profile, which the
	// runner gets through the runner protocol, as a job gets the
	// credentials of its profile.
	CredentialsProfile CredentialSource = "profile"
)

// TargetScope is one account scope that a target covers: an AWS account, an
// Azure subscription, a Google Cloud project or a Kubernetes cluster, and
// the regions to read. An empty Regions means every region.
type TargetScope struct {
	Provider string
	Scope    string
	Regions  []string
}

// Target is a scan target: what to scan, on which runner pool, with which
// command and credentials, and how often. Command is an argument list that
// the launcher of the runner starts with no shell, as it starts a job step.
// It writes the scan report to the file that the runner names in the
// variable DUSTPAN_SCAN_REPORT. Every is zero for a target that scans only
// on request.
type Target struct {
	Namespace   namespace.Name
	Name        string
	Pool        string
	Credentials CredentialSource
	Profile     string
	Scopes      []TargetScope
	Command     []string
	Every       time.Duration
	Timeout     time.Duration
	Enabled     bool
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

	if err := t.checkCredentials(); err != nil {
		return err
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

// checkCredentials reports whether t has known credentials, with a profile
// for profile credentials only. It returns ErrInvalidTarget.
func (t Target) checkCredentials() error {
	switch {
	case t.Credentials == CredentialsRunner && t.Profile != "":
		return fmt.Errorf("%w: runner credentials take no profile", ErrInvalidTarget)
	case t.Credentials == CredentialsProfile && !ident.Valid(t.Profile, maxNameLength):
		return fmt.Errorf("%w: profile credentials name a profile", ErrInvalidTarget)
	case t.Credentials != CredentialsRunner && t.Credentials != CredentialsProfile:
		return fmt.Errorf("%w: credentials %q", ErrInvalidTarget, t.Credentials)
	}

	return nil
}

// checkScopes reports whether t has 1 to MaxReportScopes valid scopes. It
// returns ErrInvalidTarget.
func (t Target) checkScopes() error {
	if len(t.Scopes) == 0 || len(t.Scopes) > MaxReportScopes {
		return fmt.Errorf("%w: 1 to %d scopes", ErrInvalidTarget, MaxReportScopes)
	}

	for _, s := range t.Scopes {
		if !validProvider(s.Provider) || s.Scope == "" || !shortText(s.Scope) {
			return fmt.Errorf("%w: scope %q of provider %q", ErrInvalidTarget, s.Scope, s.Provider)
		}
	}

	return nil
}

// checkCommand applies the rules of a step to the command of a target: 1 to
// MaxArgs arguments, MaxArgsBytes in all, no NUL byte, and a program that is
// a bare name or a clean absolute path.
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

// ScanKind says what a scan reads.
type ScanKind string

// The kinds of scan.
const (
	// ScanCloud is a scan of the cloud on a runner, with a scan report.
	ScanCloud ScanKind = "cloud"
	// ScanState is a check of the resource index alone, in the server. It
	// finds resources left behind and managed twice.
	ScanState ScanKind = "state"
)

// ScanStatus is where a scan is in its life.
type ScanStatus string

// The statuses of a scan.
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

// ScanEvent is something that happens to a scan.
type ScanEvent string

// The events of a scan.
const (
	// ScanEventDispatch starts the durable operation that a runner of the
	// pool claims.
	ScanEventDispatch ScanEvent = "dispatch"
	// ScanEventClaim is the claim of a runner, or a takeover by another
	// runner after a lost claim.
	ScanEventClaim ScanEvent = "claim"
	// ScanEventRelease is a runner that gives the scan back before an
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
	// ScanEventExpire is a scan past its timeout.
	ScanEventExpire ScanEvent = "expire"
)

// scanTransitions returns the state table of a scan. NextScan is the only
// place that reads it. A scan of the kind ScanState goes from queued to
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

// NextScan returns the status after ev in status s of a scan of kind k. It
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
		return s, fmt.Errorf("%w: %s on a %s scan that is %s", ErrTransition, ev, k, s)
	}

	to, ok := scanTransitions()[s][ev]
	if !ok {
		return s, fmt.Errorf("%w: %s on a scan that is %s", ErrTransition, ev, s)
	}

	return to, nil
}

// ScanCounts are the counts of a scan that succeeded.
type ScanCounts struct {
	Records      int64
	Managed      int64
	Elsewhere    int64
	NotInAnyUnit int64
	LeftBehind   int64
	ManagedTwice int64
	Resolved     int64
	Rejected     int64
}

// Scan is one scan of one target, or one state check of a namespace.
// Target is empty for a state check. Scopes holds the scopes of the header
// of the report, after the upload. Reason is the reason of a failure.
// StartedBy is the principal subject of a person who started it, or empty
// for a scan from the schedule.
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
