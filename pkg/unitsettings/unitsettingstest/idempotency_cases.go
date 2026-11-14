package unitsettingstest

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// The statuses of the answers that the cases build.
const (
	// decimal is the base of the numbers in the bodies of the answers.
	decimal         = 10
	statusOK        = 200
	statusNoContent = 204
	// pendingPrincipal and pendingPath name the caller and the route of the
	// replay records of the cases.
	pendingPrincipal = "alice"
	pendingPath      = "/settings"
)

func idempotencyCases() []suiteCase {
	return []suiteCase{
		{name: "put with a completion stores the answer with the stored settings", test: testCompletionPut},
		{name: "put of equal settings with a completion stores the answer", test: testCompletionPutNoChange},
		{name: "delete with a completion stores the answer", test: testCompletionDelete},
		{
			name: "set defaults with a completion stores the answer with the stored defaults",
			test: testCompletionDefaults,
		},
		{
			name: "a failed write with a completion stores no answer and keeps the record pending",
			test: testCompletionKeptPending,
		},
		{name: "put with a lost record returns lost and stores nothing", test: testCompletionPutLost},
		{name: "delete with a lost record returns lost and keeps the record", test: testCompletionDeleteLost},
		{name: "set defaults with a lost record returns lost and stores nothing", test: testCompletionDefaultsLost},
	}
}

// answerOf returns a builder that answers with a body made by text from the
// result of the write. It fails the answer for a result that text refuses.
func answerOf(status int, text func(result any) (string, bool)) func(result any) (idempotency.Answer, error) {
	return func(result any) (idempotency.Answer, error) {
		body, ok := text(result)
		if !ok {
			return idempotency.Answer{}, errNotWritten
		}

		return idempotency.Answer{Status: status, Headers: nil, Body: []byte(body)}, nil
	}
}

var errNotWritten = errors.New("the store did not pass the expected result")

// pending starts a replay record in the records of h and returns a context
// that carries the Completion of the request.
func pending(
	t *testing.T, h Harness, key string, build func(result any) (idempotency.Answer, error),
) (context.Context, *idempotency.Completion, idempotency.Ref) {
	t.Helper()

	ref := idempotency.Ref{Principal: pendingPrincipal, Method: "PUT", Path: pendingPath, Key: idempotency.Key(key)}

	res, err := h.Records.Begin(t.Context(), ref, []byte("body"), writeTime())
	if err != nil || res.Outcome != idempotency.Started {
		t.Fatalf("Begin: outcome = %d, err = %v, want Started", res.Outcome, err)
	}

	c := idempotency.NewCompletion(ref, res.Token, build)

	return idempotency.WithCompletion(t.Context(), c), c, ref
}

// replayOf returns the answer that a repeat of the request finds.
func replayOf(t *testing.T, h Harness, ref idempotency.Ref) idempotency.Answer {
	t.Helper()

	res, err := h.Records.Begin(t.Context(), ref, []byte("body"), writeTime())
	if err != nil || res.Outcome != idempotency.Replayed {
		t.Fatalf("Begin after the write: outcome = %d, err = %v, want Replayed", res.Outcome, err)
	}

	return res.Answer
}

func settingsText(result any) (string, bool) {
	out, ok := result.(unitsettings.PutResult)
	if !ok {
		return "", false
	}

	return out.Settings.Unit.UnitID() + "/" + strconv.FormatInt(out.Settings.Version, decimal), true
}

