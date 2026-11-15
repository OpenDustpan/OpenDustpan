package dustpan

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
)

// consoleHandler serves a built single-page app from dir, for GET and HEAD
// only. Paths without a file extension that do not exist fall back to
// index.html, so client-side routes survive a page reload.
//
// Each response carries the security headers in headers, including the
// error answers of the file server. The API handlers do not pass through it.
func consoleHandler(dir string, headers []consoleHeader) (http.Handler, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open console directory: %w", err)
	}

	fsys := root.FS()
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, fmt.Errorf("console directory has no index.html: %w", err)
	}

	files := http.FileServerFS(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range headers {
			w.Header().Set(h.name, h.value)
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeRouteError(
				w, http.StatusMethodNotAllowed, "method not allowed", routeAnswer(errcode.ServerMethodNotAllowed),
			)

			return
		}

		// Defense in depth: the os.Root file system also refuses such a name.
		// This check stays so that the answer is a client error and no file
		// lookup runs.
		if strings.ContainsFunc(r.URL.Path, unicode.IsControl) {
			writeRouteError(w, http.StatusBadRequest, "invalid path", routeAnswer(errcode.RequestInvalidPath))

			return
		}

		// The name goes only to fsys, which is an os.Root file system: it
		// refuses each path that leaves the console directory.
		// nosemgrep: go.lang.security.filepath-clean-misuse.filepath-clean-misuse
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && path.Ext(name) == "" {
			if _, err := fs.Stat(fsys, name); errors.Is(err, fs.ErrNotExist) {
				http.ServeFileFS(w, r, fsys, "index.html")

				return
			}
		}

		files.ServeHTTP(w, r)
	}), nil
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// ConsoleHeaderOff is the value that turns one console response header off.
const ConsoleHeaderOff = "off"

const (
	defaultContentTypeOptions = "nosniff"
	defaultFrameOptions       = "DENY"
	defaultHSTS               = "max-age=31536000"
)

// ConsoleHeaders sets the security headers of the responses that serve the
// web console files. Each field has three states: empty keeps the built-in
// default, ConsoleHeaderOff (in any case) sends no header, and any other value is the
// header value. The headers apply to the console files only, never to the API
// routes.
type ConsoleHeaders struct {
	// ContentTypeOptions is X-Content-Type-Options. Default: nosniff.
	ContentTypeOptions string
	// FrameOptions is X-Frame-Options. Default: DENY.
	FrameOptions string
	// ContentSecurityPolicy is Content-Security-Policy. The default is built
	// from the allowed origins: see defaultCSP.
	ContentSecurityPolicy string
	// StrictTransportSecurity is Strict-Transport-Security. Default:
	// max-age=31536000. The server sends it only when it serves TLS.
	StrictTransportSecurity string
}

// noConsoleHeaders answers console header settings that keep every default.
func noConsoleHeaders() ConsoleHeaders {
	return ConsoleHeaders{
		ContentTypeOptions:      "",
		FrameOptions:            "",
		ContentSecurityPolicy:   "",
		StrictTransportSecurity: "",
	}
}

// consoleHeader is one header that the console handler sets.
type consoleHeader struct {
	name  string
	value string
}

// defaultCSP is the Content-Security-Policy of the console when the operator
// sets none. The console may call the API of this server and of the listed
// origins.
func defaultCSP(origins []string) string {
	connect := append([]string{"'self'"}, origins...)

	return "default-src 'self'; connect-src " + strings.Join(connect, " ") +
		"; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; " +
		"base-uri 'none'; form-action 'self'"
}

// headerValue answers the value to send for a header: def when the setting
// is empty, and "" when the setting turns the header off. The word off is
// read without case and without the spaces around it.
func headerValue(value, def string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, ConsoleHeaderOff) {
		return ""
	}

	if value == "" {
		return def
	}

	return value
}

// resolve answers the headers to send on every console response.
func (h ConsoleHeaders) resolve(origins []string) []consoleHeader {
	var out []consoleHeader

	for _, c := range []consoleHeader{
		{"X-Content-Type-Options", headerValue(h.ContentTypeOptions, defaultContentTypeOptions)},
		{"X-Frame-Options", headerValue(h.FrameOptions, defaultFrameOptions)},
		{"Content-Security-Policy", headerValue(h.ContentSecurityPolicy, defaultCSP(origins))},
	} {
		if c.value != "" {
			out = append(out, c)
		}
	}

	return out
}

// hsts answers the Strict-Transport-Security header, or nothing when the
// setting turns it off. The caller adds it only when the server serves TLS:
// the header never goes out on plain HTTP.
func (h ConsoleHeaders) hsts() []consoleHeader {
	value := headerValue(h.StrictTransportSecurity, defaultHSTS)
	if value == "" {
		return nil
	}

	return []consoleHeader{{"Strict-Transport-Security", value}}
}

