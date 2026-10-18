package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// Work is one claimed phase or scan, as a coordinator sees it. It holds no
// secret: the launcher gets the credentials through its job session.
type Work struct {
	// Operation and Token name the durable claim.
	Operation string
	Token     int64
	ExpiresAt time.Time
	// Job and Phase name a claimed phase. Both are empty for a scan.
	Job   job.ID
	Phase int
	// Scan is the ID of a claimed scan, or empty for a phase.
	Scan string
	// Deadline is the longest time that the launcher may run: the sum of
	// the timeouts of the steps or the timeout of the scan, plus the grace
	// periods (LimitsFor).
	Deadline time.Duration
	// Grace is the time from the first signal of a cancel to the end of
	// the launcher (LimitsFor).
	Grace time.Duration
}

// Grace periods of a step after a cancel or a timeout: SIGINT first, then
// SIGKILL after the grace period. The tools stop at a safe point on SIGINT
// and save their state.
const (
	// GraceChanges is the grace period of a step that changes resources.
	GraceChanges = 10 * time.Minute
	// GraceOther is the grace period of any other step.
	GraceOther = 10 * time.Second
	// ReportTime is the time that the launcher has after the last step to
	// upload its last log chunks and finish.
	ReportTime = 2 * time.Minute
)

// GraceFor returns the grace period of s.
func GraceFor(s *job.Step) time.Duration {
	if s.ChangesResources {
		return GraceChanges
	}

	return GraceOther
}

// Limits are the time limits of one launcher.
type Limits struct {
	// Deadline is the longest time that the launcher may run. A Kubernetes
	// coordinator sets it as activeDeadlineSeconds of the Job, and a
	// machine coordinator ends the sandbox after it.
	Deadline time.Duration
	// Grace is the time from the first signal to SIGKILL: the termination
	// grace of the pod.
	Grace time.Duration
}

// LimitsFor returns the limits of a launcher that runs steps: the deadline
// is the sum of the timeout (job.DefaultStepTimeout for zero) and the grace
// period of each step, plus ReportTime. The grace is the largest grace
// period of the steps, plus ReportTime.
func LimitsFor(steps []job.Step) Limits {
	var deadline time.Duration

	maxGrace := GraceOther

	for i := range steps {
		s := &steps[i]

		t := s.Timeout
		if t == 0 {
			t = job.DefaultStepTimeout
		}

		g := GraceFor(s)
		deadline += t + g
		maxGrace = max(maxGrace, g)
	}

	return Limits{Deadline: deadline + ReportTime, Grace: maxGrace + ReportTime}
}

// Kubernetes Jobs. The Kubernetes coordinator creates one Job for each
// claim, with backoffLimit 0, so Kubernetes never starts the launcher of a
// claim twice. A takeover is a new claim, so it gets a new Job.
const (
	// KubernetesBackoffLimit is the backoffLimit of every Job.
	KubernetesBackoffLimit = 0
	// KubernetesTokenAudience is the audience of the projected service
	// account token that the launcher sends to the coordinator.
	KubernetesTokenAudience = "opendustpan-coordinator" //nolint:gosec // An audience name, not a credential.
	// KubernetesTokenTTL is the lifetime of that token: the shortest that
	// Kubernetes allows.
	KubernetesTokenTTL = 10 * time.Minute
	// KubernetesJobTTL is ttlSecondsAfterFinished of a Job, so that a
	// person can read the pod for one hour after its end.
	KubernetesJobTTL = time.Hour
	// LabelOperation is the label of each Job and its pod with the durable
	// operation of the claim.
	LabelOperation = "opendustpan.org/operation"
	// LabelClaim is the label with the token of the claim.
	LabelClaim = "opendustpan.org/claim"
	// LabelRunner is the label with the ID of the runner.
	LabelRunner = "opendustpan.org/runner"
)

// KubernetesJobName returns the name of the Kubernetes Job of the claim
// with the claim token n on the operation op: "dustpan-" and the first 20
// hex characters of the SHA-256 of both. The name has 28 characters, a
// valid DNS label, and it is the same for each retry of the create, so a
// retry never makes a second Job for one claim.
func KubernetesJobName(op string, n int64) string {
	sum := sha256.Sum256([]byte(op + "/" + strconv.FormatInt(n, 10)))

	return "dustpan-" + hex.EncodeToString(sum[:])[:jobNameHex]
}

// jobNameHex is the number of hex characters of the hash in a Job name.
const jobNameHex = 20

// KubernetesJob is what the Kubernetes coordinator creates for one claim,
// apart from the pod template of the team. It names no Kubernetes type, so
// that this package needs no Kubernetes client.
type KubernetesJob struct {
	Name      string
	Namespace string
	Labels    map[string]string
	// Image is the image of the team, with the tools. The launcher runs in
	// it as the first process.
	Image string
	// LauncherImage holds the static launcher binary. An init container
	// copies it to a volume that the main container mounts. Its release is
	// the release of the coordinator.
	LauncherImage string
	// ServiceAccount is the service account of the pod: the identity that
	// the cloud trusts for the unit or the pool.
	ServiceAccount string
	// Args is the command line of the launcher (launcher.Args).
	Args []string
	// ActiveDeadline and TerminationGrace come from LimitsFor.
	ActiveDeadline   time.Duration
	TerminationGrace time.Duration
	// MemoryVolumeSize bounds the memory-backed volume of the credential
	// files.
	MemoryVolumeSize int64
}

