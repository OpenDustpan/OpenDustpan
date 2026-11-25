package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/textbody"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// freezesPath is the path of the change freezes of a namespace.
	freezesPath = "/api/dustpan/v1/namespaces/{namespace}/freezes"
	// currentFreezeEndPath is the path that ends the active freeze of a namespace.
	currentFreezeEndPath = freezesPath + "/current/end"
	// freezeIDPrefix is the prefix of the ID of a freeze.
	freezeIDPrefix = "frz-"
	// maxFreezeBody bounds the body of a create. A reason of 500 characters is far less.
	maxFreezeBody = 16 << 10
	// maxFreezeReason is the largest number of characters of the reason of a freeze.
	maxFreezeReason = audit.MaxReasonLen
)

// errFreezeUnlisted is the failure of a store that does not list a freeze that
// it has just ended. The answer is a 500 with the generic text.
var errFreezeUnlisted = errors.New("the store does not list an ended freeze")

// errNoActiveFreeze is the answer when a namespace has no active freeze to end.
var errNoActiveFreeze = errors.New(
	"no change freeze is active in the namespace; list the freezes with GET /api/dustpan/v1/namespaces/{namespace}/freezes",
)

// freezeBody is the body of the request that creates a freeze. A field that is
// not in this type fails the request.
type freezeBody struct {
	Reason string `json:"reason"`
}

// freezeResponse is the answered document of a freeze. EndsAt and EndedBy are
// empty while the freeze is open.
type freezeResponse struct {
	ID        string     `json:"id"`
	Namespace string     `json:"namespace"`
	Reason    string     `json:"reason"`
	CreatedBy string     `json:"createdBy"`
	StartsAt  time.Time  `json:"startsAt"`
	EndsAt    *time.Time `json:"endsAt,omitempty"`
	EndedBy   string     `json:"endedBy,omitempty"`
	Active    bool       `json:"active"`
}

// listFreezesResponse is the answered document of the freeze list.
type listFreezesResponse struct {
	Freezes []freezeResponse `json:"freezes"`
}

// createFreeze answers POST /api/dustpan/v1/namespaces/{namespace}/freezes with
// the freeze that the request stores. The route wrapper allowed
// namespace:manage-freezes on the namespace. The store checks the fence and
// refuses a second open freeze of the namespace in its transaction.
func (h *Handler) createFreeze(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if h.refuseFreezeKey(ctx, w, r) {
		return
	}

	var body freezeBody

	err := textbody.Decode(w, r, maxFreezeBody, &body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the store checks the reason again. This check stays so
	// that a reason that is too long or not valid never reaches the store.
	err = checkFreezeReason(body.Reason)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	now := h.clock().UTC()
	f := job.Freeze{
		ID: ident.NewID(freezeIDPrefix), Namespace: t.ns.Name, Reason: body.Reason, StartsAt: now,
		EndsAt: time.Time{}, CreatedBy: p.Subject, EndedBy: "",
	}

	e := freezeEntry(now, p, t.ns.Name, f.ID, f.Reason)

	err = h.freezes.CreateFreeze(ctx, f, t.ns.Fence(), e)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, toFreezeResponse(&f, now))
}

