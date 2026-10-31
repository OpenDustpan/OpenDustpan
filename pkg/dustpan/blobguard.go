package dustpan

import "opendustpan.org/opendustpan/pkg/backup/blobguard"

// attachBlobGuard keeps the guard of the blob deletes in cfg: the namespace
// store, which reads the backups table and the restores table. A store that
// cannot answer leaves the guard nil, and the server logs one warning. Then no
// service deletes a blob that a backup can name. The update service and the
// workspace service share it; the update service attaches it first.
func attachBlobGuard(cfg *settings) {
	guard, ok := cfg.namespaceStore.(blobguard.Watch)
	if !ok {
		cfg.logger.Warn("the store cannot tell whether a backup or a restore runs: " +
			"the server deletes no blob that a backup can name")

		cfg.blobGuard = nil

		return
	}

	cfg.blobGuard = guard
}
