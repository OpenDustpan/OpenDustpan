package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

const (
	subscriptionsPath = v1Prefix + "/namespaces/{namespace}/subscriptions"
	subscriptionPath  = subscriptionsPath + "/{subscription_id}"
	historyPath       = subscriptionPath + "/deliveries"
	redeliverPath     = v1Prefix + "/namespaces/{namespace}/deliveries/{delivery_id}/redeliver"

	// defaultPageSize and maxPageSize bound a page of the delivery history.
	defaultPageSize = 50
	maxPageSize     = 200
)

// filterDocument is the filter of a subscription. An empty list selects every
// type or object. MinSeverity is info, warning or critical.
//
// Contract: never remove or rename these fields.
type filterDocument struct {
	Types       []string `dustpan:"optional" json:"types"`
	Objects     []string `dustpan:"optional" json:"objects"`
	MinSeverity string   `dustpan:"optional" json:"minSeverity"`
}

// subscriptionDocument is one subscription as the API answers it. Reason says
// why a suspended subscription delivers nothing. CreatedBy is the principal
// whose access the dispatcher checks before each delivery.
//
// Contract: never remove or rename these fields.
type subscriptionDocument struct {
	ID        string         `json:"id"`
	Namespace string         `json:"namespace"`
	Sink      string         `json:"sink"`
	Filter    filterDocument `json:"filter"`
	State     string         `json:"state"`
	Reason    string         `json:"reason,omitempty"`
	CreatedBy string         `json:"createdBy"`
	Version   int64          `json:"version"`
}

type subscriptionListResponse struct {
	Subscriptions []subscriptionDocument `json:"subscriptions"`
}

// deliveryDocument is one delivery as the API answers it. LastError is the
// class of the last error and nothing else. The claim of the dispatcher is not
// part of it.
//
// Contract: never remove or rename these fields.
type deliveryDocument struct {
	ID           string    `json:"id"`
	Notification string    `json:"notification"`
	Subscription string    `json:"subscription"`
	Sink         string    `json:"sink"`
	Contact      string    `json:"contact,omitempty"`
	Principal    string    `json:"principal"`
	State        string    `json:"state"`
	Attempts     int       `json:"attempts"`
	NextAt       time.Time `json:"nextAt"`
	LastError    string    `json:"lastError,omitempty"`
	DeliveredAt  time.Time `json:"deliveredAt,omitzero"`
	Version      int64     `json:"version"`
}

// historyResponse is one page of the delivery history, newest first.
// NextPageToken is empty on the last page.
type historyResponse struct {
	Deliveries    []deliveryDocument `json:"deliveries"`
	NextPageToken string             `json:"nextPageToken"`
}

// subscriptionCaller makes the caller of the subscription service from the
// request.
func subscriptionCaller(r *http.Request, p identity.Principal) *subscriptionsvc.Caller {
	c := caller(r, p)

	return &subscriptionsvc.Caller{Principal: c.Principal, By: c.By}
}

func toSubscriptionDocument(s *notify.Subscription) subscriptionDocument {
	return subscriptionDocument{
		ID: s.ID, Namespace: s.Scope.String(), Sink: s.Sink, Filter: toFilterDocument(s.Filter),
		State: string(s.State), Reason: s.Reason, CreatedBy: s.CreatedBy, Version: s.Version,
	}
}

func toFilterDocument(f notify.Filter) filterDocument {
	// The zero severity selects every notification, as info does.
	severity := max(f.MinSeverity, notify.SeverityInfo)

	return filterDocument{
		Types: nonNil(f.Types), Objects: nonNil(f.Objects), MinSeverity: severity.String(),
	}
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}

	return list
}

func toDeliveryDocument(d *notify.Delivery) deliveryDocument {
	return deliveryDocument{
		ID: d.ID, Notification: d.Notification, Subscription: d.Subscription, Sink: d.Sink, Contact: d.Contact,
		Principal: d.Principal, State: string(d.State), Attempts: d.Attempts, NextAt: d.NextAt.UTC(),
		LastError: d.LastError, DeliveredAt: d.DeliveredAt.UTC(), Version: d.Version,
	}
}

// filterBody is the filter of a write. A missing field selects everything.
type filterBody struct {
	Types       []string `dustpan:"optional" json:"types"`
	Objects     []string `dustpan:"optional" json:"objects"`
	MinSeverity *string  `dustpan:"optional" json:"minSeverity"`
}

// createSubscriptionBody is the body of a create.
type createSubscriptionBody struct {
	Sink   *string     `dustpan:"required" json:"sink"`
	Filter *filterBody `dustpan:"optional" json:"filter"`
}

// replaceSubscriptionBody is the body of a replace. State is active or
// suspended: a person resumes a suspended subscription with active.
type replaceSubscriptionBody struct {
	Version *int64      `dustpan:"required" json:"version"`
	Sink    *string     `dustpan:"required" json:"sink"`
	Filter  *filterBody `dustpan:"optional" json:"filter"`
	State   *string     `dustpan:"required" json:"state"`
}

var errSubscriptionResult = errors.New("the store passed a result that is not a subscription")

