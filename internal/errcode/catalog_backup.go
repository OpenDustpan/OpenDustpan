package errcode

// BackupCorrupt is a backup whose stored data the server cannot use, for
// example a manifest that is missing, cut or of another format.
const BackupCorrupt = "backup.corrupt"

// BackupTooManyUnverified is a backup whose data names more blobs without a
// check value than the limit allows. The backup writes no manifest.
const BackupTooManyUnverified = "backup.too-many-unverified"

// backupSpecs are the codes of the backups of the installation.
func backupSpecs() []Spec {
	return []Spec{
		{
			Code:    BackupCorrupt,
			Meaning: "the stored data of the backup is missing, cut or does not match its manifest",
			Args:    []Arg{{Name: "backup", Kind: KindIdentifier}},
		},
		{
			Code: BackupTooManyUnverified,
			Meaning: "the data names more blobs without a check value than the limit allows, " +
				"so the backup wrote no manifest",
			Args: []Arg{
				{Name: "count", Kind: KindQuantity},
				{Name: "limit", Kind: KindQuantity},
			},
		},
	}
}