// checkConsoleHeaders refuses a header value that is not printable ASCII, so
// a value cannot carry a line break into the response. It also refuses an
// allowed origin that cannot sit safely in a CSP source list.
func checkConsoleHeaders(h ConsoleHeaders, origins []string) error {
	fields := []struct{ name, value string }{
		{"ContentTypeOptions", h.ContentTypeOptions},
		{"FrameOptions", h.FrameOptions},
		{"ContentSecurityPolicy", h.ContentSecurityPolicy},
		{"StrictTransportSecurity", h.StrictTransportSecurity},
	}

	for _, f := range fields {
		if strings.ContainsFunc(f.value, func(r rune) bool { return r < ' ' || r > '~' }) {
			return fmt.Errorf(
				"%w: WithConsoleHeaders: %s must hold printable ASCII only, with no line break",
				ErrInvalidOption, f.name,
			)
		}
	}

	for _, o := range origins {
		// Defense in depth: checkAllowedOrigins checks the form of each
		// origin too; this check stays so that a space or a semicolon never
		// reaches the policy, whatever that check accepts.
		if strings.ContainsAny(o, " ;,'\"") {
			return fmt.Errorf(
				"%w: WithAllowedOrigins: origin %q cannot go into the console Content-Security-Policy",
				ErrInvalidOption, o,
			)
		}
	}

	return nil
}

// withCORS lets the listed console origins call the API with credentials.
// Requests from other origins get no CORS headers, so browsers block them.
func withCORS(next http.Handler, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		origin := apidesc.Header(r, "Origin")
		if origin == "" || !slices.Contains(allowed, origin) {
			next.ServeHTTP(w, r)

			return
		}

		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set(
				"Access-Control-Allow-Headers",
				"Authorization, Content-Type, Dustpan-Channel, "+idempotent.Header+", If-Match, X-Dustpan-CSRF",
			)
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		h.Set("Access-Control-Expose-Headers", "Retry-After, ETag, "+idempotent.ReplayedHeader)

		next.ServeHTTP(w, r)
	})
}

// checkAllowedOrigins refuses an origin that is not http(s)://host[:port]
// with no path, query or fragment. It refuses "null" and "*" too: the
// server compares each origin to the Origin header as an exact string.
func checkAllowedOrigins(origins []string) error {
	for _, o := range origins {
		if !validAllowedOrigin(o) {
			return fmt.Errorf(
				"%w: WithAllowedOrigins: origin %q must be http:// or https:// with a host and an optional port, "+
					"in lower case with an ASCII (punycode) host and no default port, "+
					"and no path, query or fragment, for example https://console.example.com",
				ErrInvalidOption, o,
			)
		}
	}

	return nil
}

// validAllowedOrigin reports whether o can equal the Origin header of a
// browser: scheme://host[:port] in lower case, ASCII only, with no default
// port.
func validAllowedOrigin(o string) bool {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) ||
		o != u.Scheme+"://"+u.Host || o != strings.ToLower(o) ||
		strings.ContainsFunc(o, func(r rune) bool { return r > unicode.MaxASCII }) ||
		strings.HasSuffix(u.Host, ":") {
		return false
	}

	return validOriginHost(u.Hostname()) && validOriginPort(u.Scheme, u.Port())
}

// validOriginHost reports whether host is an IP literal without a zone, or a
// DNS name: dot-separated labels of 1 to 63 lower-case letters, digits and
// hyphens, with no hyphen at either end, and at most 253 characters in all.
// url.Parse accepts more (for example ";", "," and quotes), and the origin
// goes into the Content-Security-Policy and into the CORS answer.
func validOriginHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}

	const maxName = 253

	if host == "" || len(host) > maxName {
		return false
	}

	numeric := true

	for label := range strings.SplitSeq(host, ".") {
		kind := dnsLabel(label)
		if kind == labelInvalid {
			return false
		}

		numeric = numeric && kind == labelDigits
	}

	// A name of digits and dots that is not an IP address is a malformed address.
	return !numeric
}

// Kinds of a DNS label, from dnsLabel.
const (
	labelInvalid = iota
	labelDigits
	labelName
)

// dnsLabel answers the kind of a label: it has 1 to 63 lower-case letters,
// digits and hyphens with no hyphen at either end, and it holds digits only
// or not.
func dnsLabel(label string) int {
	const maxLabel = 63

	if label == "" || len(label) > maxLabel || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") ||
		strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return labelInvalid
	}

	if strings.Trim(label, "0123456789") == "" {
		return labelDigits
	}

	return labelName
}

// validOriginPort reports whether port is empty or a decimal number from 1
// to 65535 with no sign and no leading zero, and not the default port of
// scheme.
func validOriginPort(scheme, port string) bool {
	if port == "" {
		return true
	}

	if (scheme == schemeHTTPS && port == "443") || (scheme == schemeHTTP && port == "80") {
		return false
	}

	n, err := strconv.Atoi(port)

	return err == nil && n >= 1 && n <= math.MaxUint16 && port == strconv.Itoa(n)
}
