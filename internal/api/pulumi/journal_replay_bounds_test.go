package pulumi_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// replayBoundFixture builds an update fixture with the real journal replayer,
// so complete replays the stored batches through the complete route.
func replayBoundFixture(t *testing.T) updateFixture {
	t.Helper()

	return newUpdateFixtureWithAuthenticator(
		t,
		allowAll(),
		tokenAuthenticator{},
		fixtureOptions{
			manual:   nil,
			checker:  nil,
			limit:    0,
			cutoff:   0,
			replayer: pulumijournal.NewReplayer(time.Now, ""),
			guard:    nil,
		},
	)
}

// mintReplayBound creates a mutating update and starts it with journal
// version 1. It returns the update ID and its lease token.
func mintReplayBound(t *testing.T, f updateFixture) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.StartWithJournalVersion(t.Context(), f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// replayBeginEntry starts operation op with sequence seq.
func replayBeginEntry(seq, op int64) string {
	return `{"version":1,"kind":0,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) + `,"removeOld":null,"removeNew":null}`
}

// replaySuccessEntry adds state for operation op with sequence seq.
func replaySuccessEntry(seq, op int64, state string) string {
	return `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) +
		`,"removeOld":null,"removeNew":null,"state":` + state + `}`
}

// replayBatchBody returns a batch body with the given entries.
func replayBatchBody(entries ...string) string {
	var b strings.Builder

	b.WriteString(`{"entries":[`)

	for i, e := range entries {
		if i > 0 {
			b.WriteString(",")
		}

		b.WriteString(e)
	}

	b.WriteString(`]}`)

	return b.String()
}

// replayPaddedState returns a resource state with pad bytes of padding.
func replayPaddedState(name string, pad int) string {
	return `{"urn":"urn:pulumi:dev::web::t:mod:T::` + name + `","custom":true,"type":"t:mod:T","pad":"` +
		strings.Repeat("x", pad) + `"}`
}

// replayNewResource is the resource the journal batches add.
const replayNewResource = `{"urn":"urn:pulumi:dev::web::t:mod:T::b","custom":true,"type":"t:mod:T","id":"b"}`

// checkReplayBoundBody checks that a 413 body carries the one-sentence replay
// bound message exactly: it names the bound, its count and limit, and the one
// command that helps, and it holds no update wrapper prefix.
func checkReplayBoundBody(t *testing.T, resp, bound string, limit int) {
	t.Helper()

	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(resp), &body); err != nil {
		t.Fatalf("decode 413 body: %v (%s)", err, resp)
	}

	want := regexp.MustCompile(
		`^This update has \d+ ` + regexp.QuoteMeta(bound) +
			`, more than the limit of ` + strconv.Itoa(limit) +
			": run `pulumi up` with fewer resources\\.$",
	)
	if !want.MatchString(body.Message) {
		t.Fatalf("413 message = %q, want the one-sentence replay bound message", body.Message)
	}
}

// appendReplayBatches stores each batch through the journal route. Every
// batch must answer 200, so a later 413 comes from the replay bound and not
// from an unstored batch.
func appendReplayBatches(t *testing.T, f updateFixture, id stack.UpdateID, lease string, batches []string) {
	t.Helper()

	for _, body := range batches {
		status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, body))
		if status != http.StatusOK {
			t.Fatalf("append batch answers %d, want %d (%s)", status, http.StatusOK, resp)
		}
	}
}

// replayBoundWant names the replay bound one complete must cross: the bound
// phrase of the message and the limit the test configured.
type replayBoundWant struct {
	bound string
	limit int
}

// completeReplayBound ends the update through the complete route and checks
// that it answers 413 with the replay bound message for the bound.
func completeReplayBound(t *testing.T, f updateFixture, id stack.UpdateID, lease string, want replayBoundWant) {
	t.Helper()

	complete := leaseRequest(http.MethodPost, mintedPaths(id).Complete, lease, `{"status":"succeeded"}`)

	status, resp := f.doUpdate(t, complete)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"complete past the %s bound answers %d, want %d (%s)",
			want.bound,
			status,
			http.StatusRequestEntityTooLarge,
			resp,
		)
	}

	checkReplayBoundBody(t, resp, want.bound, want.limit)
}

