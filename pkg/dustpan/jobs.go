package dustpan

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// WithJobStore sets the store of jobs. With it, the server serves the jobs
// API under /api/dustpan/v1/namespaces/{namespace}/jobs. Without it, the
// server serves no job route. With the runner stores (WithRunners), the
// inventory reader and a store of gates, the server also starts plan jobs
// through POST on that path.
func WithJobStore(st job.Store) Option {
	return func(o *settings) { o.jobStore = st }
}

// newJobsHandler builds the jobs API, or returns nil when no job store is
// set. starter is the job service, or nil when the server lacks a port that it needs.
func newJobsHandler(svc services, cfg *settings, onFailure func(), starter job.Starter) (http.Handler, error) {
	if cfg.jobStore == nil {
		return nil, nil //nolint:nilnil // No jobs API: no job store is set.
	}

	// The settings always hold the stream signal. defaultSettings creates it, and
	// the readiness probe sets it in drain. The jobs API reads the same signal,
	// so the handler makes no signal of its own.

	// A namespace store that keeps no audit entries is an error: a refusal
	// always writes its entry.
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	// A nil runner store must stay a nil interface: a typed nil would pass the
	// nil check of the handler and panic at the first read.
	var pools runner.PoolReader
	if cfg.runners != nil {
		pools = cfg.runners
	}

	// A nil reader must stay a nil interface: a typed nil would pass the nil
	// check of the handler and panic at the first log read.
	var artifacts job.ArtifactReader

	reader, canRead, err := newJobArtifactReader(svc, cfg)
	if err != nil {
		return nil, err
	}

	if canRead {
		artifacts = reader
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces: svc.namespaces,
		Jobs:       cfg.jobStore,
		Units:      cfg.inventoryReader,
		Artifacts:  artifacts,
		Logs: &jobsapi.LogOptions{
			Poll:         0,
			MaxLife:      0,
			WriteTimeout: 0,
			PerPrincipal: 0,
			Total:        0,
			Stopping:     cfg.streamStop.isStopping,
		},
		Cursors:        cfg.secretWrapper,
		Starter:        starter,
		Workers:        cfg.watch,
		Pools:          pools,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs api: %w", err)
	}

	return handler, nil
}

// newJobArtifactReader builds the reader of job logs over the record store of
// the job store, the blob store and the item key sealer. It returns nil when
// the server lacks one of them (false): the server then serves no log route.
func newJobArtifactReader(svc services, cfg *settings) (*job.ArtifactReads, bool, error) {
	records, ok := cfg.jobStore.(job.RecordStore)
	if !ok || cfg.blobStore == nil || svc.namespaces == nil || cfg.updateStore == nil || cfg.namespaceStore == nil {
		return nil, false, nil
	}

	refusals, err := blobRefusalsOf(cfg.namespaceStore, job.ArtifactBlobFamily(), cfg.now)
	if err != nil {
		return nil, false, err
	}

	blobs, err := job.NewArtifactBlobs(cfg.blobStore, refusals)
	if err != nil {
		return nil, false, fmt.Errorf("job log blobs: %w", err)
	}

	sealer, err := newItemSealerOf(&itemSealerInputs{
		updates: cfg.updateStore, blobs: cfg.blobStore, namespaces: cfg.namespaceStore,
		wrapper: cfg.secretWrapper, now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("job log reader: %w", err)
	}

	reader, err := job.NewArtifactReader(records, blobs, sealer, jobNamespaces{namespaces: svc.namespaces})
	if err != nil {
		return nil, false, fmt.Errorf("job log reader: %w", err)
	}

	return reader, true, nil
}

// FlushError sends the data written so far to the client, so a stream, such as a
// job log, reaches it before the handler ends. A flush before any header
// records the status 200 that the server then sends, so the status stays
// correct in the metrics and the log. It does not expose the other optional
// methods of the writer below, such as Hijack.
func (w *statusWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}

	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

// SetWriteDeadline limits the time of the next writes of a stream.
func (w *statusWriter) SetWriteDeadline(t time.Time) error {
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(t); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}

	return nil
}

// streamStop tells the log streams of the jobs API that the process stops.
type streamStop struct{ stopped atomic.Bool }

// stop sets the signal. A nil signal ignores it.
func (s *streamStop) stop() {
	if s != nil {
		s.stopped.Store(true)
	}
}

// isStopping reports whether the process stops.
func (s *streamStop) isStopping() bool { return s != nil && s.stopped.Load() }
