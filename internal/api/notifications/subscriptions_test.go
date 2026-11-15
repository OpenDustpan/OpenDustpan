package notifications_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

const deliveriesBase = "/api/dustpan/v1/namespaces/acme/deliveries"

// subscriptionWire is the part of a subscription answer that the tests read.
type subscriptionWire struct {
	ID        string     `json:"id"`
	Namespace string     `json:"namespace"`
	Sink      string     `json:"sink"`
	Filter    filterWire `json:"filter"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	CreatedBy string     `json:"createdBy"`
	Version   int64      `json:"version"`
}

type filterWire struct {
	Types       []string `json:"types"`
	Objects     []string `json:"objects"`
	MinSeverity string   `json:"minSeverity"`
}

type deliveryWire struct {
	ID           string `json:"id"`
	Subscription string `json:"subscription"`
	Sink         string `json:"sink"`
	Principal    string `json:"principal"`
	State        string `json:"state"`
	Attempts     int    `json:"attempts"`
	Version      int64  `json:"version"`
}

type historyWire struct {
	Deliveries    []deliveryWire `json:"deliveries"`
	NextPageToken string         `json:"nextPageToken"`
}

type subscriptionListWire struct {
	Subscriptions []subscriptionWire `json:"subscriptions"`
}

// makeSubscription creates a webhook sink and a subscription to it as admin.
func (f fixture) makeSubscription(t *testing.T) subscriptionWire {
	t.Helper()

	return f.makeSubscriptionTo(t, f.makeWebhook(t).ID)
}

// makeSubscriptionTo creates a subscription to the sink in acme as admin.
func (f fixture) makeSubscriptionTo(t *testing.T, sink string) subscriptionWire {
	t.Helper()

	got := f.do(t, send("admin", http.MethodPost, subscriptionsBase, `{"sink":"`+sink+`"}`))
	wantCode(t, "create subscription", got, http.StatusCreated)

	return decode[subscriptionWire](t, got)
}

// seedDelivery stores a delivery of sub in the namespace ns as the dispatcher
// does, with the next time nextAt, and stops it in the state. A pending
// delivery stays pending.
func (f fixture) seedDeliveryAt(
	t *testing.T,
	ns namespace.Name,
	sub subscriptionWire,
	state notify.DeliveryState,
	nextAt time.Time,
) notify.Delivery {
	t.Helper()

	n, err := f.namespaces.Get(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}

	d := notify.Delivery{
		ID: ident.NewID(notify.DeliveryIDPrefix), Notification: ident.NewID(notify.IDPrefix), Scope: ns,
		Subscription: sub.ID, Sink: sub.Sink, Contact: "", Principal: sub.CreatedBy, State: notify.DeliveryPending,
		Attempts: 0, NextAt: nextAt, LastError: "",
		DeliveredAt: time.Time{}, Owner: "", LeaseUntil: time.Time{}, Version: 0,
	}

	store := f.store.Notifications()
	if err = store.Enqueue(t.Context(), n.Fence(), []notify.Delivery{d}); err != nil {
		t.Fatalf("enqueue delivery: %v", err)
	}

	if state == notify.DeliveryPending {
		return d
	}

	now := f.clock.Now().UTC().Truncate(time.Millisecond)

	claimed, err := store.Claim(t.Context(), n.Fence(), notify.ClaimRequest{
		Scope: ns, Owner: "test", Now: now, Lease: time.Minute, Limit: 100,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	for i := range claimed {
		if claimed[i].ID != d.ID {
			continue
		}

		err = store.Record(t.Context(), n.Fence(), notify.Attempt{
			ID: d.ID, Owner: "test", Version: claimed[i].Version, Now: now, State: state, Attempts: 1,
			NextAt: d.NextAt, LastError: "", DeliveredAt: now,
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}

		return d
	}

	t.Fatalf("claim did not return the delivery %s", d.ID)

	return d
}

// seedDelivery stores a delivery of sub with a next time one minute before
// the clock of the fixture, and stops it in the state.
func (f fixture) seedDelivery(
	t *testing.T,
	ns namespace.Name,
	sub subscriptionWire,
	state notify.DeliveryState,
) notify.Delivery {
	t.Helper()

	return f.seedDeliveryAt(t, ns, sub, state, f.clock.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond))
}

// seedDeliveries stores count pending deliveries of sub with the next time
// nextAt, in one call of the store.
func (f fixture) seedDeliveries(
	t *testing.T,
	ns namespace.Name,
	sub subscriptionWire,
	nextAt time.Time,
	count int,
) {
	t.Helper()

	n, err := f.namespaces.Get(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}

	ds := make([]notify.Delivery, 0, count)
	for range count {
		ds = append(ds, notify.Delivery{
			ID: ident.NewID(notify.DeliveryIDPrefix), Notification: ident.NewID(notify.IDPrefix), Scope: ns,
			Subscription: sub.ID, Sink: sub.Sink, Contact: "", Principal: sub.CreatedBy, State: notify.DeliveryPending,
			Attempts: 0, NextAt: nextAt, LastError: "",
			DeliveredAt: time.Time{}, Owner: "", LeaseUntil: time.Time{}, Version: 0,
		})
	}

	if err = f.store.Notifications().Enqueue(t.Context(), n.Fence(), ds); err != nil {
		t.Fatalf("enqueue %d deliveries: %v", count, err)
	}
}

// subscriptionEntries returns the audit entries of the subscription and
// delivery actions, oldest first.
func (f fixture) subscriptionEntries(t *testing.T) []audit.Entry {
	t.Helper()

	return slices.DeleteFunc(f.entries(t), func(e audit.Entry) bool {
		return !strings.HasPrefix(e.Action, "subscription:") && !strings.HasPrefix(e.Action, "delivery:")
	})
}

func TestSubscriptionLifecycle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sink := f.makeWebhook(t)

	created := f.do(t, send("admin", http.MethodPost, subscriptionsBase,
		`{"sink":"`+sink.ID+`","filter":{"types":["lock.*"],"minSeverity":"warning"}}`))
	wantCode(t, "create", created, http.StatusCreated)

	sub := decode[subscriptionWire](t, created)
	if sub.Sink != sink.ID || sub.Namespace != "acme" || sub.State != "active" || sub.CreatedBy != "admin" ||
		sub.Version != 1 || sub.Filter.MinSeverity != "warning" || len(sub.Filter.Types) != 1 {
		t.Fatalf("created = %+v, want an active version 1 subscription of admin to %s", sub, sink.ID)
	}

	if loc := created.header.Get("Location"); loc != subscriptionsBase+"/"+sub.ID {
		t.Errorf("Location = %q, want the subscription path", loc)
	}

	// A body with no filter shows the effective filter: every type, every
	// object, from the severity info.
	plain := decode[subscriptionWire](t, f.do(t, send("admin", http.MethodPost, subscriptionsBase,
		`{"sink":"`+sink.ID+`"}`)))
	if plain.Filter.MinSeverity != "info" || plain.Filter.Types == nil || len(plain.Filter.Types) != 0 {
		t.Errorf("default filter = %+v, want empty lists and info", plain.Filter)
	}

	checkReadAndSuspend(t, f, sub, sink)
	checkResumeAndDelete(t, f, sub, sink)
}

// checkReadAndSuspend reads the subscriptions and suspends sub at version 1.
func checkReadAndSuspend(t *testing.T, f fixture, sub subscriptionWire, sink sinkWire) {
	t.Helper()

	list := f.do(t, get("reader", subscriptionsBase))
	wantCode(t, "list", list, http.StatusOK)

	if got := decode[subscriptionListWire](t, list).Subscriptions; len(got) != 2 {
		t.Errorf("list holds %d subscriptions, want 2", len(got))
	}

	one := f.do(t, get("reader", subscriptionsBase+"/"+sub.ID))
	wantCode(t, "get", one, http.StatusOK)

	suspended := f.do(t, send("admin", http.MethodPut, subscriptionsBase+"/"+sub.ID,
		`{"version":1,"sink":"`+sink.ID+`","state":"suspended","filter":{"types":["lock.*"],"minSeverity":"warning"}}`))
	wantCode(t, "suspend", suspended, http.StatusOK)

	if got := decode[subscriptionWire](t, suspended); got.State != "suspended" || got.Version != 2 || got.Reason == "" {
		t.Errorf("suspended = %+v, want version 2, state suspended and a reason", got)
	}
}

// checkResumeAndDelete resumes the suspended subscription sub at version 2,
// deletes it and reads it again.
func checkResumeAndDelete(t *testing.T, f fixture, sub subscriptionWire, sink sinkWire) {
	t.Helper()

	resumed := f.do(t, send("admin", http.MethodPut, subscriptionsBase+"/"+sub.ID,
		`{"version":2,"sink":"`+sink.ID+`","state":"active"}`))
	wantCode(t, "resume", resumed, http.StatusOK)

	if got := decode[subscriptionWire](t, resumed); got.State != "active" || got.Version != 3 || got.Reason != "" {
		t.Errorf("resumed = %+v, want version 3, state active and no reason", got)
	}

	wantCode(t, "delete", f.do(t, send("admin", http.MethodDelete, subscriptionsBase+"/"+sub.ID, "")),
		http.StatusNoContent)

	gone := f.do(t, get("admin", subscriptionsBase+"/"+sub.ID))
	wantCode(t, "get after delete", gone, http.StatusNotFound)
	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusNotFound, []byte(gone.body),
		errcodetest.Want{Code: errcode.SinkSubscriptionNotFound, Args: nil, RequestBody: nil})
}

func TestSubscriptionRefusals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sink := f.makeWebhook(t)

	// A sink of the namespace other, made by the same administrator.
	foreign := f.do(
		t,
		send("admin", http.MethodPost, "/api/dustpan/v1/namespaces/other/sinks", webhookBody(t, "Other")),
	)
	wantCode(t, "create foreign sink", foreign, http.StatusCreated)

	foreignID := decode[sinkWire](t, foreign).ID
	sub := f.makeSubscriptionTo(t, sink.ID)
	path := subscriptionsBase + "/" + sub.ID

	tests := []struct {
		name   string
		req    request
		status int
		code   string
	}{
		{
			"sink of another namespace", send("admin", http.MethodPost, subscriptionsBase, `{"sink":"`+foreignID+`"}`),
			http.StatusUnprocessableEntity, errcode.SinkSubscriptionSinkNotFound,
		},
		{
			"unknown sink", send("admin", http.MethodPost, subscriptionsBase, `{"sink":"snk-aaaaaaaaaaaaaaaaaaaa"}`),
			http.StatusUnprocessableEntity, errcode.SinkSubscriptionSinkNotFound,
		},
		{
			"replace to a sink of another namespace",
			send("admin", http.MethodPut, path, `{"version":1,"sink":"`+foreignID+`","state":"active"}`),
			http.StatusUnprocessableEntity, errcode.SinkSubscriptionSinkNotFound,
		},
		{
			"stale version", send("admin", http.MethodPut, path,
				`{"version":7,"sink":"`+sink.ID+`","state":"active"}`),
			http.StatusConflict, errcode.SinkSubscriptionConflict,
		},
		{
			"no sink", send("admin", http.MethodPost, subscriptionsBase, `{}`),
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"unknown severity", send("admin", http.MethodPost, subscriptionsBase,
				`{"sink":"`+sink.ID+`","filter":{"minSeverity":"loud"}}`),
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"unknown type in the filter", send("admin", http.MethodPost, subscriptionsBase,
				`{"sink":"`+sink.ID+`","filter":{"types":["no.such.type"]}}`),
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"unknown state", send("admin", http.MethodPut, path, `{"version":1,"sink":"`+sink.ID+`","state":"paused"}`),
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"replace with no version", send("admin", http.MethodPut, path, `{"sink":"`+sink.ID+`","state":"active"}`),
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"unknown field", send("admin", http.MethodPost, subscriptionsBase, `{"sink":"`+sink.ID+`","owner":"x"}`),
			http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
		{
			"unknown subscription", get("admin", subscriptionsBase+"/sub-aaaaaaaaaaaaaaaaaaaa"),
			http.StatusNotFound, errcode.SinkSubscriptionNotFound,
		},
		{
			"delete of an unknown subscription", send("admin", http.MethodDelete,
				subscriptionsBase+"/sub-aaaaaaaaaaaaaaaaaaaa", ""),
			http.StatusNotFound, errcode.SinkSubscriptionNotFound,
		},
		{
			"history of an unknown subscription",
			get("admin", subscriptionsBase+"/sub-aaaaaaaaaaaaaaaaaaaa/deliveries"),
			http.StatusNotFound,
			errcode.SinkSubscriptionNotFound,
		},
		{
			"write without the manage action",
			send("reader", http.MethodPost, subscriptionsBase, `{"sink":"`+sink.ID+`"}`),
			http.StatusForbidden,
			errcode.AccessDenied,
		},
		{
			"read without the read action", get("manageonly", subscriptionsBase),
			http.StatusForbidden, errcode.AccessDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, tc.req)
			wantCode(t, tc.name, got, tc.status)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: []byte(tc.req.body)})

			// The refusal never says which namespace holds a sink.
			if strings.Contains(got.body, "other") {
				t.Errorf("answer %q names the other namespace", got.body)
			}
		})
	}

	if n := len(f.subscriptionEntries(t)); n != 1 {
		t.Errorf("audit holds %d subscription entries, want only the create of the setup", n)
	}
}

func TestDeliveryHistoryPages(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)
	other := f.makeSubscription(t)

	for range 3 {
		f.seedDelivery(t, "acme", sub, notify.DeliveryDone)
	}

	path := subscriptionsBase + "/" + sub.ID + "/deliveries"

	first := f.do(t, get("reader", path+"?pageSize=2"))
	wantCode(t, "first page", first, http.StatusOK)

	page := decode[historyWire](t, first)
	if len(page.Deliveries) != 2 || page.NextPageToken == "" {
		t.Fatalf("first page = %+v, want 2 deliveries and a token", page)
	}

	second := f.do(t, get("reader", path+"?pageSize=2&pageToken="+page.NextPageToken))
	wantCode(t, "second page", second, http.StatusOK)

	last := decode[historyWire](t, second)
	if len(last.Deliveries) != 1 || last.NextPageToken != "" {
		t.Fatalf("second page = %+v, want 1 delivery and no token", last)
	}

	seen := map[string]bool{}
	for _, d := range slices.Concat(page.Deliveries, last.Deliveries) {
		if seen[d.ID] || d.Subscription != sub.ID || d.State != "delivered" {
			t.Errorf("delivery %+v is repeated or not a delivered one of the subscription", d)
		}

		seen[d.ID] = true
	}

	tests := []struct {
		name   string
		target string
		code   string
	}{
		{"page size zero", path + "?pageSize=0", errcode.RequestInvalidQuery},
		{"page size over the limit", path + "?pageSize=201", errcode.RequestInvalidQuery},
		{"page size that is no number", path + "?pageSize=many", errcode.RequestInvalidQuery},
		{"token that is no token", path + "?pageToken=abc", errcode.RequestPageTokenInvalid},
		{
			"token of another subscription",
			subscriptionsBase + "/" + other.ID + "/deliveries?pageToken=" + page.NextPageToken,
			errcode.RequestPageTokenInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, get("reader", tc.target))
			wantCode(t, tc.name, got, http.StatusBadRequest)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// historyTokenWire is the page token of the delivery history, for a test that
// forges one.
type historyTokenWire struct {
	NextAt      int64  `json:"t"`
	ID          string `json:"i"`
	Fingerprint string `json:"f"`
}

// historyTokenFor encodes a page token of the history of sub at the cursor
// (nextAt, id). The fingerprint of the history is the namespace and the
// subscription ID.
func historyTokenFor(t *testing.T, sub subscriptionWire, nextAt int64, id string) string {
	t.Helper()

	raw, err := pagetoken.Encode(historyTokenWire{
		NextAt: nextAt, ID: id, Fingerprint: "acme\x00" + sub.ID,
	})
	if err != nil {
		t.Fatalf("encode page token: %v", err)
	}

	return raw
}

// TestDeliveryHistoryRefusesBadTokens checks the cursor guards of a history
// token: an empty cursor ID, a next time before the Unix epoch and a cursor
// ID that is not a delivery ID answer the refusal of a token that is not
// valid.
func TestDeliveryHistoryRefusesBadTokens(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)

	nextAt := f.clock.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond).UnixMilli()

	tests := []struct {
		name  string
		token string
	}{
		{"empty cursor ID", historyTokenFor(t, sub, nextAt, "")},
		{"next time before the Unix epoch", historyTokenFor(t, sub, -1, "dlv-aaaaaaaaaaaaaaaaaaaa")},
		{"cursor ID that is not a delivery ID", historyTokenFor(t, sub, nextAt, "sub-aaaaaaaaaaaaaaaaaaaa")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := subscriptionsBase + "/" + sub.ID + "/deliveries?pageToken=" + tc.token

			got := f.do(t, get("reader", target))
			wantCode(t, tc.name, got, http.StatusBadRequest)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
				errcodetest.Want{Code: errcode.RequestPageTokenInvalid, Args: nil, RequestBody: nil})
		})
	}
}

// TestDeliveryHistoryStopsAtTheBound pages a history that holds more
// deliveries than the history reaches, and checks that the chain of pages
// stops at the bound and never repeats a delivery.
func TestDeliveryHistoryStopsAtTheBound(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)

	nextAt := f.clock.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	f.seedDeliveries(t, "acme", sub, nextAt, subscriptionsvc.MaxHistory+5)

	path := subscriptionsBase + "/" + sub.ID + "/deliveries"

	var (
		seen  int
		ids   = map[string]bool{}
		token string
	)

	for pages := 0; ; pages++ {
		if pages > subscriptionsvc.MaxHistory/200+1 {
			t.Fatalf("the history does not stop: %d deliveries", seen)
		}

		target := path + "?pageSize=200"
		if token != "" {
			target += "&pageToken=" + token
		}

		answer := f.do(t, get("reader", target))
		wantCode(t, fmt.Sprintf("page %d", pages+1), answer, http.StatusOK)

		resp := decode[historyWire](t, answer)
		for _, d := range resp.Deliveries {
			if ids[d.ID] {
				t.Fatalf("the page repeats the delivery %s", d.ID)
			}

			ids[d.ID] = true
			seen++
		}

		if resp.NextPageToken == "" {
			break
		}

		token = resp.NextPageToken
	}

	if seen != subscriptionsvc.MaxHistory {
		t.Fatalf("the history returned %d deliveries, want %d", seen, subscriptionsvc.MaxHistory)
	}
}

// TestDeliveryHistorySameNextAt pages the history of many deliveries that
// share one next time, with a page edge between them. Every delivery comes
// back once, in the order of the history.
func TestDeliveryHistorySameNextAt(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)

	nextAt := f.clock.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)

	want := make([]string, 0, 5)
	for range 5 {
		want = append(want, f.seedDeliveryAt(t, "acme", sub, notify.DeliveryDone, nextAt).ID)
	}

	// The history orders the rows of one next time by ID, greatest first.
	slices.SortFunc(want, func(a, b string) int { return strings.Compare(b, a) })

	path := subscriptionsBase + "/" + sub.ID + "/deliveries"

	var (
		got   []string
		token string
	)

	for page := 1; ; page++ {
		if page > 6 {
			t.Fatalf("the history does not end: %v", got)
		}

		target := path + "?pageSize=2"
		if token != "" {
			target += "&pageToken=" + token
		}

		answer := f.do(t, get("reader", target))
		wantCode(t, fmt.Sprintf("page %d", page), answer, http.StatusOK)

		resp := decode[historyWire](t, answer)
		for _, d := range resp.Deliveries {
			got = append(got, d.ID)
		}

		if resp.NextPageToken == "" {
			break
		}

		token = resp.NextPageToken
	}

	if !slices.Equal(got, want) {
		t.Fatalf("history = %v, want %v: every delivery once, in order", got, want)
	}
}

func TestRedeliver(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)
	pending := f.seedDelivery(t, "acme", sub, notify.DeliveryPending)
	done := f.seedDelivery(t, "acme", sub, notify.DeliveryDone)

	// A delivery of another namespace: this namespace reads it as unknown.
	foreign := f.seedDelivery(t, "other", subscriptionWire{
		ID:        "sub-aaaaaaaaaaaaaaaaaaaa",
		Namespace: "other",
		Sink:      "snk-aaaaaaaaaaaaaaaaaaaa",
		Filter:    filterWire{Types: nil, Objects: nil, MinSeverity: "info"},
		State:     "active",
		Reason:    "",
		CreatedBy: "admin",
		Version:   1,
	}, notify.DeliveryDone)

	tests := []struct {
		name   string
		id     string
		status int
		code   string
	}{
		{"pending delivery", pending.ID, http.StatusConflict, errcode.SinkDeliveryNotRedeliverable},
		{"unknown delivery", "dlv-aaaaaaaaaaaaaaaaaaaa", http.StatusNotFound, errcode.SinkDeliveryNotFound},
		{"delivery of another namespace", foreign.ID, http.StatusNotFound, errcode.SinkDeliveryNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, send("admin", http.MethodPost, deliveriesBase+"/"+tc.id+"/redeliver", ""))
			wantCode(t, tc.name, got, tc.status)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}

	wantCode(t, "reader redelivers", f.do(t, send("reader", http.MethodPost,
		deliveriesBase+"/"+done.ID+"/redeliver", "")), http.StatusForbidden)

	// The suspended subscription no longer sends: the delivery from before
	// stays stopped.
	suspend := f.do(t, send("admin", http.MethodPut, subscriptionsBase+"/"+sub.ID,
		`{"version":1,"sink":"`+sub.Sink+`","state":"suspended"}`))
	wantCode(t, "suspend", suspend, http.StatusOK)

	blocked := f.do(t, send("admin", http.MethodPost, deliveriesBase+"/"+done.ID+"/redeliver", ""))
	wantCode(t, "redeliver for a suspended subscription", blocked, http.StatusConflict)

	resume := f.do(t, send("admin", http.MethodPut, subscriptionsBase+"/"+sub.ID,
		fmt.Sprintf(`{"version":2,"sink":%q,"state":"active"}`, sub.Sink)))
	wantCode(t, "resume", resume, http.StatusOK)

	wantCode(t, "redeliver", f.do(t, send("admin", http.MethodPost, deliveriesBase+"/"+done.ID+"/redeliver", "")),
		http.StatusNoContent)

	history := decode[historyWire](t, f.do(t, get("admin", subscriptionsBase+"/"+sub.ID+"/deliveries")))

	for _, d := range history.Deliveries {
		if d.ID == done.ID && (d.State != "pending" || d.Attempts != 0) {
			t.Errorf("delivery after redeliver = %+v, want pending with no attempts", d)
		}
	}
}

func TestSubscriptionWritesAreIdempotent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sink := f.makeWebhook(t)

	body := `{"sink":"` + sink.ID + `"}`
	first := f.do(t, keyed(send("admin", http.MethodPost, subscriptionsBase, body), "create-1"))
	again := f.do(t, keyed(send("admin", http.MethodPost, subscriptionsBase, body), "create-1"))

	wantCode(t, "first create", first, http.StatusCreated)
	wantCode(t, "replayed create", again, http.StatusCreated)

	if first.body != again.body {
		t.Errorf("replay body %q differs from %q", again.body, first.body)
	}

	if got := decode[subscriptionListWire](t, f.do(t, get("admin", subscriptionsBase))).Subscriptions; len(got) != 1 {
		t.Errorf("list holds %d subscriptions after a replay, want 1", len(got))
	}

	sub := decode[subscriptionWire](t, first)
	done := f.seedDelivery(t, "acme", sub, notify.DeliveryDone)
	target := deliveriesBase + "/" + done.ID + "/redeliver"

	for range 2 {
		wantCode(t, "keyed redeliver", f.do(t, keyed(send("admin", http.MethodPost, target, ""), "redeliver-1")),
			http.StatusNoContent)
	}

	entries := f.subscriptionEntries(t)
	actions := make([]string, 0, len(entries))

	for _, e := range entries {
		actions = append(actions, e.Action)
	}

	if want := []string{"subscription:create", "delivery:redeliver"}; !slices.Equal(actions, want) {
		t.Errorf("audit actions = %v, want %v: a replay writes no second entry", actions, want)
	}
}

func TestSubscriptionWritesAreAudited(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sub := f.makeSubscription(t)
	done := f.seedDelivery(t, "acme", sub, notify.DeliveryDone)

	wantCode(t, "replace", f.do(t, send("admin", http.MethodPut, subscriptionsBase+"/"+sub.ID,
		`{"version":1,"sink":"`+sub.Sink+`","state":"active","filter":{"types":["lock.*"]}}`)), http.StatusOK)
	wantCode(t, "redeliver", f.do(t, send("admin", http.MethodPost, deliveriesBase+"/"+done.ID+"/redeliver", "")),
		http.StatusNoContent)
	wantCode(t, "delete", f.do(t, send("admin", http.MethodDelete, subscriptionsBase+"/"+sub.ID, "")),
		http.StatusNoContent)

	want := []struct{ action, id string }{
		{"subscription:create", sub.ID},
		{"subscription:update", sub.ID},
		{"delivery:redeliver", done.ID},
		{"subscription:delete", sub.ID},
	}

	entries := f.subscriptionEntries(t)
	if len(entries) != len(want) {
		t.Fatalf("audit holds %d entries, want %d: %+v", len(entries), len(want), entries)
	}

	for i, e := range entries {
		if e.Action != want[i].action || e.ObjectID != want[i].id || e.Subject != "admin" ||
			e.Outcome != audit.OutcomeSuccess || e.Namespace != "acme" {
			t.Errorf("entry %d = %+v, want %+v by admin", i, e, want[i])
		}

		if strings.Contains(fmt.Sprint(e), "lock.*") {
			t.Errorf("entry %d holds the text of the filter", i)
		}
	}
}
