package errcode

// BackupCorrupt is a backup whose stored data the server cannot use, for
// example a manifest that is missing, cut or of another format.
const BackupCorrupt = "backup.corrupt"

// BackupEndedFailed is a backup that ended with an error. A restore and a
// check need a backup that succeeded.
const BackupEndedFailed = "backup.ended-failed"

// BackupStillRunning is a backup that has not ended. A restore and a check
// need a backup that succeeded, so the caller waits for its end.
const BackupStillRunning = "backup.still-running"

// BackupTooManyUnverified is a backup whose data names more blobs without a
// check value than the limit allows. The backup writes no manifest.
const BackupTooManyUnverified = "backup.too-many-unverified"

// argLimit is the name of the argument that carries a limit.
const argLimit = "limit"

// BackupRegistryFilesTooLarge is a backup whose registry files are above the
// limit that a restore applies: one file, or all files of one namespace. The
// backup writes no manifest.
const BackupRegistryFilesTooLarge = "backup.registry-files-too-large"

// backupSpecs are the codes of the backups of the installation.
func backupSpecs() []Spec {
	return []Spec{
		{
			Code:    BackupCorrupt,
			Meaning: "the stored data of the backup is missing, cut or does not match its manifest",
			Args:    []Arg{{Name: "backup", Kind: KindIdentifier}},
		},
		{
			Code:    BackupEndedFailed,
			Meaning: "the backup ended with an error, so a restore or a check cannot use it",
			Args:    nil,
		},
		{
			Code: BackupRegistryFilesTooLarge,
			Meaning: "a registry file, or the registry files of one namespace, are above the limit " +
				"that a restore applies, so the backup wrote no manifest",
			Args: []Arg{
				{Name: "size", Kind: KindQuantity},
				{Name: argLimit, Kind: KindQuantity},
			},
		},
		{
			Code:    BackupStillRunning,
			Meaning: "the backup has not ended, so a restore or a check cannot use it yet",
			Args:    nil,
		},
		{
			Code: BackupTooManyUnverified,
			Meaning: "the data names more blobs without a check value than the limit allows, " +
				"so the backup wrote no manifest",
			Args: []Arg{
				{Name: "count", Kind: KindQuantity},
				{Name: argLimit, Kind: KindQuantity},
			},
		},
	}
}