// Cluster is the port of the Kubernetes coordinator to the Kubernetes API
// of its cluster. The adapter speaks to the API server with the service
// account of the coordinator, which needs to create, read, watch and
// delete Jobs and read pods in its namespace, and to create TokenReviews.
type Cluster interface {
	// CreateJob creates j. A Job of the same name that exists is not an
	// error: the create of a claim is safe to retry. It returns ErrInvalid
	// when the API server refuses j, wrapped with its message.
	CreateJob(ctx context.Context, j KubernetesJob) error
	// DeleteJob deletes the Job name and its pods, in the foreground. The
	// kubelet sends SIGTERM to the launcher, which sends SIGINT to the
	// step, and SIGKILL follows after TerminationGrace. A Job that does not
	// exist is not an error.
	DeleteJob(ctx context.Context, name string) error
	// JobEnded reports whether the Job name ended, and whether it failed
	// before its launcher opened a session, for example on an image pull
	// error. The coordinator then releases the claim. It returns
	// ErrNotFound for a Job that does not exist.
	JobEnded(ctx context.Context, name string) (ended, failedEarly bool, err error)
	// ReviewToken checks a projected service account token with a
	// TokenReview for KubernetesTokenAudience, and returns the pod that it
	// names. It returns ErrBadProof for a token that the API server does
	// not authenticate, of another audience, or with no pod.
	ReviewToken(ctx context.Context, token string) (PodIdentity, error)
	// Pod returns the identity of the pod name in the namespace of the
	// coordinator, with the labels of its Job, or ErrNotFound.
	Pod(ctx context.Context, name string) (PodIdentity, error)
}

// PodIdentity is a pod as a TokenReview or a read of the pod names it.
type PodIdentity struct {
	Namespace      string
	Name           string
	UID            string
	ServiceAccount string
	// Job is the name of the Job that owns the pod, from its owner
	// reference. It is empty in a TokenReview.
	Job    string
	Labels map[string]string
}

// CheckHandoffPod checks that the pod of a TokenReview may get the handoff
// token of the claim with the Job name want: the pod of the review and the
// pod that the API server reads have the same namespace, name and UID and
// service account, the pod belongs to the Job want, and its labels name
// the same claim. It returns ErrBadProof otherwise. A pod gets the handoff
// token of its own claim only, so a pod of another Job, or a pod that a
// person created with the same labels, gets nothing.
func CheckHandoffPod(reviewed, read PodIdentity, want KubernetesJob) error {
	switch {
	case reviewed.UID == "" || reviewed.UID != read.UID:
		return fmt.Errorf("%w: the pod of the token is not the pod that the API server reads", ErrBadProof)
	case reviewed.Namespace != want.Namespace || read.Namespace != want.Namespace || reviewed.Name != read.Name:
		return fmt.Errorf("%w: the pod %s/%s is not in the namespace of the Job", ErrBadProof, reviewed.Namespace,
			reviewed.Name)
	case reviewed.ServiceAccount != want.ServiceAccount || read.ServiceAccount != want.ServiceAccount:
		return fmt.Errorf("%w: the pod runs as another service account", ErrBadProof)
	case read.Job != want.Name:
		return fmt.Errorf("%w: the pod does not belong to Job %s", ErrBadProof, want.Name)
	case read.Labels[LabelClaim] != want.Labels[LabelClaim] ||
		read.Labels[LabelOperation] != want.Labels[LabelOperation]:
		return fmt.Errorf("%w: the labels of the pod name another claim", ErrBadProof)
	default:
		return nil
	}
}

// Sandbox is the port of the machine coordinator to one isolation kind on
// its host. Each claim gets a new sandbox, and the coordinator destroys it
// after the launcher ends. A microVM for each phase on a Firecracker host
// is a sandbox. A cloud VM for each phase is not in this design.
type Sandbox interface {
	// Isolation names the kind of the sandbox.
	Isolation() Isolation
	// Check checks that the host can start the sandbox: for example that
	// /dev/kvm exists for Firecracker, or that the gVisor runtime runs. It
	// returns ErrInvalid with the reason. The coordinator calls it before
	// it joins and before each claim.
	Check(ctx context.Context) error
	// Start starts the launcher in a new sandbox with spec, and writes the
	// handoff token to it through the Handoff of the isolation kind. It
	// returns a handle to the sandbox.
	Start(ctx context.Context, spec SandboxSpec, handoffToken io.Reader) (SandboxHandle, error)
}

// SandboxSpec is what a machine coordinator starts for one claim.
type SandboxSpec struct {
	// Name is unique on the host for the claim: KubernetesJobName gives
	// it.
	Name string
	// Image is the image of the team, with the tools.
	Image string
	// Args is the command line of the launcher (launcher.Args).
	Args []string
	// Deadline ends the sandbox: SIGINT to the launcher, then SIGKILL
	// after Grace.
	Deadline time.Duration
	Grace    time.Duration
	// MemoryVolumeSize bounds the memory-backed volume of the credential
	// files.
	MemoryVolumeSize int64
	// CPU and Memory bound the sandbox. Zero takes the defaults of the
	// coordinator.
	CPU    int
	Memory int64
}

// SandboxHandle is one started sandbox.
type SandboxHandle interface {
	// Wait waits for the launcher to end and returns its exit code.
	Wait(ctx context.Context) (int, error)
	// Interrupt sends SIGINT to the launcher. The launcher stops its step
	// with the rules of the grace periods.
	Interrupt(ctx context.Context) error
	// Destroy ends the sandbox with SIGKILL, if it runs, and deletes its
	// files and its memory-backed volume. It is safe to call more than
	// once.
	Destroy(ctx context.Context) error
}
