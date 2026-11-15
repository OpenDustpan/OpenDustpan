// Package protocol holds the messages of the runner protocol: the paths
// under /runner/v1, the headers, the JSON bodies and the error codes. The
// HTTP adapter of the server, the runner and the launcher share these
// types, so the three agree on one shape.
//
// The protocol is HTTP and JSON over HTTPS. The runner and the launcher
// open every connection. The server never connects to them. A runner
// long-polls for work. Inside one path version, a new release adds fields
// and never removes or renames one. A reader ignores fields that it does
// not know.
package protocol

import (
	"slices"
	"time"
)

// Prefix is the path of every request of the runner protocol.
const Prefix = "/runner/v1"

// Paths of the runner: requests with the proof of the runner key.
const (
	// PathJoin registers a runner key with a join token (JoinRequest,
	// JoinResponse). It is the one request with no proof of an existing
	// key: the join token and the signature of the new key authenticate
	// it.
	PathJoin = Prefix + "/join"
	// PathRotate replaces the key of the runner (RotateRequest).
	PathRotate = Prefix + "/rotate"
	// PathHeartbeat records that the runner lives and gets the cancels of
	// its claims (Heartbeat, HeartbeatResponse).
	PathHeartbeat = Prefix + "/heartbeat"
	// PathPoll long-polls for one phase or sweep of the pool of the runner
	// (PollRequest, Work). The answer is 200 with work, or 204 after
	// PollRequest.Wait with none.
	PathPoll = Prefix + "/poll"
	// PathClaimHandoff asks for the handoff token of a claim of the runner
	// (HandoffResponse). Path parameter: {operation}.
	PathClaimHandoff = Prefix + "/claims/{operation}/handoff"
	// PathClaimRenew renews a claim that has no job session yet
	// (RenewResponse), through runner.SessionStore.RunnerRenew: the answer
	// has SessionOpen once a session exists, and the code ErrorClaimLost
	// after runner.MaxSessionWait with no session.
	PathClaimRenew = Prefix + "/claims/{operation}/renew"
	// PathClaimRelease gives back a claim whose launcher never opened a
	// session, for example after an image pull error (ReleaseRequest).
	PathClaimRelease = Prefix + "/claims/{operation}/release"
)

// Paths of the launcher: requests with the proof of the session key.
const (
	// PathSession exchanges a handoff token for a job session
	// (SessionRequest, SessionResponse). The handoff token and the
	// signature of the new key authenticate it. Its proof is signed by the
	// new key, with the hex SHA-256 of the handoff token as the subject, so
	// the server can keep its nonce (NoncePaths).
	PathSession = Prefix + "/session"
	// PathSessionRenew renews the claim of the session and gets a cancel
	// (RenewResponse).
	PathSessionRenew = Prefix + "/session/renew"
	// PathSessionSource streams the source snapshot of the job: the raw
	// git pack, with the headers HeaderCommit and HeaderPackSHA256. The
	// answer is 409 with ErrorSourceNotReady and a Retry-After while the
	// server still gets the pack from the forge.
	PathSessionSource = Prefix + "/session/source"
	// PathSessionSourceObject streams one large file object of the
	// snapshot (Git LFS). Path parameter: {oid}.
	PathSessionSourceObject = Prefix + "/session/source/objects/{oid}"
	// PathSessionPlanPart streams the part plan-apply of the plan that an
	// apply phase applies. Path parameter: {part}.
	PathSessionPlanPart = Prefix + "/session/plan/parts/{part}"
	// PathSessionCredentials gets the credentials of one step of the role
	// tool, or of a sweep (CredentialsRequest, CredentialsResponse).
	PathSessionCredentials = Prefix + "/session/credentials"
	// PathSessionStepBegin records the begin of a step. Path parameter:
	// {step}. For a step that changes resources, the launcher waits for
	// its answer before it starts the process.
	PathSessionStepBegin = Prefix + "/session/steps/{step}/begin"
	// PathSessionStepEnd records the end of a step (StepEnd).
	PathSessionStepEnd = Prefix + "/session/steps/{step}/end"
	// PathSessionLog stores one chunk of the log of a step, at most
	// MaxLogChunk bytes, with the header HeaderContentSHA256. Path
	// parameters: {step} and {seq}. A retry with the same seq and hash
	// changes nothing.
	PathSessionLog = Prefix + "/session/steps/{step}/logs/{seq}"
	// PathSessionPlanUpload stages one part of the plan of a plan phase.
	// Path parameter: {part}. The server writes it to a blob of the
	// session, and the commit stores the plan.
	PathSessionPlanUpload = Prefix + "/session/plan/uploads/{part}"
	// PathSessionPlanCommit stores the staged parts as the plan of the
	// phase, in one call (PlanCommit).
	PathSessionPlanCommit = Prefix + "/session/plan/commit"
	// PathSessionSweepReport streams the report of a sweep, as JSON Lines,
	// under the claim, with the header HeaderContentSHA256. The upload ends
	// the claim (runner.SweepClaimer.Report). Only the session key of the
	// claim can upload it.
	PathSessionSweepReport = Prefix + "/session/sweep-report"
	// PathSessionFinish ends the phase with its outcome (Finish).
	PathSessionFinish = Prefix + "/session/finish"
)

