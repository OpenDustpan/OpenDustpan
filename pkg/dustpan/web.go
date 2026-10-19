package dustpan

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"unicode"
)

// consoleHandler serves a built single-page app from dir, for GET and HEAD
// only. Paths without a file extension that do not exist fall back to
// index.html, so client-side routes survive a page reload.
func consoleHandler(dir string) (http.Handler, error) {
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
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

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

// withCORS lets the listed console origins call the API with credentials.
// Requests from other origins get no CORS headers, so browsers block them.
func withCORS(next http.Handler, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		origin := r.Header.Get("Origin")
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
				"Authorization, Content-Type, Dustpan-Channel, If-Match, X-Dustpan-CSRF",
			)
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)

			return
		}

		h.Set("Access-Control-Expose-Headers", "Retry-After")

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
	const (
		schemeHTTP  = "http"
		schemeHTTPS = "https"
	)

	u, err := url.Parse(o)
	if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) || u.Hostname() == "" ||
		o != u.Scheme+"://"+u.Host || o != strings.ToLower(o) ||
		strings.ContainsFunc(o, func(r rune) bool { return r > unicode.MaxASCII }) ||
		strings.HasSuffix(u.Host, ":") {
		return false
	}

	return (u.Scheme != schemeHTTPS || u.Port() != "443") && (u.Scheme != schemeHTTP || u.Port() != "80")
}
