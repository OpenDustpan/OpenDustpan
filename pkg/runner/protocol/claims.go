package protocol

import "time"

// Paths of the claim flow of a runner: the poll that claims one phase or
// sweep, the heartbeat that renews the lease of one claim, and the upload
// that streams a report chunk under the claim of the runner.

const (
	// PathClaims asks for one claimed phase or sweep of the pool of the
	// runner. ClaimWork takes the work from the pool and claims it. The
	// answer is 200 with work, or 204 with none.
	PathClaims = Prefix + "/claims"
	// PathClaimHeartbeat renews the lease of one claim of the runner.
	// HeartbeatClaim moves the expiry of the lease of the claim by the lease,
	// and the answer holds the new expiry.
	PathClaimHeartbeat = Prefix + "/claims/heartbeat"
	// PathUploads streams one chunk of the report of the claim of the
	// runner. UploadChunk appends the chunk to the staged report of the
	// claim and answers with the byte offset that the next chunk starts at.
	PathUploads = Prefix + "/uploads"
)

// Limits of the claim flow of a runner. The lease of a claim is ClaimLease,
// the runner renews it every HeartbeatInterval, and a poll waits at most
// MaxPollWait.
const (
	// ClaimLease is the lease of a claim that a poll takes, in milliseconds.
	ClaimLease Duration = 60_000
	// HeartbeatInterval is the longest time between two renewals of a claim
	// that a runner keeps: a third of the lease.
	HeartbeatInterval Duration = 15_000
)

// Limits of the reports that a runner streams under a claim. A report is
// JSON Lines version 1. The server refuses a report that is above a limit
// with ErrorLimit, and keeps the staged bytes that it accepted.
const (
	// MaxUploadBytes is the largest report of one claim, in bytes.
	MaxUploadBytes int64 = 1 << 30
	// MaxUploadRecords is the largest number of records of one report.
	MaxUploadRecords int64 = 5_000_000
	// MaxUploadLineBytes is the largest line of one report, in bytes.
	MaxUploadLineBytes int64 = 64 << 10
)

// ClaimWorkRequest asks for one claimed phase or sweep of the pool of the
// runner. Wait is the longest wait for work, at most MaxPollWait. The
// assignment version is the highest that the launcher of the runner reads.
type ClaimWorkRequest struct {
	// Wait is the longest wait for work, at most MaxPollWait.
	Wait Duration `json:"wait"`
	// AssignmentVersion is the highest assignment version that the
	// launcher of the runner reads.
	AssignmentVersion int `json:"assignmentVersion"`
}

// HeartbeatClaimRequest names the claim of the runner that the heartbeat
// renews.
type HeartbeatClaimRequest struct {
	// Operation is the durable operation of the claimed phase or sweep, as
	// the claim that the poll took named it.
	Operation string `json:"operation"`
	// Token is the token of the claim, from the work of the poll.
	Token int64 `json:"token"`
}

// HeartbeatClaimResponse is the answer of a renewal of a claim: the new
// expiry of the lease, ClaimLease after the heartbeat.
type HeartbeatClaimResponse struct {
	// ExpiresAt is the new expiry of the lease of the claim.
	ExpiresAt time.Time `json:"expiresAt"`
}

// UploadResponse is the answer of an upload: the byte offset that the next
// chunk of the report of the claim starts at.
type UploadResponse struct {
	// Offset is the byte offset that the next chunk of the report starts
	// at, after the chunks that the claim accepted.
	Offset int64 `json:"offset"`
}

// UploadChunkRequest is the chunk of the report of a claim that the runner
// streams under the claim.
type UploadChunkRequest struct {
	// Offset is the byte offset in the report that the chunk starts at.
	Offset int64 `json:"offset"`
}