// Headers of the protocol.
const (
	// HeaderRelease carries the release of the sender on every request and
	// of the server on every answer, as major.minor.patch.
	HeaderRelease = "Dustpan-Release"
	// HeaderProof carries the proof of possession of a request:
	// "v1 sub=<runner or session ID> t=<Unix ms> n=<nonce hex>
	// sig=<signature base64url>", with single spaces.
	HeaderProof = "Dustpan-Proof"
	// HeaderContentSHA256 carries the hex SHA-256 of the body of an upload.
	// The proof signs its value as the hash of the body
	// (runner.Signed.BodySHA256). The server checks the proof first,
	// streams the body, and then compares the hash
	// (runner.CheckContentHash).
	HeaderContentSHA256 = "Dustpan-Content-SHA256"
	// HeaderCommit carries the commit of a source snapshot.
	HeaderCommit = "Dustpan-Commit"
	// HeaderPackSHA256 carries the hex SHA-256 of the pack of a source
	// snapshot.
	HeaderPackSHA256 = "Dustpan-Pack-SHA256"
)

// Long polls and limits.
const (
	// MaxPollWait is the longest wait of a long poll. It stays below the
	// idle timeouts of common proxies and load balancers.
	MaxPollWait = 50 * time.Second
	// MaxBody is the largest JSON body of a request or an answer. The
	// streams of a source, a plan part, a log chunk and a sweep report have
	// their own limits.
	MaxBody = 4 << 20
)

// Caps of the uploads of one session. A request above a cap fails with
// ErrorInvalid and changes nothing.
const (
	// MaxLogChunksPerStep and MaxLogBytesPerStep bound the log of one step.
	MaxLogChunksPerStep = 20_000
	MaxLogBytesPerStep  = 256 << 20
	// MaxLogChunksPerPhase and MaxLogBytesPerPhase bound the logs of all
	// the steps of one phase or sweep.
	MaxLogChunksPerPhase = 100_000
	MaxLogBytesPerPhase  = 1 << 30
	// MaxPlanParts bounds the parts of a plan that a session stages. The
	// server deletes the staged parts of a session that ends with no
	// commit.
	MaxPlanParts = 8
)

// NoncePaths are the paths whose requests are not safe to repeat. The
// server keeps the nonce of each proof on them until
// runner.NonceExpiry, and refuses a second proof with the same nonce with
// ErrorBadProof (runner.SessionStore.UseNonce).
//
//nolint:gochecknoglobals // A fixed list, read only.
var NoncePaths = []string{PathRotate, PathPoll, PathClaimHandoff, PathSession}

