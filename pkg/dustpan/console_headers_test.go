package dustpan_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

const wantDefaultCSP = "default-src 'self'; connect-src 'self'; img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

var securityHeaderNames = []string{ //nolint:gochecknoglobals // A fixed list of header names.
	"X-Content-Type-Options", "X-Frame-Options", "Content-Security-Policy", "Strict-Transport-Security",
}

// hdr builds ConsoleHeaders with one field set.
func hdr(field, value string) dustpan.ConsoleHeaders {
	h := dustpan.ConsoleHeaders{
		ContentTypeOptions:      "",
		FrameOptions:            "",
		ContentSecurityPolicy:   "",
		StrictTransportSecurity: "",
	}

	switch field {
	case "ContentTypeOptions":
		h.ContentTypeOptions = value
	case "FrameOptions":
		h.FrameOptions = value
	case "ContentSecurityPolicy":
		h.ContentSecurityPolicy = value
	case "StrictTransportSecurity":
		h.StrictTransportSecurity = value
	default:
		// A typo in a test must fail the test, not pass with no header set.
		h.ContentTypeOptions = "unknown field " + field
	}

	return h
}

// consoleServerHandler builds a started server that serves a console from a
// temporary directory and answers with its handler.
func consoleServerHandler(t *testing.T, opts ...dustpan.Option) http.Handler {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>console</p>"), 0o600); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	opts = append([]dustpan.Option{dustpan.WithConsoleDir(dir)}, opts...)

	return startedHandler(t, newServerWithOptions(t, "127.0.0.1:0", opts...))
}

func TestConsoleSecurityHeaders(t *testing.T) {
	t.Parallel()

	tlsFiles := []dustpan.Option{dustpan.WithTLS("cert.pem", "key.pem")}

	cases := []struct {
		name string
		opts []dustpan.Option
		path string
		want map[string]string // an empty value means the header must be absent
	}{
		{
			"defaults on the index", nil, "/",
			map[string]string{
				"X-Content-Type-Options":    "nosniff",
				"X-Frame-Options":           "DENY",
				"Content-Security-Policy":   wantDefaultCSP,
				"Strict-Transport-Security": "",
			},
		},
		{
			"defaults on a deep link", nil, "/stacks/acme/dev",
			map[string]string{
				"X-Content-Type-Options":    "nosniff",
				"X-Frame-Options":           "DENY",
				"Content-Security-Policy":   wantDefaultCSP,
				"Strict-Transport-Security": "",
			},
		},
		{
			"defaults on a missing asset", nil, "/missing.js",
			map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY"},
		},
		{
			"HSTS with TLS", tlsFiles, "/",
			map[string]string{"Strict-Transport-Security": "max-age=31536000"},
		},
		{
			"HSTS value set with TLS",
			append([]dustpan.Option{
				dustpan.WithConsoleHeaders(hdr("StrictTransportSecurity", "max-age=60")),
			}, tlsFiles...),
			"/",
			map[string]string{"Strict-Transport-Security": "max-age=60"},
		},
		{
			"HSTS value set without TLS sends nothing",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("StrictTransportSecurity", "max-age=60"))},
			"/",
			map[string]string{"Strict-Transport-Security": ""},
		},
		{
			"CSP lists the allowed origins",
			[]dustpan.Option{dustpan.WithAllowedOrigins("https://console.example.com", "http://localhost:5173")},
			"/",
			map[string]string{
				"Content-Security-Policy": strings.Replace(
					wantDefaultCSP, "connect-src 'self'",
					"connect-src 'self' https://console.example.com http://localhost:5173", 1,
				),
			},
		},
		{
			"nosniff off",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("ContentTypeOptions", dustpan.ConsoleHeaderOff))},
			"/",
			map[string]string{"X-Content-Type-Options": "", "X-Frame-Options": "DENY"},
		},
		{
			"off in upper case",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("ContentTypeOptions", "OFF"))},
			"/",
			map[string]string{"X-Content-Type-Options": "", "X-Frame-Options": "DENY"},
		},
		{
			"off in mixed case",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("FrameOptions", "Off"))},
			"/",
			map[string]string{"X-Frame-Options": "", "X-Content-Type-Options": "nosniff"},
		},
		{
			"off with spaces",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("ContentSecurityPolicy", " off "))},
			"/",
			map[string]string{"Content-Security-Policy": "", "X-Frame-Options": "DENY"},
		},
		{
			"HSTS off in upper case with TLS",
			append([]dustpan.Option{dustpan.WithConsoleHeaders(hdr("StrictTransportSecurity", "OFF"))}, tlsFiles...),
			"/",
			map[string]string{"Strict-Transport-Security": ""},
		},
		{
			"frame options off",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("FrameOptions", dustpan.ConsoleHeaderOff))},
			"/",
			map[string]string{"X-Frame-Options": "", "X-Content-Type-Options": "nosniff"},
		},
		{
			"CSP off",
			[]dustpan.Option{
				dustpan.WithConsoleHeaders(hdr("ContentSecurityPolicy", dustpan.ConsoleHeaderOff)),
			},
			"/",
			map[string]string{"Content-Security-Policy": "", "X-Frame-Options": "DENY"},
		},
		{
			"CSP replaced",
			[]dustpan.Option{dustpan.WithConsoleHeaders(hdr("ContentSecurityPolicy", "default-src 'none'"))},
			"/",
			map[string]string{"Content-Security-Policy": "default-src 'none'"},
		},
		{
			"HSTS off with TLS",
			append([]dustpan.Option{
				dustpan.WithConsoleHeaders(hdr("StrictTransportSecurity", dustpan.ConsoleHeaderOff)),
			}, tlsFiles...),
			"/",
			map[string]string{"Strict-Transport-Security": ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := consoleServerHandler(t, tc.opts...)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

			for name, want := range tc.want {
				if got := rec.Header().Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestConsoleHeadersNotOnAPIResponses(t *testing.T) {
	t.Parallel()

	opts := []dustpan.Option{
		dustpan.WithTLS("cert.pem", "key.pem"),
		dustpan.WithConsoleHeaders(dustpan.ConsoleHeaders{
			ContentTypeOptions: "nosniff", FrameOptions: "DENY", ContentSecurityPolicy: "default-src 'none'",
			StrictTransportSecurity: "max-age=60",
		}),
	}
	withHeaders := consoleServerHandler(t, opts...)
	plain := newPlainHandler(t)

	paths := []string{"/api/user", "/api/v2/ping", "/.well-known/terraform.json", "/api/dustpan/v1/nothing"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			answer := func(h http.Handler) *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
				req.Header.Set("Authorization", "Bearer "+adminToken)

				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				return rec
			}

			got, want := answer(withHeaders), answer(plain)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Errorf("answer changed: status %d body %q, want %d body %q",
					got.Code, got.Body.String(), want.Code, want.Body.String())
			}

			for _, name := range securityHeaderNames {
				if v := got.Header().Get(name); v != "" {
					t.Errorf("API response carries %s = %q", name, v)
				}
			}
		})
	}
}

