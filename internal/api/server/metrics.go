package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// metricsMediaType is the media type of the Prometheus text format,
// version 0.0.4.
const metricsMediaType = "text/plain; version=0.0.4"

// MetricsWriter writes the metrics registry in the Prometheus text format.
// The server passes its registry holder from dustpan.go. It is an interface
// so this adapter never imports the metrics package.
type MetricsWriter interface {
	Write(w io.Writer) error
}

// resolveServer returns the installation itself: the metrics route acts on
// no namespace or unit.
func resolveServer(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
	return identity.ServerObject(), nil
}

// serveMetrics writes the registry in the text format. The registration
// wrapper already authenticated the caller and authorized server:observe.
func (h *Handler) serveMetrics(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	var buf bytes.Buffer
	if err := h.metrics.Write(&buf); err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	w.Header().Set("Content-Type", metricsMediaType)
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(buf.Bytes()); err != nil {
		h.logger.WarnContext(r.Context(), "write response failed", slog.String("error", err.Error()))
	}
}