// NeedsNonce reports whether the server keeps the nonce of a proof on the
// path pattern p, one of the Path constants.
func NeedsNonce(p string) bool {
	return slices.Contains(NoncePaths, p)
}

// Error is the body of every answer with a status of 400 or above.
type Error struct {
	// Code is one of the Error constants.
	Code string `json:"code"`
	// Message says what failed, in plain words, with no secret.
	Message string `json:"message"`
	// Supported is the range of releases that the server accepts, with
	// ErrorVersionUnsupported.
	Supported string `json:"supported,omitempty"`
}

// The codes of Error, and the HTTP status of each.
const (
	ErrorInvalid            = "invalid"             // 400
	ErrorBadProof           = "bad-proof"           // 401
	ErrorKeyExpired         = "key-expired"         // 401: join again
	ErrorRetired            = "retired"             // 403
	ErrorTokenUsed          = "token-used"          // 401
	ErrorTokenExpired       = "token-expired"       // 401
	ErrorNotFound           = "not-found"           // 404
	ErrorClaimLost          = "claim-lost"          // 409: stop the work
	ErrorCanceled           = "canceled"            // 409: stop the step with the grace period
	ErrorConflict           = "conflict"            // 409
	ErrorSourceNotReady     = "source-not-ready"    // 409 with Retry-After
	ErrorPlanStale          = "plan-stale"          // 409: the phase failed
	ErrorVersionUnsupported = "version-unsupported" // 426
	ErrorVersionAhead       = "version-ahead"       // 426: retry with backoff
	ErrorRateLimited        = "rate-limited"        // 429 with Retry-After
	ErrorUnavailable        = "unavailable"         // 503 with Retry-After
)

// JoinRequest registers a runner. The token is the secret of the join
// token. Proof is the signature of runner.JoinChallenge by the new key.
type JoinRequest struct {
	Token     string            `json:"token"`
	Pool      string            `json:"pool"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Isolation string            `json:"isolation"`
	Labels    map[string]string `json:"labels,omitempty"`
	PublicKey []byte            `json:"publicKey"`
	Proof     []byte            `json:"proof"`
}

// JoinResponse is the answer to a join.
type JoinResponse struct {
	Runner      string    `json:"runner"`
	Pool        string    `json:"pool"`
	Namespace   string    `json:"namespace"`
	KeyExpires  time.Time `json:"keyExpires"`
	Lease       Duration  `json:"lease"`
	Concurrency int       `json:"concurrency"`
}

// RotateRequest replaces the key of a runner. NextProof is the signature
// of runner.RotateChallenge by the new key. The proof header carries the
// signature of the old key.
type RotateRequest struct {
	PublicKey []byte `json:"publicKey"`
	NextProof []byte `json:"nextProof"`
}

// Heartbeat reports the state of a runner every runner.HeartbeatEvery.
type Heartbeat struct {
	// Claims are the operations of the claims that the runner holds.
	Claims []string `json:"claims"`
	// Free is the number of claims that the runner can take now.
	Free int `json:"free"`
}

// HeartbeatResponse tells the runner what to do.
type HeartbeatResponse struct {
	// Cancel lists the claims of Heartbeat.Claims whose work a cancel
	// stops. The runner interrupts their launchers that have no session
	// yet. A launcher with a session sees the cancel itself.
	Cancel []string `json:"cancel,omitempty"`
	// Lost lists the claims of Heartbeat.Claims that are no longer live.
	// The runner ends their launchers and deletes their Jobs or sandboxes.
	Lost []string `json:"lost,omitempty"`
	// RotateKey is true when the key is older than runner.RotateKeyAfter.
	RotateKey bool `json:"rotateKey,omitempty"`
	// Draining is true when an administrator drains the runner.
	Draining bool `json:"draining,omitempty"`
}

// PollRequest asks for work. The server takes the durable kinds from the
// pool of the runner, never from the request.
type PollRequest struct {
	// Wait is the longest wait for work, at most MaxPollWait.
	Wait Duration `json:"wait"`
	// AssignmentVersion is the highest assignment version that the
	// launcher of the runner reads.
	AssignmentVersion int `json:"assignmentVersion"`
}

// Work is one claimed phase or sweep. It holds no secret.
type Work struct {
	Operation string    `json:"operation"`
	Token     int64     `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	Lease     Duration  `json:"lease"`
	// Job and Phase name a phase, Sweep names a sweep.
	Job   string `json:"job,omitempty"`
	Phase int    `json:"phase,omitempty"`
	Sweep string `json:"sweep,omitempty"`
	// Image is the image of the steps, from the settings of the unit or of
	// the sweep target, or empty for the default image of the runner.
	Image string `json:"image,omitempty"`
	// ServiceAccount is the Kubernetes service account of the unit, or
	// empty for the default of the coordinator. It is always empty for a
	// sweep: the coordinator of a pool for sweeps uses its own read-only
	// service account.
	ServiceAccount string   `json:"serviceAccount,omitempty"`
	Deadline       Duration `json:"deadline"`
	Grace          Duration `json:"grace"`
}

