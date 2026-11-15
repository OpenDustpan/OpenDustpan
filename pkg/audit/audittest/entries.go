package audittest

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// EntryStore is every port the entry reader cases exercise.
type EntryStore interface {
	Store
	audit.ServerStore
	audit.EntryReader
}

// EntryFactory returns a fresh, empty store for one entry reader subtest.
type EntryFactory func(t *testing.T) EntryStore

// RunEntryReader executes the conformance cases of audit.EntryReader against
// stores produced by newStore.
func RunEntryReader(t *testing.T, newStore EntryFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s EntryStore)
		name string
	}{
		{name: "entry at answers the entry of that sequence", test: testEntryAt},
		{name: "entry at a missing sequence is not found", test: testEntryAtMissing},
		{name: "last entry answers the newest entry", test: testLastEntry},
		{name: "last entry of an empty scope is not found", test: testLastEntryEmpty},
		{name: "the server scope reads as a scope", test: testEntryReaderServerScope},
		{name: "the six agent fields round-trip in both dialects", test: testAgentRoundTripEntries},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// appendThree appends three entries to a new namespace acme.
func appendThree(t *testing.T, s EntryStore) []audit.Entry {
	t.Helper()

	ns := newNamespace(t, s)

	out := make([]audit.Entry, 0, 3)
	for _, req := range []string{"req-1", "req-2", "req-3"} {
		out = append(out, appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", req, now())))
	}

	return out
}

func testEntryAt(t *testing.T, s EntryStore) {
	t.Helper()

	stored := appendThree(t, s)

	got, err := s.EntryAt(t.Context(), "acme", 2)
	if err != nil {
		t.Fatalf("entry at 2: %v", err)
	}

	if got.Sequence != 2 || got.RequestID != "req-2" || got.Hash != stored[1].Hash {
		t.Fatalf("entry at 2: got %+v, want %+v", got, stored[1])
	}
}

func testEntryAtMissing(t *testing.T, s EntryStore) {
	t.Helper()

	appendThree(t, s)

	for _, seq := range []int64{0, 4} {
		if _, err := s.EntryAt(t.Context(), "acme", seq); !errors.Is(err, audit.ErrEntryNotFound) {
			t.Fatalf("entry at %d: got %v, want ErrEntryNotFound", seq, err)
		}
	}

	if _, err := s.EntryAt(t.Context(), "globex", 1); !errors.Is(err, audit.ErrEntryNotFound) {
		t.Fatalf("entry of another scope: got %v, want ErrEntryNotFound", err)
	}
}

func testLastEntry(t *testing.T, s EntryStore) {
	t.Helper()

	stored := appendThree(t, s)

	got, found, err := s.LastEntry(t.Context(), "acme")
	if err != nil {
		t.Fatalf("last entry: %v", err)
	}

	if !found {
		t.Fatal("last entry: no entry, want the newest entry")
	}

	if got.Sequence != 3 || got.Hash != stored[2].Hash {
		t.Fatalf("last entry: got %+v, want %+v", got, stored[2])
	}
}

func testLastEntryEmpty(t *testing.T, s EntryStore) {
	t.Helper()

	newNamespace(t, s)

	_, found, err := s.LastEntry(t.Context(), "acme")
	if err != nil {
		t.Fatalf("last entry of an empty scope: %v", err)
	}

	if found {
		t.Fatal("last entry of an empty scope: found an entry, want none")
	}
}

func testEntryReaderServerScope(t *testing.T, s EntryStore) {
	t.Helper()

	e := newEntry(audit.ServerScope, "admin", "req-server", now())
	e.Kind = audit.KindChange
	e.Object = audit.ServerScope.String()

	stored, err := s.AppendServer(t.Context(), e)
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	got, err := s.EntryAt(t.Context(), audit.ServerScope, 1)
	if err != nil || got.RequestID != stored.RequestID {
		t.Fatalf("server entry at 1: got %+v, %v, want %+v", got, err, stored)
	}

	last, found, err := s.LastEntry(t.Context(), audit.ServerScope)
	if err != nil || !found || last.Sequence != 1 {
		t.Fatalf("last server entry: got %+v, %v, want sequence 1", last, err)
	}
}

// testAgentRoundTripEntries appends an entry without agent data, one with
// the built-in agent block and one with the MCP block: each lists back as
// written, and the chain stays valid, so the six columns round-trip in both
// dialects and the hash covers them.
func testAgentRoundTripEntries(t *testing.T, s EntryStore) {
	t.Helper()

	ns := newNamespace(t, s)
	appendAgentRoundTripEntries(t, s, ns)
	got := listAgentRoundTripEntries(t, s, ns)
	checkOldestAgentEntry(t, got[2])
	checkBuiltInAgentEntry(t, got[1])
	checkMCPAgentEntry(t, got[0])
	checkAgentRoundTripChain(t, s, ns, got)
}

// appendAgentRoundTripEntries appends an entry without agent data, one with
// the built-in agent block and one with the MCP block.
func appendAgentRoundTripEntries(t *testing.T, s EntryStore, ns namespace.Namespace) {
	t.Helper()

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))

	builtIn := newEntry(ns.Name, "ada", "req-2", now())
	builtIn.Channel = audit.ChannelAgent
	builtIn.Origin = mustOrigin(
		t,
		audit.OriginArgs{
			Run:          "run-1",
			Session:      "run-1",
			Tool:         "",
			ArgsDigest:   "",
			BreakGlass:   true,
			SecretStage:  "",
			DelegationID: "",
		},
	)
	appendEntry(t, s, ns.Fence(), builtIn)

	mcp := newEntry(ns.Name, "ada", "req-3", now())
	mcp.Channel = audit.ChannelMCP
	mcp.Origin = mustOrigin(t, audit.OriginArgs{
		Run:          "",
		Session:      "ses-1",
		Tool:         "read",
		ArgsDigest:   "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		BreakGlass:   false,
		SecretStage:  "open",
		DelegationID: "",
	})
	appendEntry(t, s, ns.Fence(), mcp)
}

