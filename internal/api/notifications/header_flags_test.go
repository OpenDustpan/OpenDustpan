package notifications_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	"opendustpan.org/opendustpan/pkg/notify"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each
// operation of the description match what the handler does with the header:
// If-Match, ETag and Idempotency-Key. An operation with no scenario here
// fails, so a new route cannot enter the description without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	probe := func(method, target, body string) headertest.Probe {
		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				r := send("admin", method, target, body)
				for k, v := range headers {
					r = r.withHeader(k, v)
				}

				got := f.do(t, r)

				return headertest.Answer{Status: got.code, Header: got.header, Body: got.body}
			},
		}
	}

	// The answer of a create holds the signing key, so a replay of it answers
	// 409 and never the stored body.
	replayConflicts := func(_, again headertest.Answer) bool { return again.Status == http.StatusConflict }

	scenarios := map[string]headertest.Scenario{
		"listNotificationSinks": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodGet, base, "")
		},
		"createNotificationSink": func(t *testing.T) headertest.Probe {
			t.Helper()

			p := probe(http.MethodPost, base, webhookBody(t, "Created"))
			p.Replayed = replayConflicts

			return p
		},
		"getNotificationSink": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodGet, base+"/"+f.makeWebhook(t).ID, "")
		},
		"replaceNotificationSink": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodPut, base+"/"+f.makeWebhook(t).ID, `{"version":1,"name":"Renamed"}`)
		},
		"deleteNotificationSink": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodDelete, base+"/"+f.makeWebhook(t).ID, "")
		},
		"testNotificationSink": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodPost, base+"/"+f.makeWebhook(t).ID+"/test", "")
		},
		"listNotificationSubscriptions": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodGet, subscriptionsBase, "")
		},
		"createNotificationSubscription": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodPost, subscriptionsBase, `{"sink":"`+f.makeWebhook(t).ID+`"}`)
		},
		"getNotificationSubscription": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodGet, subscriptionsBase+"/"+f.makeSubscription(t).ID, "")
		},
		"replaceNotificationSubscription": func(t *testing.T) headertest.Probe {
			t.Helper()

			sub := f.makeSubscription(t)

			return probe(http.MethodPut, subscriptionsBase+"/"+sub.ID,
				`{"version":1,"sink":"`+sub.Sink+`","state":"suspended"}`)
		},
		"deleteNotificationSubscription": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodDelete, subscriptionsBase+"/"+f.makeSubscription(t).ID, "")
		},
		"listNotificationDeliveries": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(http.MethodGet, subscriptionsBase+"/"+f.makeSubscription(t).ID+"/deliveries", "")
		},
		"redeliverNotification": func(t *testing.T) headertest.Probe {
			t.Helper()

			sub := f.makeSubscription(t)
			d := f.seedDelivery(t, "acme", sub, notify.DeliveryDone)
			f.clock.Set(f.clock.Now().Add(notify.RetryWait(1)))

			return probe(http.MethodPost, deliveriesBase+"/"+d.ID+"/redeliver", "")
		},
	}

	ops := f.handler.Operations()
	if len(ops) != len(scenarios) {
		t.Errorf("the handler has %d operations and the test has %d scenarios", len(ops), len(scenarios))
	}

	for _, op := range ops {
		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			scenario, ok := scenarios[op.ID]
			if !ok {
				t.Fatalf("%s has no scenario", op.ID)
			}

			headertest.Check(t, op, scenario, headertest.Options{RefusesKey: true})
		})
	}
}
