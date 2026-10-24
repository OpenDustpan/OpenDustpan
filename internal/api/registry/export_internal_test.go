package registry

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// WriteError writes the answer of err as a handler of the API does, so the
// external tests check the code and the arguments of an error that no request
// of the test reaches.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}

// CallerEntry returns the audit entry template that the registry routes write
// for a request r of the principal p.
func CallerEntry(r *http.Request, p identity.Principal) audit.Entry {
	return callerOf(r, p).By
}
