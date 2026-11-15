package registry

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// ParseProviderPageToken returns the cursor name of a provider page token, as
// the provider list reads it.
func ParseProviderPageToken(raw string, ns namespace.Name) (string, error) {
	return parseProviderToken(raw, ns)
}

// ParseModulePageToken returns the cursor key of a module page token, as the
// module list reads it.
func ParseModulePageToken(raw string, ns namespace.Name) (string, error) {
	return parseModuleToken(raw, ns)
}

// The causes of the guards of the page token parsers, for the external tests
// that prove each guard alone.
var (
	ErrProviderTokenLength  = errProviderTokenLength
	ErrProviderTokenDecode  = errProviderTokenDecode
	ErrProviderTokenEmpty   = errProviderTokenEmpty
	ErrProviderTokenAddress = errProviderTokenAddress
	ErrModuleTokenLength    = errModuleTokenLength
	ErrModuleTokenDecode    = errModuleTokenDecode
	ErrModuleTokenEmpty     = errModuleTokenEmpty
	ErrModuleTokenAddress   = errModuleTokenAddress
)
