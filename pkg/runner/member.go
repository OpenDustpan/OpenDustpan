package runner

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Kind names how a runner starts the launcher of each claimed phase or
// scan. Every runner is a coordinator: it claims work for its pool and
// starts a launcher in an isolated place. It never runs a step itself.
type Kind string

// The kinds of runner.
const (
	// KindKubernetes creates one Kubernetes Job for each claimed phase or
	// scan, with backoffLimit 0.
	KindKubernetes Kind = "kubernetes"
	// KindMachine starts one sandbox for each claimed phase or scan on its
	// own host.
	KindMachine Kind = "machine"
)

// Valid reports whether k is one of the kinds of this package.
func (k Kind) Valid() bool {
	return k == KindKubernetes || k == KindMachine
}

// Isolation names the place where a coordinator starts a launcher.
type Isolation string

// The isolation kinds.
const (
	// IsolationKubernetes is a pod of a Kubernetes Job. The coordinator of
	// the kind KindKubernetes uses it, and the runtime class of the pod
	// template decides more, for example gVisor.
	IsolationKubernetes Isolation = "kubernetes"
	// IsolationFirecracker is a Firecracker microVM on the host of a
	// machine coordinator. The host needs /dev/kvm.
	IsolationFirecracker Isolation = "firecracker"
	// IsolationGVisor is a container in the gVisor runtime on the host of a
	// machine coordinator.
	IsolationGVisor Isolation = "gvisor"
	// IsolationProcess is a child process of the machine coordinator in
	// its own namespaces, with no kernel boundary. It is weaker than the
	// other kinds and is for tests. A pool allows it only when its
	// Isolation lists it.
	IsolationProcess Isolation = "process"
)

// Valid reports whether k is one of the isolation kinds of this package.
func (k Isolation) Valid() bool {
	switch k {
	case IsolationKubernetes, IsolationFirecracker, IsolationGVisor, IsolationProcess:
		return true
	default:
		return false
	}
}

// Fits reports whether a runner of the kind r can use the isolation k: a
// Kubernetes coordinator uses IsolationKubernetes only, and a machine
// coordinator uses the other kinds.
func (k Isolation) Fits(r Kind) bool {
	switch r {
	case KindKubernetes:
		return k == IsolationKubernetes
	case KindMachine:
		return k == IsolationFirecracker || k == IsolationGVisor || k == IsolationProcess
	default:
		return false
	}
}

// Handoff names how a coordinator gives the handoff token to the launcher.
// The handoff token never passes through an environment variable, a
// command-line argument or a file on a disk.
type Handoff string

// The handoffs of a handoff token.
const (
	// HandoffKubernetes: the launcher sends its projected service account
	// token to the coordinator. The coordinator checks it with a Kubernetes
	// TokenReview, checks that the pod belongs to the Job of the claim,
	// and answers the handoff token once.
	HandoffKubernetes Handoff = "kubernetes"
	// HandoffStdin: the coordinator writes the handoff token to the
	// standard input of the launcher, and closes it. A container runtime
	// and a microVM console use it.
	HandoffStdin Handoff = "stdin"
	// HandoffPipe: the coordinator writes the handoff token to a pipe that
	// the launcher inherits as file descriptor 3. The launcher reads it
	// once and closes it.
	HandoffPipe Handoff = "pipe"
)

// HandoffFor returns the handoff of the isolation kind k. It returns
// ErrInvalid for an unknown kind.
func HandoffFor(k Isolation) (Handoff, error) {
	switch k {
	case IsolationKubernetes:
		return HandoffKubernetes, nil
	case IsolationFirecracker, IsolationGVisor:
		return HandoffStdin, nil
	case IsolationProcess:
		return HandoffPipe, nil
	default:
		return "", fmt.Errorf("%w: unknown isolation %q", ErrInvalid, k)
	}
}

// Status is the life-cycle status of a runner, as the server derives it.
type Status string