// mustOrigin builds the agent origin of a conformance entry and fails the
// test when its fields are not valid.
func mustOrigin(t *testing.T, fields audit.OriginArgs) *audit.Origin {
	t.Helper()

	origin, err := audit.NewOrigin(fields)
	if err != nil {
		t.Fatalf("new origin: %v", err)
	}

	return origin
}

// listAgentRoundTripEntries lists the three entries oldest first.
func listAgentRoundTripEntries(t *testing.T, s EntryStore, ns namespace.Namespace) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 3 {
		t.Fatalf("listing has %d entries, want 3", len(listing.Entries))
	}

	return listing.Entries
}

// checkOldestAgentEntry proves that the oldest entry has no agent block.
func checkOldestAgentEntry(t *testing.T, got audit.Entry) {
	t.Helper()

	if got.HasAgentBlock() {
		t.Fatalf("oldest entry has an agent block: %+v", got)
	}
}

// checkBuiltInAgentEntry proves that the middle entry lists back as written.
func checkBuiltInAgentEntry(t *testing.T, got audit.Entry) {
	t.Helper()

	o := got.Origin
	if o == nil || o.Run() != "run-1" || o.Session() != "run-1" || !o.BreakGlass() ||
		o.Tool() != "" || o.ArgsDigest() != "" || o.SecretStage() != "" {
		t.Fatalf("built-in agent entry = %+v, want the block as written", got)
	}
}

// checkMCPAgentEntry proves that the newest entry lists back as written.
func checkMCPAgentEntry(t *testing.T, got audit.Entry) {
	t.Helper()

	o := got.Origin
	if o == nil || o.Session() != "ses-1" || o.Tool() != "read" ||
		o.ArgsDigest() != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" ||
		o.SecretStage() != "open" || o.Run() != "" || o.BreakGlass() {
		t.Fatalf("MCP entry = %+v, want the block as written", got)
	}
}

// checkAgentRoundTripChain proves that a chain of the three entries checks
// valid through the store and over their hashes.
func checkAgentRoundTripChain(t *testing.T, s EntryStore, ns namespace.Namespace, got []audit.Entry) {
	t.Helper()

	checkValidChain(t, []audit.Entry{got[2], got[1], got[0]})

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != 3 {
		t.Fatalf("check = %+v, want 3 valid entries", check)
	}
}

// CheckEpochChangeStopsWrite proves that checkpoints over the entries of s,
// with blobs as their object store, sign nothing when the epoch changes after
// a cycle read it, and sign the head in the current epoch. A store package
// calls it with an empty blob store.
func CheckEpochChangeStopsWrite(t *testing.T, s EntryStore, blobs blob.Store) {
	t.Helper()

	appendThree(t, s)

	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: blobs, Entries: s, Signer: signer, Marks: nil, Now: nil,
	})

	epoch, err := cps.Epoch(t.Context())
	if err != nil {
		t.Fatalf("epoch: %v", err)
	}

	at := time.Unix(1_700_000_000, 0)

	if _, err = cps.NewEpoch(t.Context(), at); err != nil {
		t.Fatalf("new epoch: %v", err)
	}

	wrote, err := cps.WriteAt(t.Context(), "acme", at, epoch)
	if err != nil || wrote {
		t.Fatalf("write after the epoch changed: wrote %v, err %v, want false and no error", wrote, err)
	}

	if got := countObjects(t, blobs); got != 1 {
		t.Fatalf("objects after a refused write: got %d, want only the new epoch object", got)
	}

	current, err := cps.Epoch(t.Context())
	if err != nil {
		t.Fatalf("epoch: %v", err)
	}

	wrote, err = cps.WriteAt(t.Context(), "acme", at, current)
	if err != nil || !wrote {
		t.Fatalf("write in the current epoch: wrote %v, err %v, want true", wrote, err)
	}
}

// countObjects counts the objects of the audit checkpoints in blobs.
func countObjects(t *testing.T, blobs blob.Store) int {
	t.Helper()

	n := 0

	err := blob.Walk(t.Context(), blobs, audit.CheckpointPrefix+"/", func(blob.Object) error {
		n++

		return nil
	})
	if err != nil {
		t.Fatalf("list checkpoint objects: %v", err)
	}

	return n
}
