package blob

import (
	"context"
	"time"
)

// CheckTarget names the store that a store check reads. A request selects
// the store by a name from this fixed list, never by a URL, so that a
// caller cannot point the check at another place.
type CheckTarget string

// The stores that a store check can read.
const (
	// CheckTargetBlobs is the live blob store of the installation.
	CheckTargetBlobs CheckTarget = "blobs"
	// CheckTargetBackup is the backup target of the installation.
	CheckTargetBackup CheckTarget = "backup"
)

// ConditionalWrites is what a store check saw when it wrote a key twice
// with If-None-Match. Correctness never depends on it. The report shows it
// so that an operator knows which extra layer the store gives.
type ConditionalWrites string

// The values of ConditionalWrites.
const (
	// ConditionalWritesEnforced means that the store refused the second write.
	ConditionalWritesEnforced ConditionalWrites = "enforced"
	// ConditionalWritesIgnored means that the store accepted the second write.
	ConditionalWritesIgnored ConditionalWrites = "ignored"
	// ConditionalWritesUnknown means that the check did not get an answer,
	// for example because the check stopped before that step.
	ConditionalWritesUnknown ConditionalWrites = "unknown"
)

// CheckCase is the result of one conformance case of a store check.
type CheckCase struct {
	// Name is the name of the case, the same as in the blobtest suite.
	Name string
	// Message tells what failed. It is empty when the case passed. It holds
	// no credential and no endpoint.
	Message string
	// Passed is true when the case passed.
	Passed bool
}

// Latency holds the measured latency of one kind of request.
type Latency struct {
	P50 time.Duration
	P99 time.Duration
}

// CheckReport is the result of one store check. Each figure is a measured
// value on the store of the operator, at the time of the check.
type CheckReport struct {
	StartedAt  time.Time
	FinishedAt time.Time
	// Target is the store that the check read.
	Target CheckTarget
	// Prefix is the throwaway prefix that the check wrote under. The check
	// deletes it at the end.
	Prefix Key
	// ConditionalWrites is what the store did with a second conditional write.
	ConditionalWrites ConditionalWrites
	// Cases holds one result for each conformance case, in the order of the
	// suite.
	Cases []CheckCase
	// PutLatency, GetLatency and DeleteLatency are the measured latencies.
	PutLatency    Latency
	GetLatency    Latency
	DeleteLatency Latency
	// PutBytesPerSecond and GetBytesPerSecond are the measured throughput.
	PutBytesPerSecond int64
	GetBytesPerSecond int64
	// DeletesPerSecond is the measured rate of the delete path, with
	// DeleteConcurrency calls in parallel.
	DeletesPerSecond int64
	// ThrottlingSeen is true when the store answered with a throttling
	// answer, for example SlowDown, during the check.
	ThrottlingSeen bool
}

// Checker runs the deep store check on one store. It is not part of the
// start-up check. At start, the server checks only that the store answers,
// that the credentials work, and that a probe object can be written, read
// back with its bytes checked, and deleted.
//
// Check runs the blob conformance cases under prefix, measures the latency,
// the throughput and the delete rate, and finds the store capabilities. It
// writes only under prefix, and it deletes every object under prefix before
// it returns, also when a case fails. A failed case is in the report, not
// an error. Check returns an error only when it cannot run, for example
// when the store does not answer.
//
// The installation API runs a check, with an action and an audit entry for
// the start and for the end. The command-line client is a client of that
// endpoint.
type Checker interface {
	Check(ctx context.Context, store Store, target CheckTarget, prefix Key) (CheckReport, error)
}