// TestJournalCompleteAnswers413OnStateBound checks that completing a
// journaled update past the new-state bound answers 413 with the one-sentence
// message that names the bound, its limit and pulumi up.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestJournalCompleteAnswers413OnStateBound(t *testing.T) {
	old := pulumijournal.MaxNewStateBytesPerUpdate
	pulumijournal.MaxNewStateBytesPerUpdate = 2000
	defer func() { pulumijournal.MaxNewStateBytesPerUpdate = old }()

	f := replayBoundFixture(t)
	id, lease := mintReplayBound(t, f)

	appendReplayBatches(t, f, id, lease, []string{
		replayBatchBody(replayBeginEntry(1, 1)),
		replayBatchBody(replaySuccessEntry(2, 1, replayNewResource)),
		replayBatchBody(
			replayBeginEntry(3, 2),
			replaySuccessEntry(4, 2, replayPaddedState("c", 1500)),
			replaySuccessEntry(5, 3, replayPaddedState("d", 1500)),
		),
	})

	completeReplayBound(t, f, id, lease, replayBoundWant{
		bound: "new state bytes",
		limit: pulumijournal.MaxNewStateBytesPerUpdate,
	})
}

// TestJournalCompleteAnswers413OnLinkBound checks that completing a journaled
// update past the added-link bound answers 413 with the one-sentence message
// that names the bound, its limit and pulumi up.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestJournalCompleteAnswers413OnLinkBound(t *testing.T) {
	old := pulumijournal.MaxLinkIndexEntriesPerUpdate
	pulumijournal.MaxLinkIndexEntriesPerUpdate = 4
	defer func() { pulumijournal.MaxLinkIndexEntriesPerUpdate = old }()

	f := replayBoundFixture(t)
	id, lease := mintReplayBound(t, f)

	appendReplayBatches(t, f, id, lease, []string{
		replayBatchBody(replayBeginEntry(1, 1)),
		replayBatchBody(replaySuccessEntry(2, 1, replayNewResource)),
		replayBatchBody(
			replayBeginEntry(3, 2),
			replaySuccessEntry(4, 2, replayNewResource),
			replayBeginEntry(5, 3),
			replaySuccessEntry(6, 3, replayPaddedState("c", 10)),
			replayBeginEntry(7, 4),
			replaySuccessEntry(8, 4, replayPaddedState("d", 10)),
			replayBeginEntry(9, 5),
			replaySuccessEntry(10, 5, replayPaddedState("e", 10)),
		),
	})

	completeReplayBound(t, f, id, lease, replayBoundWant{
		bound: "added links",
		limit: pulumijournal.MaxLinkIndexEntriesPerUpdate,
	})
}

// TestJournalCompleteAnswers413OnAddedBytesBound checks that completing a
// journaled update past the new-state plus added-link bound answers 413 with
// the one-sentence message that names the bound, its limit and pulumi up.
//
//nolint:paralleltest // The test lowers the added-bytes bound, so it runs alone.
func TestJournalCompleteAnswers413OnAddedBytesBound(t *testing.T) {
	old := pulumijournal.MaxNewStatePlusLinkBytesPerUpdate
	pulumijournal.MaxNewStatePlusLinkBytesPerUpdate = 1000
	defer func() { pulumijournal.MaxNewStatePlusLinkBytesPerUpdate = old }()

	f := replayBoundFixture(t)
	id, lease := mintReplayBound(t, f)

	appendReplayBatches(t, f, id, lease, []string{
		replayBatchBody(replayBeginEntry(1, 1)),
		replayBatchBody(replaySuccessEntry(2, 1, replayNewResource)),
		replayBatchBody(
			replayBeginEntry(3, 2),
			replaySuccessEntry(4, 2, replayPaddedState("c", 600)),
			replaySuccessEntry(5, 3, replayPaddedState("d", 600)),
		),
	})

	completeReplayBound(t, f, id, lease, replayBoundWant{
		bound: "new-state plus added-link bytes",
		limit: pulumijournal.MaxNewStatePlusLinkBytesPerUpdate,
	})
}
