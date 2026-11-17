package dustpan

import (
	"net/http"

	decisionrecordapi "opendustpan.org/opendustpan/internal/api/decisionrecord"
	"opendustpan.org/opendustpan/pkg/gate"
)

// newDecisionRecordsHandler builds the decision records API: the read routes
// of the decision records that the gates write. It returns nil when the
// store keeps no gates, because a server without gates writes no decision
// record: a record of another server has nothing to read here. The routes
// stay unmounted, as with the gate configuration.
func newDecisionRecordsHandler(svc services, cfg *settings, onFailure func()) http.Handler {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		return nil
	}

	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil
	}

	handler, err := decisionrecordapi.NewHandler(decisionrecordapi.Config{
		Namespaces:     svc.namespaces,
		Records:        store.Records(),
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil
	}

	return handler
}
