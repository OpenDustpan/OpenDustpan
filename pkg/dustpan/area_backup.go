package dustpan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobcache "opendustpan.org/opendustpan/pkg/blob/cache"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	// errBackupNeedsExporter reports a backup target with a namespace store
	// that cannot capture exports.
	errBackupNeedsExporter = errors.New("backup target needs a namespace store that captures exports")
	// errBackupNeedsRegistry reports a backup target with a namespace store
	// that cannot track backups.
	errBackupNeedsRegistry = errors.New("backup target needs a namespace store that tracks backups")
	// errBackupNeedsOperations reports a backup target with a namespace store
	// that keeps no durable operations.
	errBackupNeedsOperations = errors.New("backup target needs a namespace store that keeps durable operations")
	// errRestoreNeedsLive reports a backup target with a namespace store
	// that cannot check, apply or fence a restore.
	errRestoreNeedsLive = errors.New("backup target needs a namespace store that restores")
	// errRestoreNeedsOperations reports a backup target with a namespace store
	// that cannot track restores.
	errRestoreNeedsOperations = errors.New(
		"backup target needs a namespace store with durable operations that cancel before a step",
	)
)

// backupSettings holds the backup target that NewServer gives to the backup
// and the restore service, and the switches of a restore of a dev backup.
type backupSettings struct {
	target          blob.Store
	targetName      string
	allowDevBackups bool
	allowDevHint    string
}

// defaultBackupSettings returns the backup settings before the options: no
// backup target and no restore of a dev backup.
func defaultBackupSettings() backupSettings {
	return backupSettings{target: nil, targetName: "", allowDevBackups: false, allowDevHint: ""}
}

// WithBackupTarget sets the backup target that holds blobs/<key>,
// backups/<id>/sql/<table>.jsonl.gz and backups/<id>/manifest.json (ADR 30 decision 5).
// target is the opened target store, for example a blob/fs store for
// "dir:/path". name is the target setting for operators, for example
// "dir:/var/backups": the backups table keeps it. Empty name with a target
// keeps the empty name. Nil target means no backup target is configured and
// NewServer builds no backup service. It is the only way the target gets in:
// no request field, header or query value sets it.
func WithBackupTarget(target blob.Store, name string) Option {
	return func(o *settings) { o.backup.target, o.backup.targetName = target, name }
}

// WithAllowDevBackups lets a production installation restore a backup of a
// dev installation. The restore then removes the dev people and their data
// from the staged data and warns. The default is false. Only code sets it: no
// environment variable, configuration file or API setting reaches this option,
// and an embedder passes it in Go code only.
func WithAllowDevBackups(allow bool) Option {
	return func(o *settings) { o.backup.allowDevBackups = allow }
}

// WithAllowDevHint sets the words that the refusal of a dev backup uses to
// name how to allow it, for example the flag in code quotes. An empty hint
// leaves the sentence out. The default is empty.
func WithAllowDevHint(hint string) Option {
	return func(o *settings) { o.backup.allowDevHint = hint }
}

// newBackupService builds the installation backup service when a backup
// target is configured, else returns nil. The exporter and the registry come
// from the namespace store: every SQL adapter serves all ports from one
// database connection. A nil target means no backup target is configured.
func newBackupService(cfg *settings, svc services) (*backup.Service, error) {
	if cfg.backup.target == nil {
		//nolint:nilnil // Nil without an error means no backup target is configured.
		return nil, nil
	}

	exporter, ok := cfg.namespaceStore.(backup.PortableExporter)
	if !ok {
		return nil, errBackupNeedsExporter
	}

	registry, ok := cfg.namespaceStore.(backup.Registry)
	if !ok {
		return nil, errBackupNeedsRegistry
	}

	// A store that cannot read its kind cannot run a restore either: the
	// error names every port of backup.RestoreLive that the store lacks.
	kinds, ok := cfg.namespaceStore.(backup.KindReader)
	if !ok {
		return nil, restoreLiveError(cfg.namespaceStore)
	}

	maxSQL := int64(0)
	if strings.HasPrefix(strings.TrimSpace(cfg.backup.targetName), "s3://") {
		maxSQL = backup.S3ObjectLimit
	}

	var opener backup.StateOpener
	if svc.workspaces != nil {
		opener = uncachedStateOpener{workspaces: svc.workspaces}
	}

	ops, err := backupOperations(cfg)
	if err != nil {
		return nil, err
	}

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       kinds,
		Registry:    registry,
		Blobs:       cfg.blobStore,
		StateOpener: opener,
		Target:      cfg.backup.target,
		TargetName:  cfg.backup.targetName,
		Now:         cfg.now,
		Version:     cfg.version,
		MaxSQLBytes: maxSQL,
		Operations:  ops,
		Owner:       durableOwner(cfg),
		Lease:       cfg.operationLease,
		Logger:      cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("backup service: %w", err)
	}

	return service, nil
}

// backupOperations returns the durable operations of the namespace store,
// where the backup operations live next to the backup rows. Its claims go
// through the claim recorder of the worker watch, so a worker that runs only
// a backup still writes its heartbeat.
//
//nolint:ireturn // The port is the result: the store adapters and the tracking wrapper both serve it.
func backupOperations(cfg *settings) (durable.Store, error) {
	p, ok := cfg.namespaceStore.(durableStoreProvider)
	if !ok || p == nil {
		return nil, errBackupNeedsOperations
	}

	ops := p.DurableOperationsPort()
	if ops == nil {
		return nil, errBackupNeedsOperations
	}

	if cfg.watch != nil {
		return trackingOps{Store: ops, watch: cfg.watch}, nil
	}

	return ops, nil
}