func testCompletionPut(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	ctx, c, ref := pending(t, h, "put", answerOf(statusOK, settingsText))

	if err := putIn(ctx, h, &s, 0, fence); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if want := s.Unit.UnitID() + "/1"; !c.Stored() || string(replayOf(t, h, ref).Body) != want {
		t.Errorf("put: stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, h, ref).Body, want)
	}
}

func testCompletionPutNoChange(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	ctx, c, ref := pending(t, h, "same", answerOf(statusOK, settingsText))

	if err := putIn(ctx, h, &s, 1, fence); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if want := s.Unit.UnitID() + "/1"; !c.Stored() || string(replayOf(t, h, ref).Body) != want {
		t.Errorf("put of equal settings: stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, h, ref).Body, want)
	}
}

func testCompletionDelete(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	unitText := func(result any) (string, bool) {
		got, ok := result.(unitsettings.Settings)

		return got.Unit.UnitID(), ok
	}
	ctx, c, ref := pending(t, h, "delete", answerOf(statusNoContent, unitText))

	if err := deleteIn(ctx, h, s.Unit, 1, fence); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := replayOf(t, h, ref); !c.Stored() || got.Status != statusNoContent || string(got.Body) != s.Unit.UnitID() {
		t.Errorf("delete: stored = %t, replay = %d %q, want true, 204, the unit ID", c.Stored(), got.Status, got.Body)
	}
}

func testCompletionDefaults(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	poolText := func(result any) (string, bool) {
		d, ok := result.(unitsettings.NamespaceDefaults)

		return d.Pool + "/" + strconv.FormatInt(d.Version, decimal), ok
	}
	ctx, c, ref := pending(t, h, "defaults", answerOf(statusOK, poolText))

	if _, err := setDefaultsIn(ctx, h, poolDefaults(fence.Namespace, testPool), 0, fence); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}

	if want := testPool + "/1"; !c.Stored() || string(replayOf(t, h, ref).Body) != want {
		t.Errorf("set defaults: stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, h, ref).Body, want)
	}
}

func testCompletionKeptPending(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	ctx, c, ref := pending(t, h, "conflict", answerOf(statusOK, settingsText))

	if err := putIn(ctx, h, &s, wrongVersion, fence); !errors.Is(
		err,
		unitsettings.ErrConflict,
	) {
		t.Fatalf("Put = %v, want ErrConflict", err)
	}

	res, err := h.Records.Begin(t.Context(), ref, []byte("body"), writeTime())
	if err != nil || res.Outcome != idempotency.InProgress || c.Stored() {
		t.Errorf("after a failed write: outcome = %d, err = %v, stored = %t, want InProgress, nil, false",
			res.Outcome, err, c.Stored())
	}
}

// lose expires the pending record of ref and lets a retry take the key, so the
// Completion of the first request has no pending record.
func lose(t *testing.T, h Harness, ref idempotency.Ref) {
	t.Helper()

	late := writeTime().Add(idempotency.PendingTTL + time.Second)

	retry, err := h.Records.Begin(t.Context(), ref, []byte("body"), late)
	if err != nil || retry.Outcome != idempotency.Started {
		t.Fatalf("retry: outcome = %d, err = %v, want Started", retry.Outcome, err)
	}
}

func testCompletionPutLost(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	ctx, c, ref := pending(t, h, "lost", answerOf(statusOK, settingsText))
	lose(t, h, ref)

	if err := putIn(ctx, h, &s, 0, fence); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("Put = %v, want ErrLost", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); !errors.Is(err, unitsettings.ErrNotFound) {
		t.Errorf("Get after a lost record = %v, want ErrNotFound", err)
	}

	if n := len(h.Audit(t, "acme")); n != 0 || c.Stored() {
		t.Errorf("after a lost record: %d audit entries, stored = %t, want 0, false", n, c.Stored())
	}
}

func testCompletionDeleteLost(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	s := settingsFor(h.Unit(t, "acme", "pulumi", "web/prod"))
	put(t, h, &s, 0, fence)

	ctx, _, ref := pending(t, h, "lost-delete", answerOf(statusNoContent, func(any) (string, bool) { return "", true }))
	lose(t, h, ref)

	if err := deleteIn(ctx, h, s.Unit, 1, fence); !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("Delete = %v, want ErrLost", err)
	}

	if _, err := h.Store.Get(t.Context(), s.Unit); err != nil {
		t.Errorf("Get after a lost record: %v, want the record", err)
	}

	if n := len(h.Audit(t, "acme")); n != 1 {
		t.Errorf("after a lost record: %d audit entries, want 1", n)
	}
}

func testCompletionDefaultsLost(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	ctx, _, ref := pending(t, h, "lost-defaults", answerOf(statusOK, func(any) (string, bool) { return "", true }))
	lose(t, h, ref)

	_, err := setDefaultsIn(ctx, h, poolDefaults(fence.Namespace, testPool), 0, fence)
	if !errors.Is(err, idempotency.ErrLost) {
		t.Fatalf("SetDefaults = %v, want ErrLost", err)
	}

	if d, getErr := h.Store.Defaults(t.Context(), fence.Namespace); getErr != nil || d.Version != 0 {
		t.Errorf("Defaults after a lost record = %+v, %v, want version 0", d, getErr)
	}

	if n := len(h.Audit(t, "acme")); n != 0 {
		t.Errorf("after a lost record: %d audit entries, want 0", n)
	}
}