func subscriptionOf(result any) (notify.Subscription, error) {
	s, ok := result.(notify.Subscription)
	if !ok {
		return notify.Subscription{}, errSubscriptionResult
	}

	return s, nil
}

func subscriptionLocation(ns namespace.Name, id string) string {
	return v1Prefix + "/namespaces/" + url.PathEscape(ns.String()) + "/subscriptions/" + url.PathEscape(id)
}

// subscriptionCreatedAnswer answers a create: 201 with the subscription and
// its Location.
func subscriptionCreatedAnswer(*requestState) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		s, err := subscriptionOf(result)
		if err != nil {
			return idempotency.Answer{}, err
		}

		doc := toSubscriptionDocument(&s)

		return jsonAnswer(http.StatusCreated, doc, map[string]string{"Location": subscriptionLocation(s.Scope, s.ID)})
	}
}

// subscriptionReplacedAnswer answers a replace: 200 with the subscription.
func subscriptionReplacedAnswer(*requestState) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		s, err := subscriptionOf(result)
		if err != nil {
			return idempotency.Answer{}, err
		}

		return jsonAnswer(http.StatusOK, toSubscriptionDocument(&s), nil)
	}
}

// parseSeverity reads the minimum severity of a filter. A missing value is
// info, the lowest severity.
func parseSeverity(raw *string) (notify.Severity, error) {
	if raw == nil {
		return notify.SeverityInfo, nil
	}

	for _, s := range []notify.Severity{notify.SeverityInfo, notify.SeverityWarning, notify.SeverityCritical} {
		if s.String() == *raw {
			return s, nil
		}
	}

	return 0, problem.BadRequest(errcode.SinkSubscriptionInvalid, "minSeverity must be info, warning or critical")
}

// filterOf reads the filter of a body. A nil body selects everything.
func filterOf(b *filterBody) (notify.Filter, error) {
	if b == nil {
		return notify.Filter{Types: nil, Objects: nil, MinSeverity: notify.SeverityInfo}, nil
	}

	severity, err := parseSeverity(b.MinSeverity)
	if err != nil {
		return notify.Filter{}, err
	}

	return notify.Filter{Types: b.Types, Objects: b.Objects, MinSeverity: severity}, nil
}

// subscriptionErrors maps the errors of the subscription routes, and then the
// errors of the sink service.
func subscriptionErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, subscriptionsvc.ErrSinkNotInNamespace):
		return problem.New(http.StatusUnprocessableEntity, errcode.SinkSubscriptionSinkNotFound,
			"the namespace holds no notification destination with this ID: "+
				"use the ID of a destination of this namespace"), true
	case errors.Is(err, notify.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.SinkSubscriptionNotFound, "subscription not found"), true
	case errors.Is(err, notify.ErrConflict):
		return problem.New(http.StatusConflict, errcode.SinkSubscriptionConflict,
			"the subscription changed since you read it: read it again and send the new version"), true
	case errors.Is(err, notify.ErrInvalid):
		return problem.New(http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
			"the subscription is not valid: check the sink, the state and the filter"), true
	default:
		return sinkErrors(err)
	}
}

// deliveryErrors maps the errors of the redelivery route, and then the errors
// of the sink service.
func deliveryErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, subscriptionsvc.ErrNotRedeliverable):
		return problem.New(http.StatusConflict, errcode.SinkDeliveryNotRedeliverable,
			"the delivery cannot be sent again: only a delivered, abandoned or skipped delivery of an active "+
				"subscription that sends to the same destination can be sent again"), true
	case errors.Is(err, notify.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.SinkDeliveryNotFound, "delivery not found"), true
	default:
		return sinkErrors(err)
	}
}

// writeSubscriptionError answers err with the codes of the subscription
// routes.
func (h *Handler) writeSubscriptionError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: subscriptionErrors}.Write(ctx, w, err)
}

// writeDeliveryError answers err with the codes of the delivery routes.
func (h *Handler) writeDeliveryError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: deliveryErrors}.Write(ctx, w, err)
}

// listSubscriptions serves GET /subscriptions.
func (h *Handler) listSubscriptions(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	all, err := h.subscriptions.List(ctx, obj.Namespace())
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	resp := subscriptionListResponse{Subscriptions: make([]subscriptionDocument, 0, len(all))}
	for i := range all {
		resp.Subscriptions = append(resp.Subscriptions, toSubscriptionDocument(&all[i]))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, resp)
}

// getSubscription serves GET /subscriptions/{subscription_id}.
func (h *Handler) getSubscription(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	s, err := h.subscriptions.Get(ctx, obj.Namespace(), r.PathValue("subscription_id"))
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, toSubscriptionDocument(&s))
}

// createSubscription serves POST /subscriptions.
func (h *Handler) createSubscription(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	var body createSubscriptionBody
	if err := route.DecodeBody(w, r, maxBodyBytes, &body); err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	if body.Sink == nil {
		h.writeSubscriptionError(ctx, w, problem.BadRequest(errcode.SinkSubscriptionInvalid, "send the field sink"))

		return
	}

	filter, err := filterOf(body.Filter)
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	made, err := h.subscriptions.Create(ctx, subscriptionCaller(r, p), obj.Namespace(), subscriptionsvc.New{
		Sink: *body.Sink, Filter: filter,
	})
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	h.send(w, r, subscriptionCreatedAnswer(stateOf(ctx)), made)
}

