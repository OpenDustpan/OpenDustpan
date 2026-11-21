package profile_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each
// operation of the description match what the handler does with the header:
// If-Match, ETag and Idempotency-Key. An operation with a flag and no scenario
// here fails, so a new flag cannot enter without a check.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	var names atomic.Int64

	// fresh creates a profile and returns its path and the ETag of its first
	// revision.
	fresh := func(t *testing.T) (string, string) {
		t.Helper()

		name := fmt.Sprintf("p%d", names.Add(1))
		got := f.do(t, send("admin", http.MethodPost, base, createBody(t, name)))
		wantCode(t, "create "+name, got, http.StatusCreated)

		var wire profileWire
		if err := json.Unmarshal([]byte(got.body), &wire); err != nil {
			t.Fatalf("decode create: %v", err)
		}

		return base + "/" + wire.ID, got.header.Get("ETag")
	}

	probe := func(t *testing.T, method, target, body, etag string) headertest.Probe {
		t.Helper()

		return headertest.Probe{
			ETag: etag, Replayed: nil,
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

	saveBody := jsonString(t, map[string]string{"document": minimalDoc, "reason": "headers"})

	scenarios := map[string]headertest.Scenario{
		"listProfiles": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, http.MethodGet, base, "", "")
		},
		"createProfile": func(t *testing.T) headertest.Probe {
			t.Helper()

			name := fmt.Sprintf("c%d", names.Add(1))

			return probe(t, http.MethodPost, base, createBody(t, name), "")
		},
		"getProfile": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, etag := fresh(t)

			return probe(t, http.MethodGet, path, "", etag)
		},
		"deleteProfile": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, etag := fresh(t)

			return probe(t, http.MethodDelete, path, "", etag)
		},
		"listProfileRevisions": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, etag := fresh(t)

			return probe(t, http.MethodGet, path+"/revisions", "", etag)
		},
		"createProfileRevision": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, etag := fresh(t)

			return probe(t, http.MethodPost, path+"/revisions", saveBody, etag)
		},
		"getProfileRevision": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, etag := fresh(t)

			return probe(t, http.MethodGet, path+"/revisions/1", "", etag)
		},
		"openProfile": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, _ := fresh(t)

			p := probe(t, http.MethodPost, path+"/open", openBody(t), "")
			p.Replayed = replayNamesSession

			return p
		},
		"readProfileSession": func(t *testing.T) headertest.Probe {
			t.Helper()

			path, _ := fresh(t)

			opened := f.do(t, send("admin", http.MethodPost, path+"/open", openBody(t)))
			if opened.code != http.StatusCreated {
				t.Fatalf("open: %d %s", opened.code, opened.body)
			}

			var wire openWire
			if err := json.Unmarshal([]byte(opened.body), &wire); err != nil {
				t.Fatalf("decode open: %v", err)
			}

			return probe(t, http.MethodGet, sessionBase+"/"+wire.SessionID, "", "")
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

			// The profile adapter answers 400 to a key on a route that does
			// not take it.
			headertest.Check(t, op, scenario, headertest.Options{RefusesKey: true})
		})
	}
}

// sessionBase is the session collection of the fixture namespace.
const sessionBase = "/api/dustpan/v1/namespaces/acme/profile-sessions"

// openBody is the body of an open with the API consumer and the defaults.
func openBody(t *testing.T) string {
	t.Helper()

	return jsonString(t, map[string]string{"consumer": "api"})
}

// openWire is the part of an open answer that the tests read.
type openWire struct {
	SessionID string `json:"sessionId"` //nolint:tagliatelle // The apidesc camelCase rule spells an ID as Id; sessionId is the wire name.
	ExpiresAt string `json:"expiresAt"`
}

// replayNamesSession reports whether again is the replay of an open whose
// answer holds a secret: 409 that names the session of the first answer and
// holds no value.
func replayNamesSession(first, again headertest.Answer) bool {
	if again.Status != http.StatusConflict {
		return false
	}

	var wire openWire
	if err := json.Unmarshal([]byte(first.Body), &wire); err != nil {
		return false
	}

	return wire.SessionID != "" && strings.Contains(again.Body, wire.SessionID)
}
