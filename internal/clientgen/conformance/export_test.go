//nolint:testpackage // The usual file for the hooks that the black-box tests need.
package conformance

import (
	"context"
	"io"
	"net/http"
)

// ReadLimited exports readLimited.
func ReadLimited(r io.Reader) ([]byte, error) { return readLimited(r) }

// MaxProxyBodyBytes exports the limit of a body.
const MaxProxyBodyBytes = maxProxyBodyBytes

// Client returns the client that the proxy uses to reach its upstream.
func (p *RecordingProxy) Client() *http.Client { return p.client }

// DevServerAt returns a DevServer that has no process and the URL of a server
// that the test runs. It has the client that StartDevServer gives.
func DevServerAt(url string) *DevServer {
	s := newDevServer(nil, func() {}, "test")
	s.url = url

	return s
}

// stubServer is a Server with a URL and a token.
type stubServer struct {
	Server

	url string
}

func (s stubServer) URL() string   { return s.url }
func (s stubServer) Token() string { return "t" }

// Direct sends a GET to a server at a URL, as the harness does for its own
// requests, and returns the status and the body.
func Direct(ctx context.Context, url, path string) (int, []byte, error) {
	out, err := direct(ctx, Env{Server: stubServer{Server: nil, url: url}}, directRequest{
		method: http.MethodGet, path: path, auth: withToken, contentType: "", body: nil,
	})

	return out.status, out.body, err
}