// replaceSubscription serves PUT /subscriptions/{subscription_id}.
func (h *Handler) replaceSubscription(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	var body replaceSubscriptionBody
	if err := route.DecodeBody(w, r, maxBodyBytes, &body); err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	if body.Version == nil || body.Sink == nil || body.State == nil {
		h.writeSubscriptionError(ctx, w, problem.BadRequest(errcode.SinkSubscriptionInvalid,
			"send the fields version, sink and state"))

		return
	}

	filter, err := filterOf(body.Filter)
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	next, err := h.subscriptions.Replace(ctx, subscriptionCaller(r, p), obj.Namespace(), r.PathValue("subscription_id"),
		subscriptionsvc.Change{
			Version: *body.Version, Sink: *body.Sink, Filter: filter, State: notify.SubscriptionState(*body.State),
		})
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	h.send(w, r, subscriptionReplacedAnswer(stateOf(ctx)), next)
}

// deleteSubscription serves DELETE /subscriptions/{subscription_id}.
func (h *Handler) deleteSubscription(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	id := r.PathValue("subscription_id")

	if err := h.subscriptions.Delete(ctx, subscriptionCaller(r, p), obj.Namespace(), id); err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	h.send(w, r, deletedAnswer(stateOf(ctx)), id)
}

// historyToken is the page token of the delivery history. NextAt is the Unix
// millisecond time of the last delivery of the page before and ID is its ID:
// the next page starts after that pair. Fingerprint binds the token to one
// namespace and subscription.
type historyToken struct {
	NextAt      int64  `json:"t"`
	ID          string `json:"i"`
	Fingerprint string `json:"f"`
}

func historyFingerprint(ns namespace.Name, id string) string {
	return ns.String() + "\x00" + id
}

var errPageToken = problem.BadRequest(errcode.RequestPageTokenInvalid,
	"pageToken does not belong to this subscription: start again without it")

// historyPage reads the size and the cursor of the request.
func historyPage(r *http.Request, fp string) (subscriptionsvc.HistoryRequest, error) {
	q := queryOf(r)
	size := defaultPageSize

	if raw := q.Get("pageSize"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 || n > maxPageSize {
			return subscriptionsvc.HistoryRequest{}, problem.BadRequest(errcode.RequestInvalidQuery,
				fmt.Sprintf("pageSize must be a number from 1 to %d", maxPageSize))
		}

		size = n
	}

	raw := q.Get("pageToken")
	if raw == "" {
		return subscriptionsvc.HistoryRequest{After: nil, Size: size}, nil
	}

	var t historyToken
	if decodeErr := pagetoken.Decode(raw, &t); decodeErr != nil || !pagetoken.SameFingerprint(t.Fingerprint, fp) ||
		t.NextAt < 0 {
		return subscriptionsvc.HistoryRequest{}, errPageToken
	}

	// Defense in depth: the store binds the cursor ID as a parameter, so a
	// value that is not a delivery ID cannot change a query. This check stays
	// so that a value of a token never reaches a store as a cursor at all.
	if !notify.ValidDeliveryID(t.ID) {
		return subscriptionsvc.HistoryRequest{}, errPageToken
	}

	return subscriptionsvc.HistoryRequest{
		After: &notify.DeliveryCursor{NextAt: time.UnixMilli(t.NextAt).UTC(), ID: t.ID},
		Size:  size,
	}, nil
}

// history serves GET /subscriptions/{subscription_id}/deliveries.
func (h *Handler) history(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()
	ns, id := obj.Namespace(), r.PathValue("subscription_id")
	fp := historyFingerprint(ns, id)

	window, err := historyPage(r, fp)
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	page, err := h.subscriptions.History(ctx, ns, id, window)
	if err != nil {
		h.writeSubscriptionError(ctx, w, err)

		return
	}

	resp := historyResponse{Deliveries: make([]deliveryDocument, 0, len(page.Deliveries)), NextPageToken: ""}
	for i := range page.Deliveries {
		resp.Deliveries = append(resp.Deliveries, toDeliveryDocument(&page.Deliveries[i]))
	}

	if page.More {
		last := page.Deliveries[len(page.Deliveries)-1]
		token, encodeErr := pagetoken.Encode(historyToken{
			NextAt: last.NextAt.UnixMilli(), ID: last.ID, Fingerprint: fp,
		})
		if encodeErr != nil {
			h.writeSubscriptionError(ctx, w, encodeErr)

			return
		}

		resp.NextPageToken = token
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, resp)
}

// redeliver serves POST /deliveries/{delivery_id}/redeliver.
func (h *Handler) redeliver(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()
	id := r.PathValue("delivery_id")

	if err := h.subscriptions.Redeliver(ctx, subscriptionCaller(r, p), obj.Namespace(), id); err != nil {
		h.writeDeliveryError(ctx, w, err)

		return
	}

	h.send(w, r, deletedAnswer(stateOf(ctx)), id)
}
