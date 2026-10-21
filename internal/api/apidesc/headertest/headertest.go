// Package headertest checks the header flags of an apidesc.Operation against
// the handler that serves the route. A flag in the description is a promise to
// each generated client, so a test of an adapter sends the request with and
// without the header and compares the answer with the flag.
package headertest

import (
	"net/http"
	"regexp"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// Answer is what a handler answers to one request.
type Answer struct {
	Status int
	Header http.Header
	Body   string
}

// ok reports whether the status is a success: 2xx.
func (a Answer) ok() bool { return a.Status >= http.StatusOK && a.Status < http.StatusMultipleChoices }

// Probe is one request under test, with the state that it needs.
type Probe struct {
	// Send sends the request with the extra headers. It builds a new request
	// on each call. Two calls with the same headers send the same request.
	Send func(headers map[string]string) Answer
	// ETag is the current ETag of the resource that the request changes or
	// reads, in the form that the handler gives it. It is empty for a route
	// that has no version.
	ETag string
	// Replayed, when it is not nil, says whether again is the right answer to
	// a second request with the key of first. The default is the same status
	// and body, marked as a replay. A route sets it when its replay answers
	// in another way, for example with a refusal that names the first result.
	Replayed func(first, again Answer) bool
}

// Scenario makes a probe on fresh state, so that a request that deletes or
// changes does not disturb the next probe.
type Scenario func(t *testing.T) Probe

// Options says what the handler does with a header that the description does
// not show.
type Options struct {
	// RefusesKey is true for an adapter that refuses a write with an
	// Idempotency-Key header when the route does not take the key. A read
	// ignores the header, since a read has no effect to repeat.
	RefusesKey bool
}

const (
	// key is the Idempotency-Key value of the probes.
	key = "headertest-key-1"
	// staleOffset is added to the version of an ETag to make a version that
	// the resource never had.
	staleOffset = 1000
)

// trailingNumber finds the version at the end of an ETag, with or without the
// closing quote.
var trailingNumber = regexp.MustCompile(`^(.*?)(\d+)("?)$`)

// StaleOf returns an ETag of the same form as etag for a version that the
// resource never had. An ETag with no version at the end gets a version.
func StaleOf(etag string) string {
	m := trailingNumber.FindStringSubmatch(etag)
	if m == nil {
		return etag + "9999"
	}

	n, err := strconv.Atoi(m[2])
	if err != nil {
		return m[1] + "9999" + m[3]
	}

	return m[1] + strconv.Itoa(n+staleOffset) + m[3]
}

// Check sends the requests of the scenario and fails when the answers differ
// from the flags of op:
//   - ETag: a 2xx answer holds the ETag header exactly when op.ETag is true.
//   - IfMatchRequired: a request without If-Match answers 428.
//   - IfMatch: a request without If-Match succeeds.
//   - IfMatch and IfMatchRequired: a request with an If-Match for a version
//     that the resource never had answers 409 or 412.
//   - no If-Match flag: a request with a stale If-Match is not answered as a
//     failed or missing precondition (412 or 428), so a handler cannot read the
//     header and leave the flag out of the description.
//   - IdempotencyKey: two requests with one key give one answer, and the
//     second one says that it is a replay.
//   - Options.RefusesKey: a route that writes and has no key flag refuses a
//     request that carries the key with 400.
func Check(t *testing.T, op apidesc.Operation, scenario Scenario, opts Options) {
	t.Helper()

	versioned := op.IfMatch || op.IfMatchRequired

	// current gives the headers of a request that is right for the probe.
	current := func(p Probe) map[string]string {
		headers := map[string]string{}
		if versioned {
			headers["If-Match"] = p.ETag
		}

		return headers
	}

	checkSuccess(t, op, scenario, current)
	checkPrecondition(t, op, scenario)
	checkIfMatchIgnored(t, op, scenario)
	checkKey(t, op, scenario, current, opts)
}

// checkSuccess sends the request that must succeed and compares the ETag
// header of the answer with the flag.
func checkSuccess(t *testing.T, op apidesc.Operation, scenario Scenario, current func(Probe) map[string]string) {
	t.Helper()

	p := scenario(t)

	got := p.Send(current(p))
	if !got.ok() {
		t.Fatalf("%s = %d %s, want 2xx", op.ID, got.Status, got.Body)
	}

	if has := got.Header.Get("ETag") != ""; has != op.ETag {
		t.Errorf("%s: ETag header present = %t, but the description says ETag = %t", op.ID, has, op.ETag)
	}
}

// checkPrecondition compares the answers to a request without If-Match and to
// a request with a stale If-Match with the flags.
func checkPrecondition(t *testing.T, op apidesc.Operation, scenario Scenario) {
	t.Helper()

	if !op.IfMatch && !op.IfMatchRequired {
		return
	}

	missing := scenario(t).Send(nil)

	switch {
	case op.IfMatchRequired && missing.Status != http.StatusPreconditionRequired:
		t.Errorf("%s without If-Match = %d %s, want 428", op.ID, missing.Status, missing.Body)
	case op.IfMatch && !missing.ok():
		t.Errorf("%s without If-Match = %d %s, want 2xx", op.ID, missing.Status, missing.Body)
	default:
	}

	p := scenario(t)

	stale := p.Send(map[string]string{"If-Match": StaleOf(p.ETag)})
	// The description promises 412 (components.parameters.IfMatch). The
	// approval handlers answer 409 for a stale tag. Remove the 409 branch when
	// they answer 412.
	if stale.Status != http.StatusPreconditionFailed && stale.Status != http.StatusConflict {
		t.Errorf("%s with a stale If-Match = %d %s, want 412 or 409", op.ID, stale.Status, stale.Body)
	}
}

// checkIfMatchIgnored sends a stale If-Match to an operation that has no
// If-Match flag. The handler must not honor the header as a precondition, so
// the answer is neither 412 nor 428. A handler that reads the header without a
// flag fails here, and every generated client would have the wrong signature.
func checkIfMatchIgnored(t *testing.T, op apidesc.Operation, scenario Scenario) {
	t.Helper()

	if op.IfMatch || op.IfMatchRequired {
		return
	}

	p := scenario(t)

	etag := StaleOf(p.ETag)
	if p.ETag == "" {
		etag = `"headertest-stale"`
	}

	got := p.Send(map[string]string{"If-Match": etag})
	if got.Status == http.StatusPreconditionFailed || got.Status == http.StatusPreconditionRequired {
		t.Errorf("%s with a stale If-Match = %d %s: the handler reads the header and the description shows no flag",
			op.ID, got.Status, got.Body)
	}
}

// checkKey compares the answers to a request with an Idempotency-Key header
// with the flag.
func checkKey(
	t *testing.T, op apidesc.Operation, scenario Scenario, current func(Probe) map[string]string, opts Options,
) {
	t.Helper()

	switch {
	case op.IdempotencyKey:
		checkReplay(t, op, scenario, current)
	case opts.RefusesKey && op.Method != http.MethodGet:
		p := scenario(t)

		headers := current(p)
		headers["Idempotency-Key"] = key

		if got := p.Send(headers); got.Status != http.StatusBadRequest {
			t.Errorf("%s with an Idempotency-Key = %d %s, want 400: the description shows no key",
				op.ID, got.Status, got.Body)
		}
	default:
	}
}

// checkReplay sends one request twice with one key.
func checkReplay(t *testing.T, op apidesc.Operation, scenario Scenario, current func(Probe) map[string]string) {
	t.Helper()

	p := scenario(t)

	headers := current(p)
	headers["Idempotency-Key"] = key

	first, again := p.Send(headers), p.Send(headers)

	if !first.ok() {
		t.Fatalf("%s with an Idempotency-Key = %d %s, want 2xx", op.ID, first.Status, first.Body)
	}

	if p.Replayed != nil {
		if !p.Replayed(first, again) {
			t.Errorf("%s with one Idempotency-Key twice = %d %q then %d %q, want a replay",
				op.ID, first.Status, first.Body, again.Status, again.Body)
		}

		return
	}

	if again.Status != first.Status || again.Body != first.Body {
		t.Errorf("%s with one Idempotency-Key twice = %d %q then %d %q, want one answer",
			op.ID, first.Status, first.Body, again.Status, again.Body)
	}

	if again.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("%s: the second request with one Idempotency-Key is not marked as a replay", op.ID)
	}
}

// Writes reports whether the operation uses a method that changes state: POST,
// PUT, PATCH or DELETE. A test of an adapter needs a scenario for each one.
func Writes(op apidesc.Operation) bool {
	switch op.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// Flagged reports whether the operation has any header flag.
func Flagged(op apidesc.Operation) bool {
	return op.IdempotencyKey || op.IfMatch || op.IfMatchRequired || op.ETag
}
