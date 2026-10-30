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
	Name string `json:"name"`
	// Message tells what failed. It is empty when the case passed. It holds
	// no credential and no endpoint, also when the adapter error holds one.
	Message string `json:"message,omitempty"`
	// Passed is true when the case passed.
	Passed bool `json:"passed"`
}

// Latency holds the measured latency of one kind of request.
type Latency struct {
	P50 time.Duration `json:"p50"`
	P99 time.Duration `json:"p99"`
}

// Report is the result of one check. Each figure is a measured value on the
// store of the operator, at the time of the check. It holds no endpoint, no
// bucket name and no credential: the text of a failed case is fixed text of
// this package, never the text of an adapter error.
type Report struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	// Target is the store that the check read.
	Target Target `json:"target"`
	// Prefix is the throwaway prefix that the check wrote under.
	Prefix blob.Key `json:"prefix"`
	// Passed is true when every case passed.
	Passed bool `json:"passed"`
	// ConditionalWrites is what the store did with a second conditional write.
	ConditionalWrites ConditionalWrites `json:"conditionalWrites"`
	// Cases holds one result for each conformance case, in the order of the
	// suite.
	Cases []Case `json:"cases"`
	// PutLatency, GetLatency and DeleteLatency are the measured latencies.
	PutLatency    Latency `json:"putLatency"`
	GetLatency    Latency `json:"getLatency"`
	DeleteLatency Latency `json:"deleteLatency"`
	// PutBytesPerSecond and GetBytesPerSecond are the measured throughput.
	PutBytesPerSecond int64 `json:"putBytesPerSecond"`
	GetBytesPerSecond int64 `json:"getBytesPerSecond"`
	// DeletesPerSecond is the measured rate of the one delete path.
	DeletesPerSecond int64 `json:"deletesPerSecond"`
	// ThrottlingSeen is true when a request failed with a throttling answer
	// that the retries of the adapter did not get through, for example
	// SlowDown, during the check.
	ThrottlingSeen bool `json:"throttlingSeen"`
	// PrefixDeleted is false when the store refused the delete of the
	// prefix, for example a target with object lock, or when an object stayed
	// in the listing after the delete. The delete case then does not pass.
	PrefixDeleted bool `json:"prefixDeleted"`
}