// The statuses of a runner.
const (
	// StatusOnline: the last heartbeat is within OfflineAfter.
	StatusOnline Status = "online"
	// StatusOffline: no heartbeat within OfflineAfter. The runner can come
	// back with its key while the key is not older than MaxKeyAge.
	StatusOffline Status = "offline"
	// StatusDraining: an administrator asked the runner to claim no new
	// work. Its claims run to their end.
	StatusDraining Status = "draining"
	// StatusMustRejoin: the key of the runner is older than MaxKeyAge. The
	// runner must join again with a new join token. The console shows the
	// command of RejoinArgs.
	StatusMustRejoin Status = "must-rejoin"
	// StatusRetired: an administrator retired the runner. Every request of
	// its key fails with ErrRetired.
	StatusRetired Status = "retired"
)

// Heartbeats. A runner sends one every HeartbeatEvery, also while it
// long-polls.
const (
	// HeartbeatEvery is the time between two heartbeats of a runner.
	HeartbeatEvery = 15 * time.Second
	// OfflineAfter is the time with no heartbeat after which the server
	// shows a runner offline.
	OfflineAfter = 2 * time.Minute
)

// Runner is one registered runner of a pool.
type Runner struct {
	// ID has the prefix RunnerIDPrefix.
	ID        string
	Pool      string
	Namespace namespace.Name
	// Name is unique in the pool, from the join request.
	Name      string
	Kind      Kind
	Isolation Isolation
	// Labels are free text for people, for example a region. A job does not
	// match on labels.
	Labels map[string]string
	// Release is the release of the runner from its last request.
	Release Release
	// Key is the current public key of the runner. A rotation replaces it
	// in one write: the runner keeps its old key until a request with the
	// new key succeeds.
	Key Key
	// JoinedWith is the ID of the join token of the last join.
	JoinedWith string
	Draining   bool
	RetiredAt  time.Time
	LastSeenAt time.Time
	JoinedAt   time.Time
}

// StatusAt returns the status of r at now.
func (r *Runner) StatusAt(now time.Time) Status {
	switch {
	case !r.RetiredAt.IsZero():
		return StatusRetired
	case r.Key.ExpiredAt(now):
		return StatusMustRejoin
	case r.Draining:
		return StatusDraining
	case now.Sub(r.LastSeenAt) > OfflineAfter:
		return StatusOffline
	default:
		return StatusOnline
	}
}

// JoinRequest is what a runner sends with a join token.
type JoinRequest struct {
	// Token is the secret of the join token. The store keeps only its
	// SHA-256.
	Token     string
	Name      string
	Kind      Kind
	Isolation Isolation
	Labels    map[string]string
	Release   Release
	// Key is the public key that the runner made for this join. The private
	// key never leaves the runner.
	Key Key
	// Proof is the signature of JoinChallenge by Key, so a join token
	// alone cannot register a key that its sender does not hold.
	Proof []byte
}

// Validate checks the fields of req that do not need the store. It returns
// an error that wraps ErrInvalid for an empty token, a name that is not
// valid, an unknown kind, an isolation that does not fit the kind, more
// than MaxLabels labels, or a key that is not valid.
func (req *JoinRequest) Validate() error {
	switch {
	case req.Token == "":
		return fmt.Errorf("%w: a join needs a join token", ErrInvalid)
	case !ValidName(req.Name):
		return fmt.Errorf(
			"%w: runner name must be 1 to %d lowercase letters, digits and '-'",
			ErrInvalid,
			MaxNameLength,
		)
	case !req.Kind.Valid():
		return fmt.Errorf("%w: unknown runner kind %q", ErrInvalid, req.Kind)
	case !req.Isolation.Fits(req.Kind):
		return fmt.Errorf("%w: a %s runner cannot use the isolation %q", ErrInvalid, req.Kind, req.Isolation)
	case len(req.Labels) > MaxLabels:
		return fmt.Errorf("%w: a runner has at most %d labels", ErrInvalid, MaxLabels)
	default:
		return req.Key.Validate()
	}
}
