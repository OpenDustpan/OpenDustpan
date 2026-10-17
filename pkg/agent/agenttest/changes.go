package agenttest

import (
	"encoding/json"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/identity"
)

// ChangesHarness is one fresh job model for one case of the changes suite.
type ChangesHarness struct {
	Changes agent.Changes
	// Actor holds every action of both kinds on Unit, and can read and
	// cancel its changes.
	Actor agent.Actor
	// Outsider holds no action on Unit.
	Outsider agent.Actor
	Unit     identity.Object
	// WriteBackKind is a kind of change that supports a review request for
	// Unit. RunKind is a kind that does not.
	WriteBackKind string
	RunKind       string
	// Freeze starts a change freeze that covers Unit.
	Freeze func(t *testing.T)
}

// ChangesFactory returns a fresh harness for one case.
type ChangesFactory func(t *testing.T) ChangesHarness

// changesCase is one case of the changes suite.
type changesCase struct {
	name string
	run  func(t *testing.T, h ChangesHarness)
}

// RunChanges runs the changes suite against the job model that f makes.
func RunChanges(t *testing.T, f ChangesFactory) {
	t.Helper()

	for _, c := range changesCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// ChangeRequestFor returns a request of h.Actor for a change of kind on
// h.Unit in mode, with key.
func ChangeRequestFor(h ChangesHarness, key, kind string, mode agent.ChangeMode) agent.ChangeRequest {
	return agent.ChangeRequest{
		Key:    key,
		Actor:  h.Actor,
		Unit:   h.Unit,
		Kind:   kind,
		Mode:   mode,
		Spec:   json.RawMessage(`{"replicas":3}`),
		Reason: "The service needs a third replica.",
	}
}

func changesCases() []changesCase {
	return []changesCase{
		{name: "support of each kind", run: caseSupport},
		{name: "propose approve-first", run: caseProposeApproveFirst},
		{name: "propose twice with one key", run: caseProposeTwice},
		{name: "one key with another spec", run: caseKeyConflict},
		{name: "review request without write-back", run: caseModeNotSupported},
		{name: "change freeze", run: caseFrozen},
		{name: "actor without the action", run: caseDenied},
		{name: "unknown kind", run: caseUnknownKind},
		{name: "empty key", run: caseEmptyKey},
		{name: "get", run: caseGet},
		{name: "cancel", run: caseCancel},
	}
}

func caseSupport(t *testing.T, h ChangesHarness) {
	t.Helper()

	for kind, want := range map[string]bool{h.WriteBackKind: true, h.RunKind: false} {
		got, err := h.Changes.Support(t.Context(), h.Unit, kind)
		if err != nil || got.WriteBack != want {
			t.Fatalf("Support(%q) = %+v, %v, want write-back %v", kind, got, err, want)
		}
	}
}

func caseProposeApproveFirst(t *testing.T, h ChangesHarness) {
	t.Helper()

	c, err := h.Changes.Propose(t.Context(), ChangeRequestFor(h, "k1", h.RunKind, agent.ModeApproveFirst))
	if err != nil {
		t.Fatalf("Propose() = %v", err)
	}

	switch {
	case c.ID == "" || c.Key != "k1" || c.Kind != h.RunKind || c.Unit.String() != h.Unit.String():
		t.Fatalf("Propose() = %+v, want an ID and the key, kind and unit of the request", c)
	case c.Mode != agent.ModeApproveFirst || c.State != agent.ChangeWaiting:
		t.Fatalf("Propose() mode %q state %q, want approve-first waiting", c.Mode, c.State)
	case c.Proposer != h.Actor.Principal.Subject || c.RunID != h.Actor.RunID:
		t.Fatalf(
			"Propose() proposer %q run %q, want %q %q",
			c.Proposer,
			c.RunID,
			h.Actor.Principal.Subject,
			h.Actor.RunID,
		)
	default:
	}
}

func caseProposeTwice(t *testing.T, h ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.WriteBackKind, agent.ModeReviewRequest)

	first, err := h.Changes.Propose(t.Context(), req)
	if err != nil {
		t.Fatalf("first Propose() = %v", err)
	}

	second, err := h.Changes.Propose(t.Context(), req)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second Propose() = %+v, %v, want change %q", second, err, first.ID)
	}
}

