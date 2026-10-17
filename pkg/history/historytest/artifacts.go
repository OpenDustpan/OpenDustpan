package historytest

import (
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// PlanSuiteStore is every persistence port that RunPlans exercises.
type PlanSuiteStore interface {
	namespace.Store
	secret.ContentKeyIndex
	history.RedactionLog
	history.PlanStore
}

// PlanFactory returns a fresh, empty store for one subtest.
type PlanFactory func(t *testing.T) PlanSuiteStore

// JobLogSuiteStore is every persistence port that RunJobLogs exercises.
type JobLogSuiteStore interface {
	namespace.Store
	secret.ContentKeyIndex
	history.RedactionLog
	history.JobLogStore
}

// JobLogFactory returns a fresh, empty store for one subtest.
type JobLogFactory func(t *testing.T) JobLogSuiteStore

// RunPlans runs the conformance suite for history.PlanStore.
func RunPlans(t *testing.T, newStore PlanFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s PlanSuiteStore)
		name string
	}{
		{name: "create then get returns the plan with its parts", test: testPlanCreateGet},
		{name: "a second upload of one plan ID is ErrPlanExists", test: testPlanTwice},
		{name: "get of a missing plan is ErrPlanNotFound", test: testPlanMissing},
		{name: "list answers the plans of one unit newest first", test: testPlanList},
		{name: "expired plans are listed by expiry time", test: testPlanExpired},
		{name: "redact keeps the plan row, shreds its key and logs the redaction", test: testPlanRedact},
		{name: "a second redaction of one plan is ErrRedacted", test: testPlanRedactTwice},
		{name: "redact of a missing plan is ErrItemNotFound", test: testPlanRedactMissing},
		{name: "a stale fence refuses create and redact", test: testPlanFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// RunJobLogs runs the conformance suite for history.JobLogStore.
func RunJobLogs(t *testing.T, newStore JobLogFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s JobLogSuiteStore)
		name string
	}{
		{name: "create then get returns an open log", test: testLogCreateGet},
		{name: "a second log for one job is ErrJobLogExists", test: testLogTwice},
		{name: "chunks append in order and list in pages", test: testLogChunks},
		{name: "a chunk sent again with the same bytes changes nothing", test: testLogChunkRetry},
		{name: "a chunk sent again with other bytes is ErrChunkConflict", test: testLogChunkConflict},
		{name: "a gap in the sequence is ErrChunkOutOfOrder", test: testLogChunkGap},
		{name: "a closed log takes no chunk and a second close changes nothing", test: testLogClose},
		{name: "redact of an open log is ErrInProgress", test: testLogRedactOpen},
		{name: "redact of a closed log shreds its key", test: testLogRedactClosed},
		{name: "expired logs are closed logs listed by expiry time", test: testLogExpired},
		{name: "a stale fence refuses every write", test: testLogFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// partBlob returns the blob key of one part or chunk.
func partBlob(t *testing.T, s string) blob.Key {
	t.Helper()

	k, err := blob.ParseKey("history/" + s)
	if err != nil {
		t.Fatalf("blob key: %v", err)
	}

	return k
}

// newPlan returns a plan of unit made at created, with its content key.
func newPlan(t *testing.T, s PlanSuiteStore, unit history.Unit, created time.Time, fence namespace.Fence) history.Plan {
	t.Helper()

	p := history.Plan{
		CreatedAt:  created,
		ExpiresAt:  history.DefaultRetention().PlanExpiry(created),
		Unit:       unit,
		ID:         history.NewPlanID(),
		JobID:      "job-1",
		By:         actor(),
		ContentKey: "",
		Redaction:  "",
		Parts: []history.Part{
			{
				Name:   "plan",
				Blob:   partBlob(t, "plan-file"),
				Size:   10,
				SHA256: sha256.Sum256([]byte("plan")),
				Secret: true,
			},
			{
				Name:   "json",
				Blob:   partBlob(t, "plan-json"),
				Size:   20,
				SHA256: sha256.Sum256([]byte("json")),
				Secret: false,
			},
		},
	}
	p.ContentKey = contentKeyFor(t, s, p.Item(), fence)

	return p
}

// mustCreatePlan stores p.
func mustCreatePlan(t *testing.T, s PlanSuiteStore, p history.Plan, fence namespace.Fence) {
	t.Helper()

	if err := s.CreatePlan(t.Context(), p, fence); err != nil {
		t.Fatalf("create plan: %v", err)
	}
}

// redactionOfItem returns a record that redacts item with its content key.
func redactionOfItem(item history.ItemRef, key secret.ContentKeyID) history.Redaction {
	r := newRedaction(item, suiteTime().Add(time.Hour))
	r.ContentKey = key

	return r
}

func testPlanCreateGet(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	p := newPlan(t, s, unitOf("ws-one"), suiteTime(), fence)
	mustCreatePlan(t, s, p, fence)

	got, err := s.GetPlan(t.Context(), "acme", p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}

	if got.ID != p.ID || got.Unit != p.Unit || got.JobID != p.JobID || got.By != p.By ||
		got.ContentKey != p.ContentKey ||
		!got.CreatedAt.Equal(p.CreatedAt) ||
		!got.ExpiresAt.Equal(p.ExpiresAt) ||
		got.Redaction != "" ||
		!slices.Equal(got.Parts, p.Parts) {
		t.Errorf("get plan = %+v, want %+v", got, p)
	}
}

func testPlanTwice(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	p := newPlan(t, s, unitOf("ws-one"), suiteTime(), fence)
	mustCreatePlan(t, s, p, fence)

	if err := s.CreatePlan(t.Context(), p, fence); !errors.Is(err, history.ErrPlanExists) {
		t.Errorf("second upload err = %v, want ErrPlanExists", err)
	}
}

func testPlanMissing(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	makeNamespace(t, s)

	if _, err := s.GetPlan(t.Context(), "acme", history.NewPlanID()); !errors.Is(err, history.ErrPlanNotFound) {
		t.Errorf("get missing plan err = %v, want ErrPlanNotFound", err)
	}
}

func testPlanList(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	var want []history.PlanID

	for i := range 3 {
		p := newPlan(t, s, unitOf("ws-one"), suiteTime().Add(time.Duration(i)*time.Minute), fence)
		mustCreatePlan(t, s, p, fence)
		want = append([]history.PlanID{p.ID}, want...)
	}

	mustCreatePlan(t, s, newPlan(t, s, unitOf("ws-two"), suiteTime(), fence), fence)

	first, err := s.ListPlans(t.Context(), unitOf("ws-one"), nil, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d plans, %v, want 2", len(first), err)
	}

	last := first[len(first)-1]

	rest, err := s.ListPlans(
		t.Context(),
		unitOf("ws-one"),
		&history.PlanCursor{CreatedAt: last.CreatedAt, ID: last.ID},
		2,
	)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	items := slices.Concat(first, rest)
	got := make([]history.PlanID, 0, len(items))
	for n := range items {
		p := items[n]
		got = append(got, p.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("listed = %v, want %v", got, want)
	}
}

func testPlanExpired(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	unit := unitOf("ws-one")

	late := newPlan(t, s, unit, suiteTime(), fence)
	late.ExpiresAt = suiteTime().Add(2 * time.Hour)
	early := newPlan(t, s, unit, suiteTime(), fence)
	early.ExpiresAt = suiteTime().Add(time.Hour)
	future := newPlan(t, s, unit, suiteTime(), fence)
	future.ExpiresAt = suiteTime().Add(48 * time.Hour)
	redacted := newPlan(t, s, unit, suiteTime(), fence)
	redacted.ExpiresAt = suiteTime().Add(time.Hour)

	items := []history.Plan{late, early, future, redacted}
	for n := range items {
		p := items[n]
		mustCreatePlan(t, s, p, fence)
	}

	r := redactionOfItem(redacted.Item(), redacted.ContentKey)
	if _, err := s.Redact(t.Context(), r, nil, changeEntry(redacted.Item()), fence); err != nil {
		t.Fatalf("redact: %v", err)
	}

	got, err := s.ListExpiredPlans(t.Context(), suiteTime().Add(3*time.Hour), 10)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}

	ids := make([]history.PlanID, 0, len(got))
	for i := range got {
		p := got[i]
		ids = append(ids, p.ID)
	}

	if want := []history.PlanID{early.ID, late.ID}; !slices.Equal(ids, want) {
		t.Errorf("expired = %v, want %v (redacted and future plans left out)", ids, want)
	}
}

func testPlanRedact(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	p := newPlan(t, s, unitOf("ws-one"), suiteTime(), fence)
	mustCreatePlan(t, s, p, fence)

	r := redactionOfItem(p.Item(), p.ContentKey)

	stored, err := s.Redact(t.Context(), r, []string{backupTarget}, changeEntry(p.Item()), fence)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}

	if stored.ID != r.ID || len(stored.Targets) != 2 {
		t.Errorf("redaction = %+v, want ID %s with the live and the backup target", stored, r.ID)
	}

	got, err := s.GetPlan(t.Context(), "acme", p.ID)
	if err != nil {
		t.Fatalf("get after redact: %v", err)
	}

	if got.Redaction != r.ID || !slices.Equal(got.Parts, p.Parts) {
		t.Errorf("plan after redact = %+v, want its parts kept and the redaction %s", got, r.ID)
	}

	ref, err := p.Item().ContentRef()
	if err != nil {
		t.Fatalf("content ref: %v", err)
	}

	if _, err := s.GetContentKey(t.Context(), ref); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("content key after redact err = %v, want ErrShredded", err)
	}

	if logged, err := s.RedactionOf(t.Context(), p.Item()); err != nil || logged.ID != r.ID {
		t.Errorf("redaction log = %s, %v, want %s", logged.ID, err, r.ID)
	}
}

func testPlanRedactTwice(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	p := newPlan(t, s, unitOf("ws-one"), suiteTime(), fence)
	mustCreatePlan(t, s, p, fence)
	first := redactionOfItem(p.Item(), p.ContentKey)

	if _, err := s.Redact(t.Context(), first, nil, changeEntry(p.Item()), fence); err != nil {
		t.Fatalf("first redact: %v", err)
	}

	got, err := s.Redact(
		t.Context(),
		redactionOfItem(p.Item(), p.ContentKey),
		nil,
		changeEntry(p.Item()),
		fence,
	)
	if !errors.Is(err, history.ErrRedacted) || got.ID != first.ID {
		t.Errorf("second redact = %s, %v, want ErrRedacted with %s", got.ID, err, first.ID)
	}
}

func testPlanRedactMissing(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	item := history.ItemRef{
		Unit: unitOf("ws-one"),
		Kind: secret.ContentKindPlan,
		Item: string(history.NewPlanID()),
	}

	_, err := s.Redact(
		t.Context(),
		redactionOfItem(item, secret.NewContentKeyID()),
		nil,
		changeEntry(item),
		fence,
	)
	if !errors.Is(err, history.ErrItemNotFound) {
		t.Errorf("redact of a missing plan err = %v, want ErrItemNotFound", err)
	}
}

func testPlanFenced(t *testing.T, s PlanSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	stale := staleFence(fence)
	p := newPlan(t, s, unitOf("ws-one"), suiteTime(), fence)

	if err := s.CreatePlan(t.Context(), p, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create with a stale fence err = %v, want ErrFenced", err)
	}

	mustCreatePlan(t, s, p, fence)

	_, err := s.Redact(t.Context(), redactionOfItem(p.Item(), p.ContentKey), nil, changeEntry(p.Item()), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("redact with a stale fence err = %v, want ErrFenced", err)
	}

	if got, err := s.GetPlan(t.Context(), "acme", p.ID); err != nil || got.Redaction != "" {
		t.Errorf("a fenced redact changed the plan: %+v, %v", got, err)
	}
}

// newJobLog returns an open log of the job jobID of unit, with its content
// key.
func newJobLog(
	t *testing.T,
	s JobLogSuiteStore,
	unit history.Unit,
	jobID string,
	fence namespace.Fence,
) history.JobLog {
	t.Helper()

	l := history.JobLog{
		StartedAt:  suiteTime(),
		ClosedAt:   time.Time{},
		ExpiresAt:  history.DefaultRetention().JobLogExpiry(suiteTime()),
		Unit:       unit,
		JobID:      jobID,
		By:         history.Actor{Subject: "runner", TokenID: "tok-r", JobID: jobID},
		ContentKey: "",
		Redaction:  "",
		Size:       0,
		Chunks:     0,
		SHA256:     [sha256.Size]byte{},
	}
	l.ContentKey = contentKeyFor(t, s, l.Item(), fence)

	return l
}

// mustCreateLog stores l.
func mustCreateLog(t *testing.T, s JobLogSuiteStore, l history.JobLog, fence namespace.Fence) {
	t.Helper()

	if err := s.CreateJobLog(t.Context(), l, fence); err != nil {
		t.Fatalf("create job log: %v", err)
	}
}

// chunk returns the chunk seq with the bytes text.
func chunk(t *testing.T, seq int64, text string) history.Chunk {
	t.Helper()

	return history.Chunk{
		Blob:   partBlob(t, "chunk-"+text),
		Seq:    seq,
		Size:   int64(len(text)),
		SHA256: sha256.Sum256([]byte(text)),
	}
}

// mustAppendChunk appends c to the log of the job "job-1".
func mustAppendChunk(t *testing.T, s JobLogSuiteStore, c history.Chunk, fence namespace.Fence) {
	t.Helper()

	const jobID = "job-1"

	if err := s.AppendChunk(t.Context(), fence.Namespace, jobID, c, fence); err != nil {
		t.Fatalf("append chunk %d: %v", c.Seq, err)
	}
}

// totals returns the totals of a log of size bytes with the content text,
// closed after the time after the start of the suite.
func totals(size int64, text string, after time.Duration) history.JobLogTotals {
	return history.JobLogTotals{ClosedAt: suiteTime().Add(after), Size: size, SHA256: sha256.Sum256([]byte(text))}
}

// mustClose closes the log of jobID.
func mustClose(t *testing.T, s JobLogSuiteStore, jobID string, fence namespace.Fence) history.JobLog {
	t.Helper()

	l, err := s.CloseJobLog(t.Context(), fence.Namespace, jobID, totals(3, "abc", time.Hour), fence)
	if err != nil {
		t.Fatalf("close job log: %v", err)
	}

	return l
}

func testLogCreateGet(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	l := newJobLog(t, s, unitOf("ws-one"), "job-1", fence)
	mustCreateLog(t, s, l, fence)

	got, err := s.GetJobLog(t.Context(), "acme", "job-1")
	if err != nil {
		t.Fatalf("get job log: %v", err)
	}

	if got.Closed() || got.JobID != l.JobID || got.Unit != l.Unit || got.By != l.By || got.ContentKey != l.ContentKey ||
		!got.ExpiresAt.Equal(l.ExpiresAt) {
		t.Errorf("get job log = %+v, want %+v", got, l)
	}

	if _, err := s.GetJobLog(t.Context(), "acme", "job-2"); !errors.Is(err, history.ErrJobLogNotFound) {
		t.Errorf("get missing log err = %v, want ErrJobLogNotFound", err)
	}
}

func testLogTwice(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	l := newJobLog(t, s, unitOf("ws-one"), "job-1", fence)
	mustCreateLog(t, s, l, fence)

	if err := s.CreateJobLog(t.Context(), l, fence); !errors.Is(err, history.ErrJobLogExists) {
		t.Errorf("second log err = %v, want ErrJobLogExists", err)
	}
}

func testLogChunks(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	mustCreateLog(t, s, newJobLog(t, s, unitOf("ws-one"), "job-1", fence), fence)

	want := []history.Chunk{chunk(t, 0, "a"), chunk(t, 1, "b"), chunk(t, 2, "c")}
	for _, c := range want {
		mustAppendChunk(t, s, c, fence)
	}

	first, err := s.ListChunks(t.Context(), "acme", "job-1", -1, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d chunks, %v, want 2", len(first), err)
	}

	rest, err := s.ListChunks(t.Context(), "acme", "job-1", first[len(first)-1].Seq, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if got := slices.Concat(first, rest); !slices.Equal(got, want) {
		t.Errorf("chunks = %+v, want %+v", got, want)
	}
}

func testLogChunkRetry(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	mustCreateLog(t, s, newJobLog(t, s, unitOf("ws-one"), "job-1", fence), fence)
	mustAppendChunk(t, s, chunk(t, 0, "a"), fence)
	mustAppendChunk(t, s, chunk(t, 0, "a"), fence)

	got, err := s.ListChunks(t.Context(), "acme", "job-1", -1, 10)
	if err != nil || len(got) != 1 {
		t.Errorf("chunks after a retry = %d, %v, want 1", len(got), err)
	}
}

func testLogChunkConflict(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	mustCreateLog(t, s, newJobLog(t, s, unitOf("ws-one"), "job-1", fence), fence)
	mustAppendChunk(t, s, chunk(t, 0, "a"), fence)

	err := s.AppendChunk(
		t.Context(),
		"acme",
		"job-1",
		chunk(t, 0, "z"),
		fence,
	)
	if !errors.Is(
		err,
		history.ErrChunkConflict,
	) {
		t.Errorf("conflicting chunk err = %v, want ErrChunkConflict", err)
	}
}

func testLogChunkGap(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	mustCreateLog(t, s, newJobLog(t, s, unitOf("ws-one"), "job-1", fence), fence)
	mustAppendChunk(t, s, chunk(t, 0, "a"), fence)

	err := s.AppendChunk(
		t.Context(),
		"acme",
		"job-1",
		chunk(t, 2, "c"),
		fence,
	)
	if !errors.Is(
		err,
		history.ErrChunkOutOfOrder,
	) {
		t.Errorf("chunk after a gap err = %v, want ErrChunkOutOfOrder", err)
	}

	err = s.AppendChunk(
		t.Context(),
		"acme",
		"job-2",
		chunk(t, 0, "a"),
		fence,
	)
	if !errors.Is(
		err,
		history.ErrJobLogNotFound,
	) {
		t.Errorf("chunk of a missing log err = %v, want ErrJobLogNotFound", err)
	}
}

func testLogClose(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	mustCreateLog(t, s, newJobLog(t, s, unitOf("ws-one"), "job-1", fence), fence)
	mustAppendChunk(t, s, chunk(t, 0, "abc"), fence)
	closed := mustClose(t, s, "job-1", fence)

	if !closed.Closed() || closed.Size != 3 || closed.SHA256 != sha256.Sum256([]byte("abc")) {
		t.Errorf("closed log = %+v, want closed with the totals", closed)
	}

	err := s.AppendChunk(
		t.Context(),
		"acme",
		"job-1",
		chunk(t, 1, "d"),
		fence,
	)
	if !errors.Is(
		err,
		history.ErrJobLogClosed,
	) {
		t.Errorf("chunk after close err = %v, want ErrJobLogClosed", err)
	}

	again, err := s.CloseJobLog(t.Context(), "acme", "job-1", totals(99, "other", 2*time.Hour), fence)
	if err != nil {
		t.Fatalf("second close: %v", err)
	}

	if again.Size != 3 || !again.ClosedAt.Equal(closed.ClosedAt) {
		t.Errorf("second close = %+v, want the first close kept", again)
	}
}

func testLogRedactOpen(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	l := newJobLog(t, s, unitOf("ws-one"), "job-1", fence)
	mustCreateLog(t, s, l, fence)

	_, err := s.Redact(t.Context(), redactionOfItem(l.Item(), l.ContentKey), nil, changeEntry(l.Item()), fence)
	if !errors.Is(err, history.ErrInProgress) {
		t.Errorf("redact of an open log err = %v, want ErrInProgress", err)
	}
}

func testLogRedactClosed(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	l := newJobLog(t, s, unitOf("ws-one"), "job-1", fence)
	mustCreateLog(t, s, l, fence)
	mustAppendChunk(t, s, chunk(t, 0, "abc"), fence)
	mustClose(t, s, "job-1", fence)
	r := redactionOfItem(l.Item(), l.ContentKey)

	if _, err := s.Redact(t.Context(), r, nil, changeEntry(l.Item()), fence); err != nil {
		t.Fatalf("redact: %v", err)
	}

	got, err := s.GetJobLog(t.Context(), "acme", "job-1")
	if err != nil || got.Redaction != r.ID || got.Size != 3 {
		t.Errorf("log after redact = %+v, %v, want the totals kept and the redaction %s", got, err, r.ID)
	}

	ref, err := l.Item().ContentRef()
	if err != nil {
		t.Fatalf("content ref: %v", err)
	}

	if _, err := s.GetContentKey(t.Context(), ref); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("content key after redact err = %v, want ErrShredded", err)
	}
}

func testLogExpired(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	unit := unitOf("ws-one")

	for _, job := range []string{"job-b", "job-a", "job-open"} {
		l := newJobLog(t, s, unit, job, fence)
		l.ExpiresAt = suiteTime().Add(time.Hour)
		mustCreateLog(t, s, l, fence)
	}

	mustClose(t, s, "job-b", fence)
	mustClose(t, s, "job-a", fence)

	got, err := s.ListExpiredJobLogs(t.Context(), suiteTime().Add(2*time.Hour), 10)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}

	jobs := make([]string, 0, len(got))
	for i := range got {
		l := got[i]
		jobs = append(jobs, l.JobID)
	}

	if want := []string{"job-a", "job-b"}; !slices.Equal(jobs, want) {
		t.Errorf("expired = %v, want %v (the open log left out)", jobs, want)
	}
}

func testLogFenced(t *testing.T, s JobLogSuiteStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	stale := staleFence(fence)
	l := newJobLog(t, s, unitOf("ws-one"), "job-1", fence)

	if err := s.CreateJobLog(t.Context(), l, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create with a stale fence err = %v, want ErrFenced", err)
	}

	mustCreateLog(t, s, l, fence)

	err := s.AppendChunk(
		t.Context(),
		"acme",
		"job-1",
		chunk(t, 0, "a"),
		stale,
	)
	if !errors.Is(
		err,
		namespace.ErrFenced,
	) {
		t.Errorf("append with a stale fence err = %v, want ErrFenced", err)
	}

	_, err = s.CloseJobLog(t.Context(), "acme", "job-1", totals(0, "", 0), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("close with a stale fence err = %v, want ErrFenced", err)
	}
}