// newPlainHandler builds a started server with no console and no header option.
func newPlainHandler(t *testing.T) http.Handler {
	t.Helper()

	return startedHandler(t, newServerWithOptions(t, "127.0.0.1:0"))
}

func TestConsoleHeadersRefuseUnsafeValues(t *testing.T) {
	t.Parallel()

	bad := map[string]dustpan.ConsoleHeaders{
		"line break in nosniff": hdr("ContentTypeOptions", "nosniff\r\nSet-Cookie: a=b"),
		"line break in frame":   hdr("FrameOptions", "DENY\nX-Evil: 1"),
		"line break in CSP":     hdr("ContentSecurityPolicy", "default-src 'self'\r\nX-Evil: 1"),
		"control in HSTS":       hdr("StrictTransportSecurity", "max-age=1\x00"),
		"non-ASCII in CSP":      hdr("ContentSecurityPolicy", "default-src 'self' é"),
	}

	for name, headers := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)

			_, err := dustpan.NewServer(append(
				fullServerOptions(t, st, blobs, authn), dustpan.WithConsoleHeaders(headers),
			)...)
			if err == nil {
				t.Fatal("NewServer accepted an unsafe header value")
			}
		})
	}
}

// TestConsoleCSPRefusesHostileOrigins sends origins that would add a
// directive or a source to the policy. No public input reaches the origin
// check of the console headers without the allowed-origins check running
// first: WithAllowedOrigins is the only source of the origins. So this test
// drives both layers through the public option and checks the outcome: the
// server does not start, and no policy is built from the origin.
func TestConsoleCSPRefusesHostileOrigins(t *testing.T) {
	t.Parallel()

	hostile := map[string]string{
		"directive injection": "https://a.example;script-src *",
		"two sources":         "https://a.example, https://b.example",
		"space":               "https://a.example https://b.example",
		"quote":               "https://a.example'",
		"double quote":        `https://a.example"`,
		"comma":               "https://a.example,https://b.example",
		"line break":          "https://a.example\r\nX-Evil: 1",
	}

	for name, origin := range hostile {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)

			_, err := dustpan.NewServer(append(
				fullServerOptions(t, st, blobs, authn),
				dustpan.WithConsoleDir(t.TempDir()), dustpan.WithAllowedOrigins(origin),
			)...)
			if !errors.Is(err, dustpan.ErrInvalidOption) {
				t.Fatalf("NewServer with origin %q: err = %v, want ErrInvalidOption", origin, err)
			}
		})
	}
}
