package conformance_test

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// proxyFixture is a proxy in front of a small server that counts its requests.
type proxyFixture struct {
	proxy *conformance.RecordingProxy
	hits  *atomic.Int64
	// client does not follow a redirect.
	client *http.Client
}

func newProxyFixture(t *testing.T) *proxyFixture {
	t.Helper()

	hits := new(atomic.Int64)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"name":"a","state":"open","list":["x"]}`); err != nil {
			return
		}
	}))
	t.Cleanup(upstream.Close)

	proxy, err := conformance.NewProxy(t.Context(), conformance.ProxyOptions{
		Upstream: upstream.URL,
		Operations: []model.Operation{
			route("getThing", model.MethodGet, "/api/dustpan/v1/things/{thing_id}"),
			route("listThings", model.MethodGet, "/api/dustpan/v1/things"),
		},
	})
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := proxy.Close(); closeErr != nil {
			t.Errorf("close proxy: %v", closeErr)
		}
	})

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil},
	}
	t.Cleanup(client.CloseIdleConnections)

	return &proxyFixture{proxy: proxy, hits: hits, client: client}
}

// closeBody closes the body of an answer.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Errorf("close the body: %v", err)
	}
}

// get sends a GET through the proxy and returns the status and the body.
func (f *proxyFixture) get(t *testing.T, path string, header http.Header) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.proxy.URL()+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	maps.Copy(req.Header, header)

	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	defer closeBody(t, resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func TestProxyRecordsAndRedactsTheToken(t *testing.T) {
	t.Parallel()

	f := newProxyFixture(t)

	status, body, _ := f.get(t, "/api/dustpan/v1/things/a%2Fb?x=1", http.Header{
		"Authorization": {"Bearer super-secret"}, "If-Match": {`"1"`},
	})
	if status != http.StatusOK || !strings.Contains(body, `"name":"a"`) {
		t.Fatalf("want the answer of the server, got %d %s", status, body)
	}

	ex := f.proxy.Exchanges()
	if len(ex) != 1 {
		t.Fatalf("want 1 exchange, got %d", len(ex))
	}

	if ex[0].Path != "/api/dustpan/v1/things/a%2Fb" || ex[0].RawQuery != "x=1" || ex[0].Status != http.StatusOK {
		t.Errorf("exchange: %+v", ex[0])
	}

	if got := ex[0].Headers["Authorization"]; got != "Bearer <redacted>" {
		t.Errorf("the record holds the token: %q", got)
	}

	if ex[0].Headers["If-Match"] != `"1"` {
		t.Errorf("the record has no If-Match: %+v", ex[0].Headers)
	}

	f.proxy.Reset()

	if len(f.proxy.Exchanges()) != 0 {
		t.Error("Reset keeps exchanges")
	}
}

// faultCase is one row of TestProxyFaults.
type faultCase struct {
	name       string
	fault      conformance.Fault
	path       string
	wantStatus int
	wantBody   string
	wantHits   int64
}

// faultOn makes a fault with a field and a value.
func faultOn(kind conformance.FaultKind, field, value string) conformance.Fault {
	f := fault(kind, "listThings")
	f.Field, f.Value = field, []byte(value)

	return f
}

// statusFault makes a FaultStatus.
func statusFault(op string, status int, body, contentType string) conformance.Fault {
	f := fault(conformance.FaultStatus, op)
	f.Status, f.Body, f.ContentType = status, []byte(body), contentType

	return f
}

func faultCases() []faultCase {
	const (
		things = "/api/dustpan/v1/things"
		list   = "listThings"
	)

	down := statusFault(list, 503, "down", "text/plain")
	down.RetryAfter = "1"

	return []faultCase{
		{"status", down, things, 503, "down", 0},
		{"add field", faultOn(conformance.FaultAddField, "/extra", `{"n":1}`), things, 200, `"extra":{"n":1}`, 1},
		{
			"add item to a list",
			faultOn(conformance.FaultAddField, "/list/0", `"y"`),
			things,
			200,
			`"list":["y"]`,
			1,
		},
		{
			"enum value",
			faultOn(conformance.FaultEnumValue, "/state", `"newState"`),
			things,
			200,
			`"state":"newState"`,
			1,
		},
		{
			"enum value of a member that is not there", faultOn(conformance.FaultEnumValue, "/nope", `"x"`),
			things, 502, "no member", 1,
		},
		{"pointer without a slash", faultOn(conformance.FaultAddField, "extra", `1`), things, 502, "slash", 1},
		{"redirect", fault(conformance.FaultRedirect, "getThing"), things + "/abc", 307, "", 0},
		{"unknown kind", fault(conformance.FaultKind(99), list), things, 500, "unknown fault kind", 0},
		{"fault for another operation", statusFault("getThing", 503, "", ""), things, 200, `"name":"a"`, 1},
	}
}

// TestProxyFaults checks each kind of fault, and that a fault is for the next
// exchange of its operation only.
func TestProxyFaults(t *testing.T) {
	t.Parallel()

	for _, tt := range faultCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newProxyFixture(t)
			f.proxy.Inject(tt.fault)

			status, body, header := f.get(t, tt.path, nil)
			if status != tt.wantStatus || !strings.Contains(body, tt.wantBody) {
				t.Errorf("want %d with %q, got %d %q", tt.wantStatus, tt.wantBody, status, body)
			}

			if got := f.hits.Load(); got != tt.wantHits {
				t.Errorf("the server got %d requests, want %d", got, tt.wantHits)
			}

			checkRedirect(t, f, tt, header)

			// A second request gets no fault: it was for one exchange.
			if again, _, _ := f.get(t, "/api/dustpan/v1/things", nil); again != http.StatusOK {
				t.Errorf("the fault stayed: %d", again)
			}
		})
	}
}

// checkRedirect checks the Location header of a FaultRedirect.
func checkRedirect(t *testing.T, f *proxyFixture, tt faultCase, header http.Header) {
	t.Helper()

	if tt.fault.Kind != conformance.FaultRedirect {
		return
	}

	if want := f.proxy.OtherURL() + tt.path; header.Get("Location") != want {
		t.Errorf("Location: want %s, got %s", want, header.Get("Location"))
	}
}

func TestProxyDropsTheAnswerAfterTheServerRan(t *testing.T) {
	t.Parallel()

	f := newProxyFixture(t)
	f.proxy.Inject(fault(conformance.FaultDropAnswer, "listThings"))

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		f.proxy.URL()+"/api/dustpan/v1/things",
		http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}

	if resp, doErr := f.client.Do(req); doErr == nil {
		closeBody(t, resp)

		t.Fatal("the client got an answer")
	}

	if f.hits.Load() != 1 {
		t.Errorf("the server should have run the request once: %d", f.hits.Load())
	}
}

func TestProxyForwardsOnlyToItsUpstream(t *testing.T) {
	t.Parallel()

	f := newProxyFixture(t)

	// A request that names another host in the Host header or in the
	// request line still goes to the fixed upstream.
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		f.proxy.URL()+"/api/dustpan/v1/things",
		http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}

	req.Host = "evil.example"

	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	closeBody(t, resp)

	if resp.StatusCode != http.StatusOK || f.hits.Load() != 1 {
		t.Errorf("want the fixed upstream to answer: %d, hits %d", resp.StatusCode, f.hits.Load())
	}
}

func TestProxyRefusesUpstreamsThatAreNotLoopback(t *testing.T) {
	t.Parallel()

	for _, up := range []string{"http://example.com", "http://10.0.0.1:80", "http://192.168.1.1", "ftp://127.0.0.1", "", "127.0.0.1:80", "http://[2001:db8::1]"} {
		t.Run(up, func(t *testing.T) {
			t.Parallel()

			_, err := conformance.NewProxy(t.Context(), conformance.ProxyOptions{Upstream: up, Operations: nil})
			if !errors.Is(err, conformance.ErrNotLoopback) {
				t.Fatalf("want ErrNotLoopback, got %v", err)
			}
		})
	}
}

func TestProxyOtherListenerRecordsAndAnswers204(t *testing.T) {
	t.Parallel()

	f := newProxyFixture(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.proxy.OtherURL()+"/x", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer t")

	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	closeBody(t, resp)

	other := f.proxy.OtherExchanges()
	if resp.StatusCode != http.StatusNoContent || len(other) != 1 ||
		other[0].Headers["Authorization"] != "Bearer <redacted>" {
		t.Errorf("status %d, exchanges %+v", resp.StatusCode, other)
	}

	if !strings.HasPrefix(f.proxy.OtherURL(), "http://localhost:") ||
		!strings.HasPrefix(f.proxy.URL(), "http://127.0.0.1:") {
		t.Errorf("the two listeners should differ by host name: %s %s", f.proxy.URL(), f.proxy.OtherURL())
	}
}
