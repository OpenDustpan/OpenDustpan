package dustpan

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// passkeyWiring holds the service of the passkeys. The service is nil when
// the store keeps no passkeys, when the server has no public URL, or when the
// public URL cannot give a relying party. The passkey routes are then not
// mounted.
type passkeyWiring struct {
	service *mfasvc.Service
}

// newPasskeyService builds the service of the passkeys on the namespace
// store when that store also holds passkeys. The relying party ID and the
// origin come from publicURL, the external URL of the server. The entries of
// the changes and of the refusals go to entries, in the scope of the
// installation.
//
// A public URL that cannot give a relying party is not a start-up error: the
// server runs as before, with no passkey route, and the log line names the
// reason. A browser runs a passkey ceremony only in a secure context, so the
// URL is https, or http on localhost.
func newPasskeyService(
	store namespace.Store, publicURL string, entries mfasvc.Auditor, now func() time.Time, log *slog.Logger,
) (passkeyWiring, error) {
	none := passkeyWiring{service: nil}

	passkeys, ok := store.(mfa.PasskeyStore)
	if !ok {
		log.Info("passkeys are off: the store keeps no passkeys")

		return none, nil
	}

	external := strings.TrimSpace(publicURL)
	if external == "" {
		log.Info("passkeys are off: the server has no public URL")

		return none, nil
	}

	rp, err := gowebauthn.New(external)
	if err != nil {
		log.Warn("passkeys are off: the public URL gives no relying party", slog.String("reason", err.Error()))

		return none, nil
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: passkeys, RP: rp, Audit: entries, Now: now, Log: log})
	if err != nil {
		return none, fmt.Errorf("passkeys: %w", err)
	}

	return passkeyWiring{service: svc}, nil
}