func caseKeyConflict(t *testing.T, h ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunKind, agent.ModeApproveFirst)
	if _, err := h.Changes.Propose(t.Context(), req); err != nil {
		t.Fatalf("first Propose() = %v", err)
	}

	req.Spec = json.RawMessage(`{"replicas":30}`)
	c, err := h.Changes.Propose(t.Context(), req)
	wantChangeErr(t, "Propose() with another spec", c, err, agent.ErrChangeKeyConflict)
}

func caseModeNotSupported(t *testing.T, h ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunKind, agent.ModeReviewRequest)
	c, err := h.Changes.Propose(t.Context(), req)
	wantChangeErr(t, "Propose()", c, err, agent.ErrModeNotSupported)
}

func caseFrozen(t *testing.T, h ChangesHarness) {
	t.Helper()

	h.Freeze(t)

	for _, mode := range []agent.ChangeMode{agent.ModeApproveFirst, agent.ModeDirect} {
		req := ChangeRequestFor(h, "k-"+string(mode), h.RunKind, mode)
		c, err := h.Changes.Propose(t.Context(), req)
		wantChangeErr(t, "Propose() in "+string(mode), c, err, agent.ErrChangeFrozen)
	}
}

func caseDenied(t *testing.T, h ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunKind, agent.ModeApproveFirst)
	req.Actor = h.Outsider
	c, err := h.Changes.Propose(t.Context(), req)
	wantChangeErr(t, "Propose() by the outsider", c, err, identity.ErrDenied)
}

func caseUnknownKind(t *testing.T, h ChangesHarness) {
	t.Helper()

	if _, err := h.Changes.Support(t.Context(), h.Unit, "no-such-kind"); !errors.Is(err, agent.ErrUnknownChangeKind) {
		t.Fatalf("Support() = %v, want ErrUnknownChangeKind", err)
	}

	req := ChangeRequestFor(h, "k1", "no-such-kind", agent.ModeApproveFirst)
	c, err := h.Changes.Propose(t.Context(), req)
	wantChangeErr(t, "Propose()", c, err, agent.ErrUnknownChangeKind)
}

func caseEmptyKey(t *testing.T, h ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "", h.RunKind, agent.ModeApproveFirst)
	c, err := h.Changes.Propose(t.Context(), req)
	wantChangeErr(t, "Propose()", c, err, agent.ErrInvalid)
}

func caseGet(t *testing.T, h ChangesHarness) {
	t.Helper()

	c, err := h.Changes.Propose(t.Context(), ChangeRequestFor(h, "k1", h.RunKind, agent.ModeApproveFirst))
	if err != nil {
		t.Fatalf("Propose() = %v", err)
	}

	got, err := h.Changes.Get(t.Context(), h.Actor, c.ID)
	if err != nil || got.ID != c.ID || got.State != agent.ChangeWaiting {
		t.Fatalf("Get() = %+v, %v, want the waiting change %q", got, err, c.ID)
	}

	got, err = h.Changes.Get(t.Context(), h.Outsider, c.ID)
	wantChangeErr(t, "Get() by the outsider", got, err, agent.ErrChangeNotFound)
	got, err = h.Changes.Get(t.Context(), h.Actor, "no-such-change")
	wantChangeErr(t, "Get() of an unknown ID", got, err, agent.ErrChangeNotFound)
}

func caseCancel(t *testing.T, h ChangesHarness) {
	t.Helper()

	c, err := h.Changes.Propose(t.Context(), ChangeRequestFor(h, "k1", h.RunKind, agent.ModeApproveFirst))
	if err != nil {
		t.Fatalf("Propose() = %v", err)
	}

	got, err := h.Changes.Cancel(t.Context(), h.Outsider, c.ID)
	wantChangeErr(t, "Cancel() by the outsider", got, err, agent.ErrChangeNotFound)

	for i := range 2 {
		canceled, cancelErr := h.Changes.Cancel(t.Context(), h.Actor, c.ID)
		if cancelErr != nil || canceled.State != agent.ChangeCanceled {
			t.Fatalf("Cancel() number %d = %+v, %v, want a canceled change", i+1, canceled, cancelErr)
		}
	}

	got, err = h.Changes.Cancel(t.Context(), h.Actor, "no-such-change")
	wantChangeErr(t, "Cancel() of an unknown ID", got, err, agent.ErrChangeNotFound)
}

// wantChangeErr fails t unless err wraps want. c is the change that the call
// answered, for the message.
func wantChangeErr(t *testing.T, what string, c agent.Change, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %+v, %v, want %v", what, c, err, want)
	}
}
