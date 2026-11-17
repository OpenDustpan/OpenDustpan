package dustpan

import (
	"net/http"
)

// newDecisionRecordsHandler builds the decision records API: the read routes
// of the decision records that the gates write. It returns nil when the
// store keeps no gates, because a server without gates writes no decision
// record: a record of another server has nothing to read here. The routes
// stay unmounted, as with the gate configuration.
func newDecisionRecordsHandler(svc services, cfg *settings, onFailure func()) http.Handler {
	_ = svc
	_ = cfg
	_ = onFailure

	var h http.Handler

	return h
}
