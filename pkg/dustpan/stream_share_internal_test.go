package dustpan

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// TestSettingsShareStreamStopWithReadiness proves that the readiness probe of
// a new server holds the signal that the jobs API reads. The probe is built
// before the jobs handler, so the signal must exist in the settings from the
// start. A drain of the probe then stops the log streams.
func TestSettingsShareStreamStopWithReadiness(t *testing.T) {
	t.Parallel()

	cfg := defaultSettings()
	if cfg.streamStop == nil {
		t.Fatal("the default settings hold no stream signal for the readiness probe and the jobs API")
	}

	rd := newReadiness(cfg, metrics.NewServerMetrics("test", time.Time{}), nil)
	if rd.streamStop != cfg.streamStop {
		t.Fatal("the readiness probe does not hold the stream signal of the settings")
	}

	if cfg.streamStop.isStopping() {
		t.Fatal("the signal is set before the drain")
	}

	rd.drain()

	if !cfg.streamStop.isStopping() {
		t.Error("the drain of the readiness probe did not set the signal of the jobs API")
	}
}
