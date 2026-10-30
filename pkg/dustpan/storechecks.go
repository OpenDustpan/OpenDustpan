package dustpan

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// errStoreCheckMaintenance and errStoreCheckRestore are the reasons that a
// store check does not start. The texts hold no value of the request.
var (
	errStoreCheckMaintenance = errors.New("the installation is in maintenance mode")
	errStoreCheckRestore     = errors.New("a restore runs")
)

// newStoreCheckService builds the store check service. The second result is
// false when the server has none: the namespace store keeps no check rows or
// has no durable operations, or the server has no blob store. The service
// checks the live blob store and, when the server has a backup target, that
// target too. The installation API mounts the store check routes only with a
// service.
func newStoreCheckService(cfg *settings, maint *maintenance.Service) (*storecheck.Service, bool, error) {
	records, ok := cfg.namespaceStore.(storecheck.Records)
	if !ok || cfg.blobStore == nil {
		return nil, false, nil
	}

	if _, hasOps := cfg.namespaceStore.(durableStoreProvider); !hasOps {
		return nil, false, nil
	}

	ops, err := backupOperations(cfg)
	if err != nil {
		return nil, false, err
	}

	serverAudit, err := serverAuditOf(cfg.namespaceStore)
	if err != nil {
		return nil, false, err
	}

	service, err := storecheck.NewService(storecheck.Config{
		Records:    records,
		Operations: ops,
		Blobs:      cfg.blobStore,
		Backup:     cfg.backupTarget,
		Audit:      serverAudit,
		Admit:      storeCheckAdmit(cfg, maint),
		Limits:     storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:        cfg.now,
		Owner:      durableOwner(cfg),
		Lease:      cfg.operationLease,
		Logger:     cfg.logger,
	})
	if err != nil {
		return nil, false, fmt.Errorf("store check service: %w", err)
	}

	return service, true, nil
}

// storeCheckAdmit returns the check that refuses a store check in maintenance
// mode and while a restore runs. A state that it cannot read refuses too.
func storeCheckAdmit(cfg *settings, maint *maintenance.Service) func(ctx context.Context) error {
	var restores backup.RestoreOperations
	if ops, ok := cfg.namespaceStore.(backup.RestoreOperations); ok {
		restores = ops
	}

	return func(ctx context.Context) error {
		if err := refuseInMaintenance(ctx, maint); err != nil {
			return err
		}

		return refuseWhileRestoring(ctx, restores)
	}
}

// refuseInMaintenance returns an error when the installation is not in the
// normal state, or when the state cannot be read.
func refuseInMaintenance(ctx context.Context, maint *maintenance.Service) error {
	if maint == nil {
		return nil
	}

	state, err := maint.Get(ctx)
	if err != nil {
		return fmt.Errorf("read the installation state: %w", err)
	}

	if state != maintenance.StateNormal {
		return errStoreCheckMaintenance
	}

	return nil
}

// refuseWhileRestoring returns an error when a restore is checking or
// applying, or when the restores cannot be listed. A store with no restore
// rows has nothing to refuse for.
func refuseWhileRestoring(ctx context.Context, restores backup.RestoreOperations) error {
	if restores == nil {
		return nil
	}

	rows, err := restores.ListRestores(ctx)
	if err != nil {
		return fmt.Errorf("list restores: %w", err)
	}

	for i := range rows {
		if rows[i].State == backup.RestoreChecking || rows[i].State == backup.RestoreApplying {
			return errStoreCheckRestore
		}
	}

	return nil
}
