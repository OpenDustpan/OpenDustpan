package githubfake

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// BearerKind names the kind of credential that a request carried.
type BearerKind string

// The kinds of credentials.
const (
	// BearerNone is a request with no Authorization header.
	BearerNone BearerKind = "none"
	// BearerApp is a JWT of the app with a valid signature.
	BearerApp BearerKind = "app"
	// BearerInstallation is an installation token that the fake made and
	// has not revoked or expired.
	BearerInstallation BearerKind = "installation"
	// BearerUser is the token of the user.
	BearerUser BearerKind = "user"
	// BearerInvalid is any other credential.
	BearerInvalid BearerKind = "invalid"
)

// maxRecordedBody is the most bytes of a request body that the fake keeps.
const maxRecordedBody = 1 << 20

// redacted replaces the header Authorization in the record.
const redacted = "<redacted>"

// Request is one request that the fake got. The record holds no credential.
type Request struct {
	// Seq is the number of the request, from 1.
	Seq    int
	Method string
	// URI is the request target as the client sent it, with the query.
	URI string
	// Header is the header as the client sent it, with Authorization
	// replaced.
	Header http.Header
	Bearer BearerKind
	Body   []byte
	// Status is the status that the fake answered.
	Status int
}

// Requests returns the requests that the fake got, in order.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.requests)
}

// Calls returns the number of requests that the fake got.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.requests)
}

// ResetRequests forgets the requests that the fake recorded.
func (f *Fake) ResetRequests() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = nil
}

// statusRecorder keeps the status of an answer.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}

	s.ResponseWriter.WriteHeader(code)
}

// record wraps next so that each request is in the record, also a request
// that the mux redirects or refuses. The handler runs under the lock.
func (f *Fake) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRecordedBody))
		if err != nil {
			http.Error(w, "the request body cannot be read", http.StatusBadRequest)

			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))

		f.mu.Lock()
		defer f.mu.Unlock()

		rec := &statusRecorder{ResponseWriter: w, status: 0}
		bearer := f.classify(r)

		if f.faultResponse(rec, r) {
			f.add(r, bearer, body, rec.status)

			return
		}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		f.add(r, bearer, body, rec.status)
	})
}

// add appends a request to the record. The caller holds the lock.
func (f *Fake) add(r *http.Request, bearer BearerKind, body []byte, status int) {
	header := r.Header.Clone()
	if header.Get("Authorization") != "" {
		header.Set("Authorization", redacted)
	}

	f.requests = append(f.requests, Request{
		Seq: len(f.requests) + 1, Method: r.Method, URI: r.RequestURI, Header: header,
		Bearer: bearer, Body: body, Status: status,
	})
}

// faults are the failures that a test makes the fake answer.
type faults struct {
	refuseAll   bool
	garble      bool
	unavailable bool
	retryAfter  time.Duration
	status      int
	statusHdr   map[string]string
	redirectTo  string
	expiry      time.Time
	// oversize is the number of bytes that a pull request answer adds.
	oversize int
}

// faultResponse answers a request with the fault that a test turned on, and
// reports whether it did. A fault answers before the fake checks the
// credential. The caller holds the lock.
func (f *Fake) faultResponse(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case f.faults.redirectTo != "":
		http.Redirect(w, r, f.faults.redirectTo, http.StatusFound)
	case f.faults.unavailable && f.faults.retryAfter > 0:
		w.Header().Set("Retry-After", strconv.Itoa(int(f.faults.retryAfter/time.Second)))
		w.WriteHeader(http.StatusTooManyRequests)
	case f.faults.unavailable:
		w.WriteHeader(http.StatusServiceUnavailable)
	case f.faults.status != 0:
		for k, v := range f.faults.statusHdr {
			w.Header().Set(k, v)
		}

		w.WriteHeader(f.faults.status)
	default:
		return false
	}

	return true
}

// Refuse makes the fake refuse each credential from now on, as after a
// revocation, until the returned function restores it.
func (f *Fake) Refuse() func() {
	return f.setFault(func(x *faults) { x.refuseAll = true }, func(x *faults) { x.refuseAll = false })
}

// Garble makes the fake answer each call that has a body with a success
// status and a body that does not parse, until the returned function
// restores it.
func (f *Fake) Garble() func() {
	return f.setFault(func(x *faults) { x.garble = true }, func(x *faults) { x.garble = false })
}

// Unavailable makes the fake answer 503, or 429 with the header Retry-After
// when retryAfter is above zero, until the returned function restores it.
func (f *Fake) Unavailable(retryAfter time.Duration) func() {
	return f.setFault(
		func(x *faults) { x.unavailable, x.retryAfter = true, retryAfter },
		func(x *faults) { x.unavailable, x.retryAfter = false, 0 },
	)
}

// Respond makes the fake answer each call with status and headers, with no
// body, until the returned function restores it.
func (f *Fake) Respond(status int, headers map[string]string) func() {
	return f.setFault(
		func(x *faults) { x.status, x.statusHdr = status, headers },
		func(x *faults) { x.status, x.statusHdr = 0, nil },
	)
}

// Redirect makes the fake answer each call with a redirect to target, until
// the returned function restores it.
func (f *Fake) Redirect(target string) func() {
	return f.setFault(func(x *faults) { x.redirectTo = target }, func(x *faults) { x.redirectTo = "" })
}

// SetTokenExpiry sets the expiry that the fake gives for the token of the
// user. The zero time removes it, so that the token has none.
func (f *Fake) SetTokenExpiry(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.faults.expiry = at
}

func (f *Fake) setFault(on, off func(*faults)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()

	on(&f.faults)

	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		off(&f.faults)
	}
}