// endFreeze answers POST /api/dustpan/v1/namespaces/{namespace}/freezes/current/end
// with the freeze after its end. The route wrapper allowed
// namespace:manage-freezes on the namespace. A namespace with no active freeze
// answers 404.
func (h *Handler) endFreeze(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if h.refuseFreezeKey(ctx, w, r) {
		return
	}

	now := h.clock().UTC()

	active, found, err := h.activeFreeze(ctx, t.ns.Name, now)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !found {
		h.writeError(ctx, w, errNoActiveFreeze)

		return
	}

	e := freezeEntry(now, p, t.ns.Name, active.ID, "")

	err = h.freezes.EndFreeze(ctx, active.ID, p.Subject, t.ns.Fence(), e)
	// A freeze that another caller ended after the read is no active freeze now.
	if errors.Is(err, job.ErrFinished) {
		h.writeError(ctx, w, errNoActiveFreeze)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ended, err := h.freezeByID(ctx, t.ns.Name, active.ID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toFreezeResponse(&ended, h.clock()))
}

// listFreezes answers GET /api/dustpan/v1/namespaces/{namespace}/freezes with
// the freezes of the namespace in the order of their start. The route wrapper
// allowed namespace:read on the namespace.
func (h *Handler) listFreezes(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()

	freezes, err := h.freezes.ListFreezes(ctx, t.ns.Name, time.Time{})
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("list freezes: %w", err))

		return
	}

	now := h.clock()
	out := listFreezesResponse{Freezes: make([]freezeResponse, 0, len(freezes))}

	for i := range freezes {
		out.Freezes = append(out.Freezes, toFreezeResponse(&freezes[i], now))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// activeFreeze returns the freeze of ns that is active at now. The bool is false
// when no freeze is active.
func (h *Handler) activeFreeze(ctx context.Context, ns namespace.Name, now time.Time) (job.Freeze, bool, error) {
	var none job.Freeze

	freezes, err := h.freezes.ListFreezes(ctx, ns, time.Time{})
	if err != nil {
		return none, false, fmt.Errorf("list freezes: %w", err)
	}

	i := slices.IndexFunc(freezes, func(f job.Freeze) bool { return f.ActiveAt(now) })
	if i < 0 {
		return none, false, nil
	}

	return freezes[i], true, nil
}

// freezeByID returns the freeze of ns with the ID id. A freeze that the store
// does not list after its own write is a failure of the store.
func (h *Handler) freezeByID(ctx context.Context, ns namespace.Name, id string) (job.Freeze, error) {
	var none job.Freeze

	freezes, err := h.freezes.ListFreezes(ctx, ns, time.Time{})
	if err != nil {
		return none, fmt.Errorf("list freezes: %w", err)
	}

	i := slices.IndexFunc(freezes, func(f job.Freeze) bool { return f.ID == id })
	if i < 0 {
		return none, fmt.Errorf("%w: freeze %s", errFreezeUnlisted, id)
	}

	return freezes[i], nil
}

// refuseFreezeKey answers 400 when the request holds an Idempotency-Key header,
// because the freeze routes take none, and reports true. The server never
// ignores a key in silence.
func (h *Handler) refuseFreezeKey(ctx context.Context, w http.ResponseWriter, r *http.Request) bool {
	if len(apidesc.HeaderValues(r, idempotencyHeader)) == 0 {
		return false
	}

	h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

	return true
}

// checkFreezeReason checks the reason of a freeze: 1 to 500 characters, with no
// character that the audit log refuses.
func checkFreezeReason(reason string) error {
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxFreezeReason {
		return fmt.Errorf("%w: the reason of a freeze has 1 to %d characters", job.ErrInvalid, maxFreezeReason)
	}

	err := audit.ValidateReason(reason)
	if err != nil {
		return fmt.Errorf("%w: reason: %w", job.ErrInvalid, err)
	}

	return nil
}

// freezeEntry returns the audit entry of a freeze write by p on the namespace
// ns. The action is the freeze manage action of the namespace. The entry names
// the subject, the freeze and the reason, and no request data.
func freezeEntry(at time.Time, p identity.Principal, ns namespace.Name, freezeID, reason string) audit.Entry {
	action := identity.ActionNamespaceManageFreezes

	return audit.Entry{
		At: at, Namespace: ns, Subject: p.Subject, IDPSubject: "", TokenID: p.TokenID, Action: string(action),
		Object: identity.NamespaceObject(ns).String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: freezeID, PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		PrincipalKind: string(p.Kind), JobID: "", Reason: reason, Origin: nil,
	}
}

// toFreezeResponse maps a freeze to its wire shape at the time now.
func toFreezeResponse(f *job.Freeze, now time.Time) freezeResponse {
	return freezeResponse{
		ID: f.ID, Namespace: f.Namespace.String(), Reason: f.Reason, CreatedBy: f.CreatedBy,
		StartsAt: f.StartsAt, EndsAt: timeOrNil(f.EndsAt), EndedBy: f.EndedBy, Active: f.ActiveAt(now),
	}
}
