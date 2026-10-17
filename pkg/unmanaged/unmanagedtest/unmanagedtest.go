// Package unmanagedtest is the conformance suite of the ports of package
// unmanaged: the resource index, the findings and the scans.
package unmanagedtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// Pool is the runner pool that Harness.Namespace makes in each namespace.
const Pool = "scanners"

// Harness is one fresh, empty store for one subtest. The three ports share
// one store, as the SQL adapters do. Namespace makes the namespace name and
// a runner pool Pool in it, and returns the current fence of the namespace.
type Harness struct {
	Index     unmanaged.Index
	Findings  unmanaged.FindingStore
	Scans     unmanaged.ScanStore
	Namespace func(t *testing.T, name string) namespace.Fence
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// at returns the time of the fixtures.
func at() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := slices.Concat(indexCases(), findingCases(), scanCases())

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

// Unit returns a valid unit of namespace ns with ID id.
func Unit(ns, id string) graph.Unit {
	return graph.Unit{Namespace: namespace.Name(ns), Tool: "opentofu", ID: id}
}

// AWSKey returns the key of an S3 bucket named name.
func AWSKey(name string) unmanaged.ResourceKey {
	return unmanaged.ResourceKey{Provider: unmanaged.ProviderAWS, ID: "arn:aws:s3:::" + name}
}

func entry(u graph.Unit, keys ...unmanaged.ResourceKey) unmanaged.IndexEntry {
	return unmanaged.IndexEntry{Unit: u, Resource: "aws_s3_bucket.b", Type: "aws_s3_bucket", Keys: keys}
}

func auditOf(ns, action string) audit.Entry {
	return audit.Entry{
		At:           at(),
		Namespace:    namespace.Name(ns),
		Subject:      "user:ada",
		Action:       action,
		IDPSubject:   "",
		TokenID:      "",
		Object:       "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      "",
		Kind:         "",
		Outcome:      "",
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

func stale(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}