// HandoffResponse holds the handoff token of a claim. The runner gives it to
// the launcher through its handoff and keeps no copy.
type HandoffResponse struct {
	HandoffToken string    `json:"handoffToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// RenewResponse is the answer to a renewal of a claim.
type RenewResponse struct {
	ExpiresAt time.Time `json:"expiresAt"`
	// Canceled is true when a cancel stops the work. The launcher sends
	// SIGINT to the step, waits for its grace period, sends SIGKILL, and
	// finishes the phase.
	Canceled bool `json:"canceled,omitempty"`
	// SessionOpen is true on a renewal by the runner of a claim whose
	// launcher opened its job session. The claim is live, and the server
	// did not renew it. The runner stops its renewals of the claim and
	// leaves the Job or the sandbox alone. It is never the code
	// ErrorClaimLost.
	SessionOpen bool `json:"sessionOpen,omitempty"`
}

// ReleaseRequest gives back a claim. Reason is for people.
type ReleaseRequest struct {
	Reason string `json:"reason"`
}

// SessionRequest exchanges a handoff token for a job session. Proof is the
// signature of runner.SessionChallenge by the new key.
type SessionRequest struct {
	HandoffToken string `json:"handoffToken"`
	PublicKey    []byte `json:"publicKey"`
	Proof        []byte `json:"proof"`
	// Release is the release of the launcher.
	Release string `json:"release"`
}

// SessionResponse holds the job session and the assignment.
type SessionResponse struct {
	Session   string    `json:"session"`
	ExpiresAt time.Time `json:"expiresAt"`
	Lease     Duration  `json:"lease"`
	// AssignmentVersion is the version of Assignment.
	AssignmentVersion int        `json:"assignmentVersion"`
	Assignment        Assignment `json:"assignment"`
}

// Assignment is what the launcher runs. It holds no secret.
type Assignment struct {
	Namespace string `json:"namespace"`
	// Unit is the unit reference of a phase, as a string, or empty for a
	// sweep.
	Unit  string `json:"unit,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Job   string `json:"job,omitempty"`
	Phase string `json:"phase,omitempty"`
	Sweep string `json:"sweep,omitempty"`
	// ServerURL is the URL that a tool uses for its state.
	ServerURL string `json:"serverURL"`
	// Source names the snapshot of the code, or nil for a sweep.
	Source *Source `json:"source,omitempty"`
	Steps  []Step  `json:"steps"`
	// Plan is the plan of an apply phase, with the hashes of its parts.
	Plan *Plan `json:"plan,omitempty"`
	// GitCredentials is the git credential mode of the pool, and Helper
	// the helper of the mode custom.
	GitCredentials string `json:"gitCredentials"`
	GitHelper      string `json:"gitHelper,omitempty"`
	// Profile names the profile whose credentials the tool steps need, or is
	// empty when they need none.
	Profile string `json:"profile,omitempty"`
}

