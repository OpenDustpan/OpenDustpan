package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pagetoken"
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
)

// errFreezeUnlisted is the failure of a store that does not list a freeze that
// it has just ended. The answer is a 500 with the generic text.
var errFreezeUnlisted = errors.New("the store does not list an ended freeze")

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
	Freezes       []freezeResponse `json:"freezes"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

// freezePageTokenWire is the opaque page token of the freeze list.
type freezePageTokenWire struct {
	After       string `json:"f"`
	Fingerprint string `json:"n"`
}

type freezePageQuery struct {
	after string
	size  int
}

func parseFreezePageQuery(r *http.Request, ns namespace.Name) (freezePageQuery, error) {
	query := queryOf(r)
	q := freezePageQuery{after: "", size: 0}

	size, err := parsePageSize(query.Get("pageSize"))
	if err != nil {
		return q, err
	}
	q.size = size

	raw := query.Get("pageToken")
	if raw == "" {
		return q, nil
	}

	var wire freezePageTokenWire
	if err := pagetoken.Decode(raw, &wire); err != nil || wire.After == "" ||
		!ident.Valid(wire.After, maxIDLength) ||
		!pagetoken.SameFingerprint(wire.Fingerprint, freezePageFingerprint(ns)) {
		return q, errPageToken
	}
	q.after = wire.After

	return q, nil
}

func freezePageFingerprint(ns namespace.Name) string {
	return pagetoken.Fingerprint("ns=" + ns.String())
}

// createFreeze answers POST /api/dustpan/v1/namespaces/{namespace}/freezes with
// the freeze that the request stores. The route wrapper allowed
// namespace:manage-freezes on the namespace. The store checks the fence and
// refuses a second open freeze of the namespace in its transaction.
func (h *Handler) createFreeze(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	if h.refuseFreezeKey(ctx, w, r, p, t.ns.Name) {
		return
	}

	var body freezeBody

	err := textbody.Decode(w, r, maxFreezeBody, &body)
	if err != nil {
		if errors.Is(err, textbody.ErrTooLarge) {
			err = fmt.Errorf("%w: the reason of a freeze is at most %d characters", job.ErrInvalid, audit.MaxReasonLen)
			h.recordFreezeRefusal(ctx, r, p, t.ns.Name, "the reason of a freeze is too long")
			h.writeError(ctx, w, err)

			return
		}
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, "the body of the freeze is not valid")
		h.writeError(ctx, w, err)

		return
	}

	err = job.CheckReason(body.Reason)
	if err != nil {
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, "the reason of a freeze is not valid")
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
		reason := "the store refused the freeze"
		if errors.Is(err, job.ErrFreezeActive) {
			reason = reasonFreezeActive
		}
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, reason)
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

	if h.refuseFreezeKey(ctx, w, r, p, t.ns.Name) {
		return
	}

	now := h.clock().UTC()

	active, found, err := h.activeFreeze(ctx, t.ns.Name, now)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !found {
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, reasonNoActiveFreeze)
		h.writeError(ctx, w, job.ErrNoActiveFreeze)

		return
	}

	e := freezeEntry(now, p, t.ns.Name, active.ID, "")

	err = h.freezes.EndFreeze(ctx, active.ID, p.Subject, t.ns.Fence(), e)
	// A freeze that another caller ended after the read is no active freeze now.
	if errors.Is(err, job.ErrFinished) {
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, reasonNoActiveFreeze)
		h.writeError(ctx, w, job.ErrNoActiveFreeze)

		return
	}

	if err != nil {
		h.recordFreezeRefusal(ctx, r, p, t.ns.Name, "the store refused the end of the freeze")
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
func (h *Handler) listFreezes(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
	ctx := r.Context()

	page, err := parseFreezePageQuery(r, t.ns.Name)
	if err != nil {
		reason := reasonPageToken
		if !errors.Is(err, errPageToken) {
			reason = "the query of the freeze list is not valid"
		}

		h.recordFreezeListRefusal(ctx, r, p, t.ns.Name, reason)
		h.writeError(ctx, w, err)

		return
	}

	freezes, err := h.freezes.ListFreezes(ctx, t.ns.Name, time.Time{})
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("list freezes: %w", err))

		return
	}

	start := 0
	if page.after != "" {
		start = slices.IndexFunc(freezes, func(f job.Freeze) bool { return f.ID == page.after }) + 1
		if start == 0 {
			h.recordFreezeListRefusal(ctx, r, p, t.ns.Name, reasonPageToken)
			h.writeError(ctx, w, errPageToken)

			return
		}
	}

	end := max(start, min(start+page.size, len(freezes)))
	shown := freezes[start:end]
	out := listFreezesResponse{Freezes: make([]freezeResponse, 0, len(shown)), NextPageToken: ""}
	now := h.clock()

	for i := range shown {
		out.Freezes = append(out.Freezes, toFreezeResponse(&shown[i], now))
	}

	if end < len(freezes) {
		token, err := pagetoken.Encode(freezePageTokenWire{
			After: shown[len(shown)-1].ID, Fingerprint: freezePageFingerprint(t.ns.Name),
		})
		if err != nil {
			h.writeError(ctx, w, fmt.Errorf("encode page token: %w", err))

			return
		}
		out.NextPageToken = token
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// activeFreeze returns the freeze of ns that is active at now. The bool is false
// when no freeze is active.
func (h *Handler) activeFreeze(ctx context.Context, ns namespace.Name, now time.Time) (job.Freeze, bool, error) {
	gate, err := job.NewFreezeGate(ctx, h.freezes, ns, now)
	if err != nil {
		return job.Freeze{}, false, err
	}

	active, found := gate.Active()

	return active, found, nil
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
func (h *Handler) refuseFreezeKey(
	ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name,
) bool {
	if len(apidesc.HeaderValues(r, idempotencyHeader)) == 0 {
		return false
	}

	h.recordFreezeRefusal(ctx, r, p, ns, "the freeze routes take no Idempotency-Key")

	h.writeError(ctx, w, fmt.Errorf("%w: this route takes no Idempotency-Key", idempotency.ErrInvalidKey))

	return true
}

const (
	reasonFreezeActive   = "a change freeze is active"
	reasonNoActiveFreeze = "no change freeze is active"
	reasonPageToken      = "the page token of the freeze list is not valid"
)

// recordFreezeRefusal records a refusal of a freeze write, which checks
// namespace:manage-freezes.
func (h *Handler) recordFreezeRefusal(
	ctx context.Context, r *http.Request, p identity.Principal, ns namespace.Name, reason string,
) {
	h.guard.Record(ctx, r, p, refusal.Denial{
		Action: identity.ActionNamespaceManageFreezes, Object: identity.NamespaceObject(ns), Reason: reason,
		ReasonWithin: nil, Window: 0,
	})
}

// recordFreezeListRefusal records a refusal of the freeze list, which checks
// namespace:read.
func (h *Handler) recordFreezeListRefusal(
	ctx context.Context, r *http.Request, p identity.Principal, ns namespace.Name, reason string,
) {
	h.guard.Record(ctx, r, p, refusal.Denial{
		Action: identity.ActionNamespaceRead, Object: identity.NamespaceObject(ns), Reason: reason,
		ReasonWithin: nil, Window: 0,
	})
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
