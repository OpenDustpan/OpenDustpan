package dustpan

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// jobsStreamStore is the namespace store of the test. It holds no data: the
// jobs handler reads it only as the audit store of its refusals.
type jobsStreamStore struct {
	namespace.Store
	noAudit
}

// TestJobsAPIStopsWithReadinessDrain proves that the jobs API reads the stream
// signal of the settings, the same signal that the readiness probe holds: a
// drain of the probe stops the log streams of the API.
func TestJobsAPIStopsWithReadinessDrain(t *testing.T) {
	t.Parallel()

	cfg := defaultSettings()
	cfg.jobStore = plainStore{Store: nil}
	cfg.namespaceStore = jobsStreamStore{Store: nil, noAudit: noAudit{Store: nil}}
	cfg.authenticator = devPersonAuthenticator{}
	cfg.authorizer = identity.AdminOnly{}

	namespaces, err := namespace.NewService(namespace.Config{
		Store: cfg.namespaceStore, Now: cfg.now, Installation: "test",
	})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	var svc services
	svc.namespaces = namespaces

	probe := newReadiness(cfg, metrics.NewServerMetrics("test", time.Time{}), nil)

	if _, err := newJobsHandler(svc, cfg, func() {}, nil); err != nil {
		t.Fatalf("jobs handler: %v", err)
	}

	if probe.streamStop != cfg.streamStop {
		t.Fatal("the readiness probe does not hold the stream signal of the settings")
	}

	if cfg.streamStop.isStopping() {
		t.Fatal("the signal is set before the drain")
	}

	probe.drain()

	if !cfg.streamStop.isStopping() {
		t.Error("the drain of the readiness probe did not set the signal of the jobs API")
	}
}