// Source names one source snapshot.
type Source struct {
	Commit string `json:"commit"`
	// ObjectFormat is "sha1" or "sha256", as the repository uses.
	ObjectFormat string `json:"objectFormat"`
	PackSHA256   string `json:"packSha256"`
	PackSize     int64  `json:"packSize"`
	// Directory is the directory of the code location in the checkout.
	Directory string `json:"directory"`
	// Submodules are the submodules that the policy allowed, each with its
	// own pack.
	Submodules []Submodule `json:"submodules,omitempty"`
	// LargeFiles are the Git LFS objects that the policy allowed.
	LargeFiles []LargeFile `json:"largeFiles,omitempty"`
}

// Submodule is one allowed submodule of a snapshot.
type Submodule struct {
	Path       string `json:"path"`
	Commit     string `json:"commit"`
	PackSHA256 string `json:"packSha256"`
	PackSize   int64  `json:"packSize"`
}

// LargeFile is one Git LFS object of a snapshot.
type LargeFile struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

// Step is one step of the assignment, as package job defines it.
type Step struct {
	Name             string            `json:"name"`
	Role             string            `json:"role"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env,omitempty"`
	Dir              string            `json:"dir,omitempty"`
	Timeout          Duration          `json:"timeout"`
	AllowFailure     bool              `json:"allowFailure,omitempty"`
	ChangesResources bool              `json:"changesResources,omitempty"`
	Outputs          []Output          `json:"outputs,omitempty"`
}

// Output is one file that a step leaves.
type Output struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	// Needed is true when a later phase of the job reads the output.
	Needed bool `json:"needed,omitempty"`
}

// Plan is the plan that an apply phase applies.
type Plan struct {
	ID    string     `json:"id"`
	Parts []PlanPart `json:"parts"`
}

// PlanPart is one part of a plan, with the hash of its plain text.
type PlanPart struct {
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// CredentialsRequest asks for the credentials of one step.
type CredentialsRequest struct {
	Step string `json:"step"`
}

// CredentialsResponse holds the credentials of one step. The launcher
// writes Files to the memory-backed volume with mode 0600, and gives Token
// to the tool through the credentials helper or the credentials file of
// the tool. It never puts a value in a variable.
type CredentialsResponse struct {
	// Token is the job token of the phase, for the state of the unit, or
	// empty for a sweep.
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Files are the credential files of the profile, by their name in the
	// profile. Each value is masked in the log.
	Files []CredentialFile `json:"files,omitempty"`
}

// CredentialFile is one credential file of a profile. PathVariable is the
// variable that names the file for the tool, for example
// AWS_SHARED_CREDENTIALS_FILE. The launcher sets it to the path of the
// file. The variable holds a path, never a credential.
type CredentialFile struct {
	Name         string `json:"name"`
	Content      []byte `json:"content"`
	PathVariable string `json:"pathVariable,omitempty"`
}

// StepEnd records the end of a step.
type StepEnd struct {
	ExitCode   int       `json:"exitCode"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	// LogChunks is the number of chunks of the log, and LogSHA256 the hex
	// SHA-256 of the whole log.
	LogChunks int    `json:"logChunks"`
	LogSHA256 string `json:"logSha256"`
}

// PlanCommit stores the staged parts as the plan. A second commit with the
// same parts and hashes under the same claim changes nothing and answers
// 204. A commit with other parts after a commit fails with ErrorConflict.
type PlanCommit struct {
	Parts []PlanPart `json:"parts"`
}

// Finish ends the phase.
type Finish struct {
	Succeeded bool   `json:"succeeded"`
	Message   string `json:"message,omitempty"`
}

// Duration is a time.Duration that JSON carries as a whole number of
// milliseconds.
type Duration int64

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d) * time.Millisecond
}

// FromStd returns d as a Duration, rounded down to the millisecond.
func FromStd(d time.Duration) Duration {
	return Duration(d / time.Millisecond)
}
