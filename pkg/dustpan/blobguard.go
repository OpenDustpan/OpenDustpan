package dustpan

import (
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

// blobDeleteWait returns the hook that counts each refused blob delete take
// on observed, or nil when the server has no metrics. A nil hook counts
// nothing.
func blobDeleteWait(observed *metrics.ServerMetrics) blobguard.WaitCounter {
	if observed == nil {
		return nil
	}

	return func(reason blobguard.WaitReason) { observed.IncBlobDeleteWaiting(string(reason)) }
}

// attachBlobGuard keeps the guard of the blob deletes in cfg: the namespace
// store, which reads the backups table and the restores table and holds the
// leases of the deleters. A store that cannot answer leaves the guard nil,
// and the server logs one warning. Then no service deletes a blob that a
// backup can name. The update service and the workspace service share it;
// the update service attaches it first.
func attachBlobGuard(cfg *settings) {
	guard, ok := cfg.namespaceStore.(blobguard.Leases)
	if !ok {
		cfg.logger.Warn("the store cannot tell whether a backup or a restore runs: " +
			"the server deletes no blob that a backup can name")

		cfg.blobGuard = nil

		return
	}

	cfg.blobGuard = guard
}
