package dustpan_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// startedHandler starts srv, as a program that embeds the server does before
// it serves Handler, and answers the handler.
func startedHandler(tb testing.TB, srv *dustpan.Server) http.Handler {
	tb.Helper()

	if err := srv.Start(tb.Context()); err != nil {
		tb.Fatalf("start the server: %v", err)
	}

	return srv.Handler()
}
