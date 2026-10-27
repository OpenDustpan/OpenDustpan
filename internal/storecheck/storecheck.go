// Package storecheck is the deep check of a blob store that an operator
// runs. It is a concrete service, not a port: it has one implementation,
// and the installation API is its only consumer.
//
// The start-up check of the server stays minimal: the store answers, the
// credentials work, and a probe object is written, read back with its bytes
// checked, and deleted. This package does the rest. It runs the blob
// conformance cases on a throwaway prefix, measures the latency, the
// throughput and the delete rate, and finds the store capabilities.
//
// A check is a durable operation. A unique constraint in the database
// allows one running check for each installation. A check writes at most
// 1,000 objects and 256 MiB in total, with at most 64 MiB in one object,
// and stops after 10 minutes. After a check ends, a new check on the same
// target waits 15 minutes. A check does not run in maintenance mode or
// while a restore runs.
//
// A check writes only under store-checks/<check ID>/. Each of these layers
// stops a wrong prefix alone: the service refuses every other prefix, fails
// closed and writes an audit entry; every case runs on a blob.Scoped view of
// the prefix; and the prefix reaper uses the same allowlist and the same
// view.
package storecheck

import (
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// Target names the store that a check reads. A request selects the store
// by a name from this fixed list, never by a URL, so that a caller cannot
// point the check at another place.
type Target string

// The stores that a check can read.
const (
	// TargetBlobs is the live blob store of the installation.
	TargetBlobs Target = "blobs"
	// TargetBackup is the backup target of the installation.
	TargetBackup Target = "backup"
)

// ConditionalWrites is what a check saw when it wrote a key twice with
// If-None-Match. Correctness never depends on it. The report shows it so
// that an operator knows which extra layer the store gives.
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

// Case is the result of one conformance case of a check.
type Case struct {
	// Name is the name of the case, the same as in the blobtest suite.
	Name string
	// Message tells what failed. It is empty when the case passed. It holds
	// no credential and no endpoint, also when the adapter error holds one.
	Message string
	// Passed is true when the case passed.
	Passed bool
}

// Latency holds the measured latency of one kind of request.
type Latency struct {
	P50 time.Duration
	P99 time.Duration
}

// Report is the result of one check. Each figure is a measured value on the
// store of the operator, at the time of the check.
type Report struct {
	StartedAt  time.Time
	FinishedAt time.Time
	// Target is the store that the check read.
	Target Target
	// Prefix is the throwaway prefix that the check wrote under.
	Prefix blob.Key
	// ConditionalWrites is what the store did with a second conditional write.
	ConditionalWrites ConditionalWrites
	// Cases holds one result for each conformance case, in the order of the
	// suite.
	Cases []Case
	// PutLatency, GetLatency and DeleteLatency are the measured latencies.
	PutLatency    Latency
	GetLatency    Latency
	DeleteLatency Latency
	// PutBytesPerSecond and GetBytesPerSecond are the measured throughput.
	PutBytesPerSecond int64
	GetBytesPerSecond int64
	// DeletesPerSecond is the measured rate of the one delete path.
	DeletesPerSecond int64
	// ThrottlingSeen is true when the store answered with a throttling
	// answer, for example SlowDown, during the check.
	ThrottlingSeen bool
	// PrefixDeleted is false when the store refused the delete of the
	// prefix, for example a target with object lock. The delete case then
	// does not pass.
	PrefixDeleted bool
}