// newRestoreService builds the installation restore service when a backup
// target is configured, else returns nil. The live store, the registry and
// the check ports come from the namespace store: every SQL adapter serves
// all ports from one database connection. The backup service takes the
// backup of the live data before the apply. A nil target means no backup
// target is configured.
func newRestoreService(
	cfg *settings, deps installationDeps, maint *maintenance.Service,
) (*backup.RestoreService, error) {
	if cfg.backup.target == nil {
		//nolint:nilnil // Nil without an error means no backup target is configured.
		return nil, nil
	}

	if deps.backups == nil {
		return nil, errBackupNeedsRegistry
	}

	// The compiler checks that backup.RestoreLive holds the ports of the dev
	// data rules, and this assertion checks the namespace store at the start.
	// A store that lacks one of them cannot run a restore, and the server does
	// not start: a production restore never skips the kind check, the strip or
	// the report because a port is missing.
	live, ok := cfg.namespaceStore.(backup.RestoreLive)
	if !ok {
		return nil, restoreLiveError(cfg.namespaceStore)
	}

	ops, err := backupOperations(cfg)
	if err != nil {
		return nil, err
	}

	if _, ok := ops.(durable.StepCanceler); !ok {
		return nil, errRestoreNeedsOperations
	}

	admit := restoreMinimumAdmit(cfg.namespaceStore)

	var steps []restoreStep
	if deps.chain != nil {
		admit = admitBoth(admit, deps.chain.admitRestore)
		steps = append(steps, deps.chain.afterRestore)

		if deps.chain.checkpoints != nil {
			serverAudit, auditErr := serverAuditOf(cfg.namespaceStore)
			if auditErr != nil {
				return nil, auditErr
			}

			steps = append(steps, newEpochAfterRestore(deps.chain.checkpoints, serverAudit, cfg.now))
		}
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:         live,
		Operations:   ops,
		Backups:      deps.backups,
		Maintenance:  maint,
		Blobs:        cfg.blobStore,
		Target:       cfg.backup.target,
		Key:          cfg.secretWrapper,
		Installation: cfg.installation.String(),
		AfterApply:   afterRestore(steps...),
		Admit:        admit,

		AllowDevBackups: cfg.backup.allowDevBackups,
		AllowDevHint:    cfg.backup.allowDevHint,
		Owner:           durableOwner(cfg),
		Lease:           cfg.operationLease,
		CleanupLockWait: 0,
		RetryBase:       0,
		Now:             cfg.now,
		Logger:          cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("restore service: %w", err)
	}

	return service, nil
}

// restoreLiveError returns errRestoreNeedsLive with the name of each port of
// backup.RestoreLive that store lacks.
func restoreLiveError(store any) error {
	var missing []string

	check := func(name string, ok bool) {
		if !ok {
			missing = append(missing, name)
		}
	}

	_, ok := store.(backup.PortableExporter)
	check("PortableExporter", ok)

	_, ok = store.(backup.KeyPeeker)
	check("KeyPeeker", ok)

	_, ok = store.(backup.KindReader)
	check("KindReader", ok)

	_, ok = store.(backup.Fencer)
	check("Fencer", ok)

	_, ok = store.(backup.RestoreOperations)
	check("RestoreOperations", ok)

	_, ok = store.(backup.RestoreStaging)
	check("RestoreStaging", ok)

	return fmt.Errorf("%w: the store lacks %s", errRestoreNeedsLive, strings.Join(missing, ", "))
}

// restoreStep is one after-apply step of a restore.
type restoreStep func(ctx context.Context) error

// afterRestore returns the after-apply step of a restore that runs every
// step in order, also after a step fails, and joins their errors. So a
// failed step never skips the comparison of the restored audit log with the
// state of the chain.
func afterRestore(steps ...restoreStep) restoreStep {
	return func(ctx context.Context) error {
		var errs []error

		for _, step := range steps {
			if err := step(ctx); err != nil {
				errs = append(errs, err)
			}
		}

		return errors.Join(errs...)
	}
}

// uncachedStateOpener opens OpenTofu state blobs for the backup copy check
// without the blob cache, so the check reads the stored bytes and a backup
// does not fill the cache.
type uncachedStateOpener struct {
	workspaces *workspace.Service
}

// OpenStateBlob implements backup.StateOpener. Plain bytes that do not match
// the size and the MD5 of the row of their version wrap
// backup.ErrLiveBlobDamaged, at the open and at each read. Sealed bytes that
// do not open wrap backup.ErrLiveBlobUnopened: damaged bytes and a wrong key
// both cause that, so a restore stops at it. Every other error keeps its own
// class.
func (o uncachedStateOpener) OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	reader, err := o.workspaces.OpenStateBlob(blobcache.WithoutCache(ctx), key)
	if err != nil {
		return nil, fmt.Errorf("open state blob: %w", stateDamage(err))
	}

	return damageReader{ReadCloser: reader}, nil
}

// stateDamage wraps err with backup.ErrLiveBlobUnopened when err says that
// the sealed content does not open or that a key failed, and with
// backup.ErrLiveBlobDamaged when err says that the plain state does not match
// the size and the MD5 of its row. It returns any other error as it is.
func stateDamage(err error) error {
	switch {
	case errors.Is(err, workspace.ErrSealedContent), errors.Is(err, secret.ErrDecrypt):
		return fmt.Errorf("%w: %w", backup.ErrLiveBlobUnopened, err)
	case errors.Is(err, workspace.ErrStoredContentMismatch):
		return fmt.Errorf("%w: %w", backup.ErrLiveBlobDamaged, err)
	default:
		return err
	}
}

// damageReader maps the read errors of opened state with stateDamage.
type damageReader struct {
	io.ReadCloser
}

// Read returns the next plain bytes. io.EOF stays as it is.
func (r damageReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)

	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		return n, io.EOF
	}

	return n, stateDamage(err)
}
